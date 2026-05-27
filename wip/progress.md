# Progress Tracker

## Current Round: 1 — Multi-arch image

**Status:** IN PROGRESS — building and submitting

---

## History

| Round | Description | Issue # | Score | Successes | Notes |
|-------|-------------|---------|-------|-----------|-------|
| -     | 500K vectors, GOMAXPROCS=4, goroutine shards | — | -6000 | ~0 | First attempt |
| -     | 500K vectors, GOMAXPROCS=1, sequential KNN | — | -6000 | — | Removed goroutines |
| -     | 30K vectors, GOMAXPROCS=1 | #6914 | -6000 | ~126 est. | Still saturated |
| -     | 5K vectors, GOMAXPROCS=1 | #6968 | -6000 | 27 | Fewer successes — KNN not the bottleneck |

---

## Round 1 — Multi-arch (amd64 + arm64)

**Hypothesis:** Competition machine runs ARM64. Our amd64 binary runs under QEMU → 20–70× slowdown.
**Change:** Dockerfile modified to use `ARG TARGETARCH` and compile for both linux/amd64 and linux/arm64.
**Everything else:** unchanged (5K vectors, GOMAXPROCS=1, GOGC default)

- [x] Dockerfile updated (ARG TARGETARCH, preprocess-native for builder arch)
- [x] Image built and pushed (linux/amd64 + linux/arm64) — manifest sha256:5b9f9d4...
- [x] Issue submitted → #6998
- [ ] Result received
- [ ] Result recorded in results/round-01.json

**Expected outcome:** If QEMU is the cause, score should jump from -6000 to >0.

---

## Round 2 — Reduce vectors to 1K

**Hypothesis:** Even on native hardware, KNN is too slow at 5K vectors.
**Change:** `maxSamples = 1_000` in cmd/preprocess/main.go
**Cumulative:** multi-arch + 1K vectors

- [ ] maxSamples changed
- [ ] Image built and pushed
- [ ] Issue submitted
- [ ] Result received

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
**Changes:** nginx.conf — proxy_buffer_size 16k, keepalive_timeout 30s, proxy_connect_timeout 1s
**Cumulative:** all previous + nginx tuning

- [ ] nginx.conf updated
- [ ] Submission branch updated
- [ ] Image built and pushed
- [ ] Issue submitted
- [ ] Result received

---

## Round 5 — fasthttp

**Hypothesis:** net/http allocations and goroutine overhead are too expensive.
**Change:** Replace cmd/api with fasthttp-based server (zero-alloc, no goroutine-per-conn)
**Cumulative:** all previous + fasthttp

- [ ] cmd/api rewritten with fasthttp
- [ ] go.mod updated
- [ ] Image built and pushed
- [ ] Issue submitted
- [ ] Result received

---

## Round 6 — Alternative algorithm (if all else fails)

**Hypothesis:** The competition machine is 40–100× slower; KNN is fundamentally unworkable.
**Change:** Replace KNN with a pre-trained static decision tree or quantile-based rule set.

- [ ] Implement alternative classifier
- [ ] Image built and pushed
- [ ] Issue submitted
- [ ] Result received

---

## Next action for new Claude session

1. Check `progress.md` (this file) for current round and checkbox status
2. If a round is IN PROGRESS with a submitted issue: run `gh issue view <N> --repo zanfranceschi/rinha-de-backend-2026 --comments` to get result
3. Record result in `results/round-XX.json`
4. Decide: continue to next round or retry current
5. Implement next round's changes, build, submit
