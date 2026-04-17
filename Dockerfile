FROM ghcr.io/rblaine95/golang:1.25 AS deepcool-builder
WORKDIR /src
COPY . . 
RUN go build -ldflags="-s -w" -o /emeter-logger ./cmd/emeter-logger
RUN go build -ldflags="-s -w" -o /envoy-logger ./cmd/envoy-logger
RUN go build -ldflags="-s -w" -o /deepcool ./cmd/deepcool

#FROM debian:trixie-slim AS base-runtime
FROM debian:bookworm-slim AS base-runtime
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /root/

FROM base-runtime AS emeter-run
COPY --from=deepcool-builder /emeter-logger .
CMD ["./emeter-logger", "startup"]

FROM base-runtime AS envoy-run
RUN mkdir /data
COPY --from=deepcool-builder /envoy-logger .
CMD ["./envoy-logger", "startup", "--token", "/data/envoy.jwt"]

FROM base-runtime AS deepcool-run
COPY --from=deepcool-builder /deepcool .
CMD ["./deepcool", "--monitor-only"]
