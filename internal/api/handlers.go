package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/archive"
	"github.com/Harkole/fenrir-daemon/internal/compose"
	"github.com/Harkole/fenrir-daemon/internal/config"
	"github.com/Harkole/fenrir-daemon/internal/database"
	"github.com/Harkole/fenrir-daemon/internal/docker"
	"github.com/Harkole/fenrir-daemon/internal/metrics"
	"github.com/Harkole/fenrir-daemon/internal/model"
	"github.com/docker/docker/client"
)

// Handler holds dependencies for all API endpoints.
type Handler struct {
	dockerMonitor *docker.Monitor
	hostCollector *metrics.HostCollector
	db            *database.DB
	composeEngine *compose.Engine
	cfg           *config.Config
	startTime     time.Time
	version       string
}

// NewHandler creates a new Handler with injected dependencies.
func NewHandler(dm *docker.Monitor, hc *metrics.HostCollector, db *database.DB, ce *compose.Engine, cfg *config.Config, version string) *Handler {
	return &Handler{
		dockerMonitor: dm,
		hostCollector: hc,
		db:            db,
		composeEngine: ce,
		cfg:           cfg,
		startTime:     time.Now().UTC(),
		version:       version,
	}
}

// HealthResponse represents system health information.
type HealthResponse struct {
	Status        string       `json:"status"` // "ok", "degraded"
	Version       string       `json:"version"`
	UptimeSeconds float64      `json:"uptimeSeconds"`
	Docker        DockerStatus `json:"docker"`
	Database      DBStatus     `json:"database"`
}

// DockerStatus indicates connection state to the Docker daemon.
type DockerStatus struct {
	Connected bool    `json:"connected"`
	PingMs    float64 `json:"pingMs,omitempty"`
	Error     string  `json:"error,omitempty"`
}

// DBStatus indicates SQLite database connectivity and state.
type DBStatus struct {
	Connected bool   `json:"connected"`
	Path      string `json:"path,omitempty"`
	Error     string `json:"error,omitempty"`
}

// HandleHealth checks daemon health, Docker connectivity, and database status.
func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	dockerStatus := DockerStatus{
		Connected: false,
	}

	start := time.Now()
	if err := h.dockerMonitor.Ping(ctx); err != nil {
		dockerStatus.Error = err.Error()
	} else {
		dockerStatus.Connected = true
		dockerStatus.PingMs = float64(time.Since(start).Microseconds()) / 1000.0
	}

	dbStatus := DBStatus{
		Connected: false,
	}
	if h.db != nil {
		dbStatus.Path = h.db.Path()
		if err := h.db.Ping(ctx); err != nil {
			dbStatus.Error = err.Error()
		} else {
			dbStatus.Connected = true
		}
	} else {
		dbStatus.Error = "database connection not configured"
	}

	status := "ok"
	if !dockerStatus.Connected || (h.db != nil && !dbStatus.Connected) {
		status = "degraded"
	}

	writeJSON(w, http.StatusOK, HealthResponse{
		Status:        status,
		Version:       h.version,
		UptimeSeconds: time.Since(h.startTime).Seconds(),
		Docker:        dockerStatus,
		Database:      dbStatus,
	})
}

// HandleHostMetrics returns hardware and system resource metrics.
func (h *Handler) HandleHostMetrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	metrics, err := h.hostCollector.Collect(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to collect host metrics: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, metrics)
}

// HandleContainers returns the list of containers with game server classification.
func (h *Handler) HandleContainers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	all, _ := strconv.ParseBool(r.URL.Query().Get("all"))
	gameOnly, _ := strconv.ParseBool(r.URL.Query().Get("game_only"))
	includeStats, _ := strconv.ParseBool(r.URL.Query().Get("stats"))
	trackedOnly, _ := strconv.ParseBool(r.URL.Query().Get("tracked_only"))
	if !trackedOnly {
		trackedOnly, _ = strconv.ParseBool(r.URL.Query().Get("tracked"))
	}
	typeFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))

	containers, err := h.dockerMonitor.ListContainers(ctx, all)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list containers: "+err.Error())
		return
	}

	results := make([]model.ContainerSummary, 0, len(containers))
	for _, c := range containers {
		if trackedOnly && !c.Fenrir.Enabled {
			continue
		}
		if typeFilter != "" && !strings.EqualFold(c.Fenrir.Type, typeFilter) {
			continue
		}
		if gameOnly && !c.IsGameServer && c.Fenrir.Type != "game" {
			continue
		}

		if includeStats && c.State == "running" {
			statsCtx, statsCancel := context.WithTimeout(ctx, 2*time.Second)
			if stats, err := h.dockerMonitor.GetContainerStats(statsCtx, c.ID); err == nil {
				c.Stats = stats
				if h.db != nil {
					name := ""
					if len(c.Names) > 0 {
						name = c.Names[0]
					}
					_ = h.db.RecordContainerMetric(context.Background(), c.ID, name, stats, time.Now().UTC())
				}
			}
			statsCancel()
		}

		results = append(results, c)
	}

	writeJSON(w, http.StatusOK, results)
}

