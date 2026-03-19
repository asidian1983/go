package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Router struct {
	health *HealthHandler
}

func NewRouter(health *HealthHandler) *Router {
	return &Router{health: health}
}

func (r *Router) Register(engine *gin.Engine) {
	// Global middleware
	engine.Use(gin.Recovery())

	// Ops endpoints — no auth required
	engine.GET("/health", r.health.Check)

	// 404 handler
	engine.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})
}
