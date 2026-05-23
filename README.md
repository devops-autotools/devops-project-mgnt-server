# mgnt-server — Secure Server Management Dashboard

A lightweight, zero-dependency Go server monitoring and remote management tool. Monitor CPU, RAM, Disk, and Uptime of any Linux server from a single web dashboard — no SSH, no firewall holes, no external packages.

---

## Overview

**mgnt-server** works on a **reverse-polling** model:

```
[Target Server] ──outbound HTTPS POST──▶ [mgnt-server dashboard]
```

The target server runs a small bash **agent** that calls home every 5 seconds with telemetry data. The dashboard can queue shell commands back to the agent via long-poll. The agent communicates over a dedicated TLS-only port (8443) — no inbound SSH required on the target server.

---

## Features

- **Real-time telemetry** — CPU usage, RAM (used/free/total), Disk (used/free/total), uptime, OS
- **Web terminal** — Execute shell commands on any connected server directly from the browser with history navigation and Tab autocomplete
- **Dashboard overview** — Online/offline status, average CPU & RAM across all servers
- **Tag system** — Create color-coded tags, assign to servers, filter dashboard by tag
- **Secure session auth** — Cookie-based session, 24-hour expiry, in-memory cleanup
- **One-line agent install** — `curl -fsSL http://<server>/agent/install/<token> | bash`
- **Zero dependencies** — Pure Go standard library (`net/http`), no frameworks
- **Docker-ready** — Multi-stage Dockerfile, non-root container, docker-compose included
- **Auto-config** — Generates random password and TLS certs on first run
- **TLS agent channel** — Agent uses HTTPS with embedded CA cert, no cert pinning issues
- **Dark glassmorphism UI** — Vanilla JS SPA, no frontend framework

---

## Quick Start

### Option 1: Docker Compose (recommended)

```bash
docker-compose up -d
```

On first run, the server auto-generates `data/.env` with a random admin password and TLS certificates in `data/tls/`. The generated password is printed to the container log:

```bash
docker logs secure-server-manager | grep PASSWORD
```

Open: `http://localhost:8080`

### Option 2: Run Binary Directly

```bash
# Build
go build -o mgnt-server main.go

# Run (default port 8080)
./mgnt-server
```

Open: `http://localhost:8080`  
On first run, check stdout for the generated admin password.

---

## Configuration

Configuration is stored in `data/.env` (auto-created on first run, persisted via Docker volume):

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

1. Open the dashboard → click **Add New Server**
2. Enter a friendly name (e.g. `prod-db-01`)
3. Copy the generated install command:
   ```bash
   curl -fsSL http://<dashboard-ip>:8080/agent/install/<token> | bash
   ```
4. Run this command on the **target server** (no root required)
5. The dashboard shows the server online once the agent checks in

The agent installs itself to `~/.mgnt-agent/agent.sh` and runs as a background daemon via `nohup`.

---

## Tag System

- Create color-coded tags from **Servers → Manage Tags**
- Assign tags to any server from the Servers list (tag icon button)
- Filter the Servers list by clicking tag pills in the filter bar
- Tags persist across restarts in `data/tags.json`

---

## Agent Behavior

The agent runs two independent loops:

| Loop | Behavior |
|------|----------|
| **Telemetry** (background) | POST CPU/RAM/Disk to `/agent/report` every 5s |
| **Command** (foreground) | Long-poll `GET /agent/cmd/wait/<token>` — blocks 25s, retries on timeout |

CPU sampling uses `/proc/stat`. RAM uses `/proc/meminfo`. All pure bash + `awk`, no external tools needed.

### Commands handled specially

| Command | Behavior |
|---------|----------|
| `top` | Converted to `top -b -n 1` (batch mode) |
| `htop` | Converted to `TERM=dumb htop --no-color` |
| `vim`, `nano`, `vi` | Returns info message (interactive editors not supported) |
| `less`, `more` | Converted to `cat` |
| `man <cmd>` | `MANPAGER=cat man <cmd>` |
| `watch` | Returns info message |
| `clear`, `reset` | Handled client-side (clears terminal UI only) |

`cd` state is preserved between commands within a terminal session.

---

