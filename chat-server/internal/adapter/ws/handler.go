package ws

import (
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/asidian1983/chat-server/internal/adapter/http/middleware"
)

// Handler manages the HTTP → WebSocket upgrade.
type Handler struct {
	hub      *Hub
	upgrader websocket.Upgrader
	log      *zap.Logger
}

// NewHandler constructs a Handler wired to the given Hub.
// allowedOrigins is the set of origins permitted for WebSocket upgrades.
// An empty slice means same-host check only (recommended for production).
// Pass []string{"*"} to allow all origins (development only).
func NewHandler(hub *Hub, allowedOrigins []string, log *zap.Logger) *Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = struct{}{}
	}

	u := websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				// Non-browser clients (wscat, load tester, etc.) send no Origin.
				return true
			}
			// Wildcard: allow all origins (dev only).
			if _, ok := allowed["*"]; ok {
				return true
			}
			// Explicit allowlist match.
			if _, ok := allowed[origin]; ok {
				return true
			}
			// Same-host fallback: compare origin host to request host.
			u, err := url.Parse(origin)
			if err != nil {
				return false
			}
			return u.Host == r.Host
		},
	}

	return &Handler{hub: hub, upgrader: u, log: log}
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

	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
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
