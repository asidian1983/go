# Chat Server — Production-Grade WebSocket Chat in Go

A fully-featured, horizontally scalable WebSocket chat server built from scratch in Go.
Designed to handle **10,000+ concurrent connections** with sub-10ms p50 latency,
deployable in minutes via Docker Compose.

```
✦ Real-time messaging   ✦ JWT authentication    ✦ Read receipts
✦ Room-based chat       ✦ PostgreSQL history     ✦ Redis multi-node scaling
✦ Docker deployment     ✦ Load tested @ 10K WS  ✦ Security audited
```

---

## Architecture

```
                        ┌─────────────────────────────────┐
                        │          Clients (WS)           │
                        └──────────────┬──────────────────┘
                                       │ JWT  +  WebSocket
                        ┌──────────────▼──────────────────┐
                        │         Gin HTTP Server          │
                        │  /auth/login  │  /ws  │ /rooms  │
                        └──────────────┬──────────────────┘
                                       │
                        ┌──────────────▼──────────────────┐
                        │           Hub (actor)            │◄── single goroutine
                        │  Register │ Broadcast │ Read     │    no mutexes
                        │  Join     │ Leave     │ Unregist │
                        └──────┬────────────────┬──────────┘
                               │                │
               ┌───────────────▼──┐    ┌────────▼──────────┐
               │  PostgreSQL      │    │  Redis Pub/Sub     │
               │  messages        │    │  chat:room:{id}    │
               │  message_reads   │    │  (multi-node sync) │
               └──────────────────┘    └────────────────────┘
```

### Why the Hub actor pattern?

The `Hub` runs in **exactly one goroutine** and owns all connection/room state.
Every mutation flows through buffered channels — no mutexes, no data races on the hot path.

```
Client.ReadPump ──► hub.Broadcast ──► Hub.Run() ──► fanout() ──► Client.WritePump
                                           │
                                     Redis PUBLISH        (multi-node)
                                           │
                                   (all nodes) SUBSCRIBE
                                           │
                                      remoteDeliver ──► fanout()
```

---

## Features

| Category | Details |
|---|---|
| **Transport** | WebSocket (gorilla/websocket), HTTP/1.1 upgrade |
| **Auth** | JWT HS256 · 32-char minimum secret · algorithm-confusion attack prevention |
| **Rooms** | Dynamic room creation · join/leave events · member notifications |
| **Messages** | Real-time broadcast · echo-back delivery confirmation · message IDs |
| **Read Receipts** | Batched `read` event · `read_receipt` broadcast to all room members |
| **Persistence** | PostgreSQL · async writes (non-blocking hub) · keyset pagination |
| **Scaling** | Redis Pub/Sub · dynamic subscriptions · exponential-backoff reconnect |
| **Security** | Origin allowlist (CSWSH protection) · bcrypt + timing-safe auth · parameterised SQL |
| **Ops** | Docker multi-stage build · health checks · graceful shutdown · structured JSON logs |

---

## Tech Stack

```
Language  Go 1.22
HTTP      Gin v1.10
WebSocket gorilla/websocket v1.5
Auth      golang-jwt/jwt v5  +  golang.org/x/crypto (bcrypt)
DB        PostgreSQL 16  via  jackc/pgx v5
Cache     Redis 7  via  redis/go-redis v9
Config    spf13/viper
Logging   uber-go/zap
Deploy    Docker  +  Docker Compose
```

---

## Quick Start

```bash
# 1. Clone and configure
git clone https://github.com/asidian1983/go.git
cd go
cp .env.example .env
# Edit .env: set APP_JWT_SECRET (min 32 chars) and POSTGRES_PASSWORD

# 2. Start everything
docker compose up --build

# 3. Verify
curl http://localhost:8080/health
# {"status":"ok","timestamp":"..."}
```

---

## API Reference

### REST

#### `POST /auth/login`
```json
// Request
{"username": "alice", "password": "password"}

// Response 200
{"token": "<jwt>"}
```

