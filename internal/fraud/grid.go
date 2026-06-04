// Package fraud — grid-accelerated approximate KNN.
//
// GridIndex partitions the reference set on 2 dimensions (amount=dim0,
// km_from_home=dim7) into a 16×16 = 256 cell grid using quantile-based
// splits (each cell holds ~N/256 vectors). A query searches the 3×3
// neighbourhood (~9 cells, ≈175 vectors out of 5 K) instead of the full
// dataset, giving ~29× speedup while preserving approximate k-NN quality.
//
// k=5 matches the competition spec exactly: approval threshold < 0.6 means
// fraudCount ≤ 2 approved, ≥ 3 rejected. fraud_score = fraudCount/5.
//
// CACHE-FRIENDLY LAYOUT: vectors are reordered at build time so every cell's
// vectors are stored contiguously in engine.vectors. A 3×3 neighbourhood
// (~175 vectors, 4.9 KB) fits entirely in L1 cache (32 KB on competition x86),
// turning 175 scattered cache-miss loads into 9 sequential scans.
package fraud

import (
	"math"
	"sort"
)

const (
	gridDim0  = 0               // partition axis 0: amount
	gridDim1  = 7               // partition axis 1: km_from_home
	gridSize  = 32              // buckets per axis (32×32=1024 cells for 1.5M vectors, ~1465/cell)
	gridTotal = gridSize * gridSize // 1024 cells

	kGrid = 5 // k for grid-backed search (matches competition spec k=5)
)

// GridIndex stores cell boundary metadata for the contiguous-layout grid.
// After buildGrid, engine.vectors and engine.labels are reordered so that
// all vectors for cell i occupy positions [start[i], start[i+1]).
// No data[]int32 indirection — grid search reads vectors sequentially.
type GridIndex struct {
	start   [gridTotal + 1]int32  // start[i] = first vector index for cell i
	splits0 [gridSize - 1]uint16  // quantile boundaries for dim gridDim0
	splits1 [gridSize - 1]uint16  // quantile boundaries for dim gridDim1
}

// buildGrid reorders *vecsp and *labelsp in-place by grid cell, then returns
// the GridIndex describing the new layout. After this call, cell i occupies
// (*vecsp)[start[i]*dims : start[i+1]*dims] sequentially in memory.
func buildGrid(vecsp *[]uint16, labelsp *[]uint8, n int) *GridIndex {
	vectors := *vecsp
	labels := *labelsp
	g := &GridIndex{}

	// Collect raw values for split computation.
	v0 := make([]uint16, n)
	v1 := make([]uint16, n)
	for i := 0; i < n; i++ {
		v0[i] = vectors[i*dims+gridDim0]
		v1[i] = vectors[i*dims+gridDim1]
	}

	// Sort to find quantile boundaries.
	sort.Slice(v0, func(a, b int) bool { return v0[a] < v0[b] })
	sort.Slice(v1, func(a, b int) bool { return v1[a] < v1[b] })
	for i := range g.splits0 {
		g.splits0[i] = v0[(i+1)*n/gridSize]
		g.splits1[i] = v1[(i+1)*n/gridSize]
	}

	// Assign each vector to a cell and count per-cell sizes.
	cellOf := make([]int, n)
	var cnt [gridTotal]int32
	for i := 0; i < n; i++ {
		c := gridCellOf(vectors[i*dims+gridDim0], vectors[i*dims+gridDim1], g)
		cellOf[i] = c
		cnt[c]++
	}

	// Compute start offsets (prefix sum).
	g.start[0] = 0
	for i := 0; i < gridTotal; i++ {
		g.start[i+1] = g.start[i] + cnt[i]
	}

	// Reorder vectors and labels into cell-contiguous layout.
	// Each cell's vectors will be stored sequentially, so a 3×3 neighbourhood
	// search reads ~175 vectors from at most 3 discontiguous spans of ~60 vectors
	// each — all fitting in L1 cache on competition hardware (32 KB L1).
	newVecs := make([]uint16, n*dims)
	newLabels := make([]uint8, n)
	pos := [gridTotal]int32{}
	copy(pos[:], g.start[:gridTotal])
	for i := 0; i < n; i++ {
		c := cellOf[i]
		dest := int(pos[c])
		copy(newVecs[dest*dims:(dest+1)*dims], vectors[i*dims:(i+1)*dims])
		newLabels[dest] = labels[i]
		pos[c]++
	}

	*vecsp = newVecs
	*labelsp = newLabels

	return g
}

