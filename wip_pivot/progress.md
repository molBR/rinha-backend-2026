# Progress Tracker

## Current Round: 2 — Reduce vectors to 1K

**Status:** IN PROGRESS — waiting for result on issue #7007

---

## History

| Round | Description | Issue # | Score | Successes | Notes |
|-------|-------------|---------|-------|-----------|-------|
| -     | 500K vectors, GOMAXPROCS=4, goroutine shards | — | -6000 | ~0 | First attempt |
| -     | 500K vectors, GOMAXPROCS=1, sequential KNN | — | -6000 | — | Removed goroutines |
| -     | 30K vectors, GOMAXPROCS=1 | #6914 | -6000 | ~126 est. | Still saturated |
| -     | 5K vectors, GOMAXPROCS=1 (amd64 only) | #6968 | -6000 | 27 | Fewer successes — QEMU bottleneck |
| 1     | multi-arch (amd64+arm64), 5K vectors | #6998 | -6000 | **123** | 4.5× better — ARM64 confirmed |
| 2     | multi-arch, **1K vectors** | #7007 | ? | ? | IN PROGRESS |

---

## Round 1 — Multi-arch (amd64 + arm64) ✅ DONE

**Result:** -6000, 123 successes (was 27). CONFIRMED competition is ARM64.
Native arm64 binary = 4.5× faster than QEMU emulation.
nginx (0.1 CPU) still saturates at ~43 req/s on slow ARM hardware.
See: `results/round-01-issue-6998.json`

---

## Round 2 — Reduce vectors to 1K ← CURRENT

**Hypothesis:** Per-request CPU still too high; 5K KNN ≈ 468µs at 9× slowdown.
**Change:** `maxSamples = 1_000` in cmd/preprocess/main.go
**Cumulative:** multi-arch + 1K vectors

- [x] maxSamples changed to 1_000
- [x] Image built and pushed — manifest sha256:a4a96b7...
- [x] Issue submitted → #7007
- [ ] Result received
- [ ] Result recorded in results/round-02.json

**Expected:** 5× less KNN work (10µs vs 52µs on M1). Success count should jump 5× (123 → ~600+).

---

## Round 3 — GOGC=off + GOMAXPROCS=2

**Hypothesis:** GC pauses (H3) + accept starvation (H4) under CFS throttle.
**Changes:**
- `ENV GOGC=off` in Dockerfile
- `GOMAXPROCS=2` in docker-compose.yml (submission branch)
**Cumulative:** multi-arch + 1K vectors + GOGC=off + GOMAXPROCS=2

- [ ] Dockerfile updated
- [ ] Submission branch docker-compose.yml updated
- [ ] Image built and pushed
- [ ] Issue submitted
- [ ] Result received

---

## Round 4 — nginx tuning

**Hypothesis:** nginx CPU overhead per request is too high on slow hardware.
**Changes:** nginx.conf — keepalive 128, keepalive_requests 10000, proxy_buffer_size 4k, tcp_nodelay
**Cumulative:** all previous + nginx tuning

- [ ] nginx.conf updated
- [ ] Submission branch updated
- [ ] Image built and pushed
- [ ] Issue submitted
- [ ] Result received

---

## Round 5 — fasthttp

**Hypothesis:** net/http goroutine-per-connection + allocations are expensive.
**Change:** Replace cmd/api with valyala/fasthttp (zero-alloc, goroutine pool)
**Cumulative:** all previous + fasthttp

- [ ] cmd/api rewritten
- [ ] go.mod updated
- [ ] Image built and pushed
- [ ] Issue submitted
- [ ] Result received

---

## Round 6 — Alternative algorithm

**Hypothesis:** Competition machine is 40–100× slower; KNN fundamentally unworkable.
**Change:** Replace KNN with pre-trained static decision tree or quantile-based rules

- [ ] Implement alternative classifier
- [ ] Image built and pushed
- [ ] Issue submitted
- [ ] Result received

---

## Next action for new Claude session

1. Check this file for current round and status
2. If a round is WAITING FOR RESULT: `gh issue view <N> --repo zanfranceschi/rinha-de-backend-2026 --comments`
3. Record result in `results/round-XX-issue-YYYY.json`
4. Update History table and mark round done
5. Implement next round per `round-XX-changes.md` or `plan.md`
6. Build: `docker buildx build --platform linux/amd64,linux/arm64 -t molbr/rinha-backend-2026:latest --push .`
7. Submit: `gh issue create --repo zanfranceschi/rinha-de-backend-2026 --title "rinha/test molBR" --body "rinha/test molBR"`

**Current waiting on:** issue #7007 (Round 2 — 1K vectors)
