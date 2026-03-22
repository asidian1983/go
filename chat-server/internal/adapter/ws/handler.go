package ws

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/asidian1983/chat-server/internal/adapter/http/middleware"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// TODO: replace with an origin allowlist in production.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Handler manages the HTTP → WebSocket upgrade.
type Handler struct {
	hub *Hub
	log *zap.Logger
}

// NewHandler constructs a Handler wired to the given Hub.
func NewHandler(hub *Hub, log *zap.Logger) *Handler {
	return &Handler{hub: hub, log: log}
}

// ServeWS upgrades the connection to WebSocket and starts the client pumps.
//
// The JWT middleware (applied at the router level) has already validated the
// token and stored the authenticated userID in gin.Context before this handler
// is called. No token handling is done here.
func (h *Handler) ServeWS(c *gin.Context) {
	userID, ok := c.Get(middleware.UserIDKey)
	if !ok {
		// Should never happen if the JWT middleware is correctly applied.
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.log.Error("websocket upgrade failed", zap.Error(err))
		return
	}

	client := NewClient(h.hub, conn, userID.(string), h.log)
	h.hub.Register <- client

	h.log.Info("new websocket connection",
		zap.String("clientID", client.ID),
		zap.String("userID", client.UserID),
	)

	// Pump goroutines own the connection from here; this handler returns immediately.
	go client.WritePump()
	go client.ReadPump()
}
