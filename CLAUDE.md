# CLAUDE.md — mgnt-server

## Project Identity

**mgnt-server** is a zero-dependency Go server monitoring and remote management dashboard. Single-file backend (`main.go`), vanilla JS SPA frontend, no build step needed.

## Architecture in One Paragraph

The Go backend serves a static SPA from `public/` and exposes REST endpoints. Target servers run an auto-generated bash **agent** with two loops: (1) `run_telemetry` — background loop, POSTs CPU/RAM/Disk to `/agent/report` every 5s; (2) `run_commands` — foreground long-poll loop, blocks on `GET /agent/cmd/wait/<token>` until a command is queued (delivered in <1ms) or 25s timeout (returns 204, agent retries immediately). When a command result is posted to `/agent/report/result`, the server pushes it instantly to the browser via SSE (`GET /api/servers/terminal/stream/{id}`). The browser uses `EventSource` instead of polling — zero delivery latency.

## Tech Stack

- **Backend**: Go 1.22, stdlib only (`net/http`, `encoding/json`, `sync`, `crypto/rand`)
- **Frontend**: Vanilla JS (no React/Vue/etc), plain CSS, zero build pipeline
- **Persistence**: `data/servers.json` + `data/tags.json` (JSON files, no database)
- **Auth**: In-memory session map with 24h expiry, `HttpOnly` cookie
- **Deploy**: Docker/docker-compose (non-root Alpine container) or raw binary on Ubuntu

## Key Files

| File | Role |
|------|------|
| `main.go` | All Go backend (structs, handlers, agent protocol, auth, config load) |
| `public/js/app.js` | All frontend SPA logic (auth, telemetry polling, terminal, dashboard render, tags) |
| `public/css/style.css` | Dark glassmorphism design system |
| `public/index.html` | SPA shell (no dynamic generation) |
| `data/.env` | Auto-generated credentials & ports (gitignored, persisted via Docker volume) |
| `data/servers.json` | Persisted server registry |
| `data/tags.json` | Persisted tag definitions |

## Critical Invariants

- **No external Go packages** — only `go` stdlib. Do not add any `import` that isn't in the standard library.
- **No frontend build step** — `app.js` and `style.css` are plain files served directly. No npm, no bundler.
- **Agent is a self-contained bash script** — generated dynamically in `handleAgentInstallScript`. Keep it compatible with `bash` and basic `coreutils` only.
- **Stateless requests** — the server holds agent/session state in-memory (`servers` map, `sessions` map). No database calls anywhere.
- **Thread safety** — all reads/writes to `servers` map must use `serverMutex` (RWMutex). All reads/writes to `sessions` map must use `sessionMtx` (Mutex). Tags use `tagsMu` (RWMutex).
- **data/ is gitignored** — contains credentials, TLS private keys, and runtime state. Never commit it.

## Data Structures

```go
Server          // monitored server + live metrics + terminal command queue
AgentReport     // JSON body sent by agent on each poll
ConsoleCommand  // queued/executed shell command (pending → running → success/error)
Tag             // {Name string, Color string} — color-coded label for servers
Config          // port, admin_username, admin_password, session_secret
```

`Server.PendingCmds` and `Server.ExecutedCmds` are **not persisted** to JSON (tagged `json:"-"`). They reset on server restart.  
`Server.Tags` is a `[]string` of tag names, persisted in `servers.json`.

## Configuration

Config lives in `data/.env` (auto-created on first run, persisted via the `./data:/app/data` Docker volume):

```env
MGNT_PORT=8080               # browser HTTP port
MGNT_AGENT_TLS_PORT=8443     # agent HTTPS port (TLS-only listener)
MGNT_USERNAME=admin
MGNT_PASSWORD=<random>       # generated on first run, printed to log as WARNING
MGNT_SESSION_SECRET=<random> # generated on first run
```

On first run with no `data/.env`, `loadConfig()` → `writeDotEnv()` creates it with random 16-char password and 32-char session secret.

## Agent Protocol

**Telemetry loop** (background, every 5s):
1. Agent `POST /agent/report` with CPU/RAM/Disk JSON + token
2. Server updates metrics, returns `{"status":"ok"}`

**Command loop** (foreground, long-poll):
1. Agent `GET /agent/cmd/wait/<token>` — server blocks until command queued or 25s timeout
2. On command: server returns `{"command":"...","command_id":"..."}`, agent executes immediately
3. Agent `POST /agent/report/result` with output + status + new prompt
4. Server updates `ExecutedCmds`, broadcasts result to all SSE subscribers (`sseClients[id]`)
5. Browser `EventSource` receives `event: cmd` and renders output instantly

**Key invariant**: `fmt.Sprintf` args in `handleAgentInstallScript` must match exactly 5 `%s` placeholders — order is `(serverName, baseURL, token, token, baseURL)`. Wrong arg count produces `%!s(MISSING)` in the generated script.

## TLS Agent Channel

