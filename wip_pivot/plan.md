# Debugging Plan — Rinha de Backend 2026

## Failure Pattern

Every competition attempt → **-6000** (p99 ≈ 2002ms, failure_rate ≈ 99.8%).  
Only ~27 requests succeed before complete timeout saturation.  
The 27-success count is CONSISTENT across all attempts regardless of vector count (5K, 30K, 500K),
which proves KNN speed alone is NOT the bottleneck.

## Competition Environment (known)

- Docker image pulled from `molbr/rinha-backend-2026:latest`
- nginx:1.27-alpine + 2× API containers
- CFS limits: 0.45 CPU per API, 0.1 CPU for nginx
- k6 ramps 1→900 req/s over 120s, 250 max VUs, 2001ms timeout, 54,100 entries
- Runtime-info shows `"Platform":"linux"` (no arch specified)

## Hypotheses

### H1 — QEMU emulation (HIGHEST CONFIDENCE)

The competition host is likely ARM64 (AWS Graviton, GH Actions arm64 runner, or similar).
Our image was built only for `linux/amd64`. Under QEMU, every syscall is translated → 20–70× slowdown.

Evidence:
- 27 successes regardless of KNN size (nginx QEMU overhead saturates at ~20 req/s)
- nginx 0.1 CPU at 20× slowdown: ~26µs × 20 = 520µs per proxy op → max 19 req/s through nginx
- k6 reaches 19 req/s at t≈2.4s, cumulative ~26 requests → matches 27 successes perfectly

Fix: Build multi-arch image (`linux/amd64,linux/arm64`). Docker selects native layer automatically.
Risk: LOW — doesn't change any logic.

### H2 — KNN too slow on competition hardware (HIGH CONFIDENCE)

Even on native x86, the competition machine may be a slow shared VM (2–4× slower than M1 Pro).
5K vectors × 14 dims × int64 arithmetic: at 3× slowdown = 156µs; at 450 req/s = 70ms/100ms period
→ needs 0.7 CPUs vs 0.45 available → CFS throttle.

Fix: Reduce `maxSamples` to 1000 vectors (10µs on M1 → 30µs at 3× → 14ms/100ms period → OK).
Risk: Accuracy drops (but still better than -6000).

### H3 — CFS burst credit exhaustion (MEDIUM CONFIDENCE)

Linux CFS lets containers borrow burst credits during idle periods. Go runtime init + binary load
burns burst credits. After serving ~27 requests, credits run out and throttle becomes hard (container
paused 55ms out of every 100ms). Requests pile up → timeouts.

Fix: GOGC=off (no GC pauses), reduce startup CPU work.

### H4 — GOMAXPROCS=1 causes accept starvation (MEDIUM CONFIDENCE)

With GOMAXPROCS=1, the KNN goroutine holds the single OS thread for ~780µs (tight loop, no preemption).
During this time, the `Accept` goroutine can't run → new connections queue behind nginx keepalive pool.
Under CFS throttle + GOMAXPROCS=1, all goroutines stall together.

Fix: GOMAXPROCS=2 — one thread accepts/reads/writes while the other runs KNN.

### H5 — nginx CPU bottleneck (MEDIUM CONFIDENCE)

nginx has 0.1 CPU = 10ms/s. Each proxy operation ~26µs native.
On slow/QEMU hardware at 15×: 390µs per request → max 25 req/s through nginx alone.
This converges with H1: QEMU makes BOTH nginx and API slow.

Fix: Tune nginx (proxy_buffer_size, keepalive_timeout). Can't add CPU.

### H6 — net/http server overhead (LOW CONFIDENCE)

Standard net/http creates goroutines and allocates per request. Under CFS + high concurrency,
GC and goroutine scheduling overhead might accumulate.

Fix: Replace with fasthttp (3–10× faster, zero-alloc).

---

## Rounds (sequential — each builds on the previous)

Each round: implement change → commit main → build+push multi-arch → update submission branch
if needed → submit issue → wait for results → record in results/ → decide continue or stop.

