package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	httpadapter "github.com/asidian1983/chat-server/internal/adapter/http"
	"github.com/asidian1983/chat-server/internal/infrastructure/config"
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

	// Wire dependencies
	healthHandler := httpadapter.NewHealthHandler()
	router := httpadapter.NewRouter(healthHandler)
	server := httpadapter.NewServer(cfg, router, log)

	// Start server in background
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.Run()
	}()

	// Wait for interrupt or server error
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		log.Fatal("server error", zap.Error(err))
	case sig := <-quit:
		log.Info("received signal", zap.String("signal", sig.String()))
	}

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("forced shutdown", zap.Error(err))
	}

	log.Info("server exited")
}
