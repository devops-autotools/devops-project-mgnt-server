# Container Page — Design Spec
**Date:** 2026-05-24  
**Status:** Approved, implementing

## Overview
Add a Containers page to mgnt-server dashboard. Each monitored server's agent reads Docker and K8s container data and reports it via existing telemetry. The dashboard renders a sidebar-filtered table with inline accordion detail, container controls (start/stop/restart), exec shell, and log tail.

## Decisions
| Question | Decision |
|----------|----------|
| Runtimes | Docker + Kubernetes |
| Actions | View + start/stop/restart + exec shell |
| Data collection | Docker: Unix socket `/var/run/docker.sock`; K8s: `kubectl` CLI |
| Layout | Sidebar filter (server, status, runtime) + flat table |
| Detail view | Inline accordion expand per row |
| Log streaming | Last 100 lines via `docker logs --tail=100` (exec mechanism) |
| Shell exec | Reuse PTY/WS shell with `docker exec -it <id> sh` |

## Data Model

```go
type ContainerInfo struct {
    ID        string   `json:"id"`
    Name      string   `json:"name"`
    Image     string   `json:"image"`
    Status    string   `json:"status"`   // running/exited/paused/created
    Runtime   string   `json:"runtime"`  // "docker" | "k8s"
    Namespace string   `json:"namespace,omitempty"`
    CPU       float64  `json:"cpu"`
    RAM       int64    `json:"ram"`      // bytes
    Ports     []string `json:"ports"`
    Uptime    string   `json:"uptime"`
}
// Added to AgentReport and Server struct
```

## API Endpoints (new)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/containers` | All containers across all servers |
| POST | `/api/servers/{id}/containers/{cid}/action` | `{"action":"start"\|"stop"\|"restart"}` |
| GET | `/api/servers/{id}/containers/{cid}/logs` | Last 100 lines as JSON |
| WS | `/api/servers/shell/{id}/ws/{sid}?container={cid}` | PTY exec into container |

## Agent Flow
1. `collectContainers()` called in telemetry loop every 5s
2. Docker: `GET http://localhost/containers/json?all=1` via Unix socket → stats per running container via `GET /containers/{id}/stats?stream=false`
3. K8s: `exec kubectl get pods -A -o json` if kubectl in PATH
4. Results merged into `AgentReport.Containers []ContainerInfo`
5. Container shell: agent receives `{"type":"shell","session_id":"...","container_id":"<id>"}` → runs `docker exec -it <id> sh` in PTY
6. Container logs: agent receives exec command `docker logs --tail=100 <id>` → returns output via existing exec result mechanism
7. Container action: agent receives exec command `docker <start|stop|restart> <id>` → returns result

## Frontend
- New "Containers" nav item below "Servers List"
- `renderContainersView()` renders sidebar + table
- Sidebar: filter by server name, status (running/exited/all), runtime (docker/k8s/all)
- Table columns: Status · Name · Server · Runtime · Image · CPU · RAM · Ports · Uptime · Actions
- Accordion: expands on row click, shows full details + Shell/Logs/action buttons
- Shell button: `openShell(serverId, serverName, containerId)` → PTY exec
- Logs button: POST action to get logs, show in modal
- Auto-tag: containers implicitly grouped/filtered by server (no separate tag system)
- Poll: reuses SSE + 5s telemetry cycle for live updates