## TLS Agent Channel

- Agent endpoints run on **HTTPS port 8443 only** — HTTP port 8080 returns 404 for agent paths
- ECDSA P-256 CA + server cert auto-generated in `data/tls/` on first run
- Agent install script embeds the CA cert PEM → full TLS verification without needing the server IP in the cert
- Agent uses `curl --cacert ca.crt --resolve mgnt-server.local:8443:<ip>` for certificate validation

---

## API Reference

### Auth

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/auth/login` | Login `{"username":"...","password":"..."}` |
| `POST` | `/api/auth/logout` | Invalidate session |
| `GET` | `/api/auth/check` | Check session validity |

### Servers (requires auth cookie)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/servers` | List all servers with metrics |
| `POST` | `/api/servers/add` | Add server `{"name":"..."}` → returns token |
| `DELETE` | `/api/servers/{id}` | Remove server |
| `POST` | `/api/servers/execute/{id}` | Queue shell command `{"command":"..."}` |
| `GET` | `/api/servers/terminal/stream/{id}` | SSE stream for command results |

### Tags (requires auth cookie)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/tags` | List all tags |
| `POST` | `/api/tags` | Create tag `{"name":"...","color":"..."}` |
| `DELETE` | `/api/tags/{name}` | Delete tag (also removes from all servers) |
| `PUT` | `/api/servers/{id}/tags` | Set server tags `{"tags":["..."]}` |

### Agent (TLS port 8443 only)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/agent/install/{token}` | Download agent installer script |
| `POST` | `/agent/report` | Telemetry + token auth |
| `GET` | `/agent/cmd/wait/{token}` | Long-poll for queued command |
| `POST` | `/agent/report/result` | Submit command result |

---

## Deployment

### Build (Alpine container requires CGO_ENABLED=0)

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o mgnt-server main.go
```

### Deploy binary + static files to container

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

### Reinstall agent (only when agent script changes)

```bash
ssh ubuntu@<agent-server> "
  kill \$(cat ~/.mgnt-agent/agent.pid 2>/dev/null) 2>/dev/null
  curl -fsSL http://<dashboard>:8080/agent/install/<token> -o /tmp/agent.sh
  bash /tmp/agent.sh > /tmp/agent-install.log 2>&1 &
"
```

---

## File Structure

```
mgnt-server/
├── main.go              # All backend logic (Go stdlib only)
├── go.mod               # Go module (no external deps)
├── Dockerfile           # Multi-stage Docker build (non-root, Alpine)
├── docker-compose.yml   # One-command deployment
├── CLAUDE.md            # AI assistant context & dev guide
├── DOCS.md              # Extended technical documentation
├── data/                # Runtime data — NOT committed
│   ├── .env             # Auto-generated credentials & ports
│   ├── servers.json     # Persisted server registry
│   ├── tags.json        # Persisted tag definitions
│   └── tls/             # Auto-generated CA + server TLS certs
└── public/
    ├── index.html       # SPA shell
    ├── css/style.css    # Dark glassmorphism design system
    └── js/app.js        # Frontend logic (vanilla JS)
```

---

## Security Notes

- Sessions are **in-memory only** — restart clears all sessions
- Agent tokens are random 32-char hex strings (128-bit entropy)
- Cookie is `HttpOnly` — set `Secure: true` when behind HTTPS reverse proxy
- No CORS headers — serve from same origin
- Agent runs as the installing user, not root
- Container runs as non-root user (UID 1000)
- TLS private keys live only in `data/tls/` (volume-mounted, gitignored)

---

## Development Workflow

```bash
# Local dev
go run main.go

# Add a test server, copy the curl command, run it:
curl -fsSL http://localhost:8080/agent/install/<token> | bash

# Watch agent log
tail -f ~/.mgnt-agent/agent.log

# Stop agent
kill $(cat ~/.mgnt-agent/agent.pid)
```

---

## Environment

- **Language**: Go 1.22 (standard library only)
- **Frontend**: Vanilla JS + CSS (no build step)
- **Persistence**: JSON files (`data/servers.json`, `data/tags.json`)
- **Tested on**: Ubuntu 22.04, Ubuntu 24.04
