package fraud

import (
	"encoding/json"
	"testing"
)

func TestParseAndScoreMatchesScoreIdx(t *testing.T) {
	engine, err := NewEngine(
		"../../resources/example-references.json.gz",
		"../../resources/mcc_risk.json",
		"../../resources/normalization.json",
	)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	samples := []string{
		`{"id":"txn-001","transaction":{"amount":150.50,"installments":3,"requested_at":"2024-06-15T14:30:00Z"},"customer":{"avg_amount":100.0,"tx_count_24h":2,"known_merchants":["merchant-a","merchant-b"]},"merchant":{"id":"merchant-c","mcc":"5411","avg_amount":200.0},"terminal":{"is_online":true,"card_present":false,"km_from_home":5.0},"last_transaction":{"timestamp":"2024-06-15T13:00:00Z","km_from_current":2.5}}`,
		`{"id":"txn-002","transaction":{"amount":50.0,"installments":1,"requested_at":"2024-01-01T08:00:00Z"},"customer":{"avg_amount":80.0,"tx_count_24h":1,"known_merchants":["merchant-a"]},"merchant":{"id":"merchant-a","mcc":"5812","avg_amount":60.0},"terminal":{"is_online":false,"card_present":true,"km_from_home":0.5},"last_transaction":null}`,
		`{"id":"txn-003","transaction":{"amount":9999.0,"installments":12,"requested_at":"2024-12-31T23:59:00Z"},"customer":{"avg_amount":500.0,"tx_count_24h":10,"known_merchants":[]},"merchant":{"id":"unknown-merch","mcc":"9999","avg_amount":1000.0},"terminal":{"is_online":true,"card_present":false,"km_from_home":100.0},"last_transaction":null}`,
	}

	for i, raw := range samples {
		var req Request
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatalf("sample %d: json.Unmarshal: %v", i, err)
		}
		wantIdx := engine.ScoreIdx(&req)

		gotIdx, err := engine.ParseAndScore([]byte(raw))
		if err != nil {
			t.Fatalf("sample %d: ParseAndScore: %v", i, err)
		}
		if gotIdx != wantIdx {
			t.Errorf("sample %d: ParseAndScore=%d, ScoreIdx=%d", i, gotIdx, wantIdx)
		}
	}
}
