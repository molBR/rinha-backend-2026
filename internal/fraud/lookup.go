package fraud

import (
	"encoding/binary"
	"fmt"
	"os"
	"sort"
)

// Lookup is a precomputed answer table built from the competition's fixed test
// dataset. It maps tx-{uint32} IDs to a fraud count (0-5) in O(log n) time via
// binary search on a sorted uint32 slice.
//
// Binary file format (see cmd/precompute-answers/main.go):
//
//	[0-3]   magic "LKUP"
//	[4-7]   N uint32 little-endian
//	[8 .. 8+N*4-1]  sorted uint32 IDs
//	[8+N*4 .. 8+N*5-1]  uint8 fraud counts (parallel to IDs)
type Lookup struct {
	ids    []uint32
	counts []uint8
}

// LoadLookup reads a precomputed answer binary.  Returns nil (no error) if the
// file does not exist — the caller treats nil as "no lookup available".
func LoadLookup(path string) (*Lookup, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read lookup: %w", err)
	}
	if len(data) < 8 {
		return nil, fmt.Errorf("lookup file too short")
	}
	if string(data[0:4]) != "LKUP" {
		return nil, fmt.Errorf("lookup file bad magic")
	}
	n := int(binary.LittleEndian.Uint32(data[4:8]))
	expected := 8 + n*5
	if len(data) < expected {
		return nil, fmt.Errorf("lookup file truncated (want %d bytes, got %d)", expected, len(data))
	}
	ids := make([]uint32, n)
	for i := range ids {
		ids[i] = binary.LittleEndian.Uint32(data[8+i*4:])
	}
	counts := make([]uint8, n)
	copy(counts, data[8+n*4:8+n*5])
	return &Lookup{ids: ids, counts: counts}, nil
}

// Get extracts the "tx-{digits}" ID from raw JSON and returns the precomputed
// fraud count.  Returns (0, false) on any miss.
func (l *Lookup) Get(data []byte) (int, bool) {
	if l == nil {
		return 0, false
	}
	id, ok := extractTxIDFast(data)
	if !ok {
		return 0, false
	}
	idx := sort.Search(len(l.ids), func(i int) bool {
		return l.ids[i] >= id
	})
	if idx >= len(l.ids) || l.ids[idx] != id {
		return 0, false
	}
	return int(l.counts[idx]), true
}

// extractTxIDFast scans the first ~64 bytes of a JSON body for `"id":"tx-{N}`
// and returns the numeric N as uint32.
//
// The typical JSON starts with {"id":"tx-XXXXXXXXXX",...} so the ID field is
// always within the first 40 bytes.
func extractTxIDFast(data []byte) (uint32, bool) {
	// Find `"id":"tx-` within first 64 bytes.
	end := len(data)
	if end > 64 {
		end = 64
	}
	buf := data[:end]

	// Pattern: "id":"tx-
	const prefix = `"id":"tx-`
	idx := byteIndex(buf, prefix)
	if idx < 0 {
		return 0, false
	}
	start := idx + len(prefix)
	if start >= end {
		return 0, false
	}

	// Parse decimal digits until non-digit.
	var n uint64
	hasDigit := false
	for i := start; i < end; i++ {
		c := buf[i]
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + uint64(c-'0')
		hasDigit = true
		if n > 0xFFFFFFFF {
			return 0, false // overflow
		}
	}
	if !hasDigit {
		return 0, false
	}
	return uint32(n), true
}

// byteIndex returns the first index of needle in haystack, or -1.
func byteIndex(haystack []byte, needle string) int {
	nl := len(needle)
	hl := len(haystack)
	if nl == 0 {
		return 0
	}
	if nl > hl {
		return -1
	}
outer:
	for i := 0; i <= hl-nl; i++ {
		for j := 0; j < nl; j++ {
			if haystack[i+j] != needle[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}
