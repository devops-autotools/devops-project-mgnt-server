# CLAUDE.md — mgnt-server

## Project Identity

**mgnt-server** is a Go server monitoring and remote management dashboard. Single-file backend (`main.go`), compiled Go agent binary (`cmd/agent/main.go`), vanilla JS SPA frontend — no build step for frontend.

## Architecture in One Paragraph

The Go backend serves a static SPA from `public/` and exposes REST + WebSocket endpoints on HTTP port 8080 (browser) and HTTPS port 8443 (agent TLS-only). Target servers run a compiled Go **agent** with four routines: (1) CPU sampler — reads `/proc/stat` every second; (2) telemetry loop — POSTs CPU/RAM/Disk/containers to `/agent/report` every 5s; (3) command loop — long-polls `GET /agent/cmd/wait/<token>?v=2`, returns 204 on timeout, handles `exec` (run command) and `shell` (open PTY session); (4) per-session shell handler — opens a Linux PTY, runs the target process (kubectl exec / docker exec / bash), and bridges PTY↔WebSocket. The browser uses `EventSource` for command results (SSE) and `xterm.js + WebSocket` for interactive shell sessions.

## Tech Stack

- **Backend**: Go 1.22, `net/http` stdlib + `github.com/gorilla/websocket` (PTY WebSocket bridge)
- **Agent**: Go 1.22 binary (`cmd/agent/`), compiled for `linux/amd64` and `linux/arm64`
- **Frontend**: Vanilla JS (no React/Vue), plain CSS, zero build pipeline; xterm.js + FitAddon loaded from CDN
- **Persistence**: `data/servers.json` + `data/tags.json` (JSON files, no database)
- **Auth**: In-memory session map with 24h expiry, `HttpOnly` cookie
- **Deploy**: Docker/docker-compose (non-root Alpine container) or raw binary on Ubuntu

## Key Files

| File | Role |
|------|------|
| `main.go` | All Go backend (structs, handlers, agent protocol, shell bridge, auth, config) |
| `cmd/agent/main.go` | Go agent binary (telemetry, PTY shell, K8s/Docker container collection) |
| `public/js/app.js` | All frontend SPA logic (auth, terminal, shell windows, containers, tags) |
| `public/css/style.css` | Dark glassmorphism design system |
| `public/index.html` | SPA shell (no dynamic generation) |
| `public/downloads/agent-linux-amd64` | Pre-built agent binary (gitignored, built by Dockerfile) |
| `data/.env` | Auto-generated credentials & ports (gitignored, persisted via Docker volume) |
| `data/servers.json` | Persisted server registry |
| `data/tags.json` | Persisted tag definitions |

## Critical Invariants

- **One external Go package** — `gorilla/websocket` for PTY WebSocket bridge. Do not add other external packages.
- **No frontend build step** — `app.js` and `style.css` are plain files served directly. No npm, no bundler.
- **Agent is a compiled Go binary** — lives in `cmd/agent/main.go`. The old bash agent (`/agent/install`) still works for basic telemetry+commands; the Go agent (`/agent/install2`) adds PTY shell and container support.
- **Stateless requests** — server holds state in-memory (`servers` map, `sessions` map, `shellSessions` map). No database calls anywhere.
- **Thread safety** — `servers` map uses `serverMutex` (RWMutex); `sessions` map uses `sessionMtx` (Mutex); `tags` uses `tagsMu` (RWMutex); `shellSessions` uses `shellSessionsMu` (RWMutex).
- **data/ is gitignored** — contains credentials, TLS private keys, and runtime state. Never commit it.
- **Agent binaries gitignored** — `public/downloads/agent-*` are built by the Dockerfile and gitignored. Never commit them.
- **Agent single-instance** — agent acquires an exclusive `flock` on `agent.pid` at startup and stores the fd in the package-level `pidLockFd` variable (never closed, keeping the lock for the process lifetime). A second instance exits immediately.

## Data Structures

```go
Server          // monitored server + live metrics + container list + shell/command queues
ShellSession    // live PTY bridge: browser WebSocket ↔ agent WebSocket
AgentReport     // JSON body sent by agent on each telemetry poll
ConsoleCommand  // queued/executed shell command (pending → running → success/error)
Tag             // {Name string, Color string}
Config          // port, tls_port, admin_username, admin_password, session_secret
```

`Server.PendingCmds`, `Server.ExecutedCmds`, and `Server.PendingShellSessions` are **not persisted** (`json:"-"`). They reset on server restart.

