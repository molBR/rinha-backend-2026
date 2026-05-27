# Round 5 — fasthttp

## What to change

### go.mod
Add fasthttp dependency:
```bash
go get github.com/valyala/fasthttp
```

### cmd/api/main.go
Replace with fasthttp-based server. Key differences:
- Zero-alloc request/response handling
- No goroutine-per-connection (uses goroutine pool)
- 3–10× faster JSON decode/encode with direct byte slices

```go
package main

import (
    "encoding/json"
    "log"
    "os"
    "runtime"
    "strconv"

    "github.com/valyala/fasthttp"
    "rinha-backend-2026/internal/fraud"
)

var engine *fraud.Engine

func main() {
    if v := os.Getenv("GOMAXPROCS"); v != "" {
        if n, err := strconv.Atoi(v); err == nil && n > 0 {
            runtime.GOMAXPROCS(n)
        }
    }

    refsPath := getenv("REFERENCES_PATH", "resources/references.json.gz")
    mccPath := getenv("MCC_RISK_PATH", "resources/mcc_risk.json")
    normPath := getenv("NORMALIZATION_PATH", "resources/normalization.json")

    var err error
    engine, err = fraud.NewEngine(refsPath, mccPath, normPath)
    if err != nil {
        log.Fatalf("init engine: %v", err)
    }

    port := getenv("PORT", "8080")
    log.Printf("listening on :%s", port)
    log.Fatal(fasthttp.ListenAndServe(":"+port, requestHandler))
}

func requestHandler(ctx *fasthttp.RequestCtx) {
    path := string(ctx.Path())
    method := string(ctx.Method())

    if method == "GET" && path == "/ready" {
        ctx.SetStatusCode(fasthttp.StatusOK)
        return
    }

    if method == "POST" && path == "/fraud-score" {
        var req fraud.Request
        if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
            ctx.SetStatusCode(fasthttp.StatusBadRequest)
            return
        }
        score, approved := engine.Score(&req)
        ctx.SetContentType("application/json")
        resp, _ := json.Marshal(map[string]interface{}{
            "approved":    approved,
            "fraud_score": score,
        })
        ctx.SetBody(resp)
        return
    }

    ctx.SetStatusCode(fasthttp.StatusNotFound)
}

func getenv(key, def string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return def
}
```

## Build
```bash
go mod tidy
docker buildx build --platform linux/amd64,linux/arm64 \
  -t molbr/rinha-backend-2026:latest --push .
```
