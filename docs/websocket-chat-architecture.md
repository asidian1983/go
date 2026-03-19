# High-Performance WebSocket Chat Server — Architecture

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                        Clients                              │
│              (Browser / Mobile / Desktop)                   │
└──────────────────────┬──────────────────────────────────────┘
                       │ WSS / HTTPS
┌──────────────────────▼──────────────────────────────────────┐
│                   Load Balancer                             │
│            (nginx / AWS ALB — sticky sessions)              │
└──────┬──────────────┬──────────────┬───────────────────────┘
       │              │              │
┌──────▼──┐     ┌─────▼──┐    ┌─────▼──┐
│ Server 1│     │Server 2│    │Server N│   ← Go instances
└──────┬──┘     └─────┬──┘    └─────┬──┘
       │              │              │
       └──────────────▼──────────────┘
                 Redis Pub/Sub          ← cross-node message fan-out
                 Redis Cluster
                       │
               ┌───────▼────────┐
               │   PostgreSQL   │     ← message persistence
               │  + TimescaleDB │     ← (time-series optimized)
               └────────────────┘
```

**Request flow:**
1. Client connects → Load Balancer → Go server
2. Server validates JWT → upgrades to WebSocket
3. Server subscribes to Redis channel for the room
4. Incoming message → written to Kafka/channel → persisted async → fan-out via Redis Pub/Sub
5. All nodes subscribed to that room receive and push to their local connections

---

## Folder Structure

```
chat-server/
├── cmd/
│   └── server/
│       └── main.go                  # entrypoint, wire dependencies
│
├── internal/
│   ├── domain/                      # Enterprise business rules (no deps)
│   │   ├── entity/
│   │   │   ├── user.go              # User, UserID
│   │   │   ├── room.go              # Room, RoomID
│   │   │   └── message.go           # Message, MessageType
│   │   ├── repository/              # interfaces only
│   │   │   ├── user_repo.go
│   │   │   ├── room_repo.go
│   │   │   └── message_repo.go
│   │   └── event/
│   │       └── broker.go            # PubSub interface
│   │
│   ├── usecase/                     # Application business rules
│   │   ├── auth/
│   │   │   ├── login.go
│   │   │   └── refresh.go
│   │   ├── chat/
│   │   │   ├── send_message.go
│   │   │   ├── join_room.go
│   │   │   └── get_history.go
│   │   └── room/
│   │       ├── create_room.go
│   │       └── list_rooms.go
│   │
│   ├── adapter/                     # Interface adapters
│   │   ├── ws/                      # WebSocket handler layer
│   │   │   ├── hub.go               # connection registry per node
│   │   │   ├── client.go            # single WS connection lifecycle
│   │   │   ├── handler.go           # HTTP→WS upgrade, JWT check
│   │   │   └── router.go            # message type routing
│   │   ├── http/                    # REST handlers (auth, room mgmt)
│   │   │   ├── auth_handler.go
│   │   │   └── room_handler.go
│   │   └── middleware/
│   │       ├── jwt.go
│   │       └── ratelimit.go
│   │
│   └── infrastructure/              # Frameworks & drivers
│       ├── postgres/
│       │   ├── message_repo.go      # implements domain/repository
│       │   ├── user_repo.go
│       │   └── migrations/
│       ├── redis/
│       │   ├── pubsub.go            # implements domain/event.Broker
│       │   └── session.go           # online presence tracking
│       ├── jwt/
│       │   └── token.go             # sign / verify
│       └── config/
│           └── config.go            # env-based config (viper)
│
├── pkg/                             # Shared, importable utilities
│   ├── logger/                      # structured logging (zap)
│   ├── validator/
│   └── errkit/                      # typed errors, codes
│
├── deploy/
│   ├── docker-compose.yml
│   ├── Dockerfile
│   └── k8s/
│       ├── deployment.yaml
│       └── hpa.yaml                 # horizontal pod autoscaler
│
├── go.mod
└── go.sum
```

---

## Core Component Responsibilities

### `domain/` — Pure business logic, zero dependencies

| Entity | Responsibility |
|--------|---------------|
| `Message` | ID, RoomID, SenderID, Content, Type, CreatedAt |
| `Room` | ID, Name, Members, IsPrivate |
| `User` | ID, Username, HashedPassword, Roles |
| `repository.*` | Port interfaces — no impl, no imports |
| `event.Broker` | `Publish(channel, msg)` / `Subscribe(channel)` interface |

---

### `adapter/ws/` — WebSocket core, the performance-critical path

**`hub.go`**
- Node-local registry: `map[roomID]map[clientID]*Client`
- Goroutine-safe via `sync.RWMutex` or sharded locks
- Subscribes to Redis channels for rooms that have local members
- Routes inbound Redis events → matching local clients

**`client.go`**
- One goroutine for read (`readPump`), one for write (`writePump`)
- Buffered send channel (`chan []byte`, size 256)
- Heartbeat via `ping/pong` frames (detect dead connections fast)
- Graceful cleanup on disconnect → notifies hub

**`handler.go`**
- Validates JWT **before** WebSocket upgrade (no upgrade = no goroutine)
- Uses `gorilla/websocket` with tuned buffer sizes

```go
// Target: ~10K connections = ~20K goroutines + Redis subs
// Each goroutine stack: ~2–8KB → ~160MB baseline
// Acceptable for a single node; scale horizontally beyond that
```

---

### `infrastructure/redis/pubsub.go` — Horizontal scaling backbone

- One Redis subscription per active room per node (not per client)
- Fan-out from Redis → hub → N local clients is O(local_members)
- Uses `go-redis/v9` with connection pooling

---

### `usecase/chat/send_message.go` — Orchestration

```
Validate → Persist (async, via worker pool) → Publish to Redis
```
- Persistence is **async** — message is published immediately, written to DB via buffered channel + batch insert (improves throughput 10×)
- Uses `errgroup` for coordinated error handling

---

## Key Technology Choices

| Concern | Choice | Reason |
|---------|--------|--------|
| WebSocket | `gorilla/websocket` | Battle-tested, fine-grained control |
| HTTP router | `chi` | Lightweight, middleware-friendly |
| Pub/Sub | Redis Cluster | Low-latency, proven at scale |
| Persistence | PostgreSQL + pgx/v5 | Native binary protocol, batch support |
| Auth | JWT (RS256) | Stateless, verifiable across nodes |
| Logging | `uber-go/zap` | Zero-alloc structured logging |
| Config | `viper` | 12-factor env config |
| Metrics | Prometheus + Grafana | WS conn count, msg rate, latency |

---

## Scaling Strategy

```
10K connections / node  →  horizontal scale behind ALB
Redis Pub/Sub           →  O(rooms) subscriptions per node, not O(clients)
Batch DB writes         →  group inserts every 50ms or 100 msgs
JWT stateless auth      →  no shared session store needed
HPA on k8s              →  scale on CPU + custom metric (active_connections)
```
