package http

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/asidian1983/chat-server/internal/adapter/http/middleware"
	"github.com/asidian1983/chat-server/internal/infrastructure/auth"
)

type Router struct {
	health *HealthHandler
	auth   *AuthHandler
	ws     wsHandler
	jwt    *auth.Service
}

// wsHandler is a narrow interface so the router does not import the ws package directly.
type wsHandler interface {
	ServeWS(c *gin.Context)
}

func NewRouter(health *HealthHandler, authH *AuthHandler, ws wsHandler, jwt *auth.Service) *Router {
	return &Router{health: health, auth: authH, ws: ws, jwt: jwt}
}

func (r *Router) Register(engine *gin.Engine) {
	engine.Use(gin.Recovery())

	// Public endpoints — no auth required
	engine.GET("/health", r.health.Check)
	engine.POST("/auth/login", r.auth.Login)

	// Protected endpoints — JWT required
	protected := engine.Group("/")
	protected.Use(middleware.JWT(r.jwt))
	{
		protected.GET("/ws", r.ws.ServeWS)
	}

	engine.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})
}
