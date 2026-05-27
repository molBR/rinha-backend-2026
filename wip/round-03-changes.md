# Round 3 — GOGC=off + GOMAXPROCS=2

## What to change

### Dockerfile (add ENV)
After `ENV PORT=8080`, add:
```
ENV GOGC=off
```

### submission branch: docker-compose.yml
Change both api1 and api2 environment sections:
```yaml
environment:
  - PORT=8080
  - GOMAXPROCS=2    # was 1
```

## Build command
```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t molbr/rinha-backend-2026:latest --push .
```

Then update submission branch:
```bash
git checkout submission
# edit docker-compose.yml: GOMAXPROCS=1 → GOMAXPROCS=2
git add docker-compose.yml
git commit -m "round-3: GOMAXPROCS=2"
git push origin submission
```

## Why GOGC=off is safe
Total data loaded: 1K vectors × 14 × 2 bytes = 28KB
Total runtime memory: ~20MB (Go runtime + HTTP server + goroutines)
No repeated large allocations — GC would never collect much anyway.
Disabling it eliminates stop-the-world pauses entirely.

## Why GOMAXPROCS=2
With 1 OS thread, KNN (tight loop, no yield) blocks all other goroutines.
With 2 threads: thread A does KNN while thread B accepts/writes connections.
CFS quota is shared (still 0.45 CPU total) but concurrency is better.
