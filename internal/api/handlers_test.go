package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/config"
	"github.com/Harkole/fenrir-daemon/internal/database"
	"github.com/Harkole/fenrir-daemon/internal/docker"
	"github.com/Harkole/fenrir-daemon/internal/metrics"
	"github.com/Harkole/fenrir-daemon/internal/model"
)

func setupTestServer(t *testing.T, apiKey string) (*Server, *database.DB) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_api.db")

	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	dm, err := docker.NewMonitor("", []string{"minecraft"})
	if err != nil {
		t.Fatalf("failed to create docker monitor: %v", err)
	}
	t.Cleanup(func() {
		_ = dm.Close()
	})

	hc := metrics.NewHostCollector(1 * time.Second)
	cfg := &config.Config{
		ServerPort:                ":8080",
		AllowedOrigins:            []string{"*"},
		DatabasePath:              dbPath,
		APIKey:                    apiKey,
		MetricsCollectionInterval: 30 * time.Second,
	}

	server := NewServer(cfg, dm, hc, db, "0.2.0-test")
	return server, db
}

func TestHealthEndpoint(t *testing.T) {
	server, _ := setupTestServer(t, "")

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

	if dataMap["version"] != "0.2.0-test" {
		t.Errorf("expected version 0.2.0-test, got %v", dataMap["version"])
	}

	// Verify database status in health response
	dbInfo, ok := dataMap["database"].(map[string]any)
	if !ok {
		t.Fatalf("expected database status in health response, got %v", dataMap["database"])
	}
	if dbInfo["connected"] != true {
		t.Errorf("expected database connected: true, got %v", dbInfo["connected"])
	}
}

func TestHostMetricsEndpoint(t *testing.T) {
	server, _ := setupTestServer(t, "")

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

func TestAuthMiddleware_Unauthorized(t *testing.T) {
	server, _ := setupTestServer(t, "master-secret-key")

	// 1. Missing API Key on protected control route
	reqMissing := httptest.NewRequest(http.MethodPost, "/api/v1/containers/test-server/start", nil)
	wMissing := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(wMissing, reqMissing)

	if wMissing.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing key, got %d", wMissing.Code)
	}

	var respMissing APIResponse
	_ = json.NewDecoder(wMissing.Body).Decode(&respMissing)
	if respMissing.Success {
		t.Errorf("expected success=false for unauthenticated request")
	}

	// 2. Invalid API Key
	reqInvalid := httptest.NewRequest(http.MethodPost, "/api/v1/containers/test-server/start", nil)
	reqInvalid.Header.Set("X-API-Key", "wrong-key")
	wInvalid := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(wInvalid, reqInvalid)

	if wInvalid.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid key, got %d", wInvalid.Code)
	}
}

func TestAuthMiddleware_AuthorizedWithConfigKey(t *testing.T) {
	server, _ := setupTestServer(t, "master-secret-key")

	// Test with X-API-Key header
	req := httptest.NewRequest(http.MethodPost, "/api/v1/containers/nonexistent-container/start", nil)
	req.Header.Set("X-API-Key", "master-secret-key")
	w := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(w, req)

	// Passed auth; since container doesn't exist in local docker daemon, it should return 404 or 500, not 401
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("request was unexpectedly rejected with 401 Unauthorized")
	}
	if w.Code != http.StatusNotFound && w.Code != http.StatusInternalServerError {
		t.Logf("lifecycle returned status %d as expected without 401", w.Code)
	}

	// Test with Authorization: Bearer <key>
	reqBearer := httptest.NewRequest(http.MethodPost, "/api/v1/containers/nonexistent-container/stop", nil)
	reqBearer.Header.Set("Authorization", "Bearer master-secret-key")
	wBearer := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(wBearer, reqBearer)

	if wBearer.Code == http.StatusUnauthorized {
		t.Fatalf("bearer request was unexpectedly rejected with 401 Unauthorized")
	}
}

func TestAuthMiddleware_AuthorizedWithDatabaseKey(t *testing.T) {
	server, db := setupTestServer(t, "") // No master key in config

	dbKey, err := database.GenerateRandomKey("fn_test")
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	if _, err := db.CreateAPIKey(context.Background(), "Integration Test Key", dbKey); err != nil {
		t.Fatalf("failed to create db api key: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/containers/nonexistent-container/restart", nil)
	req.Header.Set("X-API-Key", dbKey)
	w := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Fatalf("database key was rejected with 401 Unauthorized")
	}
}

func TestContainerMetricsHistoryEndpoint(t *testing.T) {
	server, db := setupTestServer(t, "")

	// Insert historical metrics
	stats := &model.ContainerResourceStats{
		CPUPercent:    12.4,
		MemoryUsage:   1024 * 1024 * 300,
		MemoryLimit:   1024 * 1024 * 1024,
		MemoryPercent: 29.3,
	}
	err := db.RecordContainerMetric(context.Background(), "c12345", "atm10-minecraft", stats, time.Now().UTC())
	if err != nil {
		t.Fatalf("failed to record metric: %v", err)
	}

	// Query metrics history via GET /api/v1/containers/{name}/metrics
	req := httptest.NewRequest(http.MethodGet, "/api/v1/containers/atm10-minecraft/metrics?limit=10", nil)
	w := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Fatalf("expected success: true")
	}

	dataList, ok := resp.Data.([]any)
	if !ok {
		t.Fatalf("expected slice of metrics, got %T", resp.Data)
	}
	if len(dataList) != 1 {
		t.Fatalf("expected 1 metric record, got %d", len(dataList))
	}
}

func TestSettingsEndpoints(t *testing.T) {
	apiKey := "settings-admin-key"
	server, _ := setupTestServer(t, apiKey)

	// 1. Set setting with PUT /api/v1/settings/{key}
	payload := []byte(`{"value": "16G"}`)
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/settings/minecraft_memory", bytes.NewReader(payload))
	putReq.Header.Set("Content-Type", "application/json")
	putReq.Header.Set("X-API-Key", apiKey)
	putW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(putW, putReq)

	if putW.Code != http.StatusOK {
		t.Fatalf("expected 200 on PUT setting, got %d: %s", putW.Code, putW.Body.String())
	}

	// 2. Get setting with GET /api/v1/settings/{key}
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/settings/minecraft_memory", nil)
	getW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Fatalf("expected 200 on GET setting, got %d", getW.Code)
	}

	var getResp APIResponse
	_ = json.NewDecoder(getW.Body).Decode(&getResp)
	dataMap := getResp.Data.(map[string]any)
	if dataMap["value"] != "16G" {
		t.Errorf("expected setting value '16G', got %v", dataMap["value"])
	}

	// 3. Delete setting with DELETE /api/v1/settings/{key}
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/settings/minecraft_memory", nil)
	delReq.Header.Set("X-API-Key", apiKey)
	delW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(delW, delReq)

	if delW.Code != http.StatusOK {
		t.Fatalf("expected 200 on DELETE setting, got %d", delW.Code)
	}

	// 4. Verify gone
	getAgainReq := httptest.NewRequest(http.MethodGet, "/api/v1/settings/minecraft_memory", nil)
	getAgainW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(getAgainW, getAgainReq)
	if getAgainW.Code != http.StatusNotFound {
		t.Errorf("expected 404 after deletion, got %d", getAgainW.Code)
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