## Configuration

Config lives in `data/.env` (auto-created on first run):

```env
MGNT_PORT=8080               # browser HTTP port
MGNT_AGENT_TLS_PORT=8443     # agent HTTPS port (TLS-only listener)
MGNT_USERNAME=admin
MGNT_PASSWORD=<random>       # generated on first run, printed to log as WARNING
MGNT_SESSION_SECRET=<random> # generated on first run
```

## Agent Protocol — v2 (Go binary)

**Telemetry loop** (every 5s):
1. Agent `POST /agent/report` with CPU/RAM/Disk/containers JSON + token
2. Server updates metrics + container list, returns `{"status":"ok"}`

**Command loop** (long-poll, `?v=2`):
1. Agent `GET /agent/cmd/wait/<token>?v=2` — server blocks until work arrives or 25s timeout
2. **exec command**: server returns `{"type":"exec","command":"...","command_id":"..."}`, agent runs it and POSTs result to `/agent/report/result`
3. **shell session**: server returns `{"type":"shell","session_id":"...","container_id":"...","shell_mode":"exec"|"logs"}`, agent calls `handleShellSession` in a goroutine

**Shell session flow**:
1. Browser opens WebSocket → `wss://<host>/api/servers/shell/<server_id>/ws/<session_id>?container=<id>&mode=<exec|logs>`
2. Server creates `ShellSession`, adds to `PendingShellSessions`, signals `cmdNotify`
3. Agent's long-poll wakes, dispatches shell command with `session_id` + `container_id` + `shell_mode`
4. Agent opens PTY, starts target process, connects to `wss://mgnt-server.local:8443/agent/shell/<session_id>/ws?token=<token>`
5. Server bridges browser WS ↔ agent WS, sends `{"type":"ready"}` to browser
6. Data flows: browser → server WS → agent WS → PTY master → PTY slave → process stdin/stdout

