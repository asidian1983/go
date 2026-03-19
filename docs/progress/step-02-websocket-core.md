# 2단계: WebSocket 코어 (backend-dev)

## 개요

gorilla/websocket 기반의 고성능 WebSocket 코어를 구현합니다.
Hub-Client 패턴으로 10K+ 동시 연결을 처리합니다.

---

## 구현 파일

```
chat-server/internal/adapter/ws/
├── hub.go       # 중앙 연결 매니저 (이벤트 루프)
├── client.go    # 단일 WS 연결 수명주기 (readPump / writePump)
└── handler.go   # HTTP → WebSocket 업그레이드 �핸들러

chat-server/internal/adapter/http/
└── router.go    # GET /ws 라우트 추가

chat-server/cmd/server/
└── main.go      # Hub 와이어링 및 graceful shutdown 연동
```

---

## 동시성 모델

```
                   ┌─────────────────────────────┐
                   │         Hub.Run()            │
                   │    (single goroutine)        │
                   │                              │
                   │  clients map — NO mutex      │
                   │  owned exclusively here      │
                   └────────────┬────────────────┘
                                │ select on 4 channels
         ┌──────────────────────┼─────────────────────┐
         │                      │                      │
      Register             Unregister             Broadcast
        chan                  chan                   chan
         │                      │                      │
┌────────▼──────────────────────▼──────────────────────▼─────────┐
│                     Client (연결 1개당)                          │
│                                                                  │
│  readPump goroutine            writePump goroutine               │
│  ─────────────────             ──────────────────                │
│  conn.ReadMessage()            ← client.send chan                │
│         │                      conn.WriteMessage()               │
│         ▼                      ping ticker (54s keepalive)       │
│  hub.Broadcast ──────────────────────────────────────────►      │
└──────────────────────────────────────────────────────────────────┘
```

---

## 핵심 컴포넌트

### `ws/hub.go` — 중앙 이벤트 루프

| 채널 | 버퍼 | 역할 |
|------|------|------|
| `Register` | 256 | 신규 클라이언트 등록 |
| `Unregister` | 256 | 클라이언트 제거 + send 채널 close |
| `Broadcast` | 512 | 전체 클라이언트에 메시지 전파 |

- `clients map`은 `Run()` goroutine이 단독 소유 → **mutex 없음**
- 느린 클라이언트(send 버퍼 풀): 즉시 drop + 강제 제거
- `stop` 채널 수신 시 모든 클라이언트 정리 후 종료

### `ws/client.go` — 단일 연결 수명주기

| 상수 | 값 | 역할 |
|------|-----|------|
| `writeWait` | 10s | 쓰기 데드라인 |
| `pongWait` | 60s | pong 수신 대기 |
| `pingPeriod` | 54s | ping 전송 주기 (< pongWait) |
| `maxMessageSize` | 4096 bytes | 읽기 크기 제한 |

**readPump**
- `conn.ReadMessage()`만 호출 (단독 goroutine → concurrent read 없음)
- 메시지 수신 시 `hub.Broadcast`로 전달
- 오류 또는 close 시 `hub.Unregister`로 자신을 제거

**writePump**
- `conn.WriteMessage()`만 호출 (단독 goroutine → concurrent write 없음)
- `client.send` 채널에서 메시지를 받아 WS 프레임으로 전송
- 큐에 대기 중인 메시지를 하나의 write frame에 일괄 처리 (throughput 향상)
- ticker로 ping 전송 → 죽은 연결 조기 감지

**클라이언트 ID**
- `crypto/rand` 기반 8바이트 hex string (외부 의존성 없음)

### `ws/handler.go` — HTTP → WS 업그레이드

- `websocket.Upgrader`: ReadBuffer/WriteBuffer 4096 bytes
- JWT 검증 전 업그레이드 차단 (다음 단계에서 미들웨어로 추가)
- 업그레이드 성공 후 즉시 `Register` 전송 → read/write pump goroutine 시작

---

## 엔드포인트

| Method | Path | 설명 |
|--------|------|------|
| `GET` | `/ws` | WebSocket 연결 업그레이드 |
| `GET` | `/health` | 헬스체크 (기존 유지) |

---

## Goroutine 예산

```
Hub.Run()          →       1 goroutine
Client.readPump    →  10,000 goroutines
Client.writePump   →  10,000 goroutines
────────────────────────────────────────
합계               →  20,001 goroutines  (~160MB stack baseline)
```

---

## 핵심 설계 결정

| 결정 | 이유 |
|------|------|
| Hub를 단일 goroutine으로 실행 | map 단독 소유로 mutex 제거 |
| 모든 상태 변경을 channel로 전달 | 호출자가 공유 상태에 직접 접근 불가 |
| send 버퍼 풀 시 drop + close | back-pressure 대신 느린 클라이언트 제거로 전체 처리량 보호 |
| readPump / writePump 분리 | conn에 concurrent access 원천 차단 |
| NextWriter로 배치 쓰기 | 여러 메시지를 하나의 프레임으로 묶어 syscall 절감 |

---

## 실행 및 테스트

```bash
cd chat-server
go mod tidy
go run ./cmd/server/main.go

# WebSocket 연결 테스트 (wscat 사용)
wscat -c ws://localhost:8080/ws

# 메시지 전송 → 같은 서버에 연결된 모든 클라이언트에 브로드캐스트
> hello world
```

---

## 다음 단계

- **3단계**: JWT 인증 미들웨어 (`/ws` 업그레이드 전 토큰 검증)
- **4단계**: Redis Pub/Sub 연동 (다중 서버 인스턴스 간 메시지 전파)
- **5단계**: PostgreSQL 메시지 영속화 (비동기 배치 insert)
