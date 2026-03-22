package ws

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/asidian1983/chat-server/internal/domain/entity"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 4096
)

// Client represents a single authenticated WebSocket connection.
//
// Concurrency model:
//   - ReadPump runs in one dedicated goroutine; it is the only reader of conn.
//   - WritePump runs in another dedicated goroutine; it is the only writer of conn.
//   - client.rooms is owned by Hub.Run() and must never be accessed from
//     ReadPump or WritePump directly — all mutations flow through hub channels.
type Client struct {
	// ID is the unique connection identifier (differs from UserID for multi-device).
	ID string
	// UserID is the authenticated identity of the connected user.
	UserID string

	// rooms tracks which rooms this client has joined.
	// Owned exclusively by Hub.Run(); must not be touched outside that goroutine.
	rooms map[string]struct{}

	hub  *Hub
	conn *websocket.Conn
	send chan []byte
	log  *zap.Logger
}

func newClientID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewClient constructs a Client for the given WebSocket connection and user.
func NewClient(hub *Hub, conn *websocket.Conn, userID string, log *zap.Logger) *Client {
	return &Client{
		ID:     newClientID(),
		UserID: userID,
		rooms:  make(map[string]struct{}),
		hub:    hub,
		conn:   conn,
		send:   make(chan []byte, 256),
		log:    log,
	}
}

// ReadPump pumps inbound WebSocket frames to the hub.
// One goroutine per client; unregisters on exit.
func (c *Client) ReadPump() {
	defer func() {
		c.hub.Unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure,
			) {
				c.log.Warn("unexpected close",
					zap.String("clientID", c.ID),
					zap.String("userID", c.UserID),
					zap.Error(err),
				)
			}
			return
		}
		c.route(raw)
	}
}

// WritePump pumps outbound messages from the send channel to the WebSocket.
// One goroutine per client; sends a close frame when the hub closes send.
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel — send a close frame and exit.
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			_, _ = w.Write(msg)

			// Batch any already-queued messages into the same write frame.
			n := len(c.send)
			for range n {
				_, _ = w.Write([]byte{'\n'})
				_, _ = w.Write(<-c.send)
			}
			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// route parses an inbound frame and dispatches to the appropriate hub channel.
func (c *Client) route(raw []byte) {
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		c.log.Debug("malformed envelope",
			zap.String("clientID", c.ID),
			zap.Error(err),
		)
		c.sendErr("bad_request", "invalid JSON envelope")
		return
	}

	switch env.Event {
	case EventJoin:
		var p JoinPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.RoomID == "" {
			c.sendErr("bad_request", "join requires a non-empty room_id in payload")
			return
		}
		c.hub.Join <- joinCmd{client: c, roomID: p.RoomID}

	case EventLeave:
		var p LeavePayload
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.RoomID == "" {
			c.sendErr("bad_request", "leave requires a non-empty room_id in payload")
			return
		}
		c.hub.Leave <- leaveCmd{client: c, roomID: p.RoomID}

	case EventMessage:
		if env.RoomID == "" {
			c.sendErr("bad_request", "message requires a non-empty room_id in the envelope")
			return
		}
		var p SendPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil || p.Message == "" {
			c.sendErr("bad_request", "message requires a non-empty message in payload")
			return
		}
		msgID := newClientID() // reuse same random-hex generator for message IDs
		now := time.Now().UTC()
		chat := ChatMessage{
			ID:        msgID,
			SenderID:  c.UserID,
			RoomID:    env.RoomID,
			Message:   p.Message,
			CreatedAt: now,
		}
		c.hub.Broadcast <- roomcast{
			roomID: env.RoomID,
			msg:    buildEnvelope(EventMessage, env.RoomID, chat),
			sender: c,
			message: &entity.Message{
				ID:        msgID,
				RoomID:    entity.RoomID(env.RoomID),
				SenderID:  entity.UserID(c.UserID),
				Type:      entity.MsgTypeChat,
				Body:      p.Message,
				CreatedAt: now,
			},
		}

	default:
		c.sendErr("unknown_event", "unrecognised event type: "+string(env.Event))
	}
}

// sendErr enqueues an error frame; drops silently if the send buffer is full.
func (c *Client) sendErr(code, message string) {
	frame := buildEnvelope(EventError, "", ErrorPayload{Code: code, Message: message})
	select {
	case c.send <- frame:
	default:
		c.log.Warn("send buffer full; dropping error frame",
			zap.String("clientID", c.ID),
		)
	}
}
