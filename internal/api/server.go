package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/config"
	"github.com/Harkole/fenrir-daemon/internal/docker"
	"github.com/Harkole/fenrir-daemon/internal/metrics"
)

// Server encapsulates the HTTP server and routing.
type Server struct {
	httpServer *http.Server
	handler    *Handler
}

// NewServer builds and configures a new HTTP Server instance.
func NewServer(cfg *config.Config, dm *docker.Monitor, hc *metrics.HostCollector, version string) *Server {
	h := NewHandler(dm, hc, version)

	mux := http.NewServeMux()

	// Register API v1 endpoints (using Go 1.22+ method + path patterns)
	mux.HandleFunc("GET /health", h.HandleHealth)
	mux.HandleFunc("GET /api/v1/health", h.HandleHealth)
	mux.HandleFunc("GET /api/v1/host", h.HandleHostMetrics)
	mux.HandleFunc("GET /api/v1/containers", h.HandleContainers)
	mux.HandleFunc("GET /api/v1/containers/{id}", h.HandleContainerDetail)

	// Apply middleware stack: Recovery -> CORS -> Logging -> Mux
	var rootHandler http.Handler = mux
	rootHandler = CORSMiddleware(cfg.AllowedOrigins)(rootHandler)
	rootHandler = LoggingMiddleware(rootHandler)
	rootHandler = RecoveryMiddleware(rootHandler)

	srv := &http.Server{
		Addr:         cfg.ServerPort,
		Handler:      rootHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
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
