# KNN Metrics — How to Measure and Interpret

## Current Architecture

```
POST /fraud-score
  → rawhttp.ServeConn (pooled buffer, keep-alive)
  → ServeFraudScore(body []byte)
  → engine.ParseAndScore(body)          ← fast_parse.go, ~2µs (M1 Pro)
      → engine.GridScoreIdxVec(vec)     ← grid.go, ~1.7µs (M1 Pro, 5K vecs)
          → grid.gridSearch(...)        ← 3×3 neighborhood, ~175 vectors
  → rawhttp.FraudResponse(idx)         ← precomputed []byte, zero-alloc
```

## Benchmarks (run locally)

```bash
# Grid search only, 5K vectors (matches production binary)
go test ./internal/fraud/ -bench=BenchmarkGridScoreIdxVec5K -benchmem -count=3

# Full hot path (parse + grid), 5K vectors
go test ./internal/fraud/ -bench=BenchmarkHotPath5K -benchmem -count=3

# Brute-force k=5 (comparison baseline), 5K vectors
go test ./internal/fraud/ -bench=BenchmarkKNNSearch5K -benchmem -count=3

# Full bench with 3M vectors (matches bench_test.go, slow — requires resources/references.json.gz)
go test ./internal/fraud/ -bench=BenchmarkGridScoreIdxVec$ -benchmem -count=1 -timeout=600s
```

### Expected Results (M1 Pro, arm64)

| Benchmark | Time/op | Allocs |
|-----------|---------|--------|
| GridScoreIdxVec5K | ~1.7µs | 0 |
| HotPath5K | ~3-5µs | 0 |
| KNNSearch5K (brute) | ~30µs | 0 |

## Competition Hardware Estimate

Competition server is linux/amd64, heavily throttled (0.45 CPU = CFS quota 45ms/100ms window).

Rule of thumb for CFS-throttled containers:
- Scalar Go code: ~3-5× slower than M1 Pro (different µarch, CFS scheduling jitter)
- Grid search 5K vecs: ~5-8µs expected on competition x86

At 5-8µs/query and 2 instances × 0.45 CPU:
```
Throughput = 2 × (1 / 7µs) × 0.45 = ~128,571 req/s theoretical
Test peak  = 900 req/s
KNN CPU use = 900 × 7µs / (2 × 450ms) = 0.7%
```
**KNN is not CPU-bound at 900 req/s.** If we're failing, it's a correctness issue, not throughput.

## Adding Runtime Metrics (ENABLE_TIMING=1)

To add per-request timing counters in `cmd/api/main.go`:

```go
import "sync/atomic"

var (
    totalRequests atomic.Int64
    totalNanos    atomic.Int64
    parseErrors   atomic.Int64
)

func (h *apiHandler) ServeFraudScore(body []byte) []byte {
    t0 := time.Now()
    idx, err := engine.ParseAndScore(body)
    totalNanos.Add(time.Since(t0).Nanoseconds())
    totalRequests.Add(1)
    if err != nil {
        parseErrors.Add(1)
        return rawhttp.BadRequestResponse()
    }
    return rawhttp.FraudResponse(idx)
}

// In a background goroutine (started in main):
func logMetrics() {
    if os.Getenv("ENABLE_TIMING") == "" {
        return
    }
    for range time.Tick(10 * time.Second) {
        n := totalRequests.Swap(0)
        ns := totalNanos.Swap(0)
        errs := parseErrors.Swap(0)
        if n > 0 {
            log.Printf("metrics: %d req/10s, avg=%.1fµs/req, parse_errors=%d",
                n, float64(ns)/float64(n)/1000, errs)
        }
    }
}
```

Then in docker-compose.yml for testing:
```yaml
api1:
  environment:
    - ENABLE_TIMING=1
```

## Grid Coverage Analysis

5K vectors in 16×16=256 cells:
- Average: 5000/256 ≈ 19.5 vectors/cell
- 3×3 neighborhood: up to 9 cells × 19.5 = ~175 vectors
- Min needed for k=5: 5 vectors in the neighborhood

Sparse boundary cells (e.g., extreme amount values) may have < 5 vectors total in 3×3.
The grid search returns whatever it finds even if < 5 neighbors.

**Potential accuracy issue**: if < 5 neighbors found, remaining `top[i].dist = MaxInt64`
are not counted as fraud, so fraudCount is biased toward 0 (approved).

**Fix options:**
1. Fallback to full `knnSearch` if found < 5 valid neighbors (auto-detects via dist < MaxInt64)
2. Use 8×8=64 cell grid (more vectors per cell, guaranteed k=5 coverage)
3. Increase reference vectors to 30K

## Comparing Our Grid vs Competitor (fraudctl)

| | Us (k=5 grid) | fraudctl |
|---|---|---|
| Reference vectors | 5K (sampled) | 3M (full dataset) |
| Search space | ~175 vectors (3×3 grid) | ~8K vectors (semantic partition) |
| Partition key | amount + km_from_home | semantic 8-bit (categorical) |
| Distance metric | squared Euclidean uint16 | squared Euclidean uint16 |
| SIMD | None (scalar) | AVX2 (8 vectors/cycle) |
| Accuracy | ~95%? (sampled) | ~99%+ (full dataset) |
| Latency (M1) | ~1.7µs | ~115µs (3M/8K search) |

The competitor trades latency for accuracy (uses all 3M vectors).
We trade accuracy for speed (5K sample). Since the test peak is only 900 req/s,
we have headroom to increase to 30K-100K vectors without throughput impact.
