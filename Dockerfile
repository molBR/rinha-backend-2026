FROM golang:1.23-alpine AS builder

WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .

# Build the API server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /api ./cmd/api

# Build the preprocessor (runs once at build time, no CPU limit)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /preprocess ./cmd/preprocess

# Convert references.json.gz → references.bin at build time.
# This avoids slow JSON parsing under Docker CPU limits at container startup.
# Binary format: uint32 N + N×14×uint16 vectors + N×uint8 labels ≈ 87MB.
RUN /preprocess /src/resources/references.json.gz /references.bin

FROM alpine:3.21
COPY --from=builder /api                   /api
COPY --from=builder /references.bin        /resources/references.bin
COPY resources/mcc_risk.json               /resources/mcc_risk.json
COPY resources/normalization.json          /resources/normalization.json

ENV REFERENCES_PATH=/resources/references.bin
ENV MCC_RISK_PATH=/resources/mcc_risk.json
ENV NORMALIZATION_PATH=/resources/normalization.json
ENV PORT=8080

EXPOSE 8080
ENTRYPOINT ["/api"]
