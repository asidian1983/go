package main

import (
	"context"
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
	if cfg.Redis.Enabled {
		rps = redispubsub.New(cfg.Redis.Addr, log)
		redisStop := make(chan struct{})
		go rps.Run(redisStop)
		defer close(redisStop)
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
		defer pool.Close()
		repo := postgres.NewMessageRepo(pool)
		msgRepo = repo
		readRepo = repo
		log.Info("postgres enabled", zap.String("dsn", cfg.Postgres.DSN))
	}

	// ── Hub ───────────────────────────────────────────────────────────────────
	hub := wsadapter.NewHub(log, rps, msgRepo, readRepo)
	hubStop := make(chan struct{})
	go hub.Run(hubStop)

	// ── HTTP wiring ───────────────────────────────────────────────────────────
	healthHandler := httpadapter.NewHealthHandler()
	authHandler := httpadapter.NewAuthHandler(demoUsers, jwtSvc, log)
	var messagesHandler *httpadapter.MessageHandler
	if msgRepo != nil {
		messagesHandler = httpadapter.NewMessageHandler(msgRepo, log)
	}
	wsHandler := wsadapter.NewHandler(hub, log)
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

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("forced shutdown", zap.Error(err))
	}

	close(hubStop)
	log.Info("server exited")
}
