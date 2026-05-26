# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

A [Rinha de Backend 2026](https://github.com/zanfranceschi/rinha-de-backend-2026) submission — a competitive challenge where implementations run under strict resource limits. The service scores credit card transactions for fraud using k-nearest neighbors (KNN) classification against a reference dataset.

**Resource budget (enforced by Docker):** 2 API instances × (0.45 CPU + 140 MB RAM) + nginx (0.1 CPU + 30 MB RAM). Every optimization decision is driven by these constraints.

## Commands

```bash
# Run tests (uses example-references.json.gz, ~small dataset)
go test ./internal/fraud/...

# Run a single test
go test ./internal/fraud/ -run TestScoreMatchesReference

# Run benchmarks (requires full resources/references.json.gz)
go test ./internal/fraud/ -bench=. -benchmem

# Build both binaries
go build ./cmd/api
go build ./cmd/preprocess

# Preprocess references JSON → binary (done at Docker build time)
go run ./cmd/preprocess resources/references.json.gz resources/references.bin

# Run locally
REFERENCES_PATH=resources/references.json.gz \
MCC_RISK_PATH=resources/mcc_risk.json \
NORMALIZATION_PATH=resources/normalization.json \
go run ./cmd/api

# Run via Docker Compose (production setup)
docker compose up --build
```

## Architecture

### Request Flow

```
POST :9999/fraud-score  →  nginx (round-robin)  →  api1:8080 or api2:8080
                                                         ↓
                                                   fraud.Engine.Score()
                                                         ↓
                                              buildVector() → knnSearch()
                                                         ↓
                                              {"approved": bool, "fraud_score": float}
```

### Fraud Engine (`internal/fraud/`)

The engine loads a large reference dataset and classifies new transactions via **k=5 nearest neighbors** in a 14-dimensional feature space.

**`engine.go`** — Core KNN implementation:
- Reference vectors are stored as flat `[]uint16` (N×14), with float64 values from `[-1, 1]` linearly encoded to `[0, 65535]`. This halves memory vs float32.
- `knnSearch` splits the dataset into 8 shards and runs each in a goroutine (via `sync.WaitGroup`), then merges top-k candidates. `sync.Pool` reuses per-goroutine `shardState` to avoid allocations on the hot path.
- `searchRange` is the inner loop — fully unrolled for all 14 dimensions to avoid loop overhead and bounds checks.
- Approval threshold: `fraudScore < 0.6` (i.e., at most 2 of 5 neighbors are fraud).
- Supports two reference file formats: `.bin` (fast binary, used in Docker) and `.json.gz` (used in tests).

**`vector.go`** — Feature engineering:
- `buildVector` maps a `Request` struct to a `[14]uint16` query vector. The 14 dimensions and their normalization parameters are documented in the function's godoc.
- `normalization.json` holds the scaling constants (max values). Update it if the dataset distribution changes.
- `mcc_risk.json` maps MCC codes to a risk score in `[0, 1]`; unknown MCCs default to `0.5`.

### Binary Preprocessing (`cmd/preprocess/`)

At Docker build time, `preprocess` converts `references.json.gz` (~JSON, slow to parse) into a compact binary:
- Layout: `uint32 N` + `N×14×uint16` vectors (little-endian) + `N×uint8` labels
- Produced file is ~87 MB for the full dataset
- This sidesteps slow JSON parsing under Docker CPU limits at container startup

### HTTP Server (`cmd/api/`)

Minimal `net/http` server. Two routes:
- `GET /ready` — health check for Docker healthcheck
- `POST /fraud-score` — decodes JSON body as `fraud.Request`, calls `engine.Score`, returns JSON

`GOMAXPROCS` is configurable via env var (set to 4 in docker-compose to match the 0.45-CPU budget).

## Key Invariants

- The encoding function `encodeVal(v)` must be identical in both `cmd/preprocess/main.go` and `internal/fraud/engine.go` — they are intentionally duplicated so the preprocess binary is self-contained.
- `TestScoreMatchesReference` validates that the uint16-quantized KNN produces the same results as a float64 brute-force reference implementation. Do not break this test.
- The `dims=14` and `k=5` constants in `engine.go` must stay in sync with the feature engineering in `vector.go` and the binary format written by `preprocess`.
