# Round 2 — Reduce vectors to 1K

## What to change

### cmd/preprocess/main.go
```
maxSamples = 1_000   // was 5_000
```

## Build command
```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t molbr/rinha-backend-2026:latest --push .
```

## Submit
```bash
gh issue create --repo zanfranceschi/rinha-de-backend-2026 \
  --title "rinha/test molBR" --body "rinha/test molBR"
```

## Note
Accuracy will drop slightly (~10-15%) because fewer training points.
But p99 and detection_score tradeoff: better to get score > -6000 with lower accuracy
than perfect accuracy with timeout.

At 1K vectors, KNN time on M1 ≈ 10µs → at 15× slowdown = 150µs → at 450 req/s = 67ms per second
→ 0.067 CPUs needed per instance (vs 0.45 budget). Huge safety margin.
