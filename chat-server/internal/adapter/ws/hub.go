package ws

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/asidian1983/chat-server/internal/domain/entity"
	"github.com/asidian1983/chat-server/internal/domain/repository"
	redispubsub "github.com/asidian1983/chat-server/internal/infrastructure/redis"
)

// ---------- Internal command types ----------
// All Hub state mutations are serialised through channels so that Hub.Run()
// is the sole owner of clients and rooms — no mutex is ever required.

type joinCmd struct {
	client *Client
	roomID string
}

type leaveCmd struct {
	client *Client
	roomID string
}

type readReceiptCmd struct {
	client    *Client
	roomID    string
	messageID string
}

// roomcast delivers msg to every member of roomID.
// sender is the originating client (used for authorisation); nil means a
// server-generated broadcast that skips the membership check.
// message is set for user chat messages and used for persistence; nil for system events.
type roomcast struct {
	roomID  string
	msg     []byte
	sender  *Client
	message *entity.Message
}

// Hub is the central, room-aware connection manager.
//
// Concurrency model:
//   - Hub.Run() occupies exactly ONE goroutine.
//   - clients and rooms maps are owned exclusively by that goroutine → no mutex.
//   - All external interaction (register, unregister, join, leave, broadcast)
//     happens through buffered channels, making the API goroutine-safe by
//     construction.
//
// Message flow (single-node):
//
//	Client.ReadPump ──► hub.Join / hub.Leave / hub.Broadcast
//	                              │
//	                         Hub.Run()
//	                              │
//	                       fanout(roomID)
//	                              │
//	               client.send ──► Client.WritePump ──► WebSocket
//
// Message flow (multi-node with Redis):
//
//	Client.ReadPump ──► hub.Broadcast
//	                              │
//	                         Hub.Run()
//	                              │
//	                      Redis PUBLISH
//	                              │
//	              (all nodes) Redis SUBSCRIBE
//	                              │
//	                    Hub.remoteDeliver
//	                              │
//	                       fanout(roomID)
//	                              │
//	               client.send ──► Client.WritePump ──► WebSocket
type Hub struct {
	// clients: connectionID → *Client. Owned by Run().
	clients map[string]*Client
	// rooms: roomID → (connectionID → *Client). Owned by Run().
	rooms map[string]map[string]*Client

	Register    chan *Client
	Unregister  chan *Client
	Join        chan joinCmd
	Leave       chan leaveCmd
	Broadcast   chan roomcast
	ReadReceipt chan readReceiptCmd

	// pubsub is optional; nil means single-node mode (no Redis).
	pubsub *redispubsub.Manager
	// remoteDeliver receives messages forwarded from Redis by a bridge goroutine.
	// It is a nil channel when pubsub is nil, so it never fires in select.
	remoteDeliver chan roomcast

	// msgRepo is optional; nil means messages are not persisted.
	msgRepo repository.MessageRepository
	// readRepo is optional; nil means read receipts are not persisted.
	readRepo repository.ReadRepository

	log *zap.Logger
}

// NewHub constructs a Hub ready to be started with Run().
// Pass a non-nil pubsub to enable Redis-backed horizontal scaling.
// Pass a non-nil msgRepo to enable message persistence.
// Pass a non-nil readRepo to enable read-receipt persistence.
func NewHub(log *zap.Logger, pubsub *redispubsub.Manager, msgRepo repository.MessageRepository, readRepo repository.ReadRepository) *Hub {
	h := &Hub{
		clients:     make(map[string]*Client),
		rooms:       make(map[string]map[string]*Client),
		Register:    make(chan *Client, 256),
		Unregister:  make(chan *Client, 256),
		Join:        make(chan joinCmd, 256),
		Leave:       make(chan leaveCmd, 256),
		Broadcast:   make(chan roomcast, 512),
		ReadReceipt: make(chan readReceiptCmd, 512),
		pubsub:      pubsub,
		msgRepo:     msgRepo,
		readRepo:    readRepo,
		log:         log,
	}
	if pubsub != nil {
		h.remoteDeliver = make(chan roomcast, 512)
	}
	return h
}

