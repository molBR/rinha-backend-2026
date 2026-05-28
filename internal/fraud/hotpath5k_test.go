package fraud

import (
	"os"
	"testing"
)

func BenchmarkHotPath5K(b *testing.B) {
	// Use TEST_VECTOR_REPEAT=50 so NewEngine inflates 100→5000 vectors and
	// builds the grid AFTER inflation (matches production behaviour with
	// the 5K preprocessed binary).
	os.Setenv("TEST_VECTOR_REPEAT", "50")
	defer os.Unsetenv("TEST_VECTOR_REPEAT")

	eng, err := NewEngine(
		"../../resources/example-references.json.gz",
		"../../resources/mcc_risk.json",
		"../../resources/normalization.json",
	)
	if err != nil {
		b.Fatal(err)
	}
	if eng.n != 5000 {
		b.Fatalf("expected 5000 vectors, got %d", eng.n)
	}

	body := []byte(`{"id":"b3d5f6e8-4a7c-4d2e-9f1b-0e8c3a5d7f90","transaction":{"amount":384.88,"installments":3,"requested_at":"2026-03-11T20:23:35Z"},"customer":{"avg_amount":769.76,"tx_count_24h":3,"known_merchants":["MERC-009","MERC-001"]},"merchant":{"id":"MERC-001","mcc":"5912","avg_amount":298.95},"terminal":{"is_online":false,"card_present":true,"km_from_home":13.71},"last_transaction":{"timestamp":"2026-03-11T14:58:35Z","km_from_current":18.86}}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		eng.ParseAndScore(body)
	}
}

func BenchmarkGridScoreIdxVec5K(b *testing.B) {
	// Grid search only, with 5K vectors (no JSON parsing overhead).
	os.Setenv("TEST_VECTOR_REPEAT", "50")
	defer os.Unsetenv("TEST_VECTOR_REPEAT")

	eng, err := NewEngine(
		"../../resources/example-references.json.gz",
		"../../resources/mcc_risk.json",
		"../../resources/normalization.json",
	)
	if err != nil {
		b.Fatal(err)
	}
	if eng.n != 5000 {
		b.Fatalf("expected 5000 vectors, got %d", eng.n)
	}

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
		eng.GridScoreIdxVec(query)
	}
}

func BenchmarkKNNSearch5K(b *testing.B) {
	// Brute-force k=5 search with 5K vectors, for comparison.
	os.Setenv("TEST_VECTOR_REPEAT", "50")
	defer os.Unsetenv("TEST_VECTOR_REPEAT")

	eng, err := NewEngine(
		"../../resources/example-references.json.gz",
		"../../resources/mcc_risk.json",
		"../../resources/normalization.json",
	)
	if err != nil {
		b.Fatal(err)
	}
	if eng.n != 5000 {
		b.Fatalf("expected 5000 vectors, got %d", eng.n)
	}

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
