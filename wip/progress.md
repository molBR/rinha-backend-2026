# Rinha Backend 2026 — Optimization Progress

## Problem Summary
~99% of requests timeout (HTTP errors). Test fires ~13,830 requests in burst;
only those completing within 2s count. nginx at 0.1 CPU is the primary bottleneck (~50-100 req/s).

| Round | Issue | Successes | HTTP errors | Notes |
|-------|-------|-----------|-------------|-------|
| Baseline R1 | #6998 | 123 | 13,707 | 5K multi-arch |
| Baseline R2 | #7007 | 8 | 13,822 | 1K regression |
| Step 1 | #7015 | — | — | 5K + GOMEMLIMIT + precomputed responses + GOAMD64=v3 |
| Step 2 | #7017 | — | — | hand-rolled JSON parser (ParseAndScore) |
| Step 3 | #7018 | — | — | custom Go LB replacing nginx (cmd/lb/) |

## Implementation Status
- [x] Step 1: Dockerfile (GOMEMLIMIT, GOAMD64=v3), engine.ScoreIdx, precomputed responses → image pushed
- [x] Step 2: fast_parse.go (ParseAndScore), sync.Pool body buffers → image pushed
- [x] Step 3: cmd/lb/ round-robin proxy, submission branch updated → issue submitted
- [ ] Awaiting results for all 3 steps

## Key Files
- `internal/fraud/engine.go` — knnSearch returns int, ScoreIdx method
- `internal/fraud/fast_parse.go` — NEW: hand-rolled JSON parser, ParseAndScore
- `cmd/api/main.go` — precomputed responses, sync.Pool buffers, no encoding/json on hot path
- `cmd/lb/main.go` — NEW: round-robin reverse proxy
- `cmd/lb/Dockerfile` — NEW: LB Docker build
- `Dockerfile` — GOMEMLIMIT=140MiB, GOAMD64=v3, GOGC=off
- `submission` branch `docker-compose.yml` — lb service (build: ./cmd/lb)
