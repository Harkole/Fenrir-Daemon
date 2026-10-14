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
│       └── main.go              # Daemon entry point, background collector, archive pruner & shutdown
├── internal/
│   ├── api/                     # HTTP router, middleware, handlers (net/http)
│   │   ├── handlers.go          # Route handlers (/health, /host, /containers, /projects, /archives)
│   │   ├── middleware.go        # Request logging, CORS, recovery, API Key authentication
│   │   ├── response.go          # Standard JSON response envelopes
│   │   └── server.go            # HTTP server wrapper & routing table
│   ├── archive/                 # 90-day rolling zip archive creation, restore, and pruning
│   │   └── archive.go           # Directory zipping, zip slip protection, restore & prune logic
│   ├── compose/                 # Docker Compose filesystem provisioning & execution engine
│   │   ├── engine.go            # Pre-flight syntax validation, stack provisioning, compose commands
│   │   ├── permissions.go       # UID/GID 1000 container permissions enforcement
│   │   └── ports.go             # Compose YAML host port parser (short & long syntax)
│   ├── config/                  # Environment variable configuration loader
│   │   └── config.go            # Runtime configuration
│   ├── database/                # SQLite pure-Go persistence layer (modernc.org/sqlite)
│   │   ├── archives.go          # Archive metadata CRUD & expiration queries
│   │   ├── db.go                # Connection pooling, WAL mode pragmas & schema migrations
│   │   ├── keys.go              # API key hashing, creation & validation
│   │   ├── metrics.go           # Container metrics history storage & pruning
│   │   ├── ports.go             # Active host port allocation & collision tracking
│   │   ├── projects.go          # Registered compose projects CRUD
│   │   └── settings.go          # Application settings key-value store
│   ├── docker/                  # Official Docker SDK client & container monitoring
│   │   └── monitor.go           # Inspection, stats calculation, lifecycle controls (start/stop/restart)
│   ├── metrics/                 # Host hardware metrics collection (gopsutil)
│   │   └── host.go              # CPU, RAM, Disk partitions, Host OS, Load
│   └── model/                   # Data transfer objects & schema definitions
│       ├── archive.go           # Archive metadata and countdown model
│       ├── container.go         # Container summary, detail, and resource stats
│       ├── database.go          # Metrics history record, setting, and API key metadata
│       ├── host.go              # Host hardware metrics models
│       └── project.go           # Compose project, service status, and teardown models
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
| `FENRIR_STACKS_DIR` | `/var/lib/fenrir/stacks/` | Isolated root directory for Docker Compose stack projects |
| `FENRIR_ARCHIVES_DIR` | `/var/lib/fenrir/archives/` | Directory where rolling 90-day backup archives are stored |
| `FENRIR_ARCHIVE_RETENTION_DAYS` | `90` | Expiration retention window in days before automatic archive pruning |
| `FENRIR_API_KEY` | *(empty)* | Pre-shared master API key for authenticating requests |
| `FENRIR_METRICS_INTERVAL_SEC` | `30` | Interval in seconds for background container metrics collection |
| `FENRIR_METRICS_CACHE_TTL_SEC` | `2` | Caching TTL for host hardware metrics |
| `FENRIR_DEFAULT_UID` | `1000` | Target user ID for stack directory file permissions (Minecraft/Java container compatibility) |
| `FENRIR_DEFAULT_GID` | `1000` | Target group ID for stack directory file permissions |
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
- **`POST /api/v1/containers/{name}/start`**: Starts a stopped container via Docker SDK.
- **`POST /api/v1/containers/{name}/stop`**: Gracefully stops a running container. Supports optional `?timeout=N` or `{"timeout": N}` body.
- **`POST /api/v1/containers/{name}/restart`**: Restarts a container with optional timeout.

