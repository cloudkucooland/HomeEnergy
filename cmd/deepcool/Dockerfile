# Step 1: Build the binary
FROM golang:1.25-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags="-s -w" -o deepcool ./cmd/deepcool

# Step 2: Run the binary in a tiny container
FROM debian:bookworm-slim
WORKDIR /root/
COPY --from=builder /app/deepcool .
CMD ["./deepcool", "--monitor-only"]
