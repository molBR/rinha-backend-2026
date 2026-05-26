package fraud

import (
	"testing"
)

func BenchmarkKNNSearch(b *testing.B) {
	eng, err := NewEngine(
		"../../resources/references.json.gz",
		"../../resources/mcc_risk.json",
		"../../resources/normalization.json",
	)
	if err != nil {
		b.Fatal(err)
	}

	// Sample query vector (uint16, encoded from [-1,1] with encodeVal)
	query := [dims]uint16{}
	for j, v := range [dims]float64{
		0.039, 0.167, 0.050, 0.783, 0.167,
		-1, -1, 0.029, 0.150, 0,
		1, 0, 0.2, 0.030,
	} {
		query[j] = encodeVal(v)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		eng.knnSearch(query)
	}
}

func BenchmarkScore(b *testing.B) {
	eng, err := NewEngine(
		"../../resources/references.json.gz",
		"../../resources/mcc_risk.json",
		"../../resources/normalization.json",
	)
	if err != nil {
		b.Fatal(err)
	}

	req := &Request{
		ID: "bench",
		Transaction: Transaction{
			Amount:       384.88,
			Installments: 3,
			RequestedAt:  "2026-03-11T20:23:35Z",
		},
		Customer: Customer{
			AvgAmount:      769.76,
			TxCount24h:     3,
			KnownMerchants: []string{"MERC-009", "MERC-001"},
		},
		Merchant: Merchant{
			ID:        "MERC-001",
			MCC:       "5912",
			AvgAmount: 298.95,
		},
		Terminal: Terminal{
			IsOnline:    false,
			CardPresent: true,
			KmFromHome:  13.71,
		},
		LastTx: &LastTx{
			Timestamp:     "2026-03-11T14:58:35Z",
			KmFromCurrent: 18.86,
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		eng.Score(req)
	}
}
