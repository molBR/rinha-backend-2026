package fraud

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	dims = 14
	k    = 5
)

// Engine holds the reference dataset and runs fraud scoring.
type Engine struct {
	n       int
	vectors []uint16 // flat N×dims, linear-encoded from [-1,1] to [0,65535]
	labels  []uint8  // 0=legit, 1=fraud

	norm    normalization
	mccRisk map[string]float64

	grid *GridIndex // accelerated k=5 search; built after vectors are loaded
}

type normalization struct {
	MaxAmount         float64 `json:"max_amount"`
	MaxInstallments   float64 `json:"max_installments"`
	AmountVsAvgRatio  float64 `json:"amount_vs_avg_ratio"`
	MaxMinutes        float64 `json:"max_minutes"`
	MaxKm             float64 `json:"max_km"`
	MaxTxCount24h     float64 `json:"max_tx_count_24h"`
	MaxMerchantAvgAmt float64 `json:"max_merchant_avg_amount"`
}

func NewEngine(refsPath, mccPath, normPath string) (*Engine, error) {
	norm, err := loadNormalization(normPath)
	if err != nil {
		return nil, fmt.Errorf("normalization: %w", err)
	}
	mcc, err := loadMCCRisk(mccPath)
	if err != nil {
		return nil, fmt.Errorf("mcc_risk: %w", err)
	}
	e := &Engine{norm: norm, mccRisk: mcc}
	if err := e.loadReferences(refsPath); err != nil {
		return nil, fmt.Errorf("references: %w", err)
	}
	return e, nil
}

func loadNormalization(path string) (normalization, error) {
	f, err := os.Open(path)
	if err != nil {
		return normalization{}, err
	}
	defer f.Close()
	var n normalization
	return n, json.NewDecoder(f).Decode(&n)
}

func loadMCCRisk(path string) (map[string]float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := make(map[string]float64)
	return m, json.NewDecoder(f).Decode(&m)
}

func (e *Engine) loadReferences(path string) error {
	log.Printf("loading reference vectors from %s...", path)
	start := time.Now()

	var presorted bool
	var err error
	if strings.HasSuffix(path, ".bin") {
		presorted, err = e.loadBinary(path)
	} else {
		err = e.loadJSON(path)
	}
	if err != nil {
		return err
	}
	e.maybeRepeatVectors()
	if !presorted {
		// Legacy path: reorder vectors into cell-contiguous layout (2× memory spike).
		e.grid = buildGrid(&e.vectors, &e.labels, e.n)
	}
	log.Printf("loaded %d reference vectors in %s (grid: %d cells, contiguous layout)", e.n, time.Since(start), gridTotal)
	return nil
}

// maybeRepeatVectors inflates the dataset by repeating vectors when TEST_VECTOR_REPEAT
// is set to N. Used for local performance testing to simulate larger datasets (e.g., N=50
// turns 100 example vectors into 5000 to match production KNN cost under Docker CFS).
// Has no effect in production (TEST_VECTOR_REPEAT is never set in the Dockerfile).
func (e *Engine) maybeRepeatVectors() {
	n, err := strconv.Atoi(os.Getenv("TEST_VECTOR_REPEAT"))
	if err != nil || n <= 1 {
		return
	}
	origN := e.n
	origVecs := make([]uint16, len(e.vectors))
	copy(origVecs, e.vectors)
	origLabels := make([]uint8, len(e.labels))
	copy(origLabels, e.labels)

	for e.n < origN*n {
		e.vectors = append(e.vectors, origVecs...)
		e.labels = append(e.labels, origLabels...)
		e.n += origN
	}
	// Trim to exact target
	target := origN * n
	e.vectors = e.vectors[:target*dims]
	e.labels = e.labels[:target]
	e.n = target
	log.Printf("TEST: inflated %d → %d vectors for local perf testing", origN, e.n)
}