#### `GET /rooms/:id/messages` _(JWT required)_
```
?limit=50&before=2024-01-15T10:00:00Z
```

### WebSocket — `GET /ws` _(JWT via header or `?token=`)_

#### Client → Server

```jsonc
// Join a room
{"event":"join","payload":{"room_id":"general"}}

// Send a message
{"event":"message","room_id":"general","payload":{"message":"hello"}}

// Mark messages as read (batched)
{"event":"read","room_id":"general","payload":{"message_ids":["abc123","def456"]}}

// Leave a room
{"event":"leave","payload":{"room_id":"general"}}
```

#### Server → Client

```jsonc
// Message (broadcast to all room members)
{"event":"message","room_id":"general","payload":{"id":"abc123","sender_id":"alice","message":"hello","created_at":"..."}}

// Read receipt (broadcast when any member reads)
{"event":"read_receipt","room_id":"general","payload":{"message_id":"abc123","user_id":"bob","read_at":"..."}}

// Operation confirmed
{"event":"ack","room_id":"general","payload":{"ok":true,"message":"joined general"}}

// Member joined/left
{"event":"notify","room_id":"general","payload":{"user_id":"alice","text":"alice joined"}}

// Error
{"event":"error","payload":{"code":"not_in_room","message":"join the room before sending"}}
```

---

## Configuration

All configuration is via environment variables (no config file required).

| Variable | Default | Description |
|---|---|---|
| `APP_JWT_SECRET` | **required** | HS256 signing key (min 32 chars) |
| `APP_ENV` | `development` | Set to `production` for release mode + JSON logs |
| `APP_SERVER_PORT` | `8080` | Listening port |
| `APP_SERVER_ALLOWED_ORIGINS` | _(same-host)_ | WebSocket origin allowlist (comma-separated) |
| `APP_POSTGRES_ENABLED` | `false` | Enable message persistence |
| `APP_POSTGRES_DSN` | — | Full PostgreSQL connection string |
| `APP_REDIS_ENABLED` | `false` | Enable Redis pub/sub for multi-node scaling |
| `APP_REDIS_ADDR` | `localhost:6379` | Redis address |

---

## Performance

Benchmarked with the included Go load tester (`tools/loadtest`) on a single MacBook Pro M3:

| Clients | p50 RTT | p95 RTT | p99 RTT | Echo/s |
|---|---|---|---|---|
| 100 | < 1 ms | 3 ms | 8 ms | ~400 |
| 1,000 | 2 ms | 12 ms | 28 ms | ~3,500 |
| 10,000 | 5 ms | 35 ms | 90 ms | ~12,000 |

```bash
# Run the load test yourself
ulimit -n 65536
cd chat-server
go run ./tools/loadtest -clients 10000 -rooms 100 -duration 30s -ramp 15s
```

**Bottleneck at 10K**: Hub `Broadcast` channel pressure and OS TCP buffer — not the Go scheduler. Goroutine count stays under 25K (3 per client).

---

## Horizontal Scaling

Enable Redis to run N identical instances behind a load balancer:

```
                 Load Balancer (nginx / Traefik)
                /               \
       chat-server-1        chat-server-2
            │                    │
            └──────────┬─────────┘
                    Redis 7
                chat:room:{id}
```

Every node publishes to Redis and subscribes to rooms with active local clients.
A message sent from a client on node-1 is instantly fanned out to clients on node-2.

```bash
APP_REDIS_ENABLED=true APP_REDIS_ADDR=redis:6379 docker compose up --scale chat-server=3
```

---

## Database Schema

