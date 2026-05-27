# Rinha Backend 2026 — Optimization Progress

## Problem Summary
~99% of requests timeout (HTTP errors). Test fires ~13,830 requests simultaneously.
Root cause: LB memory exhaustion. httputil.ReverseProxy at 13,830 concurrent connections
needs ~200MB+ in the LB (16KB per connection), but limit is 30MB → OOM/GC thrashing.
Result: essentially same throughput as nginx (both limited by LB memory).

| Round | Issue | Successes | HTTP errors | p99 | Notes |
|-------|-------|-----------|-------------|-----|-------|
| Baseline R1 | #6998 | 123 | 13,707 | 2002ms | 5K multi-arch, nginx |
| Baseline R2 | #7007 | 8 | 13,822 | 2002ms | 1K regression, nginx |
| Step 1 API only | #7015 | 126 | 13,703 | 2002ms | GOMEMLIMIT+precomputed+GOAMD64 + nginx |
| Step 2 API only | #7017 | REJECTED | — | — | build: not allowed |
| Step 3 LB+API | #7018 | REJECTED | — | — | build: not allowed |
| Pre-built LB | #7019 | 121 | 13,709 | 2002ms | Go LB (httputil.ReverseProxy) + fast API |
| Step 4 SCM_RIGHTS | #7027 | — | — | — | SCM_RIGHTS LB + rawhttp API |

## Key Findings
- All our results: ~13,830 total requests (121 success + 13,709 error = 13,830)
- nginx vs Go httputil.ReverseProxy: same ~121-126 successes → LB model unchanged
- Root cause: 13,830 concurrent connections × ~16KB (net/http bufio) = ~220MB in LB
  → GOMEMLIMIT=25MB → GC thrashing → near-zero LB throughput
- Top competitor (lucasgoveia): 54,059/54,100 with SCM_RIGHTS + Grid KD-tree index

## SCM_RIGHTS Architecture (Step 4)
1. LB: accept TCP conn → sendmsg(fd) via Unix socket → close LB's copy
   - 0 bytes copied, 0 goroutines held, ~5µs per connection
2. API: recv fd → net.FileConn() → rawhttp.ServeConn()
   - Direct connection to client, no proxy overhead
3. Shared tmpfs volume at /sockets for Unix domain sockets

## Images on DockerHub
- `molbr/rinha-backend-2026:latest` — API with rawhttp + SCM_RIGHTS FD receiver
- `molbr/rinha-backend-2026-lb:latest` — SCM_RIGHTS LB

## Implementation Status
- [x] Step 1: GOMEMLIMIT, GOAMD64=v3, precomputed responses, ScoreIdx
- [x] Step 2: fast_parse.go (ParseAndScore, hand-rolled JSON), sync.Pool buffers
- [x] Step 3: custom Go LB → rejected (build: not allowed), then pre-built → same as nginx
- [x] Step 4: SCM_RIGHTS LB + rawhttp API (issue #7027, pending)
- [ ] Step 5 (if needed): Grid KD-tree index over 3M vectors

## Key Files
- `internal/fraud/engine.go` — knnSearch returns int, ScoreIdx method
- `internal/fraud/fast_parse.go` — hand-rolled JSON parser, ParseAndScore
- `internal/rawhttp/server.go` — NEW: minimal HTTP/1.1 server, zero-alloc
- `cmd/api/main.go` — rawhttp + SCM_RIGHTS FD receiver + TCP :8080 fallback
- `cmd/lb/main.go` — NEW: SCM_RIGHTS connection passer
- `Dockerfile` — GOMEMLIMIT=140MiB, GOAMD64=v3, GOGC=off
- `docker-compose.yml` — sockets volume, CTRL_SOCKET env vars
