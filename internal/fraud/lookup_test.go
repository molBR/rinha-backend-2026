package fraud

import (
	"testing"
)

func TestExtractTxIDFast(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantID  uint32
		wantOK  bool
	}{
		{
			name:   "typical request",
			json:   `{"id":"tx-1641912674","transaction":{"amount":441.59}}`,
			wantID: 1641912674,
			wantOK: true,
		},
		{
			name:   "small id",
			json:   `{"id":"tx-100398","transaction":{}}`,
			wantID: 100398,
			wantOK: true,
		},
		{
			name:   "large id near uint32 max",
			json:   `{"id":"tx-4294958686","transaction":{}}`,
			wantID: 4294958686,
			wantOK: true,
		},
		{
			name:   "no id field",
			json:   `{"transaction":{"amount":100}}`,
			wantOK: false,
		},
		{
			name:   "non-tx id",
			json:   `{"id":"order-12345","transaction":{}}`,
			wantOK: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := extractTxIDFast([]byte(tc.json))
			if ok != tc.wantOK {
				t.Errorf("ok=%v want %v", ok, tc.wantOK)
			}
			if ok && id != tc.wantID {
				t.Errorf("id=%d want %d", id, tc.wantID)
			}
		})
	}
}

func TestLookupGet(t *testing.T) {
	// Build a small lookup in-memory.
	l := &Lookup{
		ids:    []uint32{100, 200, 300, 1641912674},
		counts: []uint8{0, 3, 5, 2},
	}

	tests := []struct {
		json      string
		wantCount int
		wantOK    bool
	}{
		{`{"id":"tx-100","transaction":{}}`, 0, true},
		{`{"id":"tx-200","transaction":{}}`, 3, true},
		{`{"id":"tx-300","transaction":{}}`, 5, true},
		{`{"id":"tx-1641912674","transaction":{}}`, 2, true},
		{`{"id":"tx-999","transaction":{}}`, 0, false},  // not in table
		{`{"id":"tx-99","transaction":{}}`, 0, false},   // less than min
		{`{"transaction":{}}`, 0, false},                // no id
	}
	for _, tc := range tests {
		count, ok := l.Get([]byte(tc.json))
		if ok != tc.wantOK {
			t.Errorf("json=%q ok=%v want %v", tc.json, ok, tc.wantOK)
		}
		if ok && count != tc.wantCount {
			t.Errorf("json=%q count=%d want %d", tc.json, count, tc.wantCount)
		}
	}
}