// loadBinary reads a binary reference file. Returns presorted=true when the
// file uses GRD2 format (vectors pre-sorted by grid cell, splits embedded in
// header) — in that case e.grid is already set and buildGrid must be skipped.
func (e *Engine) loadBinary(path string) (presorted bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<20)

	// Read first 4 bytes to detect format: "GRD2" magic vs legacy uint32 N.
	var magic [4]byte
	if _, err := io.ReadFull(br, magic[:]); err != nil {
		return false, err
	}

	if magic == [4]byte{'G', 'R', 'D', '2'} {
		if err := e.loadBinaryGRD2(br); err != nil {
			return false, err
		}
		return true, nil
	}

	// Legacy format: first 4 bytes are uint32 N (little-endian).
	n := int(binary.LittleEndian.Uint32(magic[:]))
	vectors := make([]uint16, n*dims)
	if err := binary.Read(br, binary.LittleEndian, vectors); err != nil {
		return false, err
	}
	labels := make([]uint8, n)
	if _, err := io.ReadFull(br, labels); err != nil {
		return false, err
	}
	e.n = n
	e.vectors = vectors
	e.labels = labels
	return false, nil
}

// loadBinaryGRD2 reads the GRD2 format: pre-sorted vectors with grid splits
// embedded in the header. No reorder needed — zero extra memory allocation.
//
// Format (after the 4-byte "GRD2" magic already consumed):
//
//	[4]  uint32 N
//	[4]  uint32 gridSize (sanity check)
//	[62] uint16[31] splits0
//	[62] uint16[31] splits1
//	[N×28] uint16[14] vectors (sorted by grid cell)
//	[N]  uint8 labels
func (e *Engine) loadBinaryGRD2(br *bufio.Reader) error {
	var buf4 [4]byte

	if _, err := io.ReadFull(br, buf4[:]); err != nil {
		return fmt.Errorf("GRD2: read N: %w", err)
	}
	n := int(binary.LittleEndian.Uint32(buf4[:]))

	if _, err := io.ReadFull(br, buf4[:]); err != nil {
		return fmt.Errorf("GRD2: read gridSize: %w", err)
	}
	gs := int(binary.LittleEndian.Uint32(buf4[:]))
	if gs != gridSize {
		return fmt.Errorf("GRD2: gridSize mismatch: file=%d code=%d", gs, gridSize)
	}

	var splits0, splits1 [gridSize - 1]uint16
	if err := binary.Read(br, binary.LittleEndian, splits0[:]); err != nil {
		return fmt.Errorf("GRD2: read splits0: %w", err)
	}
	if err := binary.Read(br, binary.LittleEndian, splits1[:]); err != nil {
		return fmt.Errorf("GRD2: read splits1: %w", err)
	}

	vectors := make([]uint16, n*dims)
	if err := binary.Read(br, binary.LittleEndian, vectors); err != nil {
		return fmt.Errorf("GRD2: read vectors: %w", err)
	}

	labels := make([]uint8, n)
	if _, err := io.ReadFull(br, labels); err != nil {
		return fmt.Errorf("GRD2: read labels: %w", err)
	}

	e.n = n
	e.vectors = vectors
	e.labels = labels
	// Build grid index without reordering — vectors are already cell-sorted.
	e.grid = presortedGridIndex(splits0, splits1, vectors, n)
	return nil
}

func (e *Engine) loadJSON(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	type refEntry struct {
		Vector [dims]float64 `json:"vector"`
		Label  string        `json:"label"`
	}

	dec := json.NewDecoder(gz)
	if _, err := dec.Token(); err != nil {
		return err
	}

	const initial = 3_000_000
	vectors := make([]uint16, 0, initial*dims)
	labels := make([]uint8, 0, initial)

	var entry refEntry
	for dec.More() {
		if err := dec.Decode(&entry); err != nil {
			return err
		}
		for j := 0; j < dims; j++ {
			vectors = append(vectors, encodeVal(entry.Vector[j]))
		}
		if entry.Label == "fraud" {
			labels = append(labels, 1)
		} else {
			labels = append(labels, 0)
		}
	}

	e.n = len(labels)
	e.vectors = vectors
	e.labels = labels
	return nil
}

// encodeVal maps [-1, 1] linearly to [0, 65535].
func encodeVal(v float64) uint16 {
	s := (v + 1.0) * 32767.5
	if s < 0 {
		return 0
	}
	if s > 65535 {
		return 65535
	}
	return uint16(s)
}

