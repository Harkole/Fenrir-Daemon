package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/config"
	"github.com/Harkole/fenrir-daemon/internal/docker"
	"github.com/Harkole/fenrir-daemon/internal/metrics"
	"github.com/Harkole/fenrir-daemon/internal/model"
)

func TestHealthEndpoint(t *testing.T) {
	dm, err := docker.NewMonitor("", []string{"minecraft"})
	if err != nil {
		t.Fatalf("failed to create docker monitor: %v", err)
	}
	defer dm.Close()

	hc := metrics.NewHostCollector(1 * time.Second)
	cfg := &config.Config{
		ServerPort:     ":8080",
		AllowedOrigins: []string{"*"},
	}

	server := NewServer(cfg, dm, hc, "0.1.0-test")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode json response: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected success: true, got %v", resp.Success)
	}

	dataMap, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data map, got %T", resp.Data)
	}

	if dataMap["version"] != "0.1.0-test" {
		t.Errorf("expected version 0.1.0-test, got %v", dataMap["version"])
	}
}

func TestHostMetricsEndpoint(t *testing.T) {
	dm, err := docker.NewMonitor("", []string{"minecraft"})
	if err != nil {
		t.Fatalf("failed to create docker monitor: %v", err)
	}
	defer dm.Close()

	hc := metrics.NewHostCollector(1 * time.Second)
	cfg := &config.Config{
		ServerPort:     ":8080",
		AllowedOrigins: []string{"*"},
	}

	server := NewServer(cfg, dm, hc, "0.1.0-test")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/host", nil)
	w := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected success: true, got %v", resp.Success)
	}
}

func TestContainersJSONSerialization(t *testing.T) {
	summary := model.ContainerSummary{
		ID:    "abc123456789",
		Names: []string{"mc-survival"},
		Image: "itzg/minecraft-server:latest",
		State: "running",
		Fenrir: model.FenrirMetadata{
			Enabled:     true,
			Type:        "game",
			DisplayName: "Minecraft - Survival SMP",
		},
	}

	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("failed to marshal summary: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	fenrirRaw, ok := parsed["fenrir"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'fenrir' object in JSON, got %v", parsed["fenrir"])
	}

	if fenrirRaw["enabled"] != true {
		t.Errorf("expected fenrir.enabled true, got %v", fenrirRaw["enabled"])
	}
	if fenrirRaw["type"] != "game" {
		t.Errorf("expected fenrir.type 'game', got %v", fenrirRaw["type"])
	}
	if fenrirRaw["displayName"] != "Minecraft - Survival SMP" {
		t.Errorf("expected fenrir.displayName 'Minecraft - Survival SMP', got %v", fenrirRaw["displayName"])
	}
}
