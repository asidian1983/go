package ws

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	writeWait      = 10 * time.Second    // max time to write a message
	pongWait       = 60 * time.Second    // max time to wait for pong
	pingPeriod     = (pongWait * 9) / 10 // must be < pongWait
	maxMessageSize = 4096                 // bytes
)

// Client represents a single WebSocket connection.
//
// Concurrency model:
//   - readPump runs in one dedicated goroutine.
//   - writePump runs in another dedicated goroutine.
//   - The two pumps communicate only through the hub (via channels),
//     never sharing state with each other directly.
//   - conn.WriteMessage is called only from writePump → no concurrent writes.
//   - conn.ReadMessage is called only from readPump  → no concurrent reads.
type Client struct {
	// ID uniquely identifies this connection across the hub.
	ID string

	hub  *Hub
	conn *websocket.Conn

	// send is a buffered channel of outbound messages.
	// Owned by writePump; written to by Hub.Run only.
	send chan []byte

	log *zap.Logger
}

func newClientID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func NewClient(hub *Hub, conn *websocket.Conn, log *zap.Logger) *Client {
	return &Client{
		ID:   newClientID(),
		hub:  hub,
		conn: conn,
		send: make(chan []byte, 256),
		log:  log,
	}
}

// ReadPump pumps messages from the WebSocket connection to the hub.
//
// One goroutine per client. Exits on read error or close, then
// unregisters the client from the hub.
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
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure,
			) {
				c.log.Warn("unexpected close", zap.String("id", c.ID), zap.Error(err))
			}
			break
		}
		c.hub.Broadcast <- message
	}
}

// WritePump pumps messages from the client's send channel to the WebSocket.
//
// One goroutine per client. A ticker sends periodic pings to detect
// dead connections. Exits when the send channel is closed by the hub.
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel — send a close frame.
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			_, _ = w.Write(message)

			// Flush any queued messages in the same write frame.
			n := len(c.send)
			for range n {
				_, _ = w.Write([]byte{'\n'})
				msg := <-c.send
				_, _ = w.Write(msg)
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
