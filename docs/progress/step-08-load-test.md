# Step 08 — WebSocket Load Tester (10K Concurrent Connections)

## Overview

A self-contained Go load-testing tool that simulates N authenticated WebSocket clients
concurrently. Each client logs in, joins a room, and sends messages at a configurable
interval. The tool measures round-trip latency and throughput, printing live stats every
5 seconds and a full percentile breakdown at the end.

---

## New Files

| Path | Description |
|---|---|
| `tools/loadtest/main.go` | Standalone load tester — no new module dependencies |

---

## Architecture

```
main()
  │
  ├── fetchToken(alice), fetchToken(bob)   — pre-auth, tokens round-robined
  │
  ├── ramp loop: spawn N goroutines over ramp window
  │      └── runClient(id, roomID, stop)
  │               ├── Dial WebSocket (/ws?token=...)
  │               ├── join room
  │               ├── ReadPump goroutine  — records echo latency
  │               └── WritePump loop      — sends message every msg-period
  │
  ├── statsTicker: print live stats every 5s
  └── deadline / SIGINT → closeStop() → all clients exit → wg.Wait()
                                                │
                                           FINAL REPORT
```

**Concurrency model**: one goroutine per client (read pump + write in same goroutine).
At 10K clients this produces ~20K goroutines (read pump + select loop), well within
Go's scheduler capacity.

---

## Usage

```bash
# System prerequisites (raise file descriptor limit)
ulimit -n 65536

# Linux only — widen ephemeral port range
sudo sysctl -w net.ipv4.ip_local_port_range="1024 65535"

# Run with defaults (10K clients, 100 rooms, 30s, 10s ramp)
cd chat-server
go run ./tools/loadtest

# Custom parameters
go run ./tools/loadtest \
  -addr      localhost:8080 \
  -clients   10000 \
  -rooms     100 \
  -duration  60s \
  -ramp      15s \
  -msg-period 3s \
  -v
```

### Flags

| Flag | Default | Description |
|---|---|---|
| `-addr` | `localhost:8080` | Chat server host:port |
| `-clients` | `10000` | Number of concurrent WebSocket clients |
| `-rooms` | `100` | Rooms to distribute clients across |
| `-duration` | `30s` | Total test duration |
| `-ramp` | `10s` | Ramp-up window (spreads dial load) |
| `-msg-period` | `5s` | How often each client sends a message |
| `-v` | `false` | Verbose: print per-client dial errors |

---

## Metrics

### Live output (every 5 seconds)

```
── LIVE (elapsed: 15s) ──────────────────────────
  Connections  : active=8432    total=8432    failed=12
  Messages     : sent=16100     recv=81200    echoes=15980
  Throughput   : 1065.3 echo/s
  Errors       : 3
  RTT latency  : p50=4ms      p95=18ms     p99=47ms     max=210ms
```

### Final report

```
════════════════════════════════════════════════
  FINAL REPORT
════════════════════════════════════════════════

── FINAL (elapsed: 30s) ──────────────────────────
  Connections  : active=0       total=9988    failed=12
  Messages     : sent=39700     recv=198500   echoes=39700
  Throughput   : 1323.3 echo/s
  Errors       : 3

  Latency distribution (39700 samples):
    p50.0   4ms
    p75.0   9ms
    p90.0   21ms
    p95.0   38ms
    p99.0   87ms
    p99.9   310ms

  Total connected : 9988 / 10000 (99.9% success rate)
```

---

## Latency measurement

Round-trip time is measured from the instant `WriteMessage` returns (message sent)
to the instant the echo `EventMessage` is received back from the server. Each client
tracks an in-flight map `msgID → sendTime` (mutex-protected). Only the **sender's own
echo** is used for latency — messages from other clients in the room are counted under
`msgRecv` but not `echoes`.

---

## Known limitations & tuning

| Concern | Notes |
|---|---|
| Only 2 demo users | Tokens round-robined across clients — all connections share alice/bob identity |
| `latSamples` growth | At 10K clients × 6 msg/min × 30s = ~30K samples; negligible memory |
| `pending` map per client | Bounded by in-flight messages (1 per msg-period); no leak |
| `close(stop)` safety | Protected by `sync.Once` — safe if signal and deadline fire simultaneously |
| macOS fd limit | Default is 256; must run `ulimit -n 65536` before the test |

---

## System requirements for 10K connections (single machine)

| Resource | Requirement |
|---|---|
| File descriptors | `ulimit -n 65536` |
| Goroutines | ~20K (Go default stack 8KB = ~160 MB) |
| OS TCP buffers | Default settings handle this on modern kernels |
| Server GOMAXPROCS | Set to number of CPU cores (default) |

---

## Expected baselines (localhost, single-node)

| Clients | p50 RTT | p99 RTT | Throughput |
|---|---|---|---|
| 100 | < 1ms | < 5ms | linear |
| 1,000 | 1–3ms | 10–30ms | linear |
| 10,000 | 3–10ms | 50–200ms | hub channel pressure visible |

At 10K clients the bottleneck is typically the Hub's `Broadcast` channel (512 buffer)
and the OS TCP stack, not Go's scheduler.
