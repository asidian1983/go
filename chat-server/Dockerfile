# ── Stage 1: build ───────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

# Install CA certs and git (needed for go mod download over HTTPS)
RUN apk add --no-cache ca-certificates git

WORKDIR /src

# Cache dependencies separately from source code
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build a statically-linked binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/chat-server ./cmd/server

# ── Stage 2: runtime ─────────────────────────────────────────────────────────
# Alpine gives us wget for health checks while remaining small (~10 MB).
FROM alpine:3.19

# ca-certificates: TLS to Postgres/Redis; wget: Docker health check probe
RUN apk add --no-cache ca-certificates wget && \
    addgroup -S app && adduser -S -G app app

COPY --from=builder /out/chat-server /chat-server

USER app:app

EXPOSE 8080

ENTRYPOINT ["/chat-server"]
