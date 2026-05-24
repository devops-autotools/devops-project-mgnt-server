# mgnt-server — Secure Server Management Dashboard

A lightweight Go server monitoring and remote management tool. Monitor CPU, RAM, Disk, Uptime, and containers (Docker + Kubernetes) from a single web dashboard — no SSH, no firewall holes required.

---

## Overview

**mgnt-server** works on a **reverse-polling** model:

```
[Target Server] ──outbound HTTPS──▶ [mgnt-server dashboard]
```

Each target server runs a compiled Go **agent** binary that reports telemetry every 5 seconds and maintains a long-poll connection for receiving commands. The agent also opens a WebSocket PTY bridge for live interactive shell sessions. All agent traffic runs on a dedicated TLS-only port (8443).

---

## Features

- **Real-time telemetry** — CPU, RAM (used/free/total), Disk, uptime, OS info
- **Container page** — Docker containers and Kubernetes pods across all servers in one view; filter by runtime, server, or namespace
- **K8s shell / logs** — `kubectl exec` into any running pod directly from the browser; stream pod logs with `kubectl logs -f`
- **PTY web terminal** — Full interactive shell sessions via xterm.js + WebSocket PTY bridge; resize support, history, tab autocomplete
- **Command terminal** — Queue shell commands on any server, results streamed via SSE
- **Tag system** — Color-coded tags, assign to servers, filter dashboard
- **Single-instance agent** — flock-based PID file prevents duplicate agent processes
- **Zero-inbound model** — Agent initiates all connections outbound; no firewall rules needed on target servers
- **Auto-config** — Random password and TLS certs generated on first run
- **Docker-ready** — Multi-stage Dockerfile, non-root Alpine container, docker-compose included
- **Dark glassmorphism UI** — Vanilla JS SPA, no frontend framework or build step

---

## Quick Start

### Option 1: Docker Compose (recommended)

```bash
docker-compose up -d
docker logs secure-server-manager | grep PASSWORD
```

Open `http://localhost:8080` and log in with the printed password.

### Option 2: Run Binary Directly

```bash
CGO_ENABLED=0 go build -o mgnt-server .
./mgnt-server
```

Open `http://localhost:8080`. Check stdout for the generated admin password on first run.

---

## Configuration

Stored in `data/.env` (auto-created, persisted via Docker volume):

```env
MGNT_PORT=8080               # browser HTTP port
MGNT_AGENT_TLS_PORT=8443     # agent HTTPS port (TLS-only)
MGNT_USERNAME=admin
MGNT_PASSWORD=<random>       # generated on first run
MGNT_SESSION_SECRET=<random> # generated on first run
```

**Never commit `data/` — it contains credentials and TLS private keys.**

---

## Adding a Server

### Option A — bash installer (any Linux server)

1. Open the dashboard → click **Add New Server**
2. Copy the generated install command and run it on the target server:
   ```bash
   curl -fsSL http://<dashboard>:8080/agent/install/<token> | bash
   ```
3. The server appears online within seconds.

### Option B — binary installer (Go agent, recommended)

```bash
curl -fsSL http://<dashboard>:8080/agent/install2/<token> | bash
```

Downloads a pre-built Go binary (`agent-linux-amd64` or `arm64`) and runs it directly. Provides:
- PTY shell sessions (full interactive terminal in the browser)
- Container telemetry (Docker + Kubernetes)
- Single-instance flock — prevents duplicate agent processes

The agent installs to `~/.mgnt-agent/agent` and persists as a background process. PID is tracked at `~/.mgnt-agent/agent.pid`.

---

## Container Page

The **Containers** page aggregates Docker containers and Kubernetes pods across all connected servers.

| Column | Notes |
|--------|-------|
| Status | `running` / `exited` / `paused` |
| Name | Container/pod name; namespace shown as sub-label for K8s |
| Server | Which server manages this container |
| Runtime | `K8s` or `Docker` badge |
| Image | Truncated with hover tooltip |
| CPU / RAM | Live metrics (requires metrics-server for K8s) |
| Ports | Exposed ports; truncated with hover tooltip |
| Uptime | Age since container/pod started |
| Actions | Stop / Start / Restart; expand row for Shell / Logs |