// HandleContainerDetail returns detailed information and live stats for a single container.
func (h *Handler) HandleContainerDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		id = r.PathValue("name")
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, "container id or name is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	detail, err := h.dockerMonitor.GetContainer(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "container not found or inspect failed: "+err.Error())
		return
	}

	// Persist live metrics snapshot if available
	if detail.Stats != nil && h.db != nil {
		name := ""
		if len(detail.Names) > 0 {
			name = detail.Names[0]
		}
		_ = h.db.RecordContainerMetric(context.Background(), detail.ID, name, detail.Stats, time.Now().UTC())
	}

	writeJSON(w, http.StatusOK, detail)
}

// HandleContainerStart initiates execution of a stopped container.
func (h *Handler) HandleContainerStart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		name = r.PathValue("id")
	}
	if strings.TrimSpace(name) == "" {
		writeError(w, http.StatusBadRequest, "container name or id is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if err := h.dockerMonitor.StartContainer(ctx, name); err != nil {
		if client.IsErrNotFound(err) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("container %q not found", name))
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to start container %q: %v", name, err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"container": name,
		"action":    "start",
		"status":    "started",
	})
}

// HandleContainerStop stops a running container.
func (h *Handler) HandleContainerStop(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		name = r.PathValue("id")
	}
	if strings.TrimSpace(name) == "" {
		writeError(w, http.StatusBadRequest, "container name or id is required")
		return
	}

	timeoutPtr := parseTimeout(r)

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := h.dockerMonitor.StopContainer(ctx, name, timeoutPtr); err != nil {
		if client.IsErrNotFound(err) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("container %q not found", name))
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to stop container %q: %v", name, err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"container": name,
		"action":    "stop",
		"status":    "stopped",
	})
}

// HandleContainerRestart restarts a container.
func (h *Handler) HandleContainerRestart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		name = r.PathValue("id")
	}
	if strings.TrimSpace(name) == "" {
		writeError(w, http.StatusBadRequest, "container name or id is required")
		return
	}

	timeoutPtr := parseTimeout(r)

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	if err := h.dockerMonitor.RestartContainer(ctx, name, timeoutPtr); err != nil {
		if client.IsErrNotFound(err) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("container %q not found", name))
			return
		}
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to restart container %q: %v", name, err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"container": name,
		"action":    "restart",
		"status":    "restarted",
	})
}

// HandleContainerMetricsHistory returns time-series metrics history stored in SQLite for a container.
func (h *Handler) HandleContainerMetricsHistory(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		name = r.PathValue("id")
	}
	if strings.TrimSpace(name) == "" {
		writeError(w, http.StatusBadRequest, "container name or id is required")
		return
	}

	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not initialized")
		return
	}

	limit := 50
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		if parsed, err := strconv.Atoi(rawLimit); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	var sincePtr *time.Time
	if rawSince := r.URL.Query().Get("since"); rawSince != "" {
		if d, err := time.ParseDuration(rawSince); err == nil {
			t := time.Now().UTC().Add(-d)
			sincePtr = &t
		} else if parsedTime, err := time.Parse(time.RFC3339, rawSince); err == nil {
			sincePtr = &parsedTime
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	records, err := h.db.GetContainerMetricsHistory(ctx, name, limit, sincePtr)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query metrics history: "+err.Error())
		return
	}

	if records == nil {
		records = []model.ContainerMetricRecord{}
	}

	writeJSON(w, http.StatusOK, records)
}

// --- Docker Compose Stack Management Handlers ---

// HandleListProjects returns all registered Docker Compose stacks.
func (h *Handler) HandleListProjects(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	projects, err := h.db.ListProjects(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list projects: "+err.Error())
		return
	}

	if projects == nil {
		projects = []model.Project{}
	}

	writeJSON(w, http.StatusOK, projects)
}

