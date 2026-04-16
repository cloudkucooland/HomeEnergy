# --- Builder 1: emeter ---
FROM ghcr.io/rblaine95/golang:1.25 AS emeter-builder
RUN apt-get update && apt-get install -y git
WORKDIR /src
RUN git clone https://github.com/cloudkucooland/go-kasa.git .
RUN go build -ldflags="-s -w" -o /emeterlog ./cmd/emeterlog

# --- Builder 2: envoy ---
# FIX: Rename this to envoy-builder
FROM ghcr.io/rblaine95/golang:1.25 AS envoy-builder
RUN apt-get update && apt-get install -y git
WORKDIR /src
RUN git clone https://github.com/cloudkucooland/go-envoy.git .
RUN go build -ldflags="-s -w" -o /envoylog ./cmd/logger

# --- Builder 3: deepcool (Local Context) ---
# FIX: Rename this to deepcool-builder
FROM ghcr.io/rblaine95/golang:1.25 AS deepcool-builder
WORKDIR /src
COPY . . 
RUN go build -ldflags="-s -w" -o /deepcool ./cmd/deepcool

# --- Final Base Image ---
# Switch to a base that likely has a RISC-V manifest
#FROM debian:bookworm AS base-runtime
#RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
#WORKDIR /root/
#FROM riscv64/debian:bookworm AS base-runtime
#RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
#WORKDIR /root/
FROM debian:trixie-slim AS base-runtime
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /root/

# --- TARGET: emeter-runner ---
FROM base-runtime AS emeter-run
COPY --from=emeter-builder /emeterlog .
CMD ["./emeterlog", "startup"]

# --- TARGET: envoy-runner ---
FROM base-runtime AS envoy-run
RUN mkdir /data
COPY --from=envoy-builder /envoylog .
CMD ["./envoylog", "startup", "--token", "/data/envoy.jwt"]

# --- TARGET: deepcool-runner ---
FROM base-runtime AS deepcool-run
COPY --from=deepcool-builder /deepcool .
CMD ["./deepcool", "--monitor-only"]
