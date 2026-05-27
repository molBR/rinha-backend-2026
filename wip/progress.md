# Rinha Backend 2026 — Optimization Progress

## Problem Summary
~99% of requests timeout (HTTP errors). Test fires ~13,830 requests in burst;
only those completing within 2s count. nginx at 0.1 CPU is the primary bottleneck (~50-100 req/s).

| Round | Issue | Successes | HTTP errors | Notes |
|-------|-------|-----------|-------------|-------|
| Baseline R1 | #6998 | 123 | 13,707 | 5K multi-arch |
| Baseline R2 | #7007 | 8 | 13,822 | 1K regression |
| Step 1 | TBD | — | — | 5K + GOMEMLIMIT + precomputed responses + GOAMD64=v3 |
| Step 2 | TBD | — | — | hand-rolled JSON parser |
| Step 3 | TBD | — | — | custom Go LB replacing nginx |

## Implementation Status
- [x] Step 1: Dockerfile (GOMEMLIMIT, GOAMD64=v3), engine.ScoreIdx, precomputed responses
- [ ] Step 2: fast_parse.go
- [ ] Step 3: cmd/lb/ + docker-compose.yml
