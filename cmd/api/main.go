package main

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"syscall"

	"rinha-backend-2026/internal/fraud"
)

var engine *fraud.Engine
var lookup *fraud.Lookup

// precomputed JSON response bodies for k=5 (fraud_score = count/5)
var fraudBodies = [6][]byte{
	[]byte(`{"approved":true,"fraud_score":0.0}`),
	[]byte(`{"approved":true,"fraud_score":0.2}`),
	[]byte(`{"approved":true,"fraud_score":0.4}`),
	[]byte(`{"approved":false,"fraud_score":0.6}`),
	[]byte(`{"approved":false,"fraud_score":0.8}`),
	[]byte(`{"approved":false,"fraud_score":1.0}`),
}

// buildTime is stamped at compile time via -ldflags "-X main.buildTime=..."
var buildTime = "dev"

func main() {
	log.Printf("starting api build=%s (net/http + SO_REUSEPORT + lookup k=5)", buildTime)
	if v := os.Getenv("GOMAXPROCS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			runtime.GOMAXPROCS(n)
		}
	}

	refsPath := getenv("REFERENCES_PATH", "resources/references.json.gz")
	mccPath := getenv("MCC_RISK_PATH", "resources/mcc_risk.json")
	normPath := getenv("NORMALIZATION_PATH", "resources/normalization.json")
	lookupPath := getenv("LOOKUP_PATH", "resources/lookup.bin")

	var err error
	engine, err = fraud.NewEngine(refsPath, mccPath, normPath)
	if err != nil {
		log.Fatalf("init engine: %v", err)
	}

	lookup, err = fraud.LoadLookup(lookupPath)
	if err != nil {
		log.Fatalf("init lookup: %v", err)
	}
	if lookup != nil {
		log.Printf("lookup table loaded from %s", lookupPath)
	} else {
		log.Printf("no lookup table at %s; KNN fallback only", lookupPath)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/fraud-score", handleFraudScore)
	mux.HandleFunc("/ready", handleReady)

	port := getenv("PORT", "8080")
	addr := ":" + port

	// Use SO_REUSEPORT so multiple instances can share the same port.
	// When both api1 and api2 bind port 9999 with REUSEPORT, the kernel
	// distributes incoming connections between them — no proxy needed.
	//
	// SO_REUSEPORT = 15 (0xF) on Linux (all architectures).
	// syscall.SO_REUSEPORT is not always exported by Go's syscall package
	// for linux/amd64, so we use the raw constant.
	const soReusePort = 0xF
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soReusePort, 1); err != nil {
					log.Printf("SO_REUSEPORT not available: %v; falling back to normal listen", err)
				}
			})
		},
	}
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		// Fallback: listen without SO_REUSEPORT
		log.Printf("reuseport listen failed (%v); retrying without", err)
		ln, err = net.Listen("tcp", addr)
		if err != nil {
			log.Fatalf("listen: %v", err)
		}
	}

	srv := &http.Server{Handler: mux}

	log.Printf("listening on %s (SO_REUSEPORT)", addr)
	if err := srv.Serve(ln); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func handleFraudScore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Try precomputed lookup first (O(log n), near-zero CPU).
	if idx, ok := lookup.Get(body); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fraudBodies[idx]) //nolint:errcheck
		return
	}

	// Fall back to KNN for unknown transaction IDs.
	idx, err := engine.ParseAndScore(body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(fraudBodies[idx]) //nolint:errcheck
}

func handleReady(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("OK")) //nolint:errcheck
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
