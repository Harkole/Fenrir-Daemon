package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/compose"
	"github.com/Harkole/fenrir-daemon/internal/config"
	"github.com/Harkole/fenrir-daemon/internal/database"
	"github.com/Harkole/fenrir-daemon/internal/docker"
	"github.com/Harkole/fenrir-daemon/internal/metrics"
)

// Server encapsulates the HTTP server, routing, and middleware.
type Server struct {
	httpServer *http.Server
	handler    *Handler
}

// NewServer builds and configures a new HTTP Server instance.
func NewServer(cfg *config.Config, dm *docker.Monitor, hc *metrics.HostCollector, db *database.DB, ce *compose.Engine, version string) *Server {
	h := NewHandler(dm, hc, db, ce, cfg, version)
	auth := APIKeyAuthMiddleware(cfg, db)

	mux := http.NewServeMux()

	// 1. Health and host metrics endpoints
	mux.HandleFunc("GET /health", h.HandleHealth)
	mux.HandleFunc("GET /api/v1/health", h.HandleHealth)
	mux.HandleFunc("GET /api/v1/host", h.HandleHostMetrics)

	// 2. Container inspection and metrics history
	mux.HandleFunc("GET /api/v1/containers", h.HandleContainers)
	mux.HandleFunc("GET /api/v1/containers/{id}", h.HandleContainerDetail)
	mux.HandleFunc("GET /api/v1/containers/{name}/metrics", h.HandleContainerMetricsHistory)

	// 3. Application settings endpoints
	mux.HandleFunc("GET /api/v1/settings", h.HandleGetSettings)
	mux.HandleFunc("GET /api/v1/settings/{key}", h.HandleGetSetting)
	mux.Handle("PUT /api/v1/settings/{key}", auth(http.HandlerFunc(h.HandleSetSetting)))
	mux.Handle("POST /api/v1/settings/{key}", auth(http.HandlerFunc(h.HandleSetSetting)))
	mux.Handle("DELETE /api/v1/settings/{key}", auth(http.HandlerFunc(h.HandleDeleteSetting)))

	// 4. API Key administration endpoints
	mux.Handle("GET /api/v1/auth/keys", auth(http.HandlerFunc(h.HandleListAPIKeys)))
	mux.Handle("POST /api/v1/auth/keys", auth(http.HandlerFunc(h.HandleCreateAPIKey)))
	mux.Handle("DELETE /api/v1/auth/keys/{prefix}", auth(http.HandlerFunc(h.HandleRevokeAPIKey)))

	// 5. Secure POST container lifecycle management endpoints
	mux.Handle("POST /api/v1/containers/{name}/start", auth(http.HandlerFunc(h.HandleContainerStart)))
	mux.Handle("POST /api/v1/containers/{name}/stop", auth(http.HandlerFunc(h.HandleContainerStop)))
	mux.Handle("POST /api/v1/containers/{name}/restart", auth(http.HandlerFunc(h.HandleContainerRestart)))

	// 6. Docker Compose Stack Management endpoints
	mux.HandleFunc("GET /api/v1/projects", h.HandleListProjects)
	mux.HandleFunc("GET /api/v1/ports", h.HandleListActivePorts)
	mux.Handle("POST /api/v1/projects", auth(http.HandlerFunc(h.HandleCreateProject)))
	mux.HandleFunc("GET /api/v1/projects/{name}", h.HandleGetProject)
	mux.Handle("PUT /api/v1/projects/{name}", auth(http.HandlerFunc(h.HandleUpdateProject)))
	mux.Handle("PATCH /api/v1/projects/{name}", auth(http.HandlerFunc(h.HandleUpdateProject)))
	mux.HandleFunc("GET /api/v1/projects/{name}/compose", h.HandleGetProjectCompose)
	mux.Handle("PUT /api/v1/projects/{name}/compose", auth(http.HandlerFunc(h.HandleUpdateProjectCompose)))
	mux.Handle("PATCH /api/v1/projects/{name}/compose", auth(http.HandlerFunc(h.HandleUpdateProjectCompose)))
	mux.Handle("POST /api/v1/projects/{name}/compose", auth(http.HandlerFunc(h.HandleUpdateProjectCompose)))

	// Multi-YAML file management and editing endpoints
	mux.HandleFunc("GET /api/v1/projects/{name}/yaml", h.HandleListProjectYAML)
	mux.HandleFunc("GET /api/v1/projects/{name}/yaml/{filename...}", h.HandleGetProjectYAMLFile)
	mux.Handle("PUT /api/v1/projects/{name}/yaml/{filename...}", auth(http.HandlerFunc(h.HandleUpdateProjectYAMLFile)))
	mux.Handle("PATCH /api/v1/projects/{name}/yaml/{filename...}", auth(http.HandlerFunc(h.HandleUpdateProjectYAMLFile)))
	mux.Handle("POST /api/v1/projects/{name}/yaml/{filename...}", auth(http.HandlerFunc(h.HandleUpdateProjectYAMLFile)))

	// Project directory file inspection and editing endpoints
	mux.HandleFunc("GET /api/v1/projects/{name}/files", h.HandleListProjectFiles)
	mux.HandleFunc("GET /api/v1/projects/{name}/files/{filename...}", h.HandleGetProjectFile)
	mux.Handle("PUT /api/v1/projects/{name}/files/{filename...}", auth(http.HandlerFunc(h.HandleUpdateProjectFile)))
	mux.Handle("PATCH /api/v1/projects/{name}/files/{filename...}", auth(http.HandlerFunc(h.HandleUpdateProjectFile)))

	mux.Handle("POST /api/v1/projects/{name}/up", auth(http.HandlerFunc(h.HandleProjectUp)))
	mux.Handle("POST /api/v1/projects/{name}/down", auth(http.HandlerFunc(h.HandleProjectDown)))
	mux.Handle("POST /api/v1/projects/{name}/restart", auth(http.HandlerFunc(h.HandleProjectRestart)))
	mux.HandleFunc("GET /api/v1/projects/{name}/status", h.HandleProjectStatus)
	mux.HandleFunc("GET /api/v1/projects/{name}/logs", h.HandleProjectLogs)
	mux.Handle("DELETE /api/v1/projects/{name}", auth(http.HandlerFunc(h.HandleDeleteProject)))

	// 7. 90-Day Rolling Archive & Restore endpoints
	mux.HandleFunc("GET /api/v1/archives", h.HandleListArchives)
	mux.HandleFunc("GET /api/v1/projects/{name}/archives", h.HandleGetProjectArchives)
	mux.Handle("POST /api/v1/archives/{id}/restore", auth(http.HandlerFunc(h.HandleRestoreArchive)))
	mux.Handle("DELETE /api/v1/archives/{id}", auth(http.HandlerFunc(h.HandleDeleteArchive)))

	// Apply middleware stack: Recovery -> CORS -> Logging -> Mux
	var rootHandler http.Handler = mux
	rootHandler = CORSMiddleware(cfg.AllowedOrigins)(rootHandler)
	rootHandler = LoggingMiddleware(rootHandler)
	rootHandler = RecoveryMiddleware(rootHandler)

	srv := &http.Server{
		Addr:         cfg.ServerPort,
		Handler:      rootHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 45 * time.Second, // Allow sufficient time for container/stack operations
		IdleTimeout:  60 * time.Second,
	}

	return &Server{
		httpServer: srv,
		handler:    h,
	}
}

// Start begins listening on the configured HTTP port.
func (s *Server) Start() error {
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server failure: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