**Round 1: Multi-arch image** ← CURRENT
- What changes: Dockerfile supports `ARG TARGETARCH`, builds arm64 + amd64
- Vector count: 5000 (unchanged)
- GOMAXPROCS: 1 (unchanged)
- GOGC: default (unchanged)
- Purpose: Isolate H1 (QEMU). If score improves dramatically → QEMU confirmed.
- Build cmd: `docker buildx build --platform linux/amd64,linux/arm64 -t molbr/rinha-backend-2026:latest --push .`

**Round 2: Reduce vectors to 1K**
- What changes: `maxSamples = 1_000` in cmd/preprocess/main.go
- Multi-arch: yes (from Round 1)
- Purpose: Reduce KNN time 5×, address H2.
- Note: accuracy will drop slightly but competition score is dominated by p99 penalty.

**Round 3: GOGC=off + GOMAXPROCS=2**
- What changes: `ENV GOGC=off` in Dockerfile, `GOMAXPROCS=2` in docker-compose.yml (submission branch)
- Multi-arch: yes, 1K vectors: yes
- Purpose: Eliminate GC pauses (H3), better accept concurrency (H4).

**Round 4: nginx tuning**
- What changes: nginx.conf — proxy_buffer_size 16k, keepalive_timeout 30s, proxy_connect_timeout 1s
- Everything from Round 3
- Purpose: Reduce nginx per-request overhead (H5).

**Round 5: fasthttp**
- What changes: Replace cmd/api with fasthttp-based server
- Everything from Round 4
- Purpose: 3–10× faster HTTP handling (H6), fewer allocations.

**Round 6: Alternative algorithm**
- What changes: Replace KNN with a pre-trained decision tree or bloom-filter approach
- Purpose: If hardware is 50–100× slower, KNN is fundamentally unworkable; need O(1) scoring.

---

## Decision points

After Round 1:
- Score > 0 → H1 (QEMU) confirmed → continue optimizing for better score
- Score still -6000, but more successes → partial improvement → continue with R2
- Score still -6000, same 27 successes → QEMU not the issue → skip to R3

After Round 2:
- Score > 0 → KNN was the issue → tune for best score
- Score still -6000 → need deeper changes → R3

After Round 3:
- Score > 0 → GC/concurrency was the issue
- Score still -6000 → escalate to Round 5 (fasthttp) or Round 6 (new algorithm)

---

## Accuracy vs speed tradeoff

| maxSamples | KNN time (M1) | At 3× slowdown | At 15× slowdown | At 40× slowdown |
|---|---|---|---|---|
| 5000 | ~52µs | ~156µs | ~780µs | ~2.08ms |
| 1000 | ~10µs | ~30µs | ~150µs | ~400µs |
| 500 | ~5µs | ~15µs | ~75µs | ~200µs |
| 200 | ~2µs | ~6µs | ~30µs | ~80µs |

CPU utilization at 450 req/s per instance, 0.45 CPU budget (45ms/100ms):
| Slowdown | 5K ok? | 1K ok? | 500 ok? |
|---|---|---|---|
| 3× | 156µs×450=70ms ✗ | 30µs×450=14ms ✓ | ✓ |
| 15× | 780µs×450=351ms ✗ | 150µs×450=68ms ✗ | 75µs×450=34ms ✓ |
| 40× | 2ms×450=900ms ✗ | 400µs×450=180ms ✗ | 200µs×450=90ms ✗ |

At 40× slowdown, even 200 vectors is marginal. Round 6 (new algorithm) becomes necessary.

---

## Scoring formula reference

```
p99_score = 1000 × log10(1000 / max(p99_ms, 1))   [cut at -3000 if p99 > 2000ms]
det_score = 1000×log10(1/max(ε,0.001)) - 300×log10(1+E)   [cut at -3000 if failure > 15%]
final = p99_score + det_score
E = fp×1 + fn×3 + errors×5
ε = E / N
```

Target: final_score > 3000 (top tier). Local score with 5K vectors: ~3236.