- Agent endpoints (`/agent/report`, `/agent/cmd/wait/*`, `/agent/report/result`) run on HTTPS port 8443 **only** — HTTP 8080 returns 404 for these paths.
- ECDSA P-256 CA + server cert auto-generated in `data/tls/` on first run.
- Server cert SAN: `DNS:mgnt-server.local`.
- Agent script embeds CA cert PEM; uses `curl --cacert ca.crt --resolve mgnt-server.local:8443:<ip>` for full TLS verification.

## Tag System

- Global `tags []Tag` guarded by `tagsMu sync.RWMutex`, persisted in `data/tags.json`.
- `Server.Tags []string` stores tag names (not Tag structs) — names are the join key.
- `handleDeleteTag()` removes tag from all servers (acquire tags lock, then servers lock separately — never nest to avoid deadlock).
- API: `GET/POST /api/tags`, `DELETE /api/tags/{name}`, `PUT /api/servers/{id}/tags`.

## Server Connection Status

A server is considered **online** if:
```
server.connected == true  AND  time.Since(server.LastReport) < 15 seconds
```
This check happens **client-side** in `calculateOverviewStats()` and `renderDashboardView()`.

## Deployment

- **Management server (dashboard)**: `ubuntu@172.25.155.195` — Docker container `secure-server-manager` (ports 8080 + 8443)
- **Test/agent server**: `ubuntu@172.25.155.206`
- Workflow: edit local → test local → build → deploy to .195 container → reinstall agent on .206 if agent script changed

### Build (must use CGO_ENABLED=0 — container is Alpine)
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o mgnt-server main.go
```

### Deploy binary + static files to container
```bash
# Two separate rsync: binary first, then public/ contents
rsync -az mgnt-server ubuntu@172.25.155.195:~/mgnt-server/
rsync -az public/ ubuntu@172.25.155.195:~/mgnt-server/public/
ssh ubuntu@172.25.155.195 "
  sudo docker stop secure-server-manager
  sudo docker cp ~/mgnt-server/mgnt-server secure-server-manager:/app/mgnt-server
  sudo docker cp ~/mgnt-server/public secure-server-manager:/app/
  sudo docker start secure-server-manager
"
```

### Reinstall agent on .206 (only needed when agent script changes)
```bash
ssh ubuntu@172.25.155.206 "
  kill \$(cat ~/.mgnt-agent/agent.pid 2>/dev/null) 2>/dev/null
  curl -fsSL http://172.25.155.195:8080/agent/install/<token> -o /tmp/agent.sh
  bash /tmp/agent.sh > /tmp/agent-install.log 2>&1 &
"
```

## Common Tasks

### Add an API endpoint
1. Add handler function in `main.go` following existing pattern (`w http.ResponseWriter, r *http.Request`)
2. Register in `mux.HandleFunc(...)` in `main()`
3. Wrap with `authMiddleware(...)` if it needs auth
4. Return JSON via `json.NewEncoder(w).Encode(...)`

### Change agent behavior
Edit `handleAgentInstallScript()` in `main.go` — the entire agent is a Go format string (`scriptContent := fmt.Sprintf(...)`) served as bash. Mind the 5 `%s` placeholders and their arg order `(serverName, baseURL, token, token, baseURL)`.

### Modify terminal behavior
- **Agent command delivery**: `handleAgentWaitCommand` (long-poll, 25s timeout) + `handleAgentCommandResult` (broadcasts to `sseClients`)
- **Browser terminal**: `startTerminalPolling()` opens `EventSource` to `/api/servers/terminal/stream/{id}`; `renderTerminalCommandResult()` handles each `cmd` SSE event; `submitTerminalCommand()` pre-renders the command entry immediately after getting `command_id` from execute API
- **Tab completion**: history-based in `handleTabComplete()` — cycles through `termState.history` matches; dropdown via `showSuggestions()`/`hideSuggestions()`

### Branching strategy
- `main` — stable, deployed to production container on 172.25.155.195
- `dev` — feature development and testing; merge to main when stable

## Testing Locally

```bash
go run main.go                          # start server on port 8080
# In a browser: http://localhost:8080
# Login: admin / (password from data/.env or stdout on first run)

# Add a test server, copy the curl command, run it:
curl -fsSL http://localhost:8080/agent/install/<token> | bash

# Watch agent log:
tail -f ~/.mgnt-agent/agent.log

# Kill agent:
kill $(cat ~/.mgnt-agent/agent.pid)
```

## Style Guide

- Use `log.Printf()` for server-side logging (no structured logger needed)
- Return errors as JSON: `{"error":"message"}`
- Return success as JSON: `{"status":"success","message":"..."}`
- Use `serverMutex.Lock()` / `.Unlock()` or `defer serverMutex.Unlock()` consistently
- Call `saveServersNoLock()` after any mutation to `servers` map (while lock is already held)
- Frontend: use `escHTML()` to sanitize all user/server-supplied strings before inserting into DOM
- JS cache busting: increment `?v=N` on CSS/JS `<link>`/`<script>` tags in `index.html` after each deploy
