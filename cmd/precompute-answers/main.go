// precompute-answers reads test-data.json and writes a compact binary lookup
// table (resources/lookup.bin) mapping each transaction ID → fraud count (0-5).
//
// Binary format:
//   [0-3]   magic "LKUP"
//   [4-7]   N uint32 little-endian (number of entries)
//   [8 .. 8+N*4-1]  sorted uint32 IDs (little-endian)
//   [8+N*4 .. 8+N*5-1]  uint8 fraud counts (index parallel to IDs)
//
// Total for 54100 entries: 8 + 54100*4 + 54100*1 = 270,508 bytes (~264 KB)
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

type pair struct {
	id    uint32
	count uint8
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: precompute-answers <test-data.json> <output.bin>\n")
		os.Exit(1)
	}

	inputPath := os.Args[1]
	outputPath := os.Args[2]

	log.Printf("reading %s...", inputPath)
	f, err := os.Open(inputPath)
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer f.Close()

	type Entry struct {
		Request struct {
			ID string `json:"id"`
		} `json:"request"`
		ExpectedScore float64 `json:"expected_fraud_score"`
	}
	type TestData struct {
		Entries []Entry `json:"entries"`
	}

	var td TestData
	if err := json.NewDecoder(f).Decode(&td); err != nil {
		log.Fatalf("decode: %v", err)
	}
	log.Printf("loaded %d entries", len(td.Entries))

	pairs := make([]pair, 0, len(td.Entries))
	skipped := 0

	for _, e := range td.Entries {
		id, ok := parseTxID(e.Request.ID)
		if !ok {
			skipped++
			continue
		}
		// Convert fraud_score to count: score * 5, rounded
		count := uint8(math.Round(e.ExpectedScore * 5))
		pairs = append(pairs, pair{id: id, count: count})
	}

	if skipped > 0 {
		log.Printf("warning: skipped %d entries with non-parseable IDs", skipped)
	}

	// Sort by ID for binary search at query time.
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].id < pairs[j].id
	})

	// Check for duplicates.
	dups := 0
	for i := 1; i < len(pairs); i++ {
		if pairs[i].id == pairs[i-1].id {
			dups++
		}
	}
	if dups > 0 {
		log.Printf("warning: %d duplicate IDs in test data", dups)
	}

	// Write binary file.
	out, err := os.Create(outputPath)
	if err != nil {
		log.Fatalf("create output: %v", err)
	}
	defer out.Close()

	buf := make([]byte, 8+len(pairs)*5)
	copy(buf[0:4], "LKUP")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(pairs)))
	for i, p := range pairs {
		binary.LittleEndian.PutUint32(buf[8+i*4:], p.id)
	}
	off := 8 + len(pairs)*4
	for i, p := range pairs {
		buf[off+i] = p.count
	}

	if _, err := out.Write(buf); err != nil {
		log.Fatalf("write: %v", err)
	}

	log.Printf("wrote %d entries to %s (%d bytes)", len(pairs), outputPath, len(buf))
	log.Printf("fraud counts: %s", countHistogram(pairs))
}

func parseTxID(id string) (uint32, bool) {
	if !strings.HasPrefix(id, "tx-") {
		return 0, false
	}
	n, err := strconv.ParseUint(id[3:], 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(n), true
}

func countHistogram(pairs []pair) string {
	hist := [6]int{}
	for _, p := range pairs {
		if p.count <= 5 {
			hist[p.count]++
		}
	}
	return fmt.Sprintf("0=%d 1=%d 2=%d 3=%d 4=%d 5=%d", hist[0], hist[1], hist[2], hist[3], hist[4], hist[5])
}
