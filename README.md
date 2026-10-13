# Fenrir Daemon

[![Go Version](https://img.shields.io/badge/go-1.24%2B-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

**Fenrir Daemon** is a lightweight, Go-based home lab management daemon engineered to monitor containerized game servers (especially Minecraft) and host infrastructure running on local or remote Linux hosts via Docker.

---

## Architecture & Layout

Fenrir follows standard Go project conventions with strict separation of concerns:

```
fenrir-daemon/
├── cmd/
│   └── fenrir/
│       └── main.go              # Daemon entry point & graceful shutdown
├── internal/
│   ├── api/                     # HTTP router, middleware, handlers (net/http)
│   │   ├── handlers.go          # Route handlers (/health, /host, /containers)
│   │   ├── middleware.go        # Request logging, CORS, recovery
│   │   ├── response.go          # Standard JSON response envelopes
│   │   └── server.go            # HTTP server wrapper & routing table
│   ├── config/                  # Environment variable configuration loader
│   │   └── config.go
│   ├── docker/                  # Official Docker SDK client & container monitoring
│   │   └── monitor.go           # Inspection, stats calculation, game detection
│   ├── metrics/                 # Host hardware metrics collection (gopsutil)
│   │   └── host.go              # CPU, RAM, Disk partitions, Host OS, Load
│   └── model/                   # Data transfer objects & schema definitions
│       ├── container.go         # Container summary, detail, and resource stats
│       └── host.go              # Host hardware metrics models
├── .gitignore                   # Comprehensive Go gitignore
├── go.mod
└── go.sum
```

---

## Configuration

Fenrir Daemon is configured via environment variables with sensible defaults:

| Variable | Default | Description |
|---|---|---|
| `FENRIR_PORT` | `8080` | Port for the HTTP API server |
| `DOCKER_HOST` | *(empty)* | Docker daemon endpoint (e.g. `unix:///var/run/docker.sock` or `tcp://linux-host:2375`) |
| `FENRIR_METRICS_CACHE_TTL_SEC` | `2` | Caching TTL for host hardware metrics |
| `FENRIR_GAME_KEYWORDS` | `minecraft,mc,itzg,paper,purpur,forge,fabric,spigot,bedrock,valheim` | Comma-separated keywords for auto-detecting game servers |
| `FENRIR_CORS_ORIGINS` | `*` | Allowed CORS origins |

---

## API Endpoints (Phase 1)

All endpoints return a standardized JSON envelope:
```json
{
  "success": true,
  "data": { ... }
}
```

### 1. Health & Connection Status
- **`GET /api/v1/health`** (or `GET /health`)
  - Reports daemon status (`ok` / `degraded`), uptime, version, and Docker daemon ping latency.

### 2. Host Hardware Metrics
- **`GET /api/v1/host`**
  - Returns CPU (overall + per-core percentages, core counts, CPU model), RAM and Swap metrics, mounted disk partitions usage, host OS metadata, and system load averages.

### 3. Container Listing
- **`GET /api/v1/containers`**
  - Query parameters:
    - `tracked_only=true`: Filter only containers with `fenrir.enable=true`.
    - `type=<type>`: Filter by specific container type (e.g. `type=game`, `type=database`, `type=webapp`).
    - `game_only=true`: Filter only containers classified as game servers (Minecraft, Valheim, Palworld, or tagged with `type=game`).
    - `all=true`: Include stopped/exited containers.
    - `stats=true`: Include live CPU, Memory, and Network I/O metrics for running containers.

### 4. Container Inspection
- **`GET /api/v1/containers/{id}`**
  - Full details including volume mounts, networks, sanitized environment variables (credentials masked), health status, and live resource stats.

---

## Docker Labels & Multi-Container Classification

Fenrir Daemon parses Docker labels to support heterogeneous home lab workloads (game servers, databases, web applications). To keep the daemon generic and abstracted across any game server (Minecraft, Valheim, Palworld, etc.), game containers use the generic category `game` for `fenrir.type`, while `fenrir.display_name` is formatted to contain both the game and a friendly name:

| Docker Label | Example Values | Description |
|---|---|---|
| `fenrir.enable` | `true`, `false` | Flags whether Fenrir tracks this container |
| `fenrir.type` | `game`, `database`, `webapp` | Abstracted container role (`game` for any game server) |
| `fenrir.display_name` | `Minecraft - Survival SMP`, `Valheim - Odin Realm`, `Postgres - Primary DB` | Formatted name containing the game/service and friendly name |

### Example Docker Compose / Run Labels
```yaml
services:
  # Minecraft Game Server
  minecraft:
    image: itzg/minecraft-server
    labels:
      fenrir.enable: "true"
      fenrir.type: "game"
      fenrir.display_name: "Minecraft - Survival SMP"

  # Valheim Game Server
  valheim:
    image: lloesche/valheim-server
    labels:
      fenrir.enable: "true"
      fenrir.type: "game"
      fenrir.display_name: "Valheim - Odin Realm"

  # Database
  postgres:
    image: postgres:16-alpine
    labels:
      fenrir.enable: "true"
      fenrir.type: "database"
      fenrir.display_name: "Postgres - Primary DB"

  # Web App
  dashboard:
    image: nginx:alpine
    labels:
      fenrir.enable: "true"
      fenrir.type: "webapp"
      fenrir.display_name: "Web Portal - Frontend"
```

Each container returned by `/api/v1/containers` and `/api/v1/containers/{id}` exposes:
```json
{
  "id": "c1f2e3d4...",
  "names": ["mc-server-1"],
  "fenrir": {
    "enabled": true,
    "type": "game",
    "displayName": "Minecraft - Survival SMP"
  },
  "isGameServer": true,
  "gameType": "minecraft",
  "state": "running",
  "status": "Up 4 hours"
}
```

## Building and Running

### Build
```bash
go build -o bin/fenrir ./cmd/fenrir
```

### Run Locally
```bash
./bin/fenrir
```

### Run Targeting Remote Linux Host
```bash
DOCKER_HOST="tcp://192.168.1.100:2375" FENRIR_PORT="8080" ./bin/fenrir
```

### Run Tests
```bash
go test -v ./...
```
