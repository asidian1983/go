package ws

import (
	"encoding/json"
	"time"
)

// EventType identifies the kind of a WebSocket frame.
type EventType string

const (
	// Client → Server events.
	EventJoin    EventType = "join"    // join a room
	EventLeave   EventType = "leave"   // leave a room
	EventMessage EventType = "message" // send a chat message to a room

	// Server → Client events.
	EventAck    EventType = "ack"    // operation succeeded
	EventError  EventType = "error"  // operation rejected
	EventNotify EventType = "notify" // system notification (member joined / left)
)

// Envelope is the top-level wrapper for every WebSocket frame.
// All fields are JSON-serialised; Payload holds an event-specific sub-document.
type Envelope struct {
	Event   EventType       `json:"event"`
	RoomID  string          `json:"room_id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ---------- Inbound payloads (client → server) ----------

// JoinPayload is the body of an EventJoin frame.
type JoinPayload struct {
	RoomID string `json:"room_id"`
}

// LeavePayload is the body of an EventLeave frame.
type LeavePayload struct {
	RoomID string `json:"room_id"`
}

// SendPayload is the body of an EventMessage frame.
type SendPayload struct {
	Message string `json:"message"`
}

// ---------- Outbound payloads (server → client) ----------

// ChatMessage is the authoritative message record delivered to every room member,
// including the original sender (echo-back pattern for delivery confirmation).
type ChatMessage struct {
	SenderID  string    `json:"sender_id"`
	RoomID    string    `json:"room_id"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// AckPayload confirms the result of a join or leave operation.
type AckPayload struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// ErrorPayload describes a rejected operation.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NotifyPayload carries a system-generated room notification.
type NotifyPayload struct {
	RoomID string `json:"room_id"`
	UserID string `json:"user_id"`
	Text   string `json:"text"`
}

// ---------- Helpers ----------

// mustMarshal serialises v to JSON. It panics only on non-serialisable types
// (programming errors), never on runtime data.
func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("ws: mustMarshal: " + err.Error())
	}
	return b
}

// buildEnvelope constructs a complete, marshalled WebSocket frame.
func buildEnvelope(event EventType, roomID string, payload any) []byte {
	b, _ := json.Marshal(Envelope{
		Event:   event,
		RoomID:  roomID,
		Payload: mustMarshal(payload),
	})
	return b
}