// HandleListActivePorts returns all host ports currently allocated across active stacks.
func (h *Handler) HandleListActivePorts(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	ports, err := h.db.ListActivePorts(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query active ports: "+err.Error())
		return
	}

	if ports == nil {
		ports = []model.ProjectPort{}
	}

	writeJSON(w, http.StatusOK, ports)
}

// HandleGetProject returns detailed information for a specific project.
func (h *Handler) HandleGetProject(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}

	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	p, err := h.db.GetProject(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("project %q not found", name))
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get project: "+err.Error())
		return
	}

	var composeContent string
	var files []string
	var hasDataDir bool

	if h.composeEngine != nil {
		composeContent, _ = h.composeEngine.ReadComposeFile(name)
		files, hasDataDir, _ = h.composeEngine.ListProjectFiles(name)
	}

	ports, _ := h.db.GetProjectPorts(ctx, name)
	if ports == nil {
		ports = []model.ProjectPort{}
	}

	detail := model.ProjectDetail{
		Project:        *p,
		ComposeContent: composeContent,
		Files:          files,
		HasDataDir:     hasDataDir,
		Ports:          ports,
	}

	writeJSON(w, http.StatusOK, detail)
}

// HandleCreateProject validates the payload via `docker compose config` pre-flight check,
// provisions the directory structure, writes compose.yml, and begins asynchronous deployment.
func (h *Handler) HandleCreateProject(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name        string `json:"name"`
		Compose     string `json:"compose"`
		Description string `json:"description"`
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	// Try JSON parsing
	if err := json.Unmarshal(bodyBytes, &payload); err != nil || payload.Name == "" {
		// Fallback: check query parameter ?name=<name> with raw compose body
		qName := strings.TrimSpace(r.URL.Query().Get("name"))
		if qName != "" {
			payload.Name = qName
			payload.Compose = string(bodyBytes)
		} else {
			writeError(w, http.StatusBadRequest, "invalid request: must provide JSON with name and compose fields, or ?name= query with raw body")
			return
		}
	}

	payload.Name = strings.TrimSpace(payload.Name)
	payload.Compose = strings.TrimSpace(payload.Compose)

	if payload.Name == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}
	if payload.Compose == "" {
		writeError(w, http.StatusBadRequest, "compose content is required")
		return
	}

	sanitizedContainerName := compose.SanitizeContainerName(payload.Name)
	if sanitizedContainerName == "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid project name %q: cannot produce a valid container name", payload.Name))
		return
	}

	// Apply container_name to compose YAML using the sanitized name value passed by Skoll
	updatedCompose, err := compose.ApplyContainerName(payload.Compose, sanitizedContainerName)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to apply container_name to compose: "+err.Error())
		return
	}
	payload.Compose = updatedCompose

	if !compose.IsValidProjectName(payload.Name) {
		if payload.Description == "" {
			payload.Description = payload.Name
		}
		payload.Name = sanitizedContainerName
	}

	if h.composeEngine == nil {
		writeError(w, http.StatusServiceUnavailable, "compose execution engine is not initialized")
		return
	}

	valCtx, valCancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer valCancel()

	// 1. Pre-flight syntax and schema validation
	valOutput, err := h.composeEngine.ValidateCompose(valCtx, payload.Compose)
	if err != nil {
		writeErrorWithData(w, http.StatusUnprocessableEntity, "pre-flight compose validation failed: "+err.Error(), map[string]any{
			"details": valOutput,
		})
		return
	}

	// 1b. Parse host port bindings and validate against active stacks
	ports, err := compose.ParseComposePorts(payload.Compose)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid port configuration in compose: "+err.Error())
		return
	}

	if h.db != nil {
		conflicts, err := h.db.CheckPortConflicts(r.Context(), payload.Name, ports)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed checking port conflicts: "+err.Error())
			return
		}
		if len(conflicts) > 0 {
			first := conflicts[0]
			writeErrorWithData(w, http.StatusBadRequest, fmt.Sprintf("port conflict: host port %d/%s is already allocated to project %q", first.HostPort, first.Protocol, first.AllocatedTo), map[string]any{
				"conflict":  first,
				"conflicts": conflicts,
			})
			return
		}
	}

	// 2. Provision directory structure (/var/lib/fenrir/stacks/<name>/ and data/) and write compose.yml
	provCtx, provCancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer provCancel()

	projectDir, err := h.composeEngine.ProvisionStack(provCtx, payload.Name, payload.Compose)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to provision stack files: "+err.Error())
		return
	}

	// 3. Register or update project record in SQLite
	p := &model.Project{
		Name:          payload.Name,
		DirectoryPath: projectDir,
		ComposeFile:   "compose.yml",
		Description:   payload.Description,
		Status:        "deploying",
	}

	if h.db != nil {
		if err := h.db.CreateProject(r.Context(), p); err != nil {
			// If already registered, update existing project
			_ = h.db.UpdateProject(r.Context(), p)
		}
		if err := h.db.AllocateProjectPorts(r.Context(), payload.Name, ports); err != nil {
			log.Printf("[WARN] Failed to record allocated ports for %q: %v", payload.Name, err)
		}
	}

	// 4. Trigger asynchronous deployment in background
	go func(projName string) {
		deployCtx, deployCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer deployCancel()

		_, err := h.composeEngine.Up(deployCtx, projName)
		if h.db != nil {
			if err != nil {
				_ = h.db.UpdateProjectStatus(context.Background(), projName, "failed")
			} else {
				_ = h.db.UpdateProjectStatus(context.Background(), projName, "running")
			}
		}
	}(payload.Name)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"project":       payload.Name,
		"directoryPath": projectDir,
		"status":        "deploying",
		"message":       "compose configuration validated successfully; deployment initiated asynchronously",
	})
}

