# Rinha Backend 2026 — Optimization Progress

## Competition Test Format
- k6 `ramping-arrival-rate`: 0 → 900 req/s over 120 seconds
- Total: 54,100 requests (earlier rounds: ~13,830 — see explanation below)
- Per-request timeout: 2001ms
- Target: linux/amd64, bridge network, Docker resource limits
- **Scoring**: `ε = (fp + 3×fn + 5×err) / N`. Score = 1000×log₁₀(1/ε) + latency_score.
  - `err` = HTTP errors (`res.status !== 200`), weighted 5×
  - `fn` = missed fraud (approved=true when expected=false), weighted 3×
  - `fp` = false alarm (approved=false when expected=true), weighted 1×
  - Failure rate > 15% → hard -3000

## Resource Budget
- LB:  0.1 CPU, 30 MB RAM
- API1: 0.45 CPU, 140 MB RAM
- API2: 0.45 CPU, 140 MB RAM

## Progress Table

| Round  | Issue  | Total  | Successes | Non-success | Notes                                        |
|--------|--------|--------|-----------|-------------|----------------------------------------------|
| R1     | #6998  | 13,830 | 123       | 13,707      | 5K multi-arch, nginx LB                      |
| R2     | #7007  | 13,830 | 8         | 13,822      | 1K regression (wrong accuracy)               |
| Step1  | #7015  | 13,830 | 126       | 13,703      | GOMEMLIMIT+precomputed+GOAMD64v3 + nginx      |
| Step2  | #7017  | —      | REJECTED  | —           | build: not allowed                           |
| Step3  | #7018  | —      | REJECTED  | —           | build: not allowed                           |
| Pre-LB | #7019  | 13,830 | 121       | 13,709      | Go LB (httputil) — same as nginx             |
| SCM    | #7027  | ?      | ?         | ?           | First rawhttp + SCM_RIGHTS submission        |
| Grid   | #7066  | 54,100 | ~541      | ~53,559     | k=3 grid, wrong `approved` threshold         |
| k5+700K| #7154  | TBD    | TBD       | TBD         | k=5 fix + 700K vectors, 32×32 grid           |
| 1.5M   | TBD    | TBD    | TBD       | TBD         | 1.5M vectors (50% coverage), same k=5 grid  |

## Root Cause Analysis — Why Results Didn't Change Across R1-Pre-LB

### The 13,830 Ceiling

With `ramping-arrival-rate`, `maxVUs=250`, and `timeout=2001ms`:
- If requests timeout (2001ms wait): VU pool fills up with waiting VUs
- Effective rate = 250 VUs / 2001ms ≈ 124.9 req/s (limited by VU pool)
- Total in 120s = ∫₀^120 min(7.5t, 124.9) dt ≈ 1,039 + 103.35 × 124.9 ≈ **13,830 iterations**

If requests fail FAST (connection refused, immediate error):
- VUs cycle quickly, k6 can sustain target arrival rate
- Total = ∫₀^120 7.5t dt = **54,100 iterations**

**N=13,830 means the service was NOT responding** (requests timed out after 2001ms). N=54,100 means the service responded quickly (even if with wrong answers).

### Why R1-Pre-LB Had Timeouts

R1-Step1: nginx reverse proxy holds connections open waiting for a slow backend.
- nginx default proxy_read_timeout=60s → nginx waits up to 60s for API response
- k6 VU times out at 2001ms
- API responds slowly OR API starts slow (loading JSON.gz = minutes on 0.45 CPU)

Pre-LB: httputil.ReverseProxy has similar behavior.

**Fix**: rawhttp server + SCM_RIGHTS LB responds in <1ms, so VUs never timeout.
**Evidence**: After Grid #7066, N jumped to 54,100 (all requests executed).

### Why #7066 Had 99% Non-Successes

k=3 grid with 5K reference vectors:
1. **k=3 wrong threshold**: approval = fraudCount/3 < 0.6 → fraudCount ≤ 1
   - Competition expects k=5: fraudCount/5 < 0.6 → fraudCount ≤ 2
   - Any transaction with fraudCount=2 (legit by k=5 standard) gets rejected by k=3
2. **5K/3M = 0.17% sampling**: approximate k-3 rarely matches true k-5 NN
   - ~99% of queries give wrong `approved` decision

Note: k6 test ONLY checks `body.approved` (boolean), NOT `body.fraud_score`. The fraud_score value doesn't matter for scoring.

### What #7154 Should Fix

| Parameter      | #7066 (broken) | #7154 (fixed)   |
|----------------|----------------|-----------------|
| k              | 3              | 5 ✓             |
| Reference vecs | 5K (0.17%)     | 700K (23%)      |
| Threshold      | ≤1/3 fraud     | ≤2/5 fraud ✓    |
| Parse errors   | Returns idx=0  | Returns 400 ✓   |
| Grid size      | 32×32 (same)   | 32×32           |

Expected accuracy: ~70-80% correct `approved` decisions (23% sample coverage).

## Current Architecture State

```
POST :9999/fraud-score
  → SCM_RIGHTS LB (5µs/conn, FD passing)
  → rawhttp.ServeConn (pooled 4KB buffer, keep-alive)
  → ParseAndScore(body)           ← fast_parse.go, ~3µs
      → GridScoreIdxVec(vec)      ← grid.go, ~150µs on x86 (700K vectors)
          → gridSearch(3×3 cells) ← ~6,147 vectors scanned
  → FraudResponse(idx)            ← precomputed []byte, zero-alloc
```

## Throughput Budget Analysis (1.5M vectors, x86 competition hardware)

- Grid search: ~42µs/M1 × 3× x86 factor = **~267µs per query**
- 2 instances × (1/267µs) × 0.45 CPU = **3,371 req/s theoretical max**
- Test peak: 900 req/s
- CPU usage: 450 req/s × 267µs = 120ms/s = **26.7% of 450ms budget** per instance
- **KNN is NOT the bottleneck.** Accuracy is the primary concern.

## Reference Vector Strategy

| Vectors | Coverage | Query time (x86) | CPU at 900 req/s | Accuracy (est) |
|---------|----------|-----------------|-----------------|----------------|
| 5K      | 0.17%    | ~7µs            | 1.6%            | ~1%            |
| 700K    | 23.3%    | ~125µs          | 11.3%           | ~70-80%        |
| 1.5M    | 50.0%    | ~267µs          | 24.0%           | ~85-90%        |
| 2M      | 66.7%    | ~356µs          | 32.0%           | ~90-95%        |
| 3M      | 100%     | ~534µs          | 48.0%           | ~99%           |

Note: 3M uses 53% CPU per instance (over 45% limit). 2M is borderline.
Current submission #7154: 700K. Next: 1.5M.

## Memory Budget (1.5M vectors)

- Vectors: 1.5M × 14 × 2 bytes = 42MB
- Labels: 1.5M bytes = 1.5MB
- Peak during buildGrid: 2× (old + new) = 87MB
- Plus Go runtime: 10MB
- Total peak: ~97MB < 140MB ✓
- Steady state after startup: ~54MB

## Next Steps

1. Wait for #7154 results
2. If accuracy is still poor: submit with 1.5M vectors (next PR ready)
3. If throughput issues: check rawhttp server / LB reconnect logic
4. If accuracy is good but score low: check latency (p99 must stay < 2s)
