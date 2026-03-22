package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/asidian1983/chat-server/internal/domain/entity"
)

// MessageRepo is the PostgreSQL implementation of repository.MessageRepository.
type MessageRepo struct {
	pool *pgxpool.Pool
}

// NewMessageRepo constructs a MessageRepo backed by pool.
func NewMessageRepo(pool *pgxpool.Pool) *MessageRepo {
	return &MessageRepo{pool: pool}
}

// Save inserts a message. Duplicate IDs are silently ignored (idempotent).
func (r *MessageRepo) Save(ctx context.Context, msg *entity.Message) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO messages (id, room_id, sender_id, type, body, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO NOTHING`,
		msg.ID,
		string(msg.RoomID),
		string(msg.SenderID),
		string(msg.Type),
		msg.Body,
		msg.CreatedAt,
	)
	return err
}

// ByRoom returns up to limit messages for roomID with created_at < before,
// ordered newest-first (caller reverses for display if needed).
func (r *MessageRepo) ByRoom(ctx context.Context, roomID entity.RoomID, limit int, before time.Time) ([]entity.Message, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, room_id, sender_id, type, body, created_at
		FROM   messages
		WHERE  room_id = $1 AND created_at < $2
		ORDER  BY created_at DESC
		LIMIT  $3`,
		string(roomID), before, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []entity.Message
	for rows.Next() {
		var (
			m            entity.Message
			roomIDStr    string
			senderIDStr  string
			typeStr      string
		)
		if err := rows.Scan(&m.ID, &roomIDStr, &senderIDStr, &typeStr, &m.Body, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.RoomID = entity.RoomID(roomIDStr)
		m.SenderID = entity.UserID(senderIDStr)
		m.Type = entity.MessageType(typeStr)
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