// HandleGetProjectCompose retrieves the raw compose.yml content for a project.
func (h *Handler) HandleGetProjectCompose(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}

	if h.composeEngine == nil {
		writeError(w, http.StatusServiceUnavailable, "compose engine not configured")
		return
	}

	content, err := h.composeEngine.ReadComposeFile(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "failed to read compose file: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"project": name,
		"compose": content,
	})
}

// HandleUpdateProjectCompose updates the compose.yml file after pre-flight validation and redeploys.
func (h *Handler) HandleUpdateProjectCompose(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	composeContent := string(bodyBytes)
	var jsonPayload struct {
		Name    string `json:"name"`
		Compose string `json:"compose"`
	}
	if err := json.Unmarshal(bodyBytes, &jsonPayload); err == nil && jsonPayload.Compose != "" {
		composeContent = jsonPayload.Compose
	}

	composeContent = strings.TrimSpace(composeContent)
	if composeContent == "" {
		writeError(w, http.StatusBadRequest, "compose content cannot be empty")
		return
	}

	targetName := name
	if jsonPayload.Name != "" {
		targetName = jsonPayload.Name
	}
	sanitizedContainerName := compose.SanitizeContainerName(targetName)
	if sanitizedContainerName != "" {
		if updated, err := compose.ApplyContainerName(composeContent, sanitizedContainerName); err == nil {
			composeContent = updated
		}
	}

	if h.composeEngine == nil {
		writeError(w, http.StatusServiceUnavailable, "compose engine not configured")
		return
	}

	// Pre-flight validation
	valCtx, valCancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer valCancel()

	valOutput, err := h.composeEngine.ValidateCompose(valCtx, composeContent)
	if err != nil {
		writeErrorWithData(w, http.StatusUnprocessableEntity, "pre-flight compose validation failed: "+err.Error(), map[string]any{
			"details": valOutput,
		})
		return
	}

	// Parse host port bindings and validate against active stacks
	ports, err := compose.ParseComposePorts(composeContent)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid port configuration in compose: "+err.Error())
		return
	}

	if h.db != nil {
		conflicts, err := h.db.CheckPortConflicts(r.Context(), name, ports)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed checking port conflicts: "+err.Error())
			return
		}
		if len(conflicts) > 0 {
			first := conflicts[0]
			writeErrorWithData(w, http.StatusBadRequest, fmt.Sprintf("port conflict: host port %d/%s is already allocated to project %q", first.HostPort, first.Protocol, first.AllocatedTo), map[string]any{
				"conflict":  first,
				"conflicts": conflicts,
			})
			return
		}
	}

	// Write updated compose.yml
	provCtx, provCancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer provCancel()

	_, err = h.composeEngine.ProvisionStack(provCtx, name, composeContent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write compose file: "+err.Error())
		return
	}

	if h.db != nil {
		_ = h.db.UpdateProjectStatus(r.Context(), name, "deploying")
		_ = h.db.AllocateProjectPorts(r.Context(), name, ports)
	}

	// Trigger async redeployment
	go func(projName string) {
		deployCtx, deployCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer deployCancel()

		_, err := h.composeEngine.Up(deployCtx, projName)
		if h.db != nil {
			if err != nil {
				_ = h.db.UpdateProjectStatus(context.Background(), projName, "failed")
			} else {
				_ = h.db.UpdateProjectStatus(context.Background(), projName, "running")
			}
		}
	}(name)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"project": name,
		"status":  "deploying",
		"message": "compose file updated and deployment initiated asynchronously",
	})
}

