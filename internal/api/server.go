package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

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
func NewServer(cfg *config.Config, dm *docker.Monitor, hc *metrics.HostCollector, db *database.DB, version string) *Server {
	h := NewHandler(dm, hc, db, version)
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

	// Apply middleware stack: Recovery -> CORS -> Logging -> Mux
	var rootHandler http.Handler = mux
	rootHandler = CORSMiddleware(cfg.AllowedOrigins)(rootHandler)
	rootHandler = LoggingMiddleware(rootHandler)
	rootHandler = RecoveryMiddleware(rootHandler)

	srv := &http.Server{
		Addr:         cfg.ServerPort,
		Handler:      rootHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 45 * time.Second, // Allow sufficient time for container graceful stop/restart
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