### 5. Docker Compose Stack Management & Infrastructure Safety
- **`GET /api/v1/projects`**: List all registered stacks and runtime statuses.
- **`GET /api/v1/ports`**: List all host ports currently bound by active stacks (`hostPort`, `containerPort`, `protocol`, `projectName`).
- **`POST /api/v1/projects`** *(Auth Required)*: Provision a new stack directory (`/var/lib/fenrir/stacks/<name>/`), create `data/` volume directory, write `compose.yml`, and initiate async deployment.
  - **Pre-flight Syntax & Schema Validation**: Runs `docker compose config` against a temporary file. If syntax validation fails, returns `422 Unprocessable Entity` with error details, leaving the live host completely untouched.
  - **Active Port Collision Tracking & Validation**: Parses host ports defined in the incoming `compose.yml` (supporting short and long YAML syntax and port ranges) and validates against active stacks in SQLite. If a host port is already bound, returns `400 Bad Request` with collision details (`port conflict: host port 25565/tcp is already allocated to project "atm10"`), aborting before any files or directories are created on disk.
  - **UID 1000 Container Permissions**: Recursively sets ownership and directory (`0775`) / file (`0664`) permissions across the stack directory and `data/` volume to match standard container user expectations (UID/GID 1000 or `FENRIR_DEFAULT_UID`/`GID`), preventing Java/Minecraft startup "Permission denied" errors.
- **`GET /api/v1/projects/{name}`**: Get stack details, files list, data directory presence, compose content, and allocated host ports.
- **`GET /api/v1/projects/{name}/compose`**: Read raw `compose.yml` content.
- **`PUT /api/v1/projects/{name}/compose`** *(Auth Required)*: Update `compose.yml` with pre-flight syntax and port collision validation before async redeployment.
- **`POST /api/v1/projects/{name}/up`** *(Auth Required)*: Execute `docker compose up -d` with port conflict validation.
- **`POST /api/v1/projects/{name}/down`** *(Auth Required)*: Execute `docker compose down`, immediately freeing allocated ports in SQLite.
- **`POST /api/v1/projects/{name}/restart`** *(Auth Required)*: Execute `docker compose restart`.
- **`GET /api/v1/projects/{name}/status`**: Run `docker compose ps --format json` to check service health.
- **`GET /api/v1/projects/{name}/logs`**: View service container logs.
- **`DELETE /api/v1/projects/{name}`** *(Auth Required)*: Safe teardown. Brings down stack, instantly releases host port allocations, bundles the directory into a timestamped `.zip` archive in `/var/lib/fenrir/archives/` with a 90-day retention, removes project files, and clears database records.

### 6. 90-Day Rolling Archive & Restore System
- **`GET /api/v1/archives`**: List all backup archives with a countdown showing `daysUntilDeletion` (days remaining until auto-prune).
- **`GET /api/v1/projects/{name}/archives`**: List archives specifically for a project.
- **`POST /api/v1/archives/{id}/restore`** *(Auth Required)*: Restore a `.zip` archive back into its stack directory with Zip Slip protection and recursive UID/GID 1000 permissions enforcement.
- **`DELETE /api/v1/archives/{id}`** *(Auth Required)*: Delete an archive file and database record.
- **Automated Daily Pruning**: A background goroutine checks daily and automatically removes expired archive `.zip` files and database records older than 90 days.

### 7. Application Settings (SQLite Persistence)
- **`GET /api/v1/settings`**: List all application settings.
- **`GET /api/v1/settings/{key}`**: Retrieve setting value by key.
- **`PUT /api/v1/settings/{key}`** *(Auth Required)*: Create or update setting.
- **`DELETE /api/v1/settings/{key}`** *(Auth Required)*: Remove setting.

### 8. API Key Administration
- **`GET /api/v1/auth/keys`** *(Auth Required)*: List metadata for generated keys.
- **`POST /api/v1/auth/keys`** *(Auth Required)*: Create a new API key.
- **`DELETE /api/v1/auth/keys/{prefix}`** *(Auth Required)*: Revoke an API key.

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
