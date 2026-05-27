package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"

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
	var req fraud.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	idx := engine.ScoreIdx(&req)
	w.Header().Set("Content-Type", "application/json")
	w.Write(precomputed[idx]) //nolint:errcheck
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
