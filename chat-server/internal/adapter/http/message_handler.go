package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/asidian1983/chat-server/internal/domain/entity"
	"github.com/asidian1983/chat-server/internal/domain/repository"
)

const (
	defaultLimit = 50
	maxLimit     = 100
)

// MessageHandler serves chat history for a room.
type MessageHandler struct {
	repo repository.MessageRepository
	log  *zap.Logger
}

// NewMessageHandler constructs a MessageHandler.
func NewMessageHandler(repo repository.MessageRepository, log *zap.Logger) *MessageHandler {
	return &MessageHandler{repo: repo, log: log}
}

// History returns paginated message history for a room.
//
// GET /rooms/:id/messages
// Query params:
//
//	limit  – number of messages to return (default 50, max 100)
//	before – RFC3339 timestamp; return messages created before this time (default: now)
//
// Response 200: JSON array of messages, newest-first.
func (h *MessageHandler) History(c *gin.Context) {
	roomID := entity.RoomID(c.Param("id"))
	if roomID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "room id is required"})
		return
	}

	limit := defaultLimit
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be a positive integer"})
			return
		}
		if n > maxLimit {
			n = maxLimit
		}
		limit = n
	}

	before := time.Now().UTC()
	if raw := c.Query("before"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "before must be an RFC3339 timestamp"})
			return
		}
		before = t
	}

	msgs, err := h.repo.ByRoom(c.Request.Context(), roomID, limit, before)
	if err != nil {
		h.log.Error("message history query failed",
			zap.String("roomID", string(roomID)),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not retrieve messages"})
		return
	}

	// Return an empty array rather than null when there are no messages.
	if msgs == nil {
		msgs = []entity.Message{}
	}
	c.JSON(http.StatusOK, msgs)
}
