FROM golang:1.23-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY *.go ./

# Build for target architecture. TARGETARCH is set by docker buildx --platform;
# defaults to amd64 for plain docker build (competition uses docker compose up --build).
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w" -o /lb .

FROM alpine:3.21
COPY --from=builder /lb /lb

# Single goroutine scheduler — LB is mostly I/O bound.
ENV GOMAXPROCS=1
# Disable GC — tiny footprint, no allocations to collect.
ENV GOGC=off
ENV GOMEMLIMIT=25MiB

EXPOSE 9999
ENTRYPOINT ["/lb"]
