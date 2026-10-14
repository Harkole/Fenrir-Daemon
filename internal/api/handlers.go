package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	startTime     time.Time
	version       string
}

// NewHandler creates a new Handler with injected dependencies.
func NewHandler(dm *docker.Monitor, hc *metrics.HostCollector, db *database.DB, version string) *Handler {
	return &Handler{
		dockerMonitor: dm,
		hostCollector: hc,
		db:            db,
		startTime:     time.Now().UTC(),
		version:       version,
	}
}

// HealthResponse represents system health information.
type HealthResponse struct {
	Status        string        `json:"status"` // "ok", "degraded"
	Version       string        `json:"version"`
	UptimeSeconds float64       `json:"uptimeSeconds"`
	Docker        DockerStatus  `json:"docker"`
	Database      DBStatus      `json:"database"`
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
