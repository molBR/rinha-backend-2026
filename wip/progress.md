# Rinha Backend 2026 — Optimization Progress

## Problem Summary
~99% of requests timeout (HTTP errors). Test fires ~13,830 requests in burst;
only those completing within 2s count. nginx at 0.1 CPU is primary bottleneck.

| Round | Issue | Successes | HTTP errors | p99 | Notes |
|-------|-------|-----------|-------------|-----|-------|
| Baseline R1 | #6998 | 123 | 13,707 | 2002ms | 5K multi-arch |
| Baseline R2 | #7007 | 8 | 13,822 | 2002ms | 1K regression |
| Step 1 API only | #7015 | 126 | 13,703 | 2002ms | GOMEMLIMIT+precomputed+GOAMD64 + nginx → +3 only |
| Step 2 API only | #7017 | REJECTED | — | — | build: not allowed (had LB changes on branch) |
| Step 3 LB+API | #7018 | REJECTED | — | — | build: not allowed |
| Fix: pre-built LB | #7019 | — | — | — | molbr/rinha-backend-2026-lb:latest + Step2 API |

## Key Findings
- nginx at 0.1 CPU: ~63 req/s → only ~126 requests complete in 2s window
- API improvements (precomputed responses, fast JSON) barely help while nginx is bottleneck
- competition rejects `build:` directives — ALL images must be pre-built on DockerHub
- #7019 is the first combined test: custom Go LB + fast API

## Images on DockerHub
- `molbr/rinha-backend-2026:latest` — Step 1+2 API (fast JSON parser, precomputed responses)
- `molbr/rinha-backend-2026-lb:latest` — custom Go round-robin LB (Step 3)

## Implementation Status
- [x] Step 1: GOMEMLIMIT, GOAMD64=v3, precomputed responses, ScoreIdx
- [x] Step 2: fast_parse.go (ParseAndScore, hand-rolled JSON), sync.Pool buffers
- [x] Step 3: custom Go LB (cmd/lb/), pre-built image pushed to DockerHub
- [x] Submission branch updated (be44d82): uses image: for both API and LB
- [ ] Awaiting result for #7019

## Key Files
- `internal/fraud/engine.go` — knnSearch returns int, ScoreIdx method
- `internal/fraud/fast_parse.go` — NEW: hand-rolled JSON parser, ParseAndScore
- `cmd/api/main.go` — precomputed responses, sync.Pool buffers, no encoding/json on hot path
- `cmd/lb/main.go` — NEW: round-robin reverse proxy (pre-built as molbr/rinha-backend-2026-lb)
- `Dockerfile` — GOMEMLIMIT=140MiB, GOAMD64=v3, GOGC=off
- `submission` branch `docker-compose.yml` — both API and LB use pre-built DockerHub images
