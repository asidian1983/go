# 1단계: 서버 뼈대 생성 (backend-dev)

## 개요

고성능 WebSocket 채팅 서버의 기반 HTTP 서버 구조를 구현합니다.

## 구현 내용

### 기술 스택
- **Framework**: Gin v1.10
- **Config**: Viper (환경변수 기반, 12-factor)
- **Logger**: Uber Zap (zero-alloc structured logging)
- **Shutdown**: `os/signal` + `context.WithTimeout`

---

## 파일 구조

```
chat-server/
├── go.mod
├── cmd/
│   └── server/
│       └── main.go                         # 진입점, DI 와이어링, graceful shutdown
├── internal/
│   ├── adapter/
│   │   └── http/
│   │       ├── server.go                   # *http.Server 래핑, Run/Shutdown
│   │       ├── router.go                   # gin.Engine 라우트 등록
│   │       └── health_handler.go           # GET /health
│   └── infrastructure/
│       └── config/
│           └── config.go                   # Config 구조체, Viper 로딩
└── pkg/
    └── logger/
        └── logger.go                       # 환경별 zap.Logger 팩토리
```

---

## 핵심 컴포넌트

### `cmd/server/main.go`
- 모든 의존성을 수동으로 와이어링 (DI 프레임워크 없음, 단순 명시적 주입)
- 서버를 goroutine으로 실행 후 `SIGINT` / `SIGTERM` 대기
- 시그널 수신 시 `ShutdownTimeout` 내 graceful shutdown 수행

### `internal/adapter/http/server.go`
- `*http.Server` 직접 생성 — `ReadTimeout`, `WriteTimeout` 적용
- `Run()`: 블로킹 실행, `ErrServerClosed` 정상 처리
- `Shutdown(ctx)`: context 기반 graceful drain

### `internal/adapter/http/router.go`
- `gin.Recovery()` 미들웨어 전역 등록
- `NoRoute` 핸들러로 404 표준화
- 핸들러를 생성자로 주입받아 DI 친화적 구조

### `internal/adapter/http/health_handler.go`
- `GET /health` → `{"status":"ok","timestamp":"..."}` 반환
- 인증 불필요, ops 전용 엔드포인트

### `internal/infrastructure/config/config.go`
- 환경변수 prefix: `APP_`
- config 파일(yaml) 선택적 — 없어도 기본값으로 동작
- 주요 설정값:

| 환경변수 | 기본값 | 설명 |
|---------|--------|------|
| `APP_ENV` | `development` | 실행 환경 |
| `APP_SERVER_PORT` | `8080` | 리슨 포트 |
| `APP_SERVER_READ_TIMEOUT` | `10s` | 읽기 타임아웃 |
| `APP_SERVER_WRITE_TIMEOUT` | `10s` | 쓰기 타임아웃 |
| `APP_SERVER_SHUTDOWN_TIMEOUT` | `15s` | 종료 대기 시간 |

### `pkg/logger/logger.go`
- `development`: 컬러 콘솔 출력
- `production`: JSON 구조화 로그, ISO8601 타임스탬프

---

## 실행 방법

```bash
cd chat-server
go mod tidy
go run ./cmd/server/main.go

# 환경변수 지정
APP_ENV=production APP_SERVER_PORT=9090 go run ./cmd/server/main.go

# 헬스체크
curl http://localhost:8080/health
# {"status":"ok","timestamp":"2026-03-19T00:00:00Z"}
```

---

## 다음 단계

- **2단계**: WebSocket 업그레이드 + Hub/Client 구현
- **3단계**: JWT 인증 미들웨어
- **4단계**: Redis Pub/Sub 연동
- **5단계**: PostgreSQL 메시지 영속화
