# Step 09 — Docker & Production Deployment

## Overview

Packages the chat server for production deployment using a multi-stage Docker build
and a `docker-compose.yml` that wires up the chat server, PostgreSQL, and Redis with
proper health checks, restart policies, and dependency ordering.

---

## New Files

| Path | Description |
|---|---|
| `chat-server/Dockerfile` | Multi-stage build: golang:1.22-alpine builder → alpine:3.19 runtime |
| `chat-server/.dockerignore` | Excludes secrets, tools, docs, and dev artefacts from build context |
| `docker-compose.yml` | Orchestrates chat-server + postgres:16 + redis:7 |
| `.env.example` | Documented environment variable template |
| `.gitignore` | Prevents `.env` and build outputs from being committed |

---

## Docker build (multi-stage)

```
Stage 1 — builder (golang:1.22-alpine)
  ├── go mod download   (cached layer — only re-runs on go.mod change)
  └── go build -trimpath -ldflags="-s -w"   (static binary, stripped symbols)

Stage 2 — runtime (alpine:3.19)
  ├── ca-certificates   (TLS to Postgres / Redis)
  ├── wget              (Docker health check probe)
  ├── adduser app       (non-root runtime user)
  └── COPY binary only  (~15 MB final image)
```

### Why alpine over distroless?

distroless has no shell or wget, which makes Docker health checks impossible without
an external probe binary. Alpine adds ~5 MB but enables `wget -q --spider /health`.

### Build commands

```bash
# Build image
docker build -t chat-server:latest ./chat-server

# Inspect final image size
docker images chat-server:latest

# Scan for vulnerabilities (requires Docker Scout or trivy)
trivy image chat-server:latest
```

---

## docker-compose services

| Service | Image | Port | Health check |
|---|---|---|---|
| `chat-server` | built locally | 8080 | `wget -q --spider http://localhost:8080/health` |
| `postgres` | postgres:16-alpine | 5432 | `pg_isready` |
| `redis` | redis:7-alpine | 6379 | `redis-cli ping` |

### Startup order

```
postgres (healthy) ──┐
                     ├──► chat-server starts
redis    (healthy) ──┘
```

`depends_on: condition: service_healthy` ensures the chat server only starts after
both backing services pass their health checks.

---

## Quickstart

```bash
# 1. Copy and fill in secrets
cp .env.example .env
# Edit .env: set APP_JWT_SECRET and POSTGRES_PASSWORD

# 2. Start everything
docker compose up --build

# 3. Verify health
curl http://localhost:8080/health
# {"status":"ok","timestamp":"..."}

# 4. Get a token
curl -s -X POST http://localhost:8080/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"password"}' | jq .

# 5. Connect via WebSocket
wscat -c "ws://localhost:8080/ws?token=<token>"
```

---

## Environment variables

All config is driven by environment variables (no config file needed in production).

| Variable | Required | Default | Description |
|---|---|---|---|
| `APP_JWT_SECRET` | **yes** | — | HS256 signing secret (min 32 chars) |
| `APP_JWT_EXPIRY` | no | `24h` | Token lifetime |
| `APP_ENV` | no | `development` | Set to `production` for release mode + JSON logs |
| `APP_SERVER_PORT` | no | `8080` | Listening port |
| `APP_POSTGRES_ENABLED` | no | `false` | Enable message persistence |
| `APP_POSTGRES_DSN` | no | — | Full Postgres connection string |
| `APP_REDIS_ENABLED` | no | `false` | Enable Redis pub/sub for multi-node |
| `APP_REDIS_ADDR` | no | `localhost:6379` | Redis address |
| `POSTGRES_DB` | no | `chat` | Database name (used by docker-compose) |
| `POSTGRES_USER` | no | `chat` | Database user (used by docker-compose) |
| `POSTGRES_PASSWORD` | **yes** | — | Database password (used by docker-compose) |

### Generating a secure JWT secret

```bash
openssl rand -base64 32
```

---

## Production checklist

- [ ] Set `APP_JWT_SECRET` via a secrets manager (AWS Secrets Manager, Vault, etc.)
- [ ] Set `POSTGRES_PASSWORD` via a secrets manager — never in plain `.env` in prod
- [ ] Remove exposed ports `5432` and `6379` from `docker-compose.yml` (internal only)
- [ ] Add `sslmode=require` to `APP_POSTGRES_DSN`
- [ ] Set `APP_JWT_EXPIRY` to a shorter value (e.g. `1h`) with refresh tokens
- [ ] Configure a reverse proxy (nginx / Traefik) with TLS termination in front of port 8080
- [ ] Enable Redis AUTH password: add `--requirepass` to redis command and update `APP_REDIS_ADDR`
- [ ] Add resource limits (`deploy.resources`) to docker-compose services
- [ ] Set up log aggregation (Loki, CloudWatch, Datadog) — server outputs JSON logs in production
- [ ] Run `trivy image chat-server:latest` in CI to catch image vulnerabilities

---

## Security notes

| Concern | Mitigation |
|---|---|
| Secrets in image | `.dockerignore` excludes `.env`; secrets injected at runtime only |
| Root process | Runtime user `app` (non-root) via `adduser -S` |
| Attack surface | Alpine runtime has no package manager, compiler, or shell scripts |
| Image size | Multi-stage build strips source, build tools, and debug symbols (`-ldflags="-s -w"`) |
| Postgres exposure | Port 5432 exposed for dev convenience — remove for production |
| Redis exposure | Port 6379 exposed for dev convenience — remove for production |
