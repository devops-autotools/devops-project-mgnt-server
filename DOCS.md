# mgnt-server — Technical Documentation

**Version**: 1.0  
**Language**: Go 1.22 (stdlib only)  
**Date**: 2026-05-23

---

## Table of Contents

1. [System Design](#1-system-design)
2. [Backend Architecture](#2-backend-architecture)
3. [Frontend Architecture](#3-frontend-architecture)
4. [Agent Protocol Specification](#4-agent-protocol-specification)
5. [Authentication & Session Management](#5-authentication--session-management)
6. [Data Persistence](#6-data-persistence)
7. [Web Terminal System](#7-web-terminal-system)
8. [API Reference](#8-api-reference)
9. [Deployment Guide](#9-deployment-guide)
10. [Known Limitations & Bugs](#10-known-limitations--bugs)
11. [Infrastructure Map](#11-infrastructure-map)

---

## 1. System Design

### 1.1 Core Concept

`mgnt-server` is a **pull-based** server monitoring and management system. Unlike traditional monitoring tools that require inbound SSH or open management ports, this system uses **outbound HTTP polling** from the agent to the dashboard.

```
┌─────────────────────────────┐        ┌──────────────────────────┐
│      Target Server          │        │   mgnt-server Dashboard  │
│                             │        │                          │
│  ~/.mgnt-agent/agent.sh     │        │  main.go (Go HTTP server)│
│  (bash daemon, nohup)       │        │  Port: 8080              │
│                             │        │                          │
│  Every 5s → POST /agent/report ─────▶│  Updates server metrics  │
│                             │        │  Returns queued command   │
│  ◀───── {"command": "ls"} ──┤        │                          │
│  Execute command             │        │                          │
│  POST /agent/report/result ──────────▶│  Stores output           │
│                             │        │                          │
└─────────────────────────────┘        └──────────────────────────┘
                                               ▲
                                               │ HTTP (browser)
                                        ┌──────┴────────┐
                                        │  Admin Browser │
                                        │  SPA Dashboard │
                                        └───────────────┘
```

### 1.2 Design Decisions

| Decision | Choice | Reason |
|----------|--------|--------|
| Backend language | Go stdlib | Zero dependency, single binary, easy cross-compile |
| Database | JSON file | No infrastructure overhead, sufficient for < 100 servers |
| Auth | In-memory session + cookie | Simple, secure, no JWT complexity |
| Agent | Bash script | Runs on any Linux with only coreutils + curl |
| Frontend | Vanilla JS | No build pipeline, served as static files |
| Terminal | Long-polling (not WebSocket) | Simpler architecture, works through proxies |

---

## 2. Backend Architecture

### 2.1 Entry Point — `main()`

```
main()
  ├── loadConfig()         → reads/creates config.json
  ├── loadServers()        → deserializes data/servers.json into servers map
  ├── ensureDataDir()      → creates data/, public/css/, public/js/ if missing
  ├── http.NewServeMux()   → registers all routes
  ├── go sessionCleanupLoop() → background goroutine, purges expired sessions every 10m
  └── http.ListenAndServe()
```

### 2.2 Global State

```go
var (
    config      Config                  // loaded once at startup
    servers     = make(map[string]*Server) // keyed by Server.ID
    sessions    = make(map[string]time.Time) // token → expiry
    serverMutex sync.RWMutex           // guards 'servers' map
    sessionMtx  sync.Mutex             // guards 'sessions' map
    dataFile    = "data/servers.json"
)
```

### 2.3 Concurrency Model

- `serverMutex` is a `sync.RWMutex`. Most read operations use `RLock()`. Any write (update metrics, add/delete server, enqueue command) uses `Lock()`.
- `sessionMtx` is a plain `sync.Mutex` (sessions are small, no read-heavy pattern).
- `saveServersNoLock()` must only be called while `serverMutex.Lock()` is already held.

### 2.4 Route Table

```
GET  /                              → static file server (public/)
GET  /agent/install/{token}         → handleAgentInstallScript
POST /agent/report                  → handleAgentReport
POST /agent/report/result           → handleAgentCommandResult

POST /api/auth/login                → handleLogin
POST /api/auth/logout               → handleLogout
GET  /api/auth/check                → handleAuthCheck

GET  /api/servers                   → authMiddleware → handleGetServers
POST /api/servers/add               → authMiddleware → handleAddServer
DELETE /api/servers/{id}            → authMiddleware → handleDeleteServer
POST /api/servers/execute/{id}      → authMiddleware → handleQueueCommand
GET  /api/servers/terminal/{id}     → authMiddleware → handleGetTerminalLogs
```

Uses Go 1.22 pattern routing (`{id}`, method prefix like `GET /path`).

### 2.5 Key Handler Logic

#### `handleAgentReport`
1. Decode `AgentReport` JSON body
2. Find server by `report.Token` (linear scan of `servers` map)
3. Update all telemetry fields on `*Server`
4. Check if `ActiveConsole` has gone stale (no browser poll in > 12s → set false)
5. Dequeue one `PendingCmd` if present → move to `ExecutedCmds`, set status `"running"`
6. Return `{"interval": 1|5, "command": "...", "command_id": "..."}` or just `{"interval": 5}`

#### `handleQueueCommand`
1. Validate server ID and command text
2. Set `server.ActiveConsole = true`, `server.LastTerminalPoll = now`
3. Create `ConsoleCommand{ID, Command, Status: "pending"}`
4. Append to `server.PendingCmds`
5. Return `{"status":"queued","command_id":"..."}`

#### `handleAgentInstallScript`
- Dynamically generates a self-contained bash script using `fmt.Sprintf`
- Script contains: installer wrapper + embedded agent loop with hardcoded `TOKEN` and `SERVER_URL`
- Content-Type: `text/x-shellscript`

---

## 3. Frontend Architecture

### 3.1 SPA Structure

Single HTML page (`public/index.html`) with three logical views toggled by CSS `hidden` class:
- `#page-dashboard` — cards grid with live metrics
- `#page-servers` — table list view
- `#page-server-detail` — detail view with SVG gauge rings

Active view controlled by `state.currentView` + `switchView(viewId)`.

### 3.2 Application State (`state` object)

```js
const state = {
    authenticated: false,
    servers: [],               // last fetched from /api/servers
    currentView: 'page-dashboard',
    currentDetailServerId: null,
    activeTerminalServerId: null,
    pollingInterval: null,     // setInterval ID for fetchTelemetry (5s)
    connectionCheckInterval: null, // setInterval for wizard connection wait
    newServerToken: null,
    newServerId: null,
    terminalInterval: null     // setInterval for terminal polling
};
```

### 3.3 Polling Loop

```
DOMContentLoaded
  └── checkAuth() → GET /api/servers
        ├── 401 → show login form
        └── 200 → loginSuccess()
                    ├── fetchTelemetry() → immediate render
                    └── setInterval(fetchTelemetry, 5000) → continuous updates
```

### 3.4 Online Status Calculation

Online status is computed **client-side** on every render cycle:

```js
const isOnline = s.connected && (now - new Date(s.last_report)) < 15000;
```

A server with `connected: true` but `last_report` older than 15 seconds is shown as offline.

### 3.5 SVG Gauge Rings

Three circular progress rings (CPU, RAM, Disk) use SVG stroke-dashoffset animation:

```js
const RING_CIRCUMFERENCE = 534; // 2π × r (r = 85)

function updateProgressRing(ringElement, percent) {
    const offset = RING_CIRCUMFERENCE - (percent / 100) * RING_CIRCUMFERENCE;
    ringElement.style.strokeDashoffset = offset;
}
```

---

## 4. Agent Protocol Specification

### 4.1 Agent Lifecycle

```
curl | bash → installer script
  ├── mkdir ~/.mgnt-agent/
  ├── write ~/.mgnt-agent/agent.sh  (the actual daemon)
  ├── kill old agent (if PID file exists)
  └── nohup ~/.mgnt-agent/agent.sh > ~/.mgnt-agent/agent.log 2>&1 &
      └── write PID to ~/.mgnt-agent/agent.pid
```

### 4.2 Agent Main Loop

```
while true:
  1. Every 5s: recalculate CPU usage (1s blocking read from /proc/stat)
  2. Read RAM from /proc/meminfo
  3. Read Disk from df -k /
  4. POST telemetry to /agent/report
  5. Parse response: interval, command, command_id
  6. If command received:
     a. Pre-process interactive commands (top → top -b -n 1, etc.)
     b. Write temp wrapper script to /tmp/
     c. Execute via bash wrapper (preserves cd state via pwd output)
     d. POST result to /agent/report/result
  7. Sleep based on interval (1s interval → 0.5s sleep; 5s interval → 4s sleep)
```

### 4.3 `POST /agent/report` Request Body

```json
{
  "token": "8e2eeec7634c40d66d43cfda2aec7e5a",
  "hostname": "prod-db-01",
  "ip": "10.0.0.5",
  "os": "Ubuntu 22.04.3 LTS",
  "cpu_model": "Intel(R) Xeon(R) Gold 6248R",
  "cpu_cores": 48,
  "cpu_usage": 12.5,
  "ram_total": 125.89,
  "ram_used": 42.10,
  "ram_free": 83.79,
  "ram_usage": 33.4,
  "disk_total": 931.51,
  "disk_used": 241.33,
  "disk_free": 690.18,
  "disk_usage": 26,
  "uptime": "3 days, 4 hours, 22 minutes"
}
```

### 4.4 `POST /agent/report` Response Body

```json
// Normal (no queued command)
{"status": "success", "interval": 5}

// With queued command
{"status": "success", "interval": 1, "command": "ls -la /var/log", "command_id": "a1b2-c3d4-..."}
```

### 4.5 `POST /agent/report/result` Request Body

```json
{
  "token": "8e2eeec7634c40d66d43cfda2aec7e5a",
  "command_id": "a1b2-c3d4-e5f6-g7h8-i9j0",
  "output": "total 128\ndrwxr-xr-x ...",
  "status": "success",
  "prompt": "root@prod-db-01:/var/log$"
}
```

`status` is `"success"` or `"error"` (non-zero exit code).

### 4.6 `cd` State Preservation

The agent uses a temp wrapper script and captures `pwd` to a file after execution:

```bash
# Writes to $EXIT_FILE:
# Line 1: exit code
# Line 2: current working directory (after command ran)
```

On next command execution, `CURRENT_DIR` is set to the captured directory.

---

## 5. Authentication & Session Management

### 5.1 Login Flow

```
POST /api/auth/login {"username":"admin","password":"admin123"}
  → Compare against config.AdminUsername / config.AdminPassword (plaintext)
  → generateToken() → 32 random bytes → hex → 64-char session token
  → sessions[token] = time.Now().Add(24h)
  → Set-Cookie: session_token=<token>; HttpOnly; Path=/
  → 200 {"status":"success"}
```

> Passwords are stored in plaintext in `config.json`. Production deployments should change the default password immediately. Future improvement: hash+salt storage.

### 5.2 Auth Middleware

Every protected endpoint goes through `authMiddleware`:

```go
func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        cookie, err := r.Cookie("session_token")
        // missing cookie → 401
        expiry, ok := sessions[cookie.Value]
        // unknown or expired token → 401 + delete from map
        next.ServeHTTP(w, r)
    }
}
```

### 5.3 Session Cleanup

A goroutine runs every 10 minutes and deletes expired entries from the `sessions` map.

---

## 6. Data Persistence

### 6.1 `data/servers.json` Schema

```json
[
  {
    "id": "uuid-v4-format",
    "name": "friendly server name",
    "token": "32-char hex agent token",
    "ip": "192.168.1.1",
    "os": "Ubuntu 22.04.3 LTS",
    "cpu_model": "Intel...",
    "cpu_cores": 4,
    "cpu_usage": 15.2,
    "ram_total": 7.72,
    "ram_used": 3.12,
    "ram_free": 4.60,
    "ram_usage": 40.4,
    "disk_total": 466.43,
    "disk_used": 212.43,
    "disk_free": 230.24,
    "disk_usage": 48,
    "uptime": "2 days, 4 hours",
    "last_report": "2026-05-23T11:06:45+07:00",
    "connected": true,
    "active_console": false
  }
]
```

Fields tagged `json:"-"` (`PendingCmds`, `ExecutedCmds`, `LastTerminalPoll`) are not persisted.

### 6.2 Save Strategy

`saveServersNoLock()` is called after every mutation:
- `handleAddServer` → after insert
- `handleDeleteServer` → after delete
- `handleAgentReport` → after metric update (on every agent poll)

This is a **full-rewrite** approach (marshal entire list, write whole file). Acceptable for small server counts.

---

## 7. Web Terminal System

### 7.1 Command Flow

```
Browser types command → Enter
  └── POST /api/servers/execute/{id} {"command": "ls -la"}
        └── server.PendingCmds = append(PendingCmds, newCmd{status:"pending"})
              └── server.ActiveConsole = true (triggers fast poll on agent side)

Agent next poll (within 0.5s in active mode):
  └── POST /agent/report → response includes {command, command_id}
        └── agent executes command
              └── POST /agent/report/result → output + status + prompt

Browser terminal poller (300ms when pending):
  └── GET /api/servers/terminal/{id}
        └── returns all ExecutedCmds + PendingCmds
              └── frontend renders output, updates prompt
```

### 7.2 Terminal Polling Speed

The frontend dynamically adjusts poll interval:

| State | Poll Interval |
|-------|--------------|
| No pending commands | 1000ms |
| Commands pending (waiting for output) | 300ms |

The agent similarly adjusts:

| State | Agent Sleep |
|-------|------------|
| `interval` response = 5 | sleep 4s |
| `interval` response = 1 (ActiveConsole) | sleep 0.5s |

### 7.3 Command History & Deduplication

- `termState.printedCmdLines` (Set) — prevents re-printing a command line that was already shown
- `termState.processedCmds` (Set) — prevents processing output for a command already rendered
- `termState.pendingEntries` (Map) — maps `cmd.id → DOM element` for the "Executing..." spinner

### 7.4 Stale Console Detection

If the browser stops polling `GET /api/servers/terminal/{id}` for > 12 seconds (tab closed, navigated away), the server sets `server.ActiveConsole = false` on the next agent report. This drops the agent back to 5s polling.

---

## 8. API Reference

### 8.1 Error Response Format

All errors return HTTP 4xx/5xx with JSON body:
```json
{"error": "descriptive message"}
```

### 8.2 Success Response Format

```json
{"status": "success", "message": "optional details"}
```

### 8.3 Full Endpoint Details

#### `POST /api/auth/login`
**Request**: `{"username": "admin", "password": "admin123"}`  
**Response 200**: `{"status":"success","message":"Login successful"}`  
**Response 401**: `{"error":"Tên đăng nhập hoặc mật khẩu không chính xác"}`

#### `GET /api/servers`
**Auth**: Required  
**Response 200**: JSON array of Server objects with all metrics

#### `POST /api/servers/add`
**Auth**: Required  
**Request**: `{"name": "my-server"}`  
**Response 200**: Full Server object with generated `id` and `token`

#### `DELETE /api/servers/{id}`
**Auth**: Required  
**Response 200**: `{"status":"success","message":"Server removed"}`  
**Response 404**: `{"error":"Server not found"}`

#### `POST /api/servers/execute/{id}`
**Auth**: Required  
**Request**: `{"command": "df -h"}`  
**Response 200**: `{"status":"queued","command_id":"<uuid>"}`

#### `GET /api/servers/terminal/{id}`
**Auth**: Required  
**Response 200**: Array of `ConsoleCommand` objects

```json
[
  {
    "id": "uuid",
    "command": "ls -la",
    "output": "total 8\n...",
    "status": "success",
    "prompt": "root@host:~$",
    "created_at": "2026-05-23T11:00:00Z"
  }
]
```

#### `GET /agent/install/{token}`
**Auth**: None (token is the auth)  
**Response 200**: Shell script (`text/x-shellscript`)

---

## 9. Deployment Guide

### 9.1 Server Topology

```
172.25.155.195  →  Management Server (mgnt-server binary runs here)
172.25.155.206  →  Test/Agent Server (agent.sh runs here, reports to 172.25.155.195)
Local Machine   →  Development (go run main.go)
```

### 9.2 Build

```bash
# For local testing
go run main.go

# Cross-compile for Ubuntu deploy target
GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o mgnt-server main.go
```

### 9.3 Sync to Deploy Server

```bash
# Sync source files (not data directory, not binary)
rsync -avz \
  --exclude='data/' \
  --exclude='mgnt-server' \
  --exclude='.git/' \
  ./ ubuntu@172.25.155.195:~/mgnt-server/

# Sync compiled binary separately
rsync -avz mgnt-server ubuntu@172.25.155.195:~/mgnt-server/
```

### 9.4 Start on Deploy Server

```bash
ssh ubuntu@172.25.155.195
cd ~/mgnt-server
./mgnt-server
```

Or via systemd (see README.md for unit file).

### 9.5 Install Agent on Test Server

From the dashboard at `http://172.25.155.195:8080`:
1. Login → Add New Server → name it (e.g. `vcr-test-206`)
2. Copy the curl command
3. SSH into `172.25.155.206` and run the curl command

Or directly:
```bash
ssh ubuntu@172.25.155.206
curl -fsSL http://172.25.155.195:8080/agent/install/<token> | bash
```

### 9.6 Docker Deployment

```bash
# On 172.25.155.195
docker-compose up -d

# View logs
docker-compose logs -f

# Stop
docker-compose down
```

Data is persisted via volume mount `./data:/app/data`.

---

## 10. Known Limitations & Bugs

### 10.1 Current Limitations

| Area | Limitation |
|------|-----------|
| Auth | Plaintext password in config.json |
| Auth | Single admin account only |
| Persistence | Full JSON rewrite on every agent poll (performance concern at > 50 servers) |
| Sessions | Lost on server restart (users must re-login) |
| Agent | No automatic reconnect detection beyond 15s window |
| Terminal | No PTY — truly interactive programs (vim, mysql REPL, etc.) cannot run |
| Terminal | Command output not streamed — user waits for command to complete |
| Terminal | Max 30 commands in `ExecutedCmds` backlog per server |
| Security | `Secure: false` on session cookie — must be true behind HTTPS |
| HTTPS | No TLS support built-in — requires reverse proxy (nginx, caddy) for HTTPS |

### 10.2 Bug Notes

- `handleGetServers` has dead code: the if/else for `s.Connected && time.Since < 15s` does nothing (both branches are empty, just appending `s` to list). Connection status is not updated server-side; it's calculated client-side.
- Agent CPU measurement: `get_cpu_usage()` blocks for 1 second. This is skipped when `NOW - LAST_CPU_TIME < 5`, but on the recalculation tick, the agent will be slow to respond (1s blocking call).
- `checkAuth()` in the frontend calls `GET /api/servers` (not `/api/auth/check`) to determine login state — a side effect approach that works but is non-obvious.

---

## 11. Infrastructure Map

```
┌──────────────────────────────────────────────────────────────────┐
│  Development Machine (local)                                      │
│  go run main.go → localhost:8080                                  │
│  Test: curl agent install to localhost agent                      │
└────────────────────┬─────────────────────────────────────────────┘
                     │ rsync
                     ▼
┌──────────────────────────────────────────────────────────────────┐
│  ubuntu@172.25.155.195 — DEPLOY SERVER                           │
│  ~/mgnt-server/mgnt-server  (binary)                             │
│  ~/mgnt-server/config.json  (credentials)                        │
│  ~/mgnt-server/data/servers.json  (server registry)             │
│  ~/mgnt-server/public/  (web UI)                                 │
│  Listens on :8080                                                 │
└────────────────────┬─────────────────────────────────────────────┘
                     │ Agent polls every 5s
                     │ POST http://172.25.155.195:8080/agent/report
                     │
┌────────────────────▼─────────────────────────────────────────────┐
│  ubuntu@172.25.155.206 — TEST/AGENT SERVER                       │
│  ~/.mgnt-agent/agent.sh  (bash daemon)                           │
│  ~/.mgnt-agent/agent.log                                         │
│  ~/.mgnt-agent/agent.pid                                         │
│  Reports CPU/RAM/Disk to 172.25.155.195                          │
│  Executes shell commands from dashboard terminal                  │
└──────────────────────────────────────────────────────────────────┘
```
