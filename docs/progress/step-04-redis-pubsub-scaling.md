# Step 04 — Redis Pub/Sub Horizontal Scaling

## Overview

Extends the room-based chat (step-03) with Redis Pub/Sub so that multiple
server instances can share message delivery. A message sent to a room on
Node A is transparently relayed to clients connected on Node B (or C, D …).

Single-node deployments are unaffected — Redis is **opt-in** and disabled by
default.

---

## New Files

| Path | Description |
|---|---|
| `internal/infrastructure/redis/pubsub.go` | Redis Pub/Sub manager — publish, subscribe, auto-reconnect |

## Modified Files

| Path | Change |
|---|---|
| `internal/adapter/ws/hub.go` | Redis bridge goroutine, `remoteDeliver` channel, subscribe/unsubscribe on room lifecycle, Redis-first broadcast |
| `internal/infrastructure/config/config.go` | Added `RedisConfig` (`enabled`, `addr`) with env-var and file support |
| `cmd/server/main.go` | Conditionally wires `redispubsub.Manager` into Hub at startup |
| `go.mod` | Added `github.com/redis/go-redis/v9 v9.7.0` |

---

## Architecture

### Single-node (Redis disabled — default)

```
Client.ReadPump ──► hub.Broadcast
                          │
                     Hub.Run()
                          │
                     fanout(roomID)
                          │
         client.send ──► Client.WritePump ──► WebSocket
```

### Multi-node (Redis enabled)

```
Client.ReadPump ──► hub.Broadcast
                          │
                     Hub.Run()
                          │
               Redis PUBLISH chat:room:{id}
                          │
          ┌───────────────┴───────────────┐
        Node A                          Node B
   Redis SUBSCRIBE                 Redis SUBSCRIBE
          │                               │
   remoteDeliver (chan)           remoteDeliver (chan)
          │                               │
     fanout(roomID)               fanout(roomID)
          │                               │
   local WS clients              local WS clients
```

---

## Redis Pub/Sub Manager (`internal/infrastructure/redis/pubsub.go`)

### Responsibilities

| Method | Description |
|---|---|
| `New(addr, log)` | Creates manager; connects go-redis client |
| `Publish(ctx, roomID, msg)` | JSON-wraps `msg` and publishes to `chat:room:{roomID}` |
| `Subscribe(roomID)` | Adds room to active set; subscribes live connection |
| `Unsubscribe(roomID)` | Removes room; unsubscribes live connection |
| `Run(stop)` | Receive loop with exponential-backoff reconnect (1 s → 30 s cap) |
| `Deliver()` | Returns `<-chan Delivery` consumed by the Hub bridge goroutine |

### Channel naming

```
chat:room:{roomID}
```

### Auto-reconnect

`Run()` calls `runOnce()` in a loop. When the Redis connection drops,
`runOnce` returns an error, the loop sleeps with exponential backoff, then
reconnects and **re-subscribes to all rooms** that still have local clients.

---

## Hub Changes (`internal/adapter/ws/hub.go`)

### New fields

```go
pubsub        *redispubsub.Manager  // nil → single-node mode
remoteDeliver chan roomcast          // nil channel when pubsub is nil
```

### Bridge goroutine (started inside `Hub.Run`)

Translates `redispubsub.Delivery` values into `roomcast` structs and sends
them into `remoteDeliver`. The Hub event loop then handles them in the same
`select` as local commands — **no mutex required**.

```go
go func() {
    for d := range h.pubsub.Deliver() {
        select {
        case h.remoteDeliver <- roomcast{roomID: d.RoomID, msg: d.Payload}:
        case <-stop:
            return
        }
    }
}()
```

### Subscription lifecycle

| Event | Action |
|---|---|
| First local client joins a room | `pubsub.Subscribe(roomID)` |
| Last local client leaves / is evicted | `pubsub.Unsubscribe(roomID)` |

This ensures each node only holds subscriptions for rooms with active local
clients — idle rooms consume no Redis resources.

### Broadcast path (Redis enabled)

```
hub.Broadcast → validate room + membership → Redis PUBLISH
                                                   ↓
                                         remoteDeliver (all nodes)
                                                   ↓
                                            fanout to local clients
```

On Redis publish failure the Hub falls back to direct local `fanout()` so
single-node behaviour is preserved under a Redis outage.

---

## Configuration

### Environment variables (recommended)

```bash
APP_REDIS_ENABLED=true
APP_REDIS_ADDR=localhost:6379
```

### Config file (`configs/config.yaml`)

```yaml
redis:
  enabled: true
  addr: "localhost:6379"
```

### Defaults

| Key | Default |
|---|---|
| `redis.enabled` | `false` |
| `redis.addr` | `localhost:6379` |

---

## Scalability Analysis

### Message duplication

None. Each message travels a single path:

```
Broadcast → Redis PUBLISH → Redis SUBSCRIBE (all nodes) → local fanout
```

There is no parallel local delivery when Redis is enabled. The origin node
receives its own publish back from Redis and fans out via `remoteDeliver`,
just like every other node.

### Race conditions

The Hub remains a **single-goroutine actor**. Redis messages enter through
the buffered `remoteDeliver` channel, which is consumed inside the Hub's
`select` loop. `redispubsub.Manager.Subscribe` / `Unsubscribe` use a mutex
only to guard the `ps *goredis.PubSub` pointer accessed from two goroutines
(Hub.Run and the reconnect loop).

### Horizontal scaling limits

| Concern | Detail |
|---|---|
| Redis throughput | Single Redis node handles ~100 k msg/s; use Redis Cluster for higher load |
| Fan-out latency | Adds one Redis round-trip (~0.5–2 ms LAN) per message |
| Memory per node | One `chan Delivery` (512-slot) + one `chan roomcast` (512-slot) — fixed overhead |
| Subscription count | Redis supports tens of thousands of concurrent channel subscriptions |

### Remaining scalability path

| Concern | Solution |
|---|---|
| Presence tracking | Redis `SET online:{userID} 1 EX 90`, refreshed on each WS ping |
| Message persistence | Async worker writes `Message` entity to PostgreSQL after Redis publish |
| Rate limiting | Per-client token bucket in `ReadPump` before enqueuing to Hub |
| Redis HA | Redis Sentinel or Redis Cluster for failover |

---

## Running Locally

```bash
# Start Redis
docker run -d -p 6379:6379 redis:7-alpine

# Run server with Redis enabled
cd chat-server
go mod tidy   # fetches go-redis/v9
APP_REDIS_ENABLED=true go run ./cmd/server

# Multi-node test (two terminals)
APP_REDIS_ENABLED=true APP_SERVER_PORT=8080 go run ./cmd/server
APP_REDIS_ENABLED=true APP_SERVER_PORT=8081 go run ./cmd/server
# Connect a WS client to :8080 and another to :8081, join the same room — messages cross nodes.
```
