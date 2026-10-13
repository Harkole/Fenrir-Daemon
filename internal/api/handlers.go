package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/docker"
	"github.com/Harkole/fenrir-daemon/internal/metrics"
	"github.com/Harkole/fenrir-daemon/internal/model"
)

// Handler holds dependencies for all API endpoints.
type Handler struct {
	dockerMonitor *docker.Monitor
	hostCollector *metrics.HostCollector
	startTime     time.Time
	version       string
}

// NewHandler creates a new Handler with injected dependencies.
func NewHandler(dm *docker.Monitor, hc *metrics.HostCollector, version string) *Handler {
	return &Handler{
		dockerMonitor: dm,
		hostCollector: hc,
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
}

// DockerStatus indicates connection state to the Docker daemon.
type DockerStatus struct {
	Connected bool    `json:"connected"`
	PingMs    float64 `json:"pingMs,omitempty"`
	Error     string  `json:"error,omitempty"`
}

// HandleHealth checks daemon health and Docker connectivity.
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

	status := "ok"
	if !dockerStatus.Connected {
		status = "degraded"
	}

	writeJSON(w, http.StatusOK, HealthResponse{
		Status:        status,
		Version:       h.version,
		UptimeSeconds: time.Since(h.startTime).Seconds(),
		Docker:        dockerStatus,
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
		writeError(w, http.StatusBadRequest, "container id is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	detail, err := h.dockerMonitor.GetContainer(ctx, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "container not found or inspect failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, detail)
}
