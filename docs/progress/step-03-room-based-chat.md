# Step 03 — Room-Based Chat System

## Overview

Extends the WebSocket core (step-02) with full room management, 1:1 chat,
group chat, and scoped message delivery.

---

## New Files

| Path | Description |
|---|---|
| `internal/domain/entity/user.go` | `UserID` type and `User` struct |
| `internal/domain/entity/room.go` | `RoomID`, `RoomType` (direct/group), `Room`, `DirectRoomID()` |
| `internal/domain/entity/message.go` | `MessageType` (chat/system), `Message` record |
| `internal/adapter/ws/envelope.go` | Wire protocol — event types, all inbound/outbound payloads, `buildEnvelope()` |

## Modified Files

| Path | Change |
|---|---|
| `internal/adapter/ws/hub.go` | Full rewrite — room-aware hub with `Join`/`Leave`/`Broadcast` channels |
| `internal/adapter/ws/client.go` | Full rewrite — added `UserID`, `rooms`, `route()` event dispatcher |
| `internal/adapter/ws/handler.go` | Reads `?user_id=` query param, passes identity to `NewClient` |

---

## Architecture

### Concurrency Model (unchanged from step-02)

`Hub.Run()` is the **sole owner** of all mutable state (`clients`, `rooms` maps).
All external interaction goes through buffered channels — no mutex is ever needed.

```
Client.ReadPump ──► hub.Join / hub.Leave / hub.Broadcast
                              │
                         Hub.Run()          ← single goroutine owns all state
                              │
                       fanout(roomID)       ← scoped to room members only
                              │
               client.send ──► Client.WritePump ──► WebSocket
```

### Hub Internal Commands

```go
type joinCmd  struct { client *Client; roomID string }
type leaveCmd struct { client *Client; roomID string }
type roomcast struct { roomID string; msg []byte; sender *Client }
```

---

## Wire Protocol

All WebSocket frames use a single JSON envelope:

```json
{ "event": "<type>", "room_id": "<optional>", "payload": { ... } }
```

### Event Types

| Direction | Event | Description |
|---|---|---|
| Client → Server | `join` | Join a room |
| Client → Server | `leave` | Leave a room |
| Client → Server | `message` | Send a chat message to a room |
| Server → Client | `ack` | Operation confirmed |
| Server → Client | `error` | Operation rejected (with error code) |
| Server → Client | `notify` | System notification (member joined/left) |

### Examples

**Join a room**
```json
{ "event": "join", "payload": { "room_id": "lobby" } }
```

**Send a message**
```json
{ "event": "message", "room_id": "lobby", "payload": { "message": "hello" } }
```

**Leave a room**
```json
{ "event": "leave", "payload": { "room_id": "lobby" } }
```

**Delivered message (server → all room members including sender)**
```json
{
  "event": "message",
  "room_id": "lobby",
  "payload": {
    "sender_id": "alice",
    "room_id": "lobby",
    "message": "hello",
    "created_at": "2026-03-20T10:00:00Z"
  }
}
```

**Join ack**
```json
{ "event": "ack", "room_id": "lobby", "payload": { "ok": true, "message": "joined lobby" } }
```

**Error**
```json
{ "event": "error", "payload": { "code": "not_in_room", "message": "join the room before sending messages" } }
```

---

## Room Types

### Group Chat

Any room with two or more members. Create by having clients join the same `room_id`.

### 1:1 Direct Chat

Uses a deterministic room ID so both users always resolve to the same room
regardless of who initiates:

```go
// internal/domain/entity/room.go
func DirectRoomID(a, b UserID) RoomID {
    ids := []string{string(a), string(b)}
    sort.Strings(ids)
    return RoomID("direct:" + ids[0] + ":" + ids[1])
}
// DirectRoomID("bob", "alice") == DirectRoomID("alice", "bob") == "direct:alice:bob"
```

No special Hub code path — a direct room is just a room with 2 members.

---

## Behaviour Details

| Scenario | Behaviour |
|---|---|
| Join already-joined room | Idempotent — returns `ack {ok: true, "already in room"}` |
| Leave room not joined | Returns `error {code: "not_in_room"}` |
| Send to non-existent room | Returns `error {code: "room_not_found"}` |
| Send without joining | Returns `error {code: "not_in_room"}` |
| Message delivery | Delivered to **all** members including sender (echo-back = delivery confirmation) |
| Join notification | Sent to existing members, **excluding** the joiner |
| Leave notification | Sent to remaining members |
| Client disconnect | Automatically removed from all joined rooms; empty rooms deleted |
| Slow client (full buffer) | Evicted from all rooms and disconnected |

---

## Connection

```
ws://localhost:<PORT>/ws?user_id=<yourUserID>
```

> In production, replace `?user_id=` with JWT middleware that sets the identity
> on `gin.Context`.

---

## Scalability Path

| Concern | Solution |
|---|---|
| Cross-node fan-out | Redis Pub/Sub — publish to `room:{roomID}`, each node relays to local clients |
| Presence tracking | Redis `SET online:{userID} 1 EX 90`, refreshed on each WS ping |
| Message persistence | Async worker goroutine writes `Message` entity to PostgreSQL |
| Rate limiting | Per-client token bucket in `ReadPump` before enqueuing to Hub |
