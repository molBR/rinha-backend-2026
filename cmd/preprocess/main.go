// preprocess converts references.json.gz to a compact binary format for fast startup.
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
	"os"
	"time"
)

const dims = 14

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

	outf, err := os.Create(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}
	defer outf.Close()
	bw := bufio.NewWriterSize(outf, 1<<20)

	// Reserve 4 bytes for N
	nPlaceholder := make([]byte, 4)
	if _, err := bw.Write(nPlaceholder); err != nil {
		log.Fatal(err)
	}

	type entry struct {
		Vector [dims]float64 `json:"vector"`
		Label  string        `json:"label"`
	}

	dec := json.NewDecoder(gz)
	if _, err := dec.Token(); err != nil { // consume '['
		log.Fatal(err)
	}

	log.Println("converting references...")
	start := time.Now()

	var n uint32
	vecBuf := make([]byte, dims*2)
	var e entry
	var labels []byte

	for dec.More() {
		if err := dec.Decode(&e); err != nil {
			log.Fatal(err)
		}
		for j := 0; j < dims; j++ {
			binary.LittleEndian.PutUint16(vecBuf[j*2:], encodeVal(e.Vector[j]))
		}
		if _, err := bw.Write(vecBuf); err != nil {
			log.Fatal(err)
		}
		if e.Label == "fraud" {
			labels = append(labels, 1)
		} else {
			labels = append(labels, 0)
		}
		n++
	}

	if _, err := bw.Write(labels); err != nil {
		log.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		log.Fatal(err)
	}

	// Write N at the start
	if _, err := outf.WriteAt(binary.LittleEndian.AppendUint32(nil, n), 0); err != nil {
		log.Fatal(err)
	}

	log.Printf("wrote %d vectors in %s", n, time.Since(start))
}