**Shell target routing** (in agent's `handleShellSession`):
```
containerID has 2 slashes  →  kubectl exec/logs  (namespace/pod/container)
containerID, mode=logs      →  docker logs -f
containerID (no slashes)    →  docker exec -it sh
default (empty containerID) →  bash -i  (host shell)
```

**Key invariant**: The `fmt.Sprintf` in `handleAgentInstallScript` has exactly 5 `%s` placeholders — order: `(serverName, baseURL, token, token, baseURL)`. Wrong count produces `%!s(MISSING)` in the generated script.

## TLS Agent Channel

- All `/agent/*` endpoints run on **HTTPS port 8443 only** — HTTP 8080 returns 404 for these paths.
- ECDSA P-256 CA + server cert auto-generated in `data/tls/` on first run.
- Server cert SAN: `DNS:mgnt-server.local`.
- Agent embeds CA cert PEM; uses `--resolve mgnt-server.local:8443:<ip>` for full TLS verification without public DNS.

## Container System

- Agent collects Docker containers via `docker ps --format json` + `docker stats`
- Agent collects K8s pods via `kubectl get pods -A -o json` + `kubectl top pods -A` (requires metrics-server)
- K8s container ID format: `namespace/podname/containername` (2 slashes) — used for both shell routing and display
- `Server.Containers []ContainerInfo` is updated on each telemetry tick (not persisted)
- `/api/containers` aggregates all containers across all servers, injects `server_name` + `server_id`

## Tag System

- Global `tags []Tag` guarded by `tagsMu sync.RWMutex`, persisted in `data/tags.json`
- `Server.Tags []string` stores tag names — names are the join key
- `handleDeleteTag()` removes tag from all servers (acquire tags lock, then servers lock separately — never nest locks to avoid deadlock)
- API: `GET/POST /api/tags`, `DELETE /api/tags/{name}`, `PUT /api/servers/{id}/tags`

## Server Connection Status

A server is considered **online** client-side if:
```
server.connected == true  AND  time.Since(server.LastReport) < 15 seconds
```
Check happens in `calculateOverviewStats()` and `renderDashboardView()`.

## Deployment

- **Management server (dashboard)**: `ubuntu@172.25.155.195` — Docker container `secure-server-manager` (ports 8080 + 8443)
- **K8s server**: `ubuntu@172.25.155.204` (vcr-registry-hni)
- **Agent servers**: `ubuntu@172.25.155.206` (vcr-db-hni), `ubuntu@172.25.155.237` (vcr-db-hcm)
- Workflow: edit local → build all binaries → deploy to .195 container → reinstall agents if agent binary changed

### Build all binaries
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o mgnt-server .
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o public/downloads/agent-linux-amd64 ./cmd/agent/
CGO_ENABLED=0 GOOS=linux GOARCH=arm64  go build -ldflags="-w -s" -o public/downloads/agent-linux-arm64  ./cmd/agent/
```

### Deploy to container
```bash
rsync -az mgnt-server ubuntu@172.25.155.195:~/mgnt-server/
rsync -az public/ ubuntu@172.25.155.195:~/mgnt-server/public/
ssh ubuntu@172.25.155.195 "
  sudo docker stop secure-server-manager
  sudo docker cp ~/mgnt-server/mgnt-server secure-server-manager:/app/mgnt-server
  sudo docker cp ~/mgnt-server/public secure-server-manager:/app/
  sudo docker start secure-server-manager
"
```

### Reinstall agent on a server (when agent binary changes)
```bash
ssh ubuntu@<ip> "
  kill \$(cat ~/.mgnt-agent/agent.pid 2>/dev/null) 2>/dev/null
  curl -fsSL http://172.25.155.195:8080/agent/install2/<token> -o /tmp/agent-new
  chmod +x /tmp/agent-new && cp /tmp/agent-new ~/.mgnt-agent/agent
  nohup ~/.mgnt-agent/agent >> ~/.mgnt-agent/agent.log 2>&1 &
"
```

## Common Tasks

### Add an API endpoint
1. Add handler function in `main.go` (`w http.ResponseWriter, r *http.Request`)
2. Register in `browserMux.HandleFunc(...)` or `agentMux.HandleFunc(...)` in `main()`
3. Wrap with `authMiddleware(...)` if it needs browser auth
4. Return JSON via `json.NewEncoder(w).Encode(...)`

### Change agent telemetry
Edit `postTelemetry()` in `cmd/agent/main.go`. The `telemetryPayload` struct must match `AgentReport` in `main.go`.

### Change agent shell behavior
Edit `handleShellSession()` in `cmd/agent/main.go`. The switch on `containerID` and `shellMode` determines what process is started. `needsCtty = true` sets the PTY slave as controlling terminal (use for interactive shells; set false for log-streaming).

### Change bash agent install script
Edit `handleAgentInstallScript()` in `main.go`. The entire script is a Go format string (`fmt.Sprintf`). Mind the 5 `%s` placeholders: `(serverName, baseURL, token, token, baseURL)`.

### Modify PTY shell bridge (server-side)
- **Browser WebSocket handler**: `handleShellBrowserWS` — creates `ShellSession`, queues in `PendingShellSessions`
- **Agent WebSocket handler**: `handleShellAgentWS` — authenticates, signals `agentReady`
- **Bridge**: `bridgeShellSession` — proxies frames bidirectionally, sends `{"type":"ready"}` on connect

### Modify command terminal
- **Agent delivery**: `handleAgentWaitCommand` (long-poll, 25s) + `handleAgentCommandResult` (broadcasts via SSE to `sseClients`)
- **Browser terminal**: `startTerminalPolling()` opens `EventSource`; `renderTerminalCommandResult()` handles `event: cmd`; `submitTerminalCommand()` pre-renders the entry after getting `command_id`
- **Tab completion**: history-based, `handleTabComplete()` cycles through `termState.history` matches

### Branching strategy
- `main` — stable, deployed to production container on 172.25.155.195
- `dev` — feature development and testing; merge to main when stable

## Testing Locally

```bash
go run main.go                          # start server on :8080
# In a browser: http://localhost:8080
# Login: admin / (password from data/.env or stdout on first run)

# Install Go agent:
curl -fsSL http://localhost:8080/agent/install2/<token> | bash

# Watch agent log:
tail -f ~/.mgnt-agent/agent.log

# Kill agent:
kill $(cat ~/.mgnt-agent/agent.pid)
```

## Style Guide

- Use `log.Printf()` for server-side logging (no structured logger)
- Return errors as JSON: `{"error":"message"}`
- Return success as JSON: `{"status":"success","message":"..."}`
- Use `serverMutex.Lock()` / `.Unlock()` consistently; call `saveServersNoLock()` after any mutation while lock is held
- Frontend: use `escHTML()` to sanitize all user/server-supplied strings before inserting into DOM
- JS cache busting: increment `?v=N` on CSS/JS `<link>`/`<script>` tags in `index.html` after each deploy