// gridCellOf returns the flat cell index for the given pair of dim values.
func gridCellOf(d0, d1 uint16, g *GridIndex) int {
	return gridRank(d0, g.splits0[:])*gridSize + gridRank(d1, g.splits1[:])
}

// gridRank binary-searches splits for the bucket index of value v (0..gridSize-1).
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

// ── k=5 top-k tracker ────────────────────────────────────────────────────────

// topK5 tracks the kGrid=5 closest candidates seen during a scan.
type topK5 struct {
	top    [kGrid]candidate
	maxIdx int
}

func (t *topK5) reset() {
	for i := range t.top {
		t.top[i].dist = math.MaxInt64
	}
	t.maxIdx = 0
}

func (t *topK5) update(dist int64, label uint8) {
	if dist >= t.top[t.maxIdx].dist {
		return
	}
	t.top[t.maxIdx] = candidate{dist, label}
	t.maxIdx = 0
	for i := 1; i < kGrid; i++ {
		if t.top[i].dist > t.top[t.maxIdx].dist {
			t.maxIdx = i
		}
	}
}

func (t *topK5) fraudCount() int {
	n := 0
	for _, c := range t.top {
		if c.label == 1 {
			n++
		}
	}
	return n
}

// presortedGridIndex builds a GridIndex from vectors that are already sorted
// by grid cell (as written by the preprocess binary in GRD2 format).
// This avoids the 2× memory spike of buildGrid's reorder step — no new
// vector/label arrays are allocated; only the 4KB start[] table is computed
// via a single sequential scan of the already-sorted vectors.
func presortedGridIndex(splits0, splits1 [gridSize - 1]uint16, vectors []uint16, n int) *GridIndex {
	g := &GridIndex{
		splits0: splits0,
		splits1: splits1,
	}
	var cnt [gridTotal]int32
	for i := 0; i < n; i++ {
		d0 := vectors[i*dims+gridDim0]
		d1 := vectors[i*dims+gridDim1]
		c := gridRank(d0, g.splits0[:])*gridSize + gridRank(d1, g.splits1[:])
		cnt[c]++
	}
	g.start[0] = 0
	for i := 0; i < gridTotal; i++ {
		g.start[i+1] = g.start[i] + cnt[i]
	}
	return g
}

// ── Grid search ───────────────────────────────────────────────────────────────

// gridSearch scans the 3×3 cell neighbourhood of the query using sequential
// memory access (vectors are cell-contiguous after buildGrid). Returns the
// fraud count among the kGrid=5 nearest neighbours found.
// q must already be pre-converted to int64.
func (g *GridIndex) gridSearch(vectors []uint16, labels []uint8, q [dims]int64) int {
	qr0 := gridRank(uint16(q[gridDim0]), g.splits0[:])
	qr1 := gridRank(uint16(q[gridDim1]), g.splits1[:])

	var tk topK5
	tk.reset()

	for dr0 := -1; dr0 <= 1; dr0++ {
		r0 := qr0 + dr0
		if r0 < 0 || r0 >= gridSize {
			continue
		}
		for dr1 := -1; dr1 <= 1; dr1++ {
			r1 := qr1 + dr1
			if r1 < 0 || r1 >= gridSize {
				continue
			}
			cell := r0*gridSize + r1
			from := int(g.start[cell])
			to := int(g.start[cell+1])
			// Sequential scan — all vectors for this cell are contiguous in memory.
			// No g.data[] indirection; hardware prefetcher works perfectly.
			for i := from; i < to; i++ {
				base := i * dims
				d0 := int64(vectors[base+0]) - q[0]
				d1 := int64(vectors[base+1]) - q[1]
				d2 := int64(vectors[base+2]) - q[2]
				d3 := int64(vectors[base+3]) - q[3]
				d4 := int64(vectors[base+4]) - q[4]
				d5 := int64(vectors[base+5]) - q[5]
				d6 := int64(vectors[base+6]) - q[6]
				d7 := int64(vectors[base+7]) - q[7]
				d8 := int64(vectors[base+8]) - q[8]
				d9 := int64(vectors[base+9]) - q[9]
				d10 := int64(vectors[base+10]) - q[10]
				d11 := int64(vectors[base+11]) - q[11]
				d12 := int64(vectors[base+12]) - q[12]
				d13 := int64(vectors[base+13]) - q[13]
				dist := d0*d0 + d1*d1 + d2*d2 + d3*d3 +
					d4*d4 + d5*d5 + d6*d6 + d7*d7 +
					d8*d8 + d9*d9 + d10*d10 + d11*d11 +
					d12*d12 + d13*d13
				tk.update(dist, labels[i])
			}
		}
	}

	return tk.fraudCount()
}