// Score returns the fraud score and approval decision for a transaction.
// Kept for test compatibility — hot path should use ScoreIdx.
func (e *Engine) Score(req *Request) (fraudScore float32, approved bool) {
	fraudCount := e.knnSearch(e.buildVector(req))
	fraudScore = float32(fraudCount) / 5
	approved = fraudScore < 0.6
	return
}

// ScoreIdx returns the raw fraud neighbor count (0–5) for use with
// a precomputed response table. Avoids the float32 divide and re-multiply.
func (e *Engine) ScoreIdx(req *Request) int {
	return e.knnSearch(e.buildVector(req))
}

// GridScoreIdx runs the fast grid-accelerated k=5 search on a Request and
// returns the fraud count (0–5). Used in tests to provide a reference result
// that matches the hot-path ParseAndScore result.
func (e *Engine) GridScoreIdx(req *Request) int {
	return e.GridScoreIdxVec(e.buildVector(req))
}

// GridScoreIdxVec runs the fast grid-accelerated k=5 search on an already-built
// [dims]uint16 vector and returns the fraud count (0–5). This is the hot-path
// entry point called from ParseAndScore.
func (e *Engine) GridScoreIdxVec(vec [dims]uint16) int {
	if e.grid == nil {
		// Fallback: brute-force k=5 (shouldn't happen in production).
		return e.knnSearch(vec)
	}
	var q [dims]int64
	for j := 0; j < dims; j++ {
		q[j] = int64(vec[j])
	}
	return e.grid.gridSearch(e.vectors, e.labels, q)
}

type candidate struct {
	dist  int64
	label uint8
}

// topK holds the running top-k candidates during a scan.
type topK struct {
	top    [k]candidate
	maxIdx int
}

func (t *topK) reset() {
	for i := range t.top {
		t.top[i].dist = math.MaxInt64
	}
	t.maxIdx = 0
}

// knnSearch returns the raw fraud neighbor count (0–5).
func (e *Engine) knnSearch(query [dims]uint16) int {
	// Pre-convert query to int64 to avoid repeated conversions in the inner loop.
	var q [dims]int64
	for j := 0; j < dims; j++ {
		q[j] = int64(query[j])
	}

	var tk topK
	tk.reset()
	searchRange(e.vectors, e.labels, q, 0, e.n, &tk)

	var fraudCount int
	for _, c := range tk.top {
		if c.label == 1 {
			fraudCount++
		}
	}
	return fraudCount
}

func searchRange(vecs []uint16, labels []uint8, q [dims]int64, start, end int, st *topK) {
	top := &st.top
	maxDist := top[st.maxIdx].dist

	for i := start; i < end; i++ {
		base := i * dims

		d0 := int64(vecs[base+0]) - q[0]
		d1 := int64(vecs[base+1]) - q[1]
		d2 := int64(vecs[base+2]) - q[2]
		d3 := int64(vecs[base+3]) - q[3]
		d4 := int64(vecs[base+4]) - q[4]
		d5 := int64(vecs[base+5]) - q[5]
		d6 := int64(vecs[base+6]) - q[6]
		d7 := int64(vecs[base+7]) - q[7]
		d8 := int64(vecs[base+8]) - q[8]
		d9 := int64(vecs[base+9]) - q[9]
		d10 := int64(vecs[base+10]) - q[10]
		d11 := int64(vecs[base+11]) - q[11]
		d12 := int64(vecs[base+12]) - q[12]
		d13 := int64(vecs[base+13]) - q[13]

		dist := d0*d0 + d1*d1 + d2*d2 + d3*d3 +
			d4*d4 + d5*d5 + d6*d6 + d7*d7 +
			d8*d8 + d9*d9 + d10*d10 + d11*d11 +
			d12*d12 + d13*d13

		if dist < maxDist {
			top[st.maxIdx] = candidate{dist, labels[i]}
			maxDist = 0
			st.maxIdx = 0
			for ki := 0; ki < k; ki++ {
				if top[ki].dist > maxDist {
					maxDist = top[ki].dist
					st.maxIdx = ki
				}
			}
		}
	}
}
