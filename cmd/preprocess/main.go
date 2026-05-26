// preprocess converts references.json.gz to a compact binary format for fast startup.
// It also reservoir-samples the input down to maxSamples vectors so the engine
// can complete a KNN search in a few milliseconds under the 0.45-CPU Docker limit.
//
// Binary layout:
//
//	[4 bytes]  uint32 N       – number of vectors
//	[N×28 B]   uint16×14 per vector (little-endian), values in [0,65535] from [-1,1]
//	[N bytes]  uint8 labels   – 0=legit, 1=fraud
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"log"
	"math/rand"
	"os"
	"time"
)

const (
	dims       = 14
	maxSamples = 500_000 // reservoir size — balances accuracy vs query latency
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

	log.Printf("sampled %d / %d vectors (%.1f%%) in %s",
		len(reservoir), total, 100*float64(len(reservoir))/float64(total), time.Since(start))

	// Write binary output.
	outf, err := os.Create(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	defer outf.Close()
	bw := bufio.NewWriterSize(outf, 1<<20)

	n := uint32(len(reservoir))
	if err := binary.Write(bw, binary.LittleEndian, n); err != nil {
		log.Fatal(err)
	}

	vecBuf := make([]byte, dims*2)
	for _, s := range reservoir {
		for j := 0; j < dims; j++ {
			binary.LittleEndian.PutUint16(vecBuf[j*2:], s.vec[j])
		}
		if _, err := bw.Write(vecBuf); err != nil {
			log.Fatal(err)
		}
	}

	for _, s := range reservoir {
		if err := bw.WriteByte(s.label); err != nil {
			log.Fatal(err)
		}
	}

	if err := bw.Flush(); err != nil {
		log.Fatal(err)
	}

	log.Printf("wrote %d vectors to %s", n, os.Args[2])
}
