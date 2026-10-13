package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/api"
	"github.com/Harkole/fenrir-daemon/internal/config"
	"github.com/Harkole/fenrir-daemon/internal/docker"
	"github.com/Harkole/fenrir-daemon/internal/metrics"
)

const version = "0.1.0"

func main() {
	log.Printf("==================================================")
	log.Printf("  FENRIR DAEMON v%s - Game Server Monitor", version)
	log.Printf("==================================================")

	// 1. Load runtime configuration
	cfg := config.Load()
	log.Printf("[CONFIG] Server Port: %s", cfg.ServerPort)
	if cfg.DockerHost != "" {
		log.Printf("[CONFIG] Docker Host: %s", cfg.DockerHost)
	} else {
		log.Printf("[CONFIG] Docker Host: default environment")
	}
	log.Printf("[CONFIG] Game Keywords: %v", cfg.GameFilterKeywords)

	// 2. Initialize host hardware metrics collector
	hostCollector := metrics.NewHostCollector(cfg.MetricsCacheTTL)

	// 3. Initialize Docker client monitor
	dockerMonitor, err := docker.NewMonitor(cfg.DockerHost, cfg.GameFilterKeywords)
	if err != nil {
		log.Fatalf("[FATAL] Failed to configure Docker monitor: %v", err)
	}
	defer dockerMonitor.Close()

	// Initial ping check (non-fatal, daemon stays operational if Docker is starting/remote)
	ctxPing, cancelPing := context.WithTimeout(context.Background(), 3*time.Second)
	if err := dockerMonitor.Ping(ctxPing); err != nil {
		log.Printf("[WARN] Docker daemon connectivity check failed: %v", err)
		log.Printf("[WARN] The daemon will continue running, but container endpoints may return errors until Docker is online.")
	} else {
		log.Printf("[INFO] Successfully connected to Docker daemon.")
	}
	cancelPing()

	// 4. Initialize HTTP API Server
	server := api.NewServer(cfg, dockerMonitor, hostCollector, version)

	// 5. Channel to catch OS shutdown signals
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	// Run server in background goroutine
	go func() {
		log.Printf("[INFO] HTTP API listening on %s", cfg.ServerPort)
		if err := server.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[FATAL] HTTP server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	sig := <-stopChan
	log.Printf("[INFO] Received signal %v; initiating graceful shutdown...", sig)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("[ERROR] Error during server shutdown: %v", err)
	} else {
		log.Printf("[INFO] HTTP server gracefully terminated.")
	}

	log.Printf("[INFO] Fenrir Daemon stopped.")
}
