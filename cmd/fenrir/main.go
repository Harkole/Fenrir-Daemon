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
	"github.com/Harkole/fenrir-daemon/internal/database"
	"github.com/Harkole/fenrir-daemon/internal/docker"
	"github.com/Harkole/fenrir-daemon/internal/metrics"
)

const version = "0.2.0"

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
	log.Printf("[CONFIG] Database Path: %s", cfg.DatabasePath)
	if cfg.APIKey != "" {
		log.Printf("[CONFIG] API Key Auth: ENABLED (master key configured)")
	} else {
		log.Printf("[CONFIG] API Key Auth: DATABASE-ONLY (or unconfigured)")
	}

	// 2. Initialize SQLite persistence layer
	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("[FATAL] Failed to initialize SQLite database: %v", err)
	}
	defer db.Close()
	log.Printf("[INFO] SQLite database initialized successfully at %s", cfg.DatabasePath)

	// 3. Initialize host hardware metrics collector
	hostCollector := metrics.NewHostCollector(cfg.MetricsCacheTTL)

	// 4. Initialize Docker client monitor
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

	// 5. Initialize HTTP API Server
	server := api.NewServer(cfg, dockerMonitor, hostCollector, db, version)

	// 6. Background container metrics history collection
	stopCollector := make(chan struct{})
	if cfg.MetricsCollectionInterval > 0 {
		log.Printf("[INFO] Starting background container metrics collection (interval: %v)", cfg.MetricsCollectionInterval)
		go func() {
			ticker := time.NewTicker(cfg.MetricsCollectionInterval)
			defer ticker.Stop()

			for {
				select {
				case <-stopCollector:
					return
				case <-ticker.C:
					ctxCollect, cancelCollect := context.WithTimeout(context.Background(), 15*time.Second)
					containers, err := dockerMonitor.ListContainers(ctxCollect, false)
					if err == nil {
						for _, c := range containers {
							if c.State == "running" {
								statsCtx, statsCancel := context.WithTimeout(ctxCollect, 3*time.Second)
								stats, err := dockerMonitor.GetContainerStats(statsCtx, c.ID)
								statsCancel()
								if err == nil {
									name := ""
									if len(c.Names) > 0 {
										name = c.Names[0]
									}
									_ = db.RecordContainerMetric(ctxCollect, c.ID, name, stats, time.Now().UTC())
								}
							}
						}
					}
					cancelCollect()
				}
			}
		}()
	}

	// 7. Channel to catch OS shutdown signals
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

	close(stopCollector)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("[ERROR] Error during server shutdown: %v", err)
	} else {
		log.Printf("[INFO] HTTP server gracefully terminated.")
	}

	log.Printf("[INFO] Fenrir Daemon stopped.")
}