// HandleProjectUp runs `docker compose up -d` for a project stack.
func (h *Handler) HandleProjectUp(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}

	if h.composeEngine == nil {
		writeError(w, http.StatusServiceUnavailable, "compose engine not configured")
		return
	}

	// Validate that the project's ports do not collide with another active stack
	var allocatedPorts []model.ProjectPort
	if h.db != nil {
		composeContent, err := h.composeEngine.ReadComposeFile(name)
		if err == nil {
			ports, err := compose.ParseComposePorts(composeContent)
			if err == nil && len(ports) > 0 {
				conflicts, err := h.db.CheckPortConflicts(r.Context(), name, ports)
				if err != nil {
					writeError(w, http.StatusInternalServerError, "failed checking port conflicts: "+err.Error())
					return
				}
				if len(conflicts) > 0 {
					first := conflicts[0]
					writeErrorWithData(w, http.StatusBadRequest, fmt.Sprintf("port conflict: host port %d/%s is already allocated to project %q", first.HostPort, first.Protocol, first.AllocatedTo), map[string]any{
						"conflict":  first,
						"conflicts": conflicts,
					})
					return
				}
				allocatedPorts = ports
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	output, err := h.composeEngine.Up(ctx, name)
	if err != nil {
		if h.db != nil {
			_ = h.db.UpdateProjectStatus(r.Context(), name, "failed")
		}
		writeError(w, http.StatusInternalServerError, "compose up failed: "+err.Error())
		return
	}

	if h.db != nil {
		_ = h.db.UpdateProjectStatus(r.Context(), name, "running")
		if len(allocatedPorts) > 0 {
			_ = h.db.AllocateProjectPorts(r.Context(), name, allocatedPorts)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project": name,
		"status":  "running",
		"output":  output,
	})
}

// HandleProjectDown stops and removes stack containers via `docker compose down`.
func (h *Handler) HandleProjectDown(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}

	removeVolumes, _ := strconv.ParseBool(r.URL.Query().Get("remove_volumes"))

	if h.composeEngine == nil {
		writeError(w, http.StatusServiceUnavailable, "compose engine not configured")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	output, err := h.composeEngine.Down(ctx, name, removeVolumes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "compose down failed: "+err.Error())
		return
	}

	if h.db != nil {
		_ = h.db.UpdateProjectStatus(r.Context(), name, "stopped")
		_ = h.db.ReleaseProjectPorts(r.Context(), name)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project": name,
		"status":  "stopped",
		"output":  output,
	})
}

// HandleProjectRestart runs `docker compose restart`.
func (h *Handler) HandleProjectRestart(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}

	if h.composeEngine == nil {
		writeError(w, http.StatusServiceUnavailable, "compose engine not configured")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	output, err := h.composeEngine.Restart(ctx, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "compose restart failed: "+err.Error())
		return
	}

	if h.db != nil {
		_ = h.db.UpdateProjectStatus(r.Context(), name, "running")
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project": name,
		"status":  "running",
		"output":  output,
	})
}

// HandleProjectStatus runs `docker compose ps --format json`.
func (h *Handler) HandleProjectStatus(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}

	if h.composeEngine == nil {
		writeError(w, http.StatusServiceUnavailable, "compose engine not configured")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	services, output, err := h.composeEngine.Status(ctx, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "compose status check failed: "+err.Error())
		return
	}

	currentStatus := "stopped"
	for _, s := range services {
		if strings.EqualFold(s.State, "running") {
			currentStatus = "running"
			break
		}
	}

	if h.db != nil {
		_ = h.db.UpdateProjectStatus(r.Context(), name, currentStatus)
	}

	writeJSON(w, http.StatusOK, model.ProjectStatusResponse{
		Project:  name,
		Status:   currentStatus,
		Services: services,
		Output:   output,
	})
}

