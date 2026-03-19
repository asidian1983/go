package ws

import "go.uber.org/zap"

// Hub is the central connection manager.
//
// Concurrency model:
//   - Hub.Run() occupies exactly ONE goroutine.
//   - The clients map is owned exclusively by that goroutine →
//     no mutex is ever needed to read or write the map.
//   - All external interaction (register, unregister, broadcast)
//     happens through buffered channels, making the API goroutine-safe
//     by construction: callers never touch shared state directly.
//
// Message flow:
//
//	Client.readPump ──► hub.Broadcast ──► Hub.Run ──► client.send (per client)
//	                                                       │
//	                                               Client.writePump ──► WebSocket
type Hub struct {
	// clients holds all currently connected clients.
	// Owned solely by Run(); never accessed from outside.
	clients map[*Client]struct{}

	// Register enqueues a new client for addition.
	Register chan *Client

	// Unregister enqueues a client for removal and channel close.
	Unregister chan *Client

	// Broadcast enqueues a message to be sent to all clients.
	Broadcast chan []byte

	log *zap.Logger
}

func NewHub(log *zap.Logger) *Hub {
	return &Hub{
		clients:    make(map[*Client]struct{}),
		Register:   make(chan *Client, 256),
		Unregister: make(chan *Client, 256),
		Broadcast:  make(chan []byte, 512),
		log:        log,
	}
}

// Run starts the hub event loop. Call this in its own goroutine.
// It exits cleanly when stop is closed.
func (h *Hub) Run(stop <-chan struct{}) {
	h.log.Info("hub started")
	for {
		select {
		case client := <-h.Register:
			h.clients[client] = struct{}{}
			h.log.Info("client registered",
				zap.String("id", client.ID),
				zap.Int("total", len(h.clients)),
			)

		case client := <-h.Unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send) // signals writePump to exit
				h.log.Info("client unregistered",
					zap.String("id", client.ID),
					zap.Int("total", len(h.clients)),
				)
			}

		case message := <-h.Broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					// send buffer full → client is too slow, drop and remove
					delete(h.clients, client)
					close(client.send)
					h.log.Warn("client send buffer full, dropped", zap.String("id", client.ID))
				}
			}

		case <-stop:
			h.log.Info("hub stopping")
			// Drain remaining clients
			for client := range h.clients {
				close(client.send)
				delete(h.clients, client)
			}
			return
		}
	}
}

// ConnectedCount returns the number of connected clients.
// Safe to call only from within Run()'s goroutine (e.g. in tests via a wrapper).
func (h *Hub) ConnectedCount() int {
	return len(h.clients)
}
