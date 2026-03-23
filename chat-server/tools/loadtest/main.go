// tools/loadtest/main.go — WebSocket load tester for the chat server.
//
// Simulates N concurrent authenticated clients that each join a room,
// send messages at a configurable interval, and track round-trip latency.
//
// Usage:
//
//	go run ./tools/loadtest \
//	  -addr localhost:8080 \
//	  -clients 10000 \
//	  -rooms 100 \
//	  -duration 30s \
//	  -ramp 10s
//
// System prerequisites (macOS / Linux):
//
//	ulimit -n 65536          # file descriptors
//	sysctl -w net.ipv4.ip_local_port_range="1024 65535"   # Linux only
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// ── Flags ────────────────────────────────────────────────────────────────────

var (
	flagAddr      = flag.String("addr", "localhost:8080", "chat server address (host:port)")
	flagClients   = flag.Int("clients", 10_000, "number of concurrent WebSocket clients")
	flagRooms     = flag.Int("rooms", 100, "number of rooms to spread clients across")
	flagDuration  = flag.Duration("duration", 30*time.Second, "total test duration")
	flagRamp      = flag.Duration("ramp", 10*time.Second, "ramp-up time (spread connections over this window)")
	flagMsgPeriod = flag.Duration("msg-period", 5*time.Second, "how often each client sends a message")
	flagVerbose   = flag.Bool("v", false, "verbose: print per-client errors")
)

// ── WebSocket protocol types ─────────────────────────────────────────────────

type envelope struct {
	Event   string          `json:"event"`
	RoomID  string          `json:"room_id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type chatMessage struct {
	ID        string    `json:"id"`
	SenderID  string    `json:"sender_id"`
	RoomID    string    `json:"room_id"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// ── Global metrics ────────────────────────────────────────────────────────────

type metrics struct {
	connected    atomic.Int64 // currently connected clients
	connTotal    atomic.Int64 // total connections ever established
	connFailed   atomic.Int64 // connection attempts that failed
	msgSent      atomic.Int64 // total messages sent
	msgRecv      atomic.Int64 // total messages received (any event)
	echoes       atomic.Int64 // own message echoes received (used for latency)
	errors       atomic.Int64 // protocol / send errors

	latMu    sync.Mutex
	latSamples []time.Duration // round-trip latency samples
}

func (m *metrics) recordLatency(d time.Duration) {
	m.latMu.Lock()
	m.latSamples = append(m.latSamples, d)
	m.latMu.Unlock()
}

// snapshot returns a copy of latency samples for percentile calculation.
func (m *metrics) snapshot() []time.Duration {
	m.latMu.Lock()
	cp := make([]time.Duration, len(m.latSamples))
	copy(cp, m.latSamples)
	m.latMu.Unlock()
	return cp
}

var global = &metrics{}

// ── Auth ─────────────────────────────────────────────────────────────────────

var (
	tokenMu sync.RWMutex
	tokens  []string // pre-fetched JWT tokens, round-robined across clients
)

func fetchToken(addr, username, password string) (string, error) {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := http.Post("http://"+addr+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var result map[string]string
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("parse login response: %w", err)
	}
	tok, ok := result["token"]
	if !ok {
		return "", fmt.Errorf("no token in response: %s", raw)
	}
	return tok, nil
}

func pickToken(i int) string {
	tokenMu.RLock()
	defer tokenMu.RUnlock()
	return tokens[i%len(tokens)]
}

// ── Client worker ─────────────────────────────────────────────────────────────

func runClient(id int, roomID string, stop <-chan struct{}) {
	token := pickToken(id)
	wsURL := "ws://" + *flagAddr + "/ws?token=" + token

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		global.connFailed.Add(1)
		if *flagVerbose {
			log.Printf("[client %d] dial: %v", id, err)
		}
		return
	}
	defer conn.Close()
	global.connected.Add(1)
	global.connTotal.Add(1)
	defer global.connected.Add(-1)

	// Join room.
	if err := sendEnvelope(conn, "join", roomID, map[string]string{"room_id": roomID}); err != nil {
		global.errors.Add(1)
		return
	}

	// pending tracks sent messages awaiting echo: msgID → send time.
	pending := make(map[string]time.Time)
	var pendingMu sync.Mutex

	// Read pump in a goroutine.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			global.msgRecv.Add(1)

			var env envelope
			if err := json.Unmarshal(raw, &env); err != nil {
				continue
			}
			if env.Event != "message" {
				continue
			}
			var msg chatMessage
			if err := json.Unmarshal(env.Payload, &msg); err != nil {
				continue
			}
			// Only count echoes of our own messages for latency.
			pendingMu.Lock()
			sent, ok := pending[msg.ID]
			if ok {
				delete(pending, msg.ID)
			}
			pendingMu.Unlock()
			if ok {
				global.echoes.Add(1)
				global.recordLatency(time.Since(sent))
			}
		}
	}()

	ticker := time.NewTicker(*flagMsgPeriod)
	defer ticker.Stop()

	// Add jitter so all clients don't fire at the same instant.
	time.Sleep(time.Duration(rand.Int63n(int64(*flagMsgPeriod))))

	for {
		select {
		case <-stop:
			return
		case <-readDone:
			return
		case <-ticker.C:
			msgID := randomHex(8)
			now := time.Now()
			pendingMu.Lock()
			pending[msgID] = now
			pendingMu.Unlock()

			payload := map[string]string{"message": fmt.Sprintf("load-test-%s", msgID)}
			if err := sendEnvelope(conn, "message", roomID, payload); err != nil {
				global.errors.Add(1)
				pendingMu.Lock()
				delete(pending, msgID)
				pendingMu.Unlock()
				return
			}
			global.msgSent.Add(1)
		}
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func sendEnvelope(conn *websocket.Conn, event, roomID string, payload any) error {
	p, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	env := envelope{Event: event, RoomID: roomID, Payload: json.RawMessage(p)}
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, b)
}

