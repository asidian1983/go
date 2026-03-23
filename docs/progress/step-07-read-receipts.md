# Step 07 — Scalable Read Receipts

## Overview

Adds real-time read receipts to the chat server. When a client marks one or more
messages as read, every member of the room receives a `read_receipt` event immediately.
Receipts are persisted to PostgreSQL asynchronously and broadcast via the existing
Redis pub/sub layer, so the feature works correctly in both single-node and
multi-node deployments with zero additional infrastructure.

---

## Modified Files

| Path | Change |
|---|---|
| `internal/infrastructure/postgres/db.go` | Added `message_reads` table + `idx_message_reads_message` index |
| `internal/domain/repository/message.go` | Added `ReadRepository` interface (`MarkRead`) |
| `internal/infrastructure/postgres/message_repo.go` | Implemented `MarkRead` on `MessageRepo` (idempotent) |
| `internal/adapter/ws/envelope.go` | Added `EventRead`, `EventReadReceipt`, `ReadPayload`, `ReadReceiptPayload` |
| `internal/adapter/ws/hub.go` | Added `readReceiptCmd`, `ReadReceipt` channel, `readRepo` field, handler in `Run()`, `persistRead` goroutine |
| `internal/adapter/ws/client.go` | Added `EventRead` case in `route()` — batched, validates room membership |
| `cmd/server/main.go` | Wires `readRepo` from same `*MessageRepo` instance |

---

## Architecture

```
Client ──► ReadPump ──► hub.ReadReceipt (readReceiptCmd)
                              │
                         Hub.Run()
                         │             │
                  fanout / Redis    go persistRead()
                         │             │
                    WebSocket      Postgres
                  read_receipt    INSERT message_reads
                  (all members)
```

**Single-node** — `ReadReceipt` → `fanout()` directly to all local room clients.

**Multi-node (Redis)** — `ReadReceipt` → `Redis PUBLISH chat:room:{roomID}` →
all nodes receive via `remoteDeliver` → local `fanout()`. Reuses the exact same
Redis pub/sub path as chat messages — no extra infrastructure needed.

The hub event loop is **never blocked** by the database write. Persistence runs in
a dedicated goroutine (5 s timeout). Errors are logged but do not affect delivery.

---

## Database Schema

```sql
CREATE TABLE IF NOT EXISTS message_reads (
    message_id  TEXT        NOT NULL,
    user_id     TEXT        NOT NULL,
    read_at     TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (message_id, user_id)
);

-- Covers look-ups of all readers for a given message.
CREATE INDEX IF NOT EXISTS idx_message_reads_message
    ON message_reads (message_id);
```

### Idempotency

The composite primary key `(message_id, user_id)` means a duplicate `MarkRead`
call (same user reading the same message twice) is silently ignored via
`ON CONFLICT (message_id, user_id) DO NOTHING`.

### Indexing strategy

| Query pattern | Index used |
|---|---|
| `WHERE message_id = $1` (look up all readers) | `idx_message_reads_message` |

---

## WebSocket Protocol

### Client → Server — mark messages as read (batched)

```json
{
  "event": "read",
  "room_id": "general",
  "payload": {
    "message_ids": ["a3f9c1e2", "b8d74f01"]
  }
}
```

| Field | Required | Description |
|---|---|---|
| `event` | yes | `"read"` |
| `room_id` | yes | Room the messages belong to (client must have joined) |
| `payload.message_ids` | yes | Non-empty list of message IDs to mark as read |

### Server → Client — read receipt broadcast

Sent to **all members** of the room (including the reader) for each message ID:

```json
{
  "event": "read_receipt",
  "room_id": "general",
  "payload": {
    "message_id": "a3f9c1e2",
    "user_id": "alice",
    "read_at": "2026-03-23T10:00:00Z"
  }
}
```

### Error responses

```json
{ "event": "error", "payload": { "code": "not_in_room",  "message": "join the room before sending read receipts" } }
{ "event": "error", "payload": { "code": "bad_request",  "message": "read requires a non-empty room_id in the envelope" } }
{ "event": "error", "payload": { "code": "bad_request",  "message": "read requires non-empty message_ids in payload" } }
```

---

## Running Locally

```bash
# 1. Start Postgres + Redis (if multi-node)
docker run -d --name chat-pg \
  -e POSTGRES_DB=chat \
  -e POSTGRES_PASSWORD=pass \
  -p 5432:5432 postgres:16

# 2. Run the server
APP_JWT_SECRET="dev-secret-key-at-least-32-chars!!" \
APP_POSTGRES_ENABLED=true \
APP_POSTGRES_DSN="postgres://postgres:pass@localhost:5432/chat?sslmode=disable" \
go run ./cmd/server

# 3. Get tokens for two users
TOKEN_A=$(curl -s -X POST http://localhost:8080/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"password"}' | jq -r .token)

TOKEN_B=$(curl -s -X POST http://localhost:8080/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"bob","password":"password"}' | jq -r .token)

# 4. Open two terminals — alice sends, bob reads
# Terminal 1 (alice):
wscat -c "ws://localhost:8080/ws" -H "Authorization: Bearer $TOKEN_A"
# > {"event":"join","payload":{"room_id":"general"}}
# > {"event":"message","room_id":"general","payload":{"message":"hello"}}
# Note the message "id" in the echoed response

# Terminal 2 (bob — receives the message, then marks it read):
wscat -c "ws://localhost:8080/ws" -H "Authorization: Bearer $TOKEN_B"
# > {"event":"join","payload":{"room_id":"general"}}
# > {"event":"read","room_id":"general","payload":{"message_ids":["<id from above>"]}}

# Both terminals will receive:
# {"event":"read_receipt","room_id":"general","payload":{"message_id":"...","user_id":"bob","read_at":"..."}}
```

---

## Performance Notes

| Concern | Approach |
|---|---|
| Hub throughput | `ReadReceipt` channel is buffered (512); never blocks `ReadPump` |
| DB write latency | Async goroutine — hub event loop unblocked immediately |
| Duplicate receipts | `ON CONFLICT DO NOTHING` — safe to re-send same read event |
| Multi-node fanout | Reuses Redis pub/sub — no extra subscriptions or channels |
| Batch support | Client sends N message IDs in one frame; each enqueues one `readReceiptCmd` |

---

## Production Checklist

- [ ] Add foreign key `REFERENCES messages(id)` to `message_reads.message_id` if strict integrity is required
- [ ] Add `GET /rooms/:id/messages/:msgid/reads` endpoint to query who has read a message
- [ ] Consider TTL / archival strategy for `message_reads` rows (can grow large in busy rooms)
- [ ] Add rate limiting on `EventRead` to prevent abuse
- [ ] Enable `sslmode=require` on the Postgres DSN
- [ ] Add a proper migration tool (golang-migrate, goose) to version the schema change
