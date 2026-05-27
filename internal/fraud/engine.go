package fraud

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
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

	if strings.HasSuffix(path, ".bin") {
		err := e.loadBinary(path)
		if err == nil {
			log.Printf("loaded %d reference vectors in %s", e.n, time.Since(start))
		}
		return err
	}
	err := e.loadJSON(path)
	if err == nil {
		log.Printf("loaded %d reference vectors in %s", e.n, time.Since(start))
	}
	return err
}

func (e *Engine) loadBinary(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<20)

	var nBuf [4]byte
	if _, err := br.Read(nBuf[:]); err != nil {
		return err
	}
	n := int(binary.LittleEndian.Uint32(nBuf[:]))

	vectors := make([]uint16, n*dims)
	if err := binary.Read(br, binary.LittleEndian, vectors); err != nil {
		return err
	}

	labels := make([]uint8, n)
	if _, err := br.Read(labels); err != nil {
		return err
	}

	e.n = n
	e.vectors = vectors
	e.labels = labels
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