// HandleProjectLogs fetches logs for a project stack.
func (h *Handler) HandleProjectLogs(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}

	tail := 100
	if rawTail := r.URL.Query().Get("tail"); rawTail != "" {
		if parsed, err := strconv.Atoi(rawTail); err == nil && parsed > 0 {
			tail = parsed
		}
	}
	service := strings.TrimSpace(r.URL.Query().Get("service"))

	if h.composeEngine == nil {
		writeError(w, http.StatusServiceUnavailable, "compose engine not configured")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	logs, err := h.composeEngine.Logs(ctx, name, tail, service)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch compose logs: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project": name,
		"logs":    logs,
	})
}

// HandleDeleteProject gracefully brings down the stack, generates a timestamped .zip archive
// with a 90-day retention into /var/lib/fenrir/archives/, safely removes project files, and clears the DB record.
func (h *Handler) HandleDeleteProject(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

	projectDir, err := h.composeEngine.ProjectPath(name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 1. Bring down stack gracefully and release allocated ports
	_, _ = h.composeEngine.Down(ctx, name, false)
	if h.db != nil {
		_ = h.db.ReleaseProjectPorts(ctx, name)
	}

	// 2. Safeguard: bundle the project directory into a timestamped .zip archive before teardown
	var archiveMeta *model.ArchiveMetadata
	if _, err := os.Stat(projectDir); err == nil {
		archivesDir := "/var/lib/fenrir/archives/"
		retentionDays := 90
		if h.cfg != nil {
			if h.cfg.ArchivesDir != "" {
				archivesDir = h.cfg.ArchivesDir
			}
			if h.cfg.ArchiveRetentionDays > 0 {
				retentionDays = h.cfg.ArchiveRetentionDays
			}
		}

		archiveMeta, err = archive.CreateZipArchive(ctx, name, projectDir, archivesDir, retentionDays)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "safe teardown failed: could not create backup archive: "+err.Error())
			return
		}

		if h.db != nil {
			_ = h.db.CreateArchive(ctx, archiveMeta)
		}
	}

	// 3. Remove project files from disk safely
	_ = os.RemoveAll(projectDir)

	// 4. Remove project record from database
	if h.db != nil {
		_ = h.db.DeleteProject(ctx, name)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message": "project torn down and archived successfully",
		"project": name,
		"archive": archiveMeta,
	})
}

// --- Rolling Archive Handlers ---

// HandleListArchives lists all backup archives with a countdown showing days until deletion.
func (h *Handler) HandleListArchives(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not initialized")
		return
	}

	projectName := strings.TrimSpace(r.URL.Query().Get("project"))

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	archives, err := h.db.ListArchives(ctx, projectName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query archives: "+err.Error())
		return
	}

	if archives == nil {
		archives = []model.ArchiveMetadata{}
	}

	writeJSON(w, http.StatusOK, archives)
}

// HandleGetProjectArchives lists backup archives specifically for a given project name.
func (h *Handler) HandleGetProjectArchives(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, "project name is required")
		return
	}

	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	archives, err := h.db.ListArchives(ctx, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query project archives: "+err.Error())
		return
	}

	if archives == nil {
		archives = []model.ArchiveMetadata{}
	}

	writeJSON(w, http.StatusOK, archives)
}

// HandleRestoreArchive triggers manual restoration of a backup archive into the project stack directory.
func (h *Handler) HandleRestoreArchive(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimSpace(r.PathValue("id"))
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "valid archive id is required")
		return
	}

	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	archiveMeta, err := h.db.GetArchive(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("archive %d not found", id))
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get archive metadata: "+err.Error())
		return
	}

	targetDir, err := h.composeEngine.ProjectPath(archiveMeta.ProjectName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid project directory path: "+err.Error())
		return
	}

	// Extract zip file back into project directory and enforce container permissions
	uid := 1000
	gid := 1000
	if h.cfg != nil {
		if h.cfg.DefaultUID > 0 {
			uid = h.cfg.DefaultUID
		}
		if h.cfg.DefaultGID > 0 {
			gid = h.cfg.DefaultGID
		}
	}

	if err := archive.RestoreZipArchive(ctx, archiveMeta.ArchivePath, targetDir, uid, gid); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to restore archive: "+err.Error())
		return
	}

	// Ensure project is registered in SQLite
	p := &model.Project{
		Name:          archiveMeta.ProjectName,
		DirectoryPath: targetDir,
		ComposeFile:   "compose.yml",
		Status:        "stopped",
	}
	_ = h.db.CreateProject(ctx, p)
	_ = h.db.UpdateProjectStatus(ctx, archiveMeta.ProjectName, "stopped")

	writeJSON(w, http.StatusOK, map[string]any{
		"message":       "archive restored successfully",
		"project":       archiveMeta.ProjectName,
		"directoryPath": targetDir,
		"archiveId":     id,
	})
}

