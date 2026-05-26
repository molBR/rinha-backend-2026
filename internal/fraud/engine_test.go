package fraud

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

// referenceScore computes fraud score using float64 brute force (ground truth).
func referenceScore(refs [][dims]float64, refLabels []uint8, query [dims]uint16) float32 {
	type c struct {
		dist  float64
		label uint8
	}
	top := [k]c{}
	for i := range top {
		top[i].dist = math.MaxFloat64
	}
	maxDist := math.MaxFloat64
	maxIdx := 0

	for i, rv := range refs {
		var dist float64
		for j := 0; j < dims; j++ {
			// Decode query uint16 back to float64 for ground-truth comparison.
			qv := float64(query[j])/32767.5 - 1.0
			d := rv[j] - qv
			dist += d * d
		}
		if dist < maxDist {
			top[maxIdx] = c{dist, refLabels[i]}
			maxDist = 0
			maxIdx = 0
			for ki := 0; ki < k; ki++ {
				if top[ki].dist > maxDist {
					maxDist = top[ki].dist
					maxIdx = ki
				}
			}
		}
	}

	var fraudCount int
	for _, c := range top {
		if c.label == 1 {
			fraudCount++
		}
	}
	return float32(fraudCount) / 5
}

type examplePayload struct {
	ID          string      `json:"id"`
	Transaction Transaction `json:"transaction"`
	Customer    Customer    `json:"customer"`
	Merchant    Merchant    `json:"merchant"`
	Terminal    Terminal    `json:"terminal"`
	LastTx      *LastTx     `json:"last_transaction"`
}

func TestScoreMatchesReference(t *testing.T) {
	eng, err := NewEngine(
		"../../resources/example-references.json.gz",
		"../../resources/mcc_risk.json",
		"../../resources/normalization.json",
	)
	if err != nil {
		t.Fatal(err)
	}

	// Load reference vectors for float64 ground truth
	type refEntry struct {
		Vector [dims]float64 `json:"vector"`
		Label  string        `json:"label"`
	}
	rf, _ := os.Open("../../resources/example-references.json")
	defer rf.Close()
	var refEntries []refEntry
	json.NewDecoder(rf).Decode(&refEntries)
	refs := make([][dims]float64, len(refEntries))
	refLabels := make([]uint8, len(refEntries))
	for i, re := range refEntries {
		refs[i] = re.Vector
		if re.Label == "fraud" {
			refLabels[i] = 1
		}
	}

	// Load payloads
	pf, _ := os.Open("../../resources/example-payloads.json")
	defer pf.Close()
	var payloads []examplePayload
	json.NewDecoder(pf).Decode(&payloads)

	mismatches := 0
	for _, p := range payloads {
		req := &Request{
			ID:          p.ID,
			Transaction: p.Transaction,
			Customer:    p.Customer,
			Merchant:    p.Merchant,
			Terminal:    p.Terminal,
			LastTx:      p.LastTx,
		}
		gotScore, _ := eng.Score(req)
		vec := eng.buildVector(req)
		wantScore := referenceScore(refs, refLabels, vec)

		if gotScore != wantScore {
			mismatches++
			t.Logf("id=%s got=%.1f want=%.1f", p.ID, gotScore, wantScore)
		}
	}
	if mismatches > 0 {
		t.Errorf("%d/%d mismatches", mismatches, len(payloads))
	}
}