// Run starts the hub event loop. Must be called in its own goroutine.
// It exits cleanly when stop is closed.
func (h *Hub) Run(stop <-chan struct{}) {
	// Bridge goroutine: forwards Redis deliveries into h.remoteDeliver so the
	// Hub event loop remains a single-goroutine actor.
	if h.pubsub != nil {
		go func() {
			for d := range h.pubsub.Deliver() {
				select {
				case h.remoteDeliver <- roomcast{roomID: d.RoomID, msg: d.Payload}:
				case <-stop:
					return
				}
			}
		}()
	}

	h.log.Info("hub started", zap.Bool("redis", h.pubsub != nil))
	for {
		select {

		// ── Registration ────────────────────────────────────────────────────
		case client := <-h.Register:
			h.clients[client.ID] = client
			h.log.Info("client registered",
				zap.String("clientID", client.ID),
				zap.String("userID", client.UserID),
				zap.Int("total", len(h.clients)),
			)

		case client := <-h.Unregister:
			if _, ok := h.clients[client.ID]; ok {
				h.evict(client)
				h.log.Info("client unregistered",
					zap.String("clientID", client.ID),
					zap.String("userID", client.UserID),
					zap.Int("total", len(h.clients)),
				)
			}

		// ── Room management ──────────────────────────────────────────────────
		case cmd := <-h.Join:
			// Idempotent: joining an already-joined room is a no-op.
			if _, already := cmd.client.rooms[cmd.roomID]; already {
				h.safeSend(cmd.client, buildEnvelope(EventAck, cmd.roomID,
					AckPayload{OK: true, Message: "already in room"}))
				break
			}
			isNewRoom := h.rooms[cmd.roomID] == nil
			if isNewRoom {
				h.rooms[cmd.roomID] = make(map[string]*Client)
			}
			h.rooms[cmd.roomID][cmd.client.ID] = cmd.client
			cmd.client.rooms[cmd.roomID] = struct{}{}

			// Subscribe to Redis when this is the first local client in the room.
			if isNewRoom && h.pubsub != nil {
				h.pubsub.Subscribe(cmd.roomID)
			}

			// Notify existing members (excluding the joiner).
			h.fanout(cmd.roomID,
				buildEnvelope(EventNotify, cmd.roomID, NotifyPayload{
					RoomID: cmd.roomID,
					UserID: cmd.client.UserID,
					Text:   cmd.client.UserID + " joined",
				}),
				cmd.client,
			)
			// Acknowledge the joiner.
			h.safeSend(cmd.client, buildEnvelope(EventAck, cmd.roomID,
				AckPayload{OK: true, Message: "joined " + cmd.roomID}))

			h.log.Info("client joined room",
				zap.String("clientID", cmd.client.ID),
				zap.String("userID", cmd.client.UserID),
				zap.String("roomID", cmd.roomID),
				zap.Int("members", len(h.rooms[cmd.roomID])),
			)

		case cmd := <-h.Leave:
			if _, in := cmd.client.rooms[cmd.roomID]; !in {
				h.safeSend(cmd.client, buildEnvelope(EventError, cmd.roomID,
					ErrorPayload{Code: "not_in_room", Message: "you are not in this room"}))
				break
			}
			h.removeFromRoom(cmd.client, cmd.roomID)
			// Notify remaining members.
			h.fanout(cmd.roomID,
				buildEnvelope(EventNotify, cmd.roomID, NotifyPayload{
					RoomID: cmd.roomID,
					UserID: cmd.client.UserID,
					Text:   cmd.client.UserID + " left",
				}),
				nil,
			)
			h.safeSend(cmd.client, buildEnvelope(EventAck, cmd.roomID,
				AckPayload{OK: true, Message: "left " + cmd.roomID}))

			h.log.Info("client left room",
				zap.String("clientID", cmd.client.ID),
				zap.String("userID", cmd.client.UserID),
				zap.String("roomID", cmd.roomID),
			)

		// ── Message broadcast ────────────────────────────────────────────────
		case cast := <-h.Broadcast:
			if _, ok := h.rooms[cast.roomID]; !ok {
				if cast.sender != nil {
					h.safeSend(cast.sender, buildEnvelope(EventError, cast.roomID,
						ErrorPayload{Code: "room_not_found", Message: "room does not exist; join first"}))
				}
				break
			}
			if cast.sender != nil {
				if _, in := cast.sender.rooms[cast.roomID]; !in {
					h.safeSend(cast.sender, buildEnvelope(EventError, cast.roomID,
						ErrorPayload{Code: "not_in_room", Message: "join the room before sending messages"}))
					break
				}
			}
			if h.pubsub != nil {
				// Publish to Redis; every subscribed node (including this one)
				// will receive it via remoteDeliver and fanout locally.
				if err := h.pubsub.Publish(context.Background(), cast.roomID, cast.msg); err != nil {
					h.log.Error("redis publish failed, falling back to local fanout",
						zap.String("room", cast.roomID), zap.Error(err))
					h.fanout(cast.roomID, cast.msg, nil)
				}
			} else {
				// Single-node: deliver directly.
				h.fanout(cast.roomID, cast.msg, nil)
			}
			// Persist asynchronously — must not block the hub event loop.
			if h.msgRepo != nil && cast.message != nil {
				go h.persistMessage(cast.message)
			}

		// ── Read receipts ────────────────────────────────────────────────────
		case cmd := <-h.ReadReceipt:
			if _, in := cmd.client.rooms[cmd.roomID]; !in {
				h.safeSend(cmd.client, buildEnvelope(EventError, cmd.roomID,
					ErrorPayload{Code: "not_in_room", Message: "join the room before sending read receipts"}))
				break
			}
			readAt := time.Now().UTC()
			frame := buildEnvelope(EventReadReceipt, cmd.roomID, ReadReceiptPayload{
				MessageID: cmd.messageID,
				UserID:    cmd.client.UserID,
				ReadAt:    readAt,
			})
			if h.pubsub != nil {
				if err := h.pubsub.Publish(context.Background(), cmd.roomID, frame); err != nil {
					h.log.Error("redis publish read receipt failed, falling back to local fanout",
						zap.String("room", cmd.roomID), zap.Error(err))
					h.fanout(cmd.roomID, frame, nil)
				}
			} else {
				h.fanout(cmd.roomID, frame, nil)
			}
			if h.readRepo != nil {
				go h.persistRead(cmd.messageID, cmd.client.UserID, readAt)
			}

		// ── Remote delivery (from Redis) ─────────────────────────────────────
		case cast := <-h.remoteDeliver:
			// Message arrived from another node (or echoed back from Redis).
			// Fan out to all LOCAL members of the room.
			if _, ok := h.rooms[cast.roomID]; ok {
				h.fanout(cast.roomID, cast.msg, nil)
			}

		// ── Shutdown ─────────────────────────────────────────────────────────
		case <-stop:
			h.log.Info("hub stopping")
			for _, client := range h.clients {
				close(client.send)
			}
			return
		}
	}
}