```sql
-- Chat messages with keyset-paginated history
CREATE TABLE messages (
    id         TEXT PRIMARY KEY,       -- random hex, stable across queries
    room_id    TEXT NOT NULL,
    sender_id  TEXT NOT NULL,
    type       TEXT NOT NULL,          -- 'chat' | 'system'
    body       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_messages_room_created ON messages (room_id, created_at DESC);

-- Read receipts with idempotent upserts
CREATE TABLE message_reads (
    message_id TEXT NOT NULL,
    user_id    TEXT NOT NULL,
    read_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (message_id, user_id)   -- ON CONFLICT DO NOTHING
);
CREATE INDEX idx_message_reads_message ON message_reads (message_id);
```

---

## Security Highlights

| Threat | Mitigation |
|---|---|
| Cross-site WebSocket hijacking (CSWSH) | Origin allowlist with same-host fallback |
| JWT algorithm confusion | `WithValidMethods(["HS256"])` — rejects RS256 etc. |
| User enumeration via timing | Constant-time bcrypt comparison even for unknown users |
| SQL injection | Parameterised queries (`$1`, `$2`) throughout |
| Password in logs | `redactDSN()` strips credentials before logging |
| Privilege escalation | Container runs as non-root user (`app:app`) |
| Message flooding | Slow-client eviction; send buffer overflow protection |

---

## Project Structure

```
chat-server/
├── cmd/server/          # Entry point — wires all components
├── internal/
│   ├── adapter/
│   │   ├── http/        # Gin handlers, JWT middleware, router
│   │   └── ws/          # Hub (actor), Client (pumps), envelope protocol
│   ├── domain/
│   │   ├── entity/      # Message, Room, User value types
│   │   └── repository/  # MessageRepository, ReadRepository interfaces
│   └── infrastructure/
│       ├── auth/        # JWT service, bcrypt UserStore
│       ├── config/      # Viper-based config with env-var binding
│       ├── postgres/    # pgx/v5 repository implementations + auto-migration
│       └── redis/       # Pub/Sub manager with reconnect + backoff
├── pkg/logger/          # Zap logger (dev colours / prod JSON)
└── tools/loadtest/      # 10K WebSocket load tester
```

The architecture follows **hexagonal (ports & adapters)** principles: the domain layer has zero framework dependencies, and infrastructure can be swapped by changing a constructor call.

---

## Development

```bash
cd chat-server

# Run without any external services (in-memory only)
APP_JWT_SECRET="dev-secret-key-at-least-32-chars!!" go run ./cmd/server

# Run with Postgres + Redis
docker compose up postgres redis -d
APP_JWT_SECRET="dev-secret-key-at-least-32-chars!!" \
APP_POSTGRES_ENABLED=true \
APP_POSTGRES_DSN="postgres://chat:chat@localhost:5432/chat?sslmode=disable" \
APP_REDIS_ENABLED=true \
go run ./cmd/server

# Connect via wscat
TOKEN=$(curl -s -X POST http://localhost:8080/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"password"}' | jq -r .token)

wscat -c "ws://localhost:8080/ws" -H "Authorization: Bearer $TOKEN"
> {"event":"join","payload":{"room_id":"general"}}
> {"event":"message","room_id":"general","payload":{"message":"hello world"}}
```

---

## Build Steps

This project was built incrementally — each step is documented in [`docs/progress/`](docs/progress/):

| Step | Feature |
|---|---|
| 01 | Server skeleton — Gin, Viper, Zap, graceful shutdown |
| 02 | WebSocket core — Hub actor, client pumps, ping/pong keepalive |
| 03 | Room-based chat — dynamic rooms, join/leave, member notifications |
| 04 | Redis Pub/Sub — horizontal scaling, dynamic subscriptions, reconnect |
| 05 | JWT authentication — HS256, middleware, alg-confusion prevention |
| 06 | PostgreSQL persistence — async writes, keyset pagination, idempotency |
| 07 | Read receipts — batched marking, real-time broadcast, Redis fanout |
| 08 | Load tester — 10K concurrent WebSocket clients, latency percentiles |
| 09 | Docker deployment — multi-stage build, Compose, health checks |
| 10 | Security audit — CSWSH fix, shutdown ordering, double-close, DSN redaction |

---

## License

MIT
