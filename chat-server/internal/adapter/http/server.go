package http

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/asidian1983/chat-server/internal/infrastructure/config"
)

type Server struct {
	httpServer *http.Server
	router     *Router
	cfg        *config.Config
	log        *zap.Logger
}

func NewServer(cfg *config.Config, router *Router, log *zap.Logger) *Server {
	return &Server{
		router: router,
		cfg:    cfg,
		log:    log,
	}
}

func (s *Server) Run() error {
	if s.cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()
	engine.Use(gin.Logger())

	s.router.Register(engine)

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%s", s.cfg.Server.Port),
		Handler:      engine,
		ReadTimeout:  s.cfg.Server.ReadTimeout,
		WriteTimeout: s.cfg.Server.WriteTimeout,
	}

	s.log.Info("server starting", zap.String("port", s.cfg.Server.Port), zap.String("env", s.cfg.Env))

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("listen: %w", err)
	}

	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.log.Info("server shutting down gracefully...")
	return s.httpServer.Shutdown(ctx)
}
