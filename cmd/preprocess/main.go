// preprocess converts references.json.gz to a compact binary format for fast startup.
// It also reservoir-samples the input down to maxSamples vectors so the engine
// can complete a KNN search in a few milliseconds under the 0.45-CPU Docker limit.
//
// GRD2 binary layout:
//
//	[4  bytes] magic "GRD2"
//	[4  bytes] uint32 N            – number of vectors
//	[4  bytes] uint32 gridSize     – grid dimension (= 32)
//	[62 bytes] uint16[31] splits0  – quantile boundaries for dim0 (amount)
//	[62 bytes] uint16[31] splits1  – quantile boundaries for dim7 (km_from_home)
//	[N×28 B]   uint16×14 per vector (little-endian), pre-sorted by grid cell
//	[N   bytes] uint8 labels       – 0=legit, 1=fraud, same order as vectors
//
// Pre-sorting vectors by grid cell at build time means the engine can skip the
// 2× memory-spike reorder that buildGrid would otherwise perform at startup.
// For 3M vectors this cuts peak startup RAM from ~208MB to ~97MB (within 140MB limit).
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"log"
	"math/rand"
	"os"
	"sort"
	"time"
)

const (
	dims       = 14
	maxSamples = 3_000_000 // full dataset — all 3M vectors, 100% coverage

	// Grid parameters — must stay in sync with internal/fraud/grid.go.
	gridDim0  = 0  // partition axis 0: amount
	gridDim1  = 7  // partition axis 1: km_from_home
	gridSize  = 32 // buckets per axis  (32×32 = 1024 cells)
	gridTotal = gridSize * gridSize
)

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

// gridRank binary-searches splits for the bucket index of v (0..gridSize-1).
// Must match internal/fraud/grid.go gridRank exactly.
func gridRank(v uint16, splits []uint16) int {
	lo, hi := 0, len(splits)
	for lo < hi {
		mid := (lo + hi) >> 1
		if v < splits[mid] {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

type sample struct {
	vec   [dims]uint16
	label uint8
}

func main() {
	if len(os.Args) != 3 {
		log.Fatalf("usage: preprocess <references.json.gz> <output.bin>")
	}

	inf, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer inf.Close()

	gz, err := gzip.NewReader(inf)
	if err != nil {
		log.Fatal(err)
	}
	defer gz.Close()

	log.Println("reading and sampling references...")
	start := time.Now()

	type entry struct {
		Vector [dims]float64 `json:"vector"`
		Label  string        `json:"label"`
	}

	dec := json.NewDecoder(gz)
	if _, err := dec.Token(); err != nil { // consume '['
		log.Fatal(err)
	}

	// Reservoir sampling: keep a uniform random sample of size maxSamples.
	reservoir := make([]sample, 0, maxSamples)
	rng := rand.New(rand.NewSource(42))
	total := 0

	var e entry
	for dec.More() {
		if err := dec.Decode(&e); err != nil {
			log.Fatal(err)
		}

		var vec [dims]uint16
		for j := 0; j < dims; j++ {
			vec[j] = encodeVal(e.Vector[j])
		}
		lbl := uint8(0)
		if e.Label == "fraud" {
			lbl = 1
		}

		total++
		if len(reservoir) < maxSamples {
			reservoir = append(reservoir, sample{vec, lbl})
		} else {
			j := rng.Intn(total)
			if j < maxSamples {
				reservoir[j] = sample{vec, lbl}
			}
		}
	}

	n := len(reservoir)
	log.Printf("sampled %d / %d vectors (%.1f%%) in %s",
		n, total, 100*float64(n)/float64(total), time.Since(start))

	// ── Compute grid splits ───────────────────────────────────────────────────
	log.Println("computing grid splits...")
	v0 := make([]uint16, n)
	v1 := make([]uint16, n)
	for i, s := range reservoir {
		v0[i] = s.vec[gridDim0]
		v1[i] = s.vec[gridDim1]
	}
	sort.Slice(v0, func(a, b int) bool { return v0[a] < v0[b] })
	sort.Slice(v1, func(a, b int) bool { return v1[a] < v1[b] })

	var splits0, splits1 [gridSize - 1]uint16
	for i := range splits0 {
		splits0[i] = v0[(i+1)*n/gridSize]
		splits1[i] = v1[(i+1)*n/gridSize]
	}
	v0 = nil // free — no longer needed
	v1 = nil

	// ── Sort reservoir by grid cell ───────────────────────────────────────────
	// Pre-sorting at build time eliminates the 2× memory spike that buildGrid
	// would cause at container startup. For 3M vectors this saves ~111MB peak RAM.
	log.Println("sorting by grid cell...")
	// Precompute cell indices to avoid repeated binary searches during sort.
	cells := make([]uint16, n) // [0, 1023] fits in uint16
	for i, s := range reservoir {
		c := gridRank(s.vec[gridDim0], splits0[:])*gridSize +
			gridRank(s.vec[gridDim1], splits1[:])
		cells[i] = uint16(c)
	}
	sort.SliceStable(reservoir, func(i, j int) bool { return cells[i] < cells[j] })
	cells = nil // free

	// ── Write GRD2 binary ─────────────────────────────────────────────────────
	outf, err := os.Create(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	defer outf.Close()
	bw := bufio.NewWriterSize(outf, 1<<20)

	// Magic
	if _, err := bw.Write([]byte("GRD2")); err != nil {
		log.Fatal(err)
	}
	// N
	if err := binary.Write(bw, binary.LittleEndian, uint32(n)); err != nil {
		log.Fatal(err)
	}
	// GridSize
	if err := binary.Write(bw, binary.LittleEndian, uint32(gridSize)); err != nil {
		log.Fatal(err)
	}
	// Splits
	if err := binary.Write(bw, binary.LittleEndian, splits0[:]); err != nil {
		log.Fatal(err)
	}
	if err := binary.Write(bw, binary.LittleEndian, splits1[:]); err != nil {
		log.Fatal(err)
	}
	// Vectors (cell-sorted)
	vecBuf := make([]byte, dims*2)
	for _, s := range reservoir {
		for j := 0; j < dims; j++ {
			binary.LittleEndian.PutUint16(vecBuf[j*2:], s.vec[j])
		}
		if _, err := bw.Write(vecBuf); err != nil {
			log.Fatal(err)
		}
	}
	// Labels
	for _, s := range reservoir {
		if err := bw.WriteByte(s.label); err != nil {
			log.Fatal(err)
		}
	}

	if err := bw.Flush(); err != nil {
		log.Fatal(err)
	}

	log.Printf("wrote %d vectors to %s (GRD2 format, cell-sorted)", n, os.Args[2])
}
