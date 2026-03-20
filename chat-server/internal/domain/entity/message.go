package entity

import "time"

// MessageType classifies a message as user-generated chat or a system notification.
type MessageType string

const (
	// MsgTypeChat is a message authored by a user.
	MsgTypeChat MessageType = "chat"
	// MsgTypeSystem is a server-generated notification (join, leave, etc.).
	MsgTypeSystem MessageType = "system"
)

// Message is an immutable record of a single chat event.
type Message struct {
	ID        string      `json:"id"`
	RoomID    RoomID      `json:"room_id"`
	SenderID  UserID      `json:"sender_id"`
	Type      MessageType `json:"type"`
	Body      string      `json:"body"`
	CreatedAt time.Time   `json:"created_at"`
}
