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
│       └── main.go              # Daemon entry point, background collector & graceful shutdown
├── internal/
│   ├── api/                     # HTTP router, middleware, handlers (net/http)
│   │   ├── handlers.go          # Route handlers (/health, /host, /containers, lifecycle, settings)
│   │   ├── middleware.go        # Request logging, CORS, recovery, API Key authentication
│   │   ├── response.go          # Standard JSON response envelopes
│   │   └── server.go            # HTTP server wrapper & routing table
│   ├── config/                  # Environment variable configuration loader
│   │   └── config.go            # Runtime configuration
│   ├── database/                # SQLite pure-Go persistence layer (modernc.org/sqlite)
│   │   ├── db.go                # Connection pooling, WAL mode pragmas & schema migrations
│   │   ├── keys.go              # API key hashing, creation & validation
│   │   ├── metrics.go           # Container metrics history storage & pruning
│   │   └── settings.go          # Application settings key-value store
│   ├── docker/                  # Official Docker SDK client & container monitoring
│   │   └── monitor.go           # Inspection, stats calculation, lifecycle controls (start/stop/restart)
│   ├── metrics/                 # Host hardware metrics collection (gopsutil)
│   │   └── host.go              # CPU, RAM, Disk partitions, Host OS, Load
│   └── model/                   # Data transfer objects & schema definitions
│       ├── container.go         # Container summary, detail, and resource stats
│       ├── database.go          # Metrics history record, setting, and API key metadata
│       └── host.go              # Host hardware metrics models
├── .gitignore                   # Comprehensive Go & SQLite gitignore
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
| `FENRIR_DB_PATH` | `fenrir.db` | Local SQLite database file path (pure-Go driver) |
| `FENRIR_API_KEY` | *(empty)* | Pre-shared master API key for authenticating requests |
| `FENRIR_METRICS_INTERVAL_SEC` | `30` | Interval in seconds for background container metrics collection |
| `FENRIR_METRICS_CACHE_TTL_SEC` | `2` | Caching TTL for host hardware metrics |
| `FENRIR_GAME_KEYWORDS` | `minecraft,mc,itzg,paper,purpur,forge,fabric,spigot,bedrock,valheim` | Comma-separated keywords for auto-detecting game servers |
| `FENRIR_CORS_ORIGINS` | `*` | Allowed CORS origins |

---

## Security & API Key Authentication

Control actions and sensitive data are secured by the API key authentication middleware. Requests are authenticated against:
1. The master key configured via `FENRIR_API_KEY`, and/or
2. Dynamic cryptographically hashed (SHA-256) API keys stored in SQLite (`fenrir.db`).

Clients may supply keys via:
- **Header:** `X-API-Key: <key>`
- **Header:** `Authorization: Bearer <key>` or `Authorization: ApiKey <key>`
- **Query Parameter:** `?api_key=<key>`

Unauthenticated or invalid requests to protected endpoints return `401 Unauthorized` with a standard JSON error envelope.

---

## API Endpoints

All endpoints return a standardized JSON envelope:
```json
{
  "success": true,
  "data": { ... }
}
```

### 1. Health & Connection Status
- **`GET /api/v1/health`** (or `GET /health`)
  - Reports daemon status (`ok` / `degraded`), uptime, version, Docker daemon ping, and SQLite database connectivity.

### 2. Host Hardware Metrics
- **`GET /api/v1/host`**
  - Returns CPU (overall + per-core percentages, core counts, CPU model), RAM and Swap metrics, mounted disk partitions usage, host OS metadata, and system load averages.

### 3. Container Monitoring & History
- **`GET /api/v1/containers`**
  - Query parameters:
    - `tracked_only=true`: Filter only containers with `fenrir.enable=true`.
    - `type=<type>`: Filter by specific container type (e.g. `type=game`, `type=database`, `type=webapp`).
    - `game_only=true`: Filter only containers classified as game servers.
    - `all=true`: Include stopped/exited containers.
    - `stats=true`: Include live CPU, Memory, and Network I/O metrics for running containers.
- **`GET /api/v1/containers/{id}`**
  - Full details including volume mounts, networks, sanitized environment variables (credentials masked), health status, and live resource stats.
- **`GET /api/v1/containers/{name}/metrics`**
  - Retrieves time-series container metrics history persisted in SQLite. Supports `limit` (default 50) and `since` (e.g. `1h`, `24h`, RFC3339).

### 4. Container Lifecycle Controls (Secure POST Endpoints)
Protected by API Key authentication:
- **`POST /api/v1/containers/{name}/start`**
  - Starts a stopped container utilizing the Docker SDK.
- **`POST /api/v1/containers/{name}/stop`**
  - Gracefully stops a running container. Supports optional `?timeout=N` or `{"timeout": N}` body.
- **`POST /api/v1/containers/{name}/restart`**
  - Restarts a container with optional timeout.

### 5. Application Settings (SQLite Persistence)
- **`GET /api/v1/settings`**: List all application settings.
- **`GET /api/v1/settings/{key}`**: Retrieve setting value by key.
- **`PUT /api/v1/settings/{key}`**: Create or update setting (Requires API Key).
- **`DELETE /api/v1/settings/{key}`**: Remove setting (Requires API Key).

### 6. API Key Administration
- **`GET /api/v1/auth/keys`**: List metadata for generated keys (Requires API Key).
- **`POST /api/v1/auth/keys`**: Create a new API key (Requires API Key).
- **`DELETE /api/v1/auth/keys/{prefix}`**: Revoke an API key (Requires API Key).

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
