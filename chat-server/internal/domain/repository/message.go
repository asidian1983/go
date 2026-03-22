package repository

import (
	"context"
	"time"

	"github.com/asidian1983/chat-server/internal/domain/entity"
)

// MessageRepository is the persistence contract for chat messages.
type MessageRepository interface {
	// Save persists a single message. Duplicate IDs must be silently ignored.
	Save(ctx context.Context, msg *entity.Message) error

	// ByRoom returns up to limit messages for a room with created_at < before,
	// ordered newest-first. Pass time.Now() to get the latest page.
	ByRoom(ctx context.Context, roomID entity.RoomID, limit int, before time.Time) ([]entity.Message, error)
}
