// Package main is a minimal round-robin HTTP reverse proxy for the Rinha Backend
// competition. It forwards requests to two backend API instances with connection
// pooling and keep-alive, replacing nginx at a fraction of the CPU cost.
package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync/atomic"
	"time"
)

var counter atomic.Uint64

func main() {
	// Backend addresses — competition uses DNS names via docker-compose service names.
	backendAddrs := []string{
		getenv("BACKEND_1", "http://api1:8080"),
		getenv("BACKEND_2", "http://api2:8080"),
	}

	// Shared transport: reuses TCP connections to backends (keep-alive pool).
	// MaxIdleConnsPerHost=256 ensures we never run out of pooled connections under burst.
	transport := &http.Transport{
		MaxIdleConns:        512,
		MaxIdleConnsPerHost: 256,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true, // backends don't compress
	}

	// Build one ReverseProxy per backend.
	proxies := make([]*httputil.ReverseProxy, len(backendAddrs))
	for i, addr := range backendAddrs {
		u, err := url.Parse(addr)
		if err != nil {
			log.Fatalf("bad backend address %q: %v", addr, err)
		}
		p := httputil.NewSingleHostReverseProxy(u)
		p.Transport = transport

		// Suppress the default X-Forwarded-For header addition — backends don't use it
		// and it saves a string allocation per request.
		p.Director = makeDirector(u)

		// Don't log backend errors to stderr — keep stdout clean for the competition runner.
		p.ErrorLog = log.New(os.Stderr, "lb: ", 0)
		proxies[i] = p
	}

	port := getenv("PORT", "9999")

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Atomic round-robin: no mutex, no contention.
		idx := counter.Add(1) % uint64(len(proxies))
		proxies[idx].ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("lb listening on :%s → %v", port, backendAddrs)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("lb: %v", err)
	}
}

// makeDirector returns a Director that rewrites the request URL to the backend
// without adding X-Forwarded-For (backends don't need it and it saves allocs).
func makeDirector(target *url.URL) func(*http.Request) {
	return func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
		// Remove X-Forwarded-For that httputil would otherwise add.
		req.Header.Del("X-Forwarded-For")
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
