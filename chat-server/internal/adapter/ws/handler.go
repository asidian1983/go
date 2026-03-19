package ws

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// TODO: replace with an allowlist check in production
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Handler handles the HTTP → WebSocket upgrade.
type Handler struct {
	hub *Hub
	log *zap.Logger
}

func NewHandler(hub *Hub, log *zap.Logger) *Handler {
	return &Handler{hub: hub, log: log}
}

// ServeWS upgrades the HTTP connection to WebSocket, creates a Client,
// registers it with the Hub, and starts its read/write pumps.
//
// Each pump runs in its own goroutine so this function returns immediately
// after registration — it never blocks the HTTP handler goroutine.
func (h *Handler) ServeWS(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.log.Error("websocket upgrade failed", zap.Error(err))
		return
	}

	client := NewClient(h.hub, conn, h.log)
	h.hub.Register <- client

	h.log.Info("new websocket connection", zap.String("id", client.ID))

	// Pump goroutines own the connection from here on.
	go client.WritePump()
	go client.ReadPump()
}
