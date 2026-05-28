// loadtest: measures throughput of the fraud-score API under competition-like burst load.
//
// Usage:
//   go run ./cmd/loadtest [--addr :9999] [--concurrency 100] [--requests 1000]
//
// Sends all requests concurrently (like the competition harness does) and reports:
// - Total successes / errors
// - Requests completed within 2s (the competition window)
// - p50/p95/p99/max latency
// - Overall req/s
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
)

func main() {
	addr := flag.String("addr", "http://localhost:9999", "target base URL")
	concurrency := flag.Int("concurrency", 200, "number of concurrent requests")
	requests := flag.Int("requests", 1000, "total number of requests")
	payloadFile := flag.String("payloads", "resources/example-payloads.json", "JSON file with example payloads")
	timeout := flag.Duration("timeout", 5*time.Second, "per-request timeout")
	burst := flag.Bool("burst", false, "fire all requests simultaneously (competition mode)")
	flag.Parse()

	// Load payloads
	data, err := os.ReadFile(*payloadFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read payloads: %v\n", err)
		os.Exit(1)
	}
	var payloads []json.RawMessage
	if err := json.Unmarshal(data, &payloads); err != nil {
		fmt.Fprintf(os.Stderr, "parse payloads: %v\n", err)
		os.Exit(1)
	}
	if len(payloads) == 0 {
		fmt.Fprintln(os.Stderr, "no payloads")
		os.Exit(1)
	}
	fmt.Printf("Loaded %d example payloads\n", len(payloads))

	client := &http.Client{
		Timeout: *timeout,
		Transport: &http.Transport{
			MaxIdleConnsPerHost: *concurrency + 10,
			// Competition harness does NOT send Connection: close — fresh TCP
			// connections are established but keep-alive is the default.
			// DisableKeepAlives: true causes Connection:close header which
			// creates a RST-race with our rawhttp server.
		},
	}

	url := *addr + "/fraud-score"
	latencies := make([]time.Duration, *requests)
	errs := make([]error, *requests)

	var wg sync.WaitGroup
	sem := make(chan struct{}, *concurrency)

	start := time.Now()

	if *burst {
		// Fire all requests simultaneously (true burst, matches competition harness)
		fmt.Printf("BURST MODE: firing %d requests simultaneously...\n", *requests)
		var ready sync.WaitGroup
		ready.Add(1)
		for i := 0; i < *requests; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				body := payloads[idx%len(payloads)]
				ready.Wait() // all goroutines wait until we signal
				t0 := time.Now()
				resp, err := client.Post(url, "application/json", bytes.NewReader(body))
				latencies[idx] = time.Since(t0)
				if err != nil {
					errs[idx] = err
					return
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 {
					errs[idx] = fmt.Errorf("status %d", resp.StatusCode)
				}
			}(i)
		}
		time.Sleep(10 * time.Millisecond) // let all goroutines reach the wait
		ready.Done()                       // release the burst
	} else {
		// Sliding window concurrency
		for i := 0; i < *requests; i++ {
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int) {
				defer wg.Done()
				defer func() { <-sem }()
				body := payloads[idx%len(payloads)]
				t0 := time.Now()
				resp, err := client.Post(url, "application/json", bytes.NewReader(body))
				latencies[idx] = time.Since(t0)
				if err != nil {
					errs[idx] = err
					return
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 {
					errs[idx] = fmt.Errorf("status %d", resp.StatusCode)
				}
			}(i)
		}
	}

	wg.Wait()
	elapsed := time.Since(start)

	// Analyze results
	var successes, errors int
	var within2s int
	var validLat []time.Duration
	for i := 0; i < *requests; i++ {
		if errs[i] != nil {
			errors++
		} else {
			successes++
			if latencies[i] <= 2*time.Second {
				within2s++
			}
			validLat = append(validLat, latencies[i])
		}
	}

	sort.Slice(validLat, func(i, j int) bool { return validLat[i] < validLat[j] })

	pct := func(p float64) time.Duration {
		if len(validLat) == 0 {
			return 0
		}
		idx := int(math.Ceil(p/100.0*float64(len(validLat)))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(validLat) {
			idx = len(validLat) - 1
		}
		return validLat[idx]
	}

	fmt.Printf("\n=== Results ===\n")
	fmt.Printf("Total requests:    %d\n", *requests)
	fmt.Printf("Successes:         %d (%.1f%%)\n", successes, 100*float64(successes)/float64(*requests))
	fmt.Printf("Errors:            %d\n", errors)
	fmt.Printf("Within 2s window:  %d (competition score)\n", within2s)
	fmt.Printf("Elapsed:           %v\n", elapsed.Round(time.Millisecond))
	fmt.Printf("Throughput:        %.0f req/s\n", float64(successes)/elapsed.Seconds())
	if len(validLat) > 0 {
		fmt.Printf("\nLatency (successful requests):\n")
		fmt.Printf("  p50: %v\n", pct(50).Round(time.Microsecond))
		fmt.Printf("  p95: %v\n", pct(95).Round(time.Microsecond))
		fmt.Printf("  p99: %v\n", pct(99).Round(time.Microsecond))
		fmt.Printf("  max: %v\n", validLat[len(validLat)-1].Round(time.Microsecond))
		fmt.Printf("  min: %v\n", validLat[0].Round(time.Microsecond))
	}

	// Show first few errors
	shown := 0
	for i := 0; i < *requests && shown < 3; i++ {
		if errs[i] != nil {
			fmt.Printf("  sample error: %v\n", errs[i])
			shown++
		}
	}
}