// HandleDeleteArchive deletes an archive zip file and its database record.
func (h *Handler) HandleDeleteArchive(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimSpace(r.PathValue("id"))
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "valid archive id is required")
		return
	}

	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	archiveMeta, err := h.db.GetArchive(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("archive %d not found", id))
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to query archive: "+err.Error())
		return
	}

	// Delete file from disk
	_ = os.Remove(archiveMeta.ArchivePath)

	// Delete from database
	if err := h.db.DeleteArchive(ctx, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete archive record: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":   "archive deleted successfully",
		"archiveId": id,
	})
}

// --- Settings Handlers ---

// HandleGetSettings returns all application settings stored in SQLite.
func (h *Handler) HandleGetSettings(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	settings, err := h.db.ListSettingsDetails(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load settings: "+err.Error())
		return
	}

	if settings == nil {
		settings = []model.AppSetting{}
	}

	writeJSON(w, http.StatusOK, settings)
}

// HandleGetSetting returns the value of a specific setting key.
func (h *Handler) HandleGetSetting(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.PathValue("key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "setting key is required")
		return
	}

	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	val, err := h.db.GetSetting(ctx, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("setting %q not found", key))
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get setting: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"key":   key,
		"value": val,
	})
}

// HandleSetSetting creates or updates a setting key in SQLite.
func (h *Handler) HandleSetSetting(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.PathValue("key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "setting key is required")
		return
	}

	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not initialized")
		return
	}

	var payload struct {
		Value string `json:"value"`
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		payload.Value = string(body)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := h.db.SetSetting(ctx, key, payload.Value); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save setting: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"key":   key,
		"value": payload.Value,
	})
}

// HandleDeleteSetting deletes a setting key from SQLite.
func (h *Handler) HandleDeleteSetting(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.PathValue("key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "setting key is required")
		return
	}

	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := h.db.DeleteSetting(ctx, key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("setting %q not found", key))
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete setting: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "setting deleted",
		"key":     key,
	})
}

// --- API Key Handlers ---

// HandleListAPIKeys lists metadata for all generated API keys.
func (h *Handler) HandleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	keys, err := h.db.ListAPIKeys(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list api keys: "+err.Error())
		return
	}

	if keys == nil {
		keys = []model.APIKeyMetadata{}
	}

	writeJSON(w, http.StatusOK, keys)
}

// HandleCreateAPIKey generates and stores a new API key.
func (h *Handler) HandleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not initialized")
		return
	}

	var req struct {
		Name string `json:"name"`
		Key  string `json:"key,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if req.Name == "" {
		req.Name = "default"
	}

	rawKey := req.Key
	if rawKey == "" {
		var err error
		rawKey, err = database.GenerateRandomKey("fn")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to generate key: "+err.Error())
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	meta, err := h.db.CreateAPIKey(ctx, req.Name, rawKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store api key: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":        meta.ID,
		"name":      meta.Name,
		"prefix":    meta.Prefix,
		"key":       rawKey,
		"createdAt": meta.CreatedAt,
	})
}

// HandleRevokeAPIKey revokes an API key by prefix or ID.
func (h *Handler) HandleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	prefixOrID := strings.TrimSpace(r.PathValue("prefix"))
	if prefixOrID == "" {
		writeError(w, http.StatusBadRequest, "prefix or id is required")
		return
	}

	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not initialized")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := h.db.RevokeAPIKey(ctx, prefixOrID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "api key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to revoke api key: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"message":    "api key revoked",
		"identifier": prefixOrID,
	})
}

func parseTimeout(r *http.Request) *int {
	if raw := r.URL.Query().Get("timeout"); raw != "" {
		if val, err := strconv.Atoi(raw); err == nil && val >= 0 {
			return &val
		}
	}
	if r.Body != nil && r.Header.Get("Content-Type") == "application/json" {
		var body struct {
			Timeout *int `json:"timeout"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.Timeout != nil && *body.Timeout >= 0 {
			return body.Timeout
		}
	}
	return nil
}
