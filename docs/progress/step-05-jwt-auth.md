# Step 05 — JWT Authentication

## Overview

Replaces the insecure `?user_id=` query-parameter identity with a full
JWT-based authentication flow. Clients must obtain a signed token via
`POST /auth/login` before they can open a WebSocket connection.

---

## New Files

| Path | Description |
|---|---|
| `internal/infrastructure/auth/jwt.go` | JWT service — generate and validate HS256 tokens |
| `internal/infrastructure/auth/users.go` | In-memory user store with bcrypt password hashing |
| `internal/adapter/http/auth_handler.go` | `POST /auth/login` endpoint |
| `internal/adapter/http/middleware/jwt.go` | JWT validation middleware for Gin |

## Modified Files

| Path | Change |
|---|---|
| `internal/infrastructure/config/config.go` | Added `JWTConfig` (`secret`, `expiry`); secret is required at startup |
| `internal/adapter/http/router.go` | Added `/auth/login` route; applied JWT middleware to `/ws` |
| `internal/adapter/ws/handler.go` | Reads `userID` from `gin.Context` (set by middleware) instead of query param |
| `cmd/server/main.go` | Wires `auth.Service` and `auth.UserStore`; passes `jwtSvc` to router |
| `go.mod` | Added `github.com/golang-jwt/jwt/v5`, `golang.org/x/crypto` |

---

## Authentication Flow

```
1. Client                    Server
   │                           │
   │  POST /auth/login         │
   │  {"username","password"}  │
   │ ─────────────────────►    │
   │                           │  bcrypt.CompareHashAndPassword()
   │                           │  jwt.NewWithClaims(HS256, claims)
   │  200 {"token":"<jwt>"}    │
   │ ◄─────────────────────    │
   │                           │
   │  GET /ws                  │
   │  Authorization: Bearer <token>   │  (or ?token=<jwt>)
   │ ─────────────────────►    │
   │                           │  JWT middleware: ParseWithClaims()
   │                           │  sets userID in gin.Context
   │                           │  upgrader.Upgrade()
   │  101 Switching Protocols  │
   │ ◄─────────────────────    │
   │  (WebSocket established)  │
```

---

## JWT Token

### Algorithm
HS256 (HMAC-SHA256) — symmetric, secret-keyed.

### Claims

| Field | Description |
|---|---|
| `sub` | User ID (also the authenticated identity used by Hub) |
| `username` | Human-readable username |
| `iat` | Issued-at timestamp |
| `exp` | Expiry timestamp (default 24 h) |

### Security properties
- Algorithm is pinned to HS256 (`jwt.WithValidMethods`); tokens signed with a different algorithm are rejected — prevents algorithm-confusion attacks.
- Expiry is enforced (`jwt.WithExpirationRequired()`).
- Secret minimum length is 32 characters, validated at startup.

---

## Password Security

`auth.UserStore` bcrypt-hashes all passwords at construction time (`bcrypt.DefaultCost = 10`).

**Timing-safe authentication**: when a username does not exist, a dummy bcrypt comparison is performed so the response time is indistinguishable from a wrong-password response. This prevents username enumeration via timing side-channels.

---

## JWT Middleware (`internal/adapter/http/middleware/jwt.go`)

Token lookup order:

1. `Authorization: Bearer <token>` header — for API clients, `wscat`, Postman.
2. `?token=<jwt>` query parameter — for browser `WebSocket` (JS cannot set custom headers on `new WebSocket(url)`).

On failure: `401 Unauthorized` with a generic `"invalid or expired token"` message. No information about *why* the token failed is leaked.

---

## Configuration

### Required

```bash
APP_JWT_SECRET=<min-32-character-random-string>
```

### Optional

```bash
APP_JWT_EXPIRY=24h   # default: 24h
```

### Config file (`configs/config.yaml`)

```yaml
jwt:
  secret: "your-secret-key-at-least-32-chars!!"
  expiry: "24h"
```

---

## API Reference

### `POST /auth/login`

**Request**
```json
{ "username": "alice", "password": "password" }
```

**Response 200**
```json
{ "token": "<signed-jwt>" }
```

**Response 400** — missing fields
```json
{ "error": "username and password are required" }
```

**Response 401** — wrong credentials
```json
{ "error": "invalid credentials" }
```

### `GET /ws` (JWT-protected)

**With header:**
```
GET /ws HTTP/1.1
Authorization: Bearer <token>
Upgrade: websocket
```

**With query param (browser):**
```
ws://localhost:8080/ws?token=<jwt>
```

**Response 401** — missing or invalid token
```json
{ "error": "missing token" }
{ "error": "invalid or expired token" }
```

---

## Running Locally

```bash
# Required: set a secret (min 32 chars)
export APP_JWT_SECRET="dev-secret-key-at-least-32-chars!!"

cd chat-server
go mod tidy
go run ./cmd/server

# 1. Get a token
curl -s -X POST http://localhost:8080/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"alice","password":"password"}' | jq .

# 2. Connect via wscat (npm install -g wscat)
wscat -c "ws://localhost:8080/ws" \
  -H "Authorization: Bearer <token>"

# 3. Join a room and send a message
{"event":"join","payload":{"room_id":"lobby"}}
{"event":"message","room_id":"lobby","payload":{"message":"hello"}}
```

---

## Security Review

| Concern | Status | Detail |
|---|---|---|
| Algorithm confusion | Mitigated | `jwt.WithValidMethods(["HS256"])` rejects unexpected algorithms |
| Token expiry | Enforced | `jwt.WithExpirationRequired()` |
| Secret strength | Validated | Startup panic if secret < 32 chars |
| Token leakage in logs | Safe | Only `userID` is logged, never the token string |
| Username enumeration | Mitigated | Constant-time dummy bcrypt on unknown users |
| Password storage | Secure | bcrypt (`DefaultCost=10`), never stored in plain text |
| WS upgrade without auth | Blocked | JWT middleware runs before `upgrader.Upgrade()` |
| `?user_id=` bypass | Removed | Handler reads only from `gin.Context`, not query params |

## Production Checklist

- [ ] Replace demo `UserStore` with a database-backed implementation
- [ ] Use HTTPS/WSS (TLS) — JWT in `?token=` query param is visible in server logs without TLS
- [ ] Store JWT secret in a secrets manager (Vault, AWS Secrets Manager, etc.)
- [ ] Add token revocation (Redis-backed deny-list) for logout/session invalidation
- [ ] Add refresh tokens for longer sessions without re-login