func randomHex(n int) string {
	const chars = "0123456789abcdef"
	b := make([]byte, n*2)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

// ── Percentile calculation ────────────────────────────────────────────────────

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p / 100.0)
	return sorted[idx]
}

// ── Stats printer ─────────────────────────────────────────────────────────────

func printStats(label string, elapsed time.Duration) {
	connected := global.connected.Load()
	connTotal := global.connTotal.Load()
	connFailed := global.connFailed.Load()
	sent := global.msgSent.Load()
	recv := global.msgRecv.Load()
	echoes := global.echoes.Load()
	errs := global.errors.Load()

	samples := global.snapshot()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

	throughput := float64(echoes) / elapsed.Seconds()

	fmt.Printf("\n── %s (elapsed: %s) ──────────────────────────\n", label, elapsed.Round(time.Second))
	fmt.Printf("  Connections  : active=%-6d  total=%-6d  failed=%-6d\n", connected, connTotal, connFailed)
	fmt.Printf("  Messages     : sent=%-8d  recv=%-8d  echoes=%-8d\n", sent, recv, echoes)
	fmt.Printf("  Throughput   : %.1f echo/s\n", throughput)
	fmt.Printf("  Errors       : %d\n", errs)
	if len(samples) > 0 {
		fmt.Printf("  RTT latency  : p50=%-8s  p95=%-8s  p99=%-8s  max=%s\n",
			percentile(samples, 50).Round(time.Millisecond),
			percentile(samples, 95).Round(time.Millisecond),
			percentile(samples, 99).Round(time.Millisecond),
			samples[len(samples)-1].Round(time.Millisecond),
		)
	} else {
		fmt.Printf("  RTT latency  : (no samples yet)\n")
	}
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	flag.Parse()

	// Pre-flight check.
	fmt.Printf("Chat-server load tester\n")
	fmt.Printf("  target    : %s\n", *flagAddr)
	fmt.Printf("  clients   : %d\n", *flagClients)
	fmt.Printf("  rooms     : %d\n", *flagRooms)
	fmt.Printf("  duration  : %s\n", *flagDuration)
	fmt.Printf("  ramp      : %s\n", *flagRamp)
	fmt.Printf("  msg/period: %s per client\n", *flagMsgPeriod)
	fmt.Println()

	// Authenticate — fetch tokens for the demo users.
	users := []struct{ user, pass string }{
		{"alice", "password"},
		{"bob", "password"},
	}
	fmt.Println("Authenticating demo users...")
	for _, u := range users {
		tok, err := fetchToken(*flagAddr, u.user, u.pass)
		if err != nil {
			log.Fatalf("login %s: %v", u.user, err)
		}
		tokens = append(tokens, tok)
		fmt.Printf("  ✓ %s\n", u.user)
	}
	fmt.Println()

	stop := make(chan struct{})
	var stopOnce sync.Once
	closeStop := func() { stopOnce.Do(func() { close(stop) }) }

	// Signal handler.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println("\nInterrupted — shutting down...")
		closeStop()
	}()

	// Spawn clients gradually over the ramp window.
	rampDelay := time.Duration(0)
	if *flagClients > 1 {
		rampDelay = *flagRamp / time.Duration(*flagClients)
	}

	start := time.Now()
	fmt.Printf("Ramping up %d clients over %s...\n", *flagClients, *flagRamp)

	var wg sync.WaitGroup
	for i := 0; i < *flagClients; i++ {
		select {
		case <-stop:
			goto done
		default:
		}

		roomID := fmt.Sprintf("loadtest-room-%d", i%*flagRooms)
		wg.Add(1)
		go func(id int, room string) {
			defer wg.Done()
			runClient(id, room, stop)
		}(i, roomID)

		if rampDelay > 0 {
			time.Sleep(rampDelay)
		}
	}

	fmt.Printf("All %d clients spawned. Running for %s...\n\n", *flagClients, *flagDuration)

	// Periodic stats during the test.
	statsTicker := time.NewTicker(5 * time.Second)
	defer statsTicker.Stop()

	deadline := time.After(*flagDuration)
loop:
	for {
		select {
		case <-stop:
			break loop
		case <-deadline:
			closeStop()
			break loop
		case <-statsTicker.C:
			printStats("LIVE", time.Since(start))
		}
	}

done:
	wg.Wait()

	// Final report.
	elapsed := time.Since(start)
	fmt.Println("\n════════════════════════════════════════════════")
	fmt.Println("  FINAL REPORT")
	fmt.Println("════════════════════════════════════════════════")
	printStats("FINAL", elapsed)

	samples := global.snapshot()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	if len(samples) > 0 {
		fmt.Printf("\n  Latency distribution (%d samples):\n", len(samples))
		for _, p := range []float64{50, 75, 90, 95, 99, 99.9} {
			fmt.Printf("    p%-5.1f  %s\n", p, percentile(samples, p).Round(time.Millisecond))
		}
	}

	fmt.Printf("\n  Total connected : %d / %d (%.1f%% success rate)\n",
		global.connTotal.Load(),
		*flagClients,
		100.0*float64(global.connTotal.Load())/float64(*flagClients),
	)
	fmt.Println()
}