Clicking a row expands a detail panel with full info and **Shell (exec)** / **Logs** buttons.

### K8s requirements
- `kubectl` must be installed and configured on the server running the agent
- Metrics (`kubectl top`) requires **metrics-server** in the cluster

---

## PTY Shell Sessions

Clicking **Shell** on a server or container opens a floating xterm.js terminal window:

| Target | Command |
|--------|---------|
| Host server | `bash -i` on the agent host |
| Docker container | `docker exec -it <id> sh` |
| K8s pod | `kubectl exec -it <pod> -n <ns> -c <ctr> -- sh` |
| K8s logs | `kubectl logs -f --tail=200 -n <ns> <pod> -c <ctr>` |

Multiple shell windows can be open simultaneously. Each is an independent PTY bridge over WebSocket.

---

## Agent Behavior

The Go agent binary runs four concurrent routines:

| Routine | Behavior |
|---------|----------|
| **CPU sampler** | Reads `/proc/stat` every second, maintains rolling CPU% |
| **Telemetry loop** | POST to `/agent/report` every 5s (CPU, RAM, Disk, containers) |
| **Command loop** | Long-poll `GET /agent/cmd/wait/<token>?v=2` — blocks 25s, handles `exec` and `shell` commands |
| **Shell handler** | Per-session goroutine: opens PTY, runs command, bridges to WebSocket |

### Command remapping (exec terminal)

| Input | Remapped to |
|-------|-------------|
| `top` | `top -b -n 1` |
| `htop` | `TERM=dumb htop --no-color` |
| `vim`, `nano`, `vi` | Info message (use Shell PTY instead) |
| `less`, `more` | `cat` |
| `man <cmd>` | `MANPAGER=cat man <cmd>` |
| `watch` | Info message |
| `clear`, `reset` | Handled client-side |

`cd` state is preserved across commands within a terminal session.

### Single-instance protection

At startup the agent acquires an exclusive non-blocking `flock` on `~/.mgnt-agent/agent.pid`. A second instance exits immediately with a log message — prevents stale old binaries from stealing shell sessions.

---

## TLS Agent Channel

- All agent endpoints run on **HTTPS port 8443 only** — HTTP 8080 returns 404 for agent paths
- ECDSA P-256 CA + server cert auto-generated in `data/tls/` on first run
- Server cert SAN: `DNS:mgnt-server.local`
- Agent embeds the CA cert PEM and uses `--resolve` to map the virtual hostname to the real IP — full TLS verification without public DNS

---

## API Reference

### Auth

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/auth/login` | `{"username":"...","password":"..."}` |
| `POST` | `/api/auth/logout` | Invalidate session |
| `GET` | `/api/auth/check` | Check session validity |

### Servers (auth required)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/servers` | List all servers with live metrics |
| `POST` | `/api/servers/add` | Add server `{"name":"..."}` → token |
| `DELETE` | `/api/servers/{id}` | Remove server |
| `POST` | `/api/servers/execute/{id}` | Queue shell command `{"command":"..."}` |
| `GET` | `/api/servers/terminal/stream/{id}` | SSE stream for command results |
| `GET` | `/api/servers/shell/{id}/ws/{session_id}` | WebSocket PTY shell (browser side) |

### Containers (auth required)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/containers` | All containers across all servers |
| `POST` | `/api/servers/{id}/containers/{cid}/action` | `{"action":"start"\|"stop"\|"restart"}` |

### Tags (auth required)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/tags` | List tags |
| `POST` | `/api/tags` | Create `{"name":"...","color":"..."}` |
| `DELETE` | `/api/tags/{name}` | Delete (also removes from all servers) |
| `PUT` | `/api/servers/{id}/tags` | Set tags `{"tags":["..."]}` |

