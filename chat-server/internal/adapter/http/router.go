package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Router struct {
	health *HealthHandler
	ws     wsHandler
}

// wsHandler is a narrow interface so the router does not import the ws package directly.
type wsHandler interface {
	ServeWS(c *gin.Context)
}

func NewRouter(health *HealthHandler, ws wsHandler) *Router {
	return &Router{health: health, ws: ws}
}

func (r *Router) Register(engine *gin.Engine) {
	// Global middleware
	engine.Use(gin.Recovery())

	// Ops endpoints — no auth required
	engine.GET("/health", r.health.Check)

	// WebSocket endpoint
	engine.GET("/ws", r.ws.ServeWS)

	// 404 handler
	engine.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})
}
