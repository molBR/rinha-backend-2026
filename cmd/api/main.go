package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"

	"rinha-backend-2026/internal/fraud"
)

// precomputed holds the 6 possible JSON responses indexed by fraud neighbor count (0–5).
// Avoids json.Marshal on the hot path.
var precomputed = [6][]byte{
	[]byte(`{"approved":true,"fraud_score":0.0}`),
	[]byte(`{"approved":true,"fraud_score":0.2}`),
	[]byte(`{"approved":true,"fraud_score":0.4}`),
	[]byte(`{"approved":false,"fraud_score":0.6}`),
	[]byte(`{"approved":false,"fraud_score":0.8}`),
	[]byte(`{"approved":false,"fraud_score":1.0}`),
}

// bodyPool reuses read buffers to avoid per-request allocation for request bodies.
// Competition payloads are ~500–800 bytes; 4096 is always sufficient.
var bodyPool = sync.Pool{
	New: func() any {
		b := make([]byte, 4096)
		return &b
	},
}

var engine *fraud.Engine

func main() {
	if v := os.Getenv("GOMAXPROCS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			runtime.GOMAXPROCS(n)
		}
	}

	refsPath := getenv("REFERENCES_PATH", "resources/references.json.gz")
	mccPath := getenv("MCC_RISK_PATH", "resources/mcc_risk.json")
	normPath := getenv("NORMALIZATION_PATH", "resources/normalization.json")

	var err error
	engine, err = fraud.NewEngine(refsPath, mccPath, normPath)
	if err != nil {
		log.Fatalf("init engine: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", readyHandler)
	mux.HandleFunc("POST /fraud-score", fraudScoreHandler)

	port := getenv("PORT", "8080")
	log.Printf("listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

func readyHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func fraudScoreHandler(w http.ResponseWriter, r *http.Request) {
	// Read body into pooled buffer — avoids allocation for the common case.
	bufPtr := bodyPool.Get().(*[]byte)
	buf := *bufPtr
	n, err := io.ReadFull(r.Body, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		// Body larger than 4096 — fall back (shouldn't happen in practice).
		bodyPool.Put(bufPtr)
		rest, err2 := io.ReadAll(r.Body)
		if err2 != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		data := append(buf[:n], rest...)
		idx, parseErr := engine.ParseAndScore(data)
		if parseErr != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(precomputed[idx]) //nolint:errcheck
		return
	}

	body := buf[:n]
	idx, parseErr := engine.ParseAndScore(body)
	bodyPool.Put(bufPtr)
	if parseErr != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(precomputed[idx]) //nolint:errcheck
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