// fanout sends msg to every member of roomID except exclude (may be nil).
// Slow clients whose send buffer is full are evicted.
func (h *Hub) fanout(roomID string, msg []byte, exclude *Client) {
	for _, c := range h.rooms[roomID] {
		if c == exclude {
			continue
		}
		select {
		case c.send <- msg:
		default:
			h.log.Warn("send buffer full, evicting client",
				zap.String("clientID", c.ID),
				zap.String("userID", c.UserID),
			)
			h.evict(c)
		}
	}
}

// safeSend enqueues msg on the client's send channel without blocking.
// Drops the frame (with a warning) if the buffer is full.
func (h *Hub) safeSend(client *Client, msg []byte) {
	select {
	case client.send <- msg:
	default:
		h.log.Warn("targeted send dropped, buffer full",
			zap.String("clientID", client.ID),
		)
	}
}

// evict closes the client's send channel and removes it from all rooms and the
// clients map. Must be called from Hub.Run() only.
func (h *Hub) evict(client *Client) {
	for roomID := range client.rooms {
		h.removeFromRoom(client, roomID)
	}
	delete(h.clients, client.ID)
	close(client.send)
}

// removeFromRoom removes client from one room. Deletes the room when it
// becomes empty and unsubscribes from Redis if enabled. Does NOT close client.send.
func (h *Hub) removeFromRoom(client *Client, roomID string) {
	if members, ok := h.rooms[roomID]; ok {
		delete(members, client.ID)
		if len(members) == 0 {
			delete(h.rooms, roomID)
			// No local clients remain — stop receiving Redis messages for this room.
			if h.pubsub != nil {
				h.pubsub.Unsubscribe(roomID)
			}
		}
	}
	delete(client.rooms, roomID)
}

// persistRead saves a read receipt to the repository.
// Runs in its own goroutine so it never blocks the hub event loop.
func (h *Hub) persistRead(messageID, userID string, readAt time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.readRepo.MarkRead(ctx, messageID, entity.UserID(userID), readAt); err != nil {
		h.log.Error("read receipt persist failed",
			zap.String("messageID", messageID),
			zap.String("userID", userID),
			zap.Error(err),
		)
	}
}

// persistMessage saves a chat message to the repository.
// Runs in its own goroutine so it never blocks the hub event loop.
func (h *Hub) persistMessage(msg *entity.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.msgRepo.Save(ctx, msg); err != nil {
		h.log.Error("message persist failed",
			zap.String("id", msg.ID),
			zap.String("roomID", string(msg.RoomID)),
			zap.Error(err),
		)
	}
}

// ClientCount returns the number of active connections (for metrics / tests).
// Must be called from Hub.Run()'s goroutine.
func (h *Hub) ClientCount() int { return len(h.clients) }

// RoomCount returns the number of active rooms (for metrics / tests).
// Must be called from Hub.Run()'s goroutine.
func (h *Hub) RoomCount() int { return len(h.rooms) }
