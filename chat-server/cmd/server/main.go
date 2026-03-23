package main

import (
	"context"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	httpadapter "github.com/asidian1983/chat-server/internal/adapter/http"
	wsadapter "github.com/asidian1983/chat-server/internal/adapter/ws"
	"github.com/asidian1983/chat-server/internal/domain/repository"
	"github.com/asidian1983/chat-server/internal/infrastructure/auth"
	"github.com/asidian1983/chat-server/internal/infrastructure/config"
	"github.com/asidian1983/chat-server/internal/infrastructure/postgres"
	redispubsub "github.com/asidian1983/chat-server/internal/infrastructure/redis"
	"github.com/asidian1983/chat-server/pkg/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	log, err := logger.New(cfg.Env)
	if err != nil {
		panic("failed to init logger: " + err.Error())
	}
	defer log.Sync() //nolint:errcheck

	// ── Auth ──────────────────────────────────────────────────────────────────
	jwtSvc, err := auth.NewService(cfg.JWT.Secret, cfg.JWT.Expiry)
	if err != nil {
		panic("failed to init jwt: " + err.Error())
	}

	// Demo user store — replace with a database-backed implementation in production.
	demoUsers, err := auth.NewUserStore(map[string]string{
		"alice": "password",
		"bob":   "password",
	})
	if err != nil {
		panic("failed to init user store: " + err.Error())
	}

	// ── Redis Pub/Sub (optional) ──────────────────────────────────────────────
	var rps *redispubsub.Manager
	redisStop := make(chan struct{})
	if cfg.Redis.Enabled {
		rps = redispubsub.New(cfg.Redis.Addr, log)
		go rps.Run(redisStop)
		log.Info("redis pubsub enabled", zap.String("addr", cfg.Redis.Addr))
	}

	// ── Postgres (optional) ───────────────────────────────────────────────────
	var msgRepo repository.MessageRepository
	var readRepo repository.ReadRepository
	if cfg.Postgres.Enabled {
		pool, err := postgres.Open(context.Background(), cfg.Postgres.DSN)
		if err != nil {
			panic("failed to connect to postgres: " + err.Error())
		}
		defer pool.Close() // safe: closed only after hubDone (see shutdown below)
		repo := postgres.NewMessageRepo(pool)
		msgRepo = repo
		readRepo = repo
		// Redact password before logging (HIGH-7).
		log.Info("postgres enabled", zap.String("dsn", redactDSN(cfg.Postgres.DSN)))
	}

	// ── Hub ───────────────────────────────────────────────────────────────────
	hub := wsadapter.NewHub(log, rps, msgRepo, readRepo)
	hubStop := make(chan struct{})
	hubDone := make(chan struct{})
	go func() {
		hub.Run(hubStop)
		close(hubDone)
	}()

	// ── HTTP wiring ───────────────────────────────────────────────────────────
	healthHandler := httpadapter.NewHealthHandler()
	authHandler := httpadapter.NewAuthHandler(demoUsers, jwtSvc, log)
	var messagesHandler *httpadapter.MessageHandler
	if msgRepo != nil {
		messagesHandler = httpadapter.NewMessageHandler(msgRepo, log)
	}
	wsHandler := wsadapter.NewHandler(hub, cfg.Server.AllowedOrigins, log)
	router := httpadapter.NewRouter(healthHandler, authHandler, messagesHandler, wsHandler, jwtSvc)
	server := httpadapter.NewServer(cfg, router, log)

	// ── Start ─────────────────────────────────────────────────────────────────
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		log.Fatal("server error", zap.Error(err))
	case sig := <-quit:
		log.Info("received signal", zap.String("signal", sig.String()))
	}

	// ── Graceful shutdown (CRITICAL-2: ordered teardown) ──────────────────────
	// Order:
	//   1. Stop accepting new HTTP/WS connections (drains in-flight HTTP requests).
	//   2. Stop the Hub event loop; wait for it to finish all in-flight DB writes.
	//   3. Stop Redis (hub is idle — no more publishes in flight).
	//   4. pool.Close() via defer (safe: hubDone guarantees no active DB calls).
	shutCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutCtx); err != nil {
		// LOW-1: use Error, not Fatal — deferred cleanup must still run.
		log.Error("HTTP server forced shutdown", zap.Error(err))
	}

	close(hubStop)
	<-hubDone // blocks until Hub.Run() returns (including persistWG.Wait())

	close(redisStop) // stop Redis after hub is fully idle

	log.Info("server exited")
}

// redactDSN returns the DSN with the password stripped for safe logging.
func redactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "[unparseable DSN]"
	}
	if u.User != nil {
		u.User = url.User(u.User.Username()) // keep username, drop password
	}
	return u.String()
}
