package ws

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
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
// Requires ?user_id=<id> query parameter.
// In production, replace this with JWT validation in middleware and read the
// authenticated identity from gin.Context (e.g. c.GetString("userID")).
func (h *Handler) ServeWS(c *gin.Context) {
	userID := strings.TrimSpace(c.Query("user_id"))
	if userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id query parameter is required"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.log.Error("websocket upgrade failed", zap.Error(err))
		return
	}

	client := NewClient(h.hub, conn, userID, h.log)
	h.hub.Register <- client

	h.log.Info("new websocket connection",
		zap.String("clientID", client.ID),
		zap.String("userID", client.UserID),
	)

	// Pump goroutines own the connection from here; this handler returns immediately.
	go client.WritePump()
	go client.ReadPump()
}
