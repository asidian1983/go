package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const channelPrefix = "chat:room:"

// redisMsg is the envelope serialised into every Redis Pub/Sub message.
type redisMsg struct {
	Payload []byte `json:"payload"`
}

// Delivery carries a decoded Redis message to the Hub.
type Delivery struct {
	RoomID  string
	Payload []byte
}

// Manager handles Redis Pub/Sub with dynamic room subscriptions and
// automatic reconnect on connection loss.
//
// Concurrency:
//   - Subscribe / Unsubscribe are called from Hub.Run() (single goroutine).
//   - Publish is called from Hub.Run() too, so no additional locking is needed
//     for the caller, but mu guards the internal ps pointer accessed from
//     both Hub.Run() and the reconnect goroutine.
type Manager struct {
	client *goredis.Client
	log    *zap.Logger

	mu     sync.Mutex
	active map[string]struct{} // rooms that currently have local clients
	ps     *goredis.PubSub     // live connection; nil while reconnecting

	deliver chan Delivery // Hub reads from this channel
}

// New returns a Manager wired to the Redis instance at addr.
func New(addr string, log *zap.Logger) *Manager {
	rdb := goredis.NewClient(&goredis.Options{
		Addr:         addr,
		DialTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		// ReadTimeout 0 = no deadline (required for blocking SUBSCRIBE).
	})
	return &Manager{
		client:  rdb,
		log:     log,
		active:  make(map[string]struct{}),
		deliver: make(chan Delivery, 512),
	}
}

// Deliver returns the channel that the Hub should range over for remote messages.
func (m *Manager) Deliver() <-chan Delivery { return m.deliver }

// Publish serialises msg and pushes it to the Redis channel for roomID.
// All servers subscribed to that room—including this one—will receive it.
func (m *Manager) Publish(ctx context.Context, roomID string, msg []byte) error {
	data, err := json.Marshal(redisMsg{Payload: msg})
	if err != nil {
		return err
	}
	return m.client.Publish(ctx, channelPrefix+roomID, data).Err()
}

// Subscribe marks roomID as active and subscribes the live Redis connection
// (if one exists). Called from Hub.Run() when the first local client joins a room.
func (m *Manager) Subscribe(roomID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.active[roomID]; ok {
		return
	}
	m.active[roomID] = struct{}{}
	if m.ps != nil {
		if err := m.ps.Subscribe(context.Background(), channelPrefix+roomID); err != nil {
			m.log.Warn("redis: subscribe failed",
				zap.String("room", roomID), zap.Error(err))
		}
	}
	// If ps is nil we are mid-reconnect; runOnce will re-subscribe active rooms.
}

// Unsubscribe removes roomID from active subscriptions.
// Called from Hub.Run() when the last local client leaves a room.
func (m *Manager) Unsubscribe(roomID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.active, roomID)
	if m.ps != nil {
		if err := m.ps.Unsubscribe(context.Background(), channelPrefix+roomID); err != nil {
			m.log.Warn("redis: unsubscribe failed",
				zap.String("room", roomID), zap.Error(err))
		}
	}
}

// Run starts the receive loop with exponential-backoff reconnect.
// Blocks until stop is closed.
func (m *Manager) Run(stop <-chan struct{}) {
	backoff := time.Second
	for {
		if err := m.runOnce(stop); err == nil {
			return // clean shutdown
		}
		select {
		case <-stop:
			return
		case <-time.After(backoff):
			m.log.Warn("redis: reconnecting", zap.Duration("backoff", backoff))
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}
}

// runOnce opens a single PubSub connection, subscribes to all active rooms,
// and pumps messages until the connection drops or stop fires.
// Returns nil only on clean stop.
func (m *Manager) runOnce(stop <-chan struct{}) error {
	ctx := context.Background()

	m.mu.Lock()
	channels := make([]string, 0, len(m.active))
	for r := range m.active {
		channels = append(channels, channelPrefix+r)
	}
	ps := m.client.Subscribe(ctx, channels...)
	m.ps = ps
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.ps = nil
		m.mu.Unlock()
		_ = ps.Close()
	}()

	msgCh := ps.Channel()
	m.log.Info("redis pubsub connected", zap.Int("rooms", len(channels)))

	for {
		select {
		case <-stop:
			return nil

		case raw, ok := <-msgCh:
			if !ok {
				return fmt.Errorf("redis channel closed")
			}
			var rm redisMsg
			if err := json.Unmarshal([]byte(raw.Payload), &rm); err != nil {
				m.log.Warn("redis: malformed message", zap.Error(err))
				continue
			}
			roomID := raw.Channel[len(channelPrefix):]
			select {
			case m.deliver <- Delivery{RoomID: roomID, Payload: rm.Payload}:
			default:
				m.log.Warn("redis: deliver buffer full, dropping message",
					zap.String("room", roomID))
			}
		}
	}
}