### Agent (TLS port 8443 only)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/agent/install/{token}` | bash installer script |
| `GET` | `/agent/install2/{token}` | Go binary installer script |
| `POST` | `/agent/report` | Telemetry payload |
| `GET` | `/agent/cmd/wait/{token}` | Long-poll command queue |
| `POST` | `/agent/report/result` | Command result submission |
| `GET` | `/agent/shell/{session_id}/ws` | WebSocket PTY bridge (agent side) |

---

## Deployment

### Build

```bash
# Server binary (Alpine container requires CGO_ENABLED=0)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o mgnt-server .

# Agent binaries (multi-arch)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o public/downloads/agent-linux-amd64 ./cmd/agent/
CGO_ENABLED=0 GOOS=linux GOARCH=arm64  go build -ldflags="-w -s" -o public/downloads/agent-linux-arm64  ./cmd/agent/
```

The Dockerfile handles all of this automatically — agent binaries are placed in `public/downloads/` and served by the dashboard.

### Deploy to container

```bash
rsync -az mgnt-server ubuntu@<host>:~/mgnt-server/
rsync -az public/ ubuntu@<host>:~/mgnt-server/public/
ssh ubuntu@<host> "
  sudo docker stop secure-server-manager
  sudo docker cp ~/mgnt-server/mgnt-server secure-server-manager:/app/mgnt-server
  sudo docker cp ~/mgnt-server/public secure-server-manager:/app/
  sudo docker start secure-server-manager
"
```

### Reinstall agent on a target server

```bash
ssh ubuntu@<agent-server> "
  kill \$(cat ~/.mgnt-agent/agent.pid 2>/dev/null) 2>/dev/null
  curl -fsSL http://<dashboard>:8080/agent/install2/<token> -o /tmp/agent-new
  chmod +x /tmp/agent-new && cp /tmp/agent-new ~/.mgnt-agent/agent
  nohup ~/.mgnt-agent/agent >> ~/.mgnt-agent/agent.log 2>&1 &
"
```

---

## File Structure

```
mgnt-server/
├── main.go                    # All backend logic
├── go.mod / go.sum            # Go module (gorilla/websocket)
├── Dockerfile                 # Multi-stage: builds server + agent binaries
├── docker-compose.yml
├── CLAUDE.md                  # AI assistant context & dev guide
├── cmd/
│   └── agent/
│       └── main.go            # Go agent binary (PTY, telemetry, K8s)
├── public/
│   ├── index.html             # SPA shell
│   ├── css/style.css          # Dark glassmorphism design
│   ├── js/app.js              # Frontend SPA logic (vanilla JS)
│   └── downloads/
│       ├── agent-linux-amd64  # Pre-built agent (gitignored)
│       └── agent-linux-arm64
└── data/                      # Runtime data — NOT committed
    ├── .env                   # Credentials & ports
    ├── servers.json           # Server registry
    ├── tags.json              # Tag definitions
    └── tls/                   # CA + TLS certs
```

---

## Security Notes

- Sessions are **in-memory only** — server restart clears all sessions
- Agent tokens are random 32-char hex strings (128-bit entropy)
- Cookie is `HttpOnly`; set `Secure: true` when behind an HTTPS reverse proxy
- No CORS headers — serve from same origin only
- Agent runs as the installing user, never root
- Container runs as non-root (UID 1000)
- TLS private keys live only in `data/tls/` (volume-mounted, gitignored)

---

## Development Workflow

```bash
# Local dev server
go run main.go

# Install test agent (bash)
curl -fsSL http://localhost:8080/agent/install/<token> | bash

# Install test agent (Go binary)
curl -fsSL http://localhost:8080/agent/install2/<token> | bash

# Watch agent log
tail -f ~/.mgnt-agent/agent.log

# Stop agent
kill $(cat ~/.mgnt-agent/agent.pid)
```

---

## Environment

- **Language**: Go 1.22
- **External dependency**: `github.com/gorilla/websocket v1.5.3` (PTY shell WebSocket)
- **Frontend**: Vanilla JS + CSS (no build step)
- **Persistence**: JSON files (`data/servers.json`, `data/tags.json`)
- **Tested on**: Ubuntu 22.04, Ubuntu 24.04
