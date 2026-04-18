ARG GO_VERSION=1.25
ARG VERSION=0.5.0-dev
ARG BUILD_DATE

FROM golang:${GO_VERSION}-alpine AS deepcool-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . . 
RUN go build -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildDate=${BUILD_DATE}" -o /emeter-logger ./cmd/emeter-logger && \
    go build -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildDate=${BUILD_DATE}" -o /envoy-logger ./cmd/envoy-logger && \
    go build -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildDate=${BUILD_DATE}" -o /deepcool ./cmd/deepcool

FROM debian:bookworm-slim AS base-runtime
LABEL org.opencontainers.image.title="HomeEnergy" \
      org.opencontainers.image.description="Electric Optimization project" \
      org.opencontainers.image.version="${VERSION}"

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
RUN useradd -m -u 1000 appuser
WORKDIR /root/

FROM base-runtime AS emeter-run
COPY --from=deepcool-builder /emeter-logger .
USER appuser
CMD ["./emeter-logger", "startup"]

FROM base-runtime AS envoy-run
RUN mkdir -p /data && chown appuser:appuser /data
COPY --from=deepcool-builder /envoy-logger .
USER appuser
CMD ["./envoy-logger", "startup", "--token", "/data/envoy.jwt"]

FROM base-runtime AS deepcool-run
COPY --from=deepcool-builder /deepcool .
USER appuser
CMD ["./deepcool", "--monitor-only"]
