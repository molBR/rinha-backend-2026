# Rinha de Backend 2026 — Debugging WIP

## Context

This is a fraud-scoring HTTP API for [Rinha de Backend 2026](https://github.com/zanfranceschi/rinha-de-backend-2026).  
The service classifies credit card transactions using **k=5 KNN in 14-dimensional space** against a reference dataset.

**Resource limits (enforced by Docker):**
- 2 API instances × (0.45 CPU + 140 MB RAM)
- 1 nginx instance × (0.1 CPU + 30 MB RAM)

## Problem

Every competition attempt returns **-6000** (worst possible score).  
Local tests work perfectly (score ~3236, p99 ~1.4ms, 0 HTTP errors).

## Root Cause Hypotheses

See `plan.md` for the full analysis. TL;DR: the competition machine is likely running our
linux/amd64 Docker image under QEMU emulation on an ARM host, causing 20–70× slowdown.
nginx (0.1 CPU) also saturates extremely early on slow hardware.

## State Machine

```
Round 1 → submitted (#6968 FAILED) → Round 2 (IN PROGRESS) → ...
```

See `progress.md` for current round and results.

## How to Continue (for new Claude sessions)

1. Read `progress.md` — tells you exactly which round is in progress and what to do next
2. Read `plan.md` — full hypothesis list and all planned rounds
3. Look at `results/` — JSON files from each competition attempt
4. The competition issue URL pattern: `https://github.com/zanfranceschi/rinha-de-backend-2026/issues/<N>`

### Quick commands

```bash
# Check latest competition result
gh issue view <N> --repo zanfranceschi/rinha-de-backend-2026 --comments

# Build and push multi-arch image
docker buildx build --platform linux/amd64,linux/arm64 \
  -t molbr/rinha-backend-2026:latest --push .

# Submit competition test
gh issue create --repo zanfranceschi/rinha-de-backend-2026 \
  --title "rinha/test molBR" --body "rinha/test molBR"

# Run local test (requires k6 and the API running)
go run ./cmd/api &
k6 run test/test_local.js
```

## Repo structure

- `cmd/api/` — HTTP server (net/http, minimal)
- `cmd/preprocess/` — converts references.json.gz → references.bin at Docker build time
- `internal/fraud/` — KNN engine + feature engineering
- `nginx/nginx.conf` — reverse proxy config
- `Dockerfile` — multi-stage: build → preprocess references → final image
- `docker-compose.yml` (main branch) — uses `build: .` for local dev
- `docker-compose.yml` (submission branch) — uses `image: molbr/rinha-backend-2026:latest`
- `resources/references.json.gz` — 3M reference vectors (training data)
- `test/` — k6 test suite + results

## Key invariants

- `encodeVal()` must be identical in `cmd/preprocess/main.go` and `internal/fraud/engine.go`
- KNN threshold: `fraudScore < 0.6` → approved
- `maxSamples` in `cmd/preprocess/main.go` controls how many reference vectors are embedded
