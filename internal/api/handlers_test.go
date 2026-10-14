package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/compose"
	"github.com/Harkole/fenrir-daemon/internal/config"
	"github.com/Harkole/fenrir-daemon/internal/database"
	"github.com/Harkole/fenrir-daemon/internal/docker"
	"github.com/Harkole/fenrir-daemon/internal/metrics"
	"github.com/Harkole/fenrir-daemon/internal/model"
)

func setupTestServer(t *testing.T, apiKey string) (*Server, *database.DB, *compose.Engine, *config.Config) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_api.db")
	stacksDir := filepath.Join(tempDir, "stacks")
	archivesDir := filepath.Join(tempDir, "archives")

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

	ce := compose.NewEngine(stacksDir, "", 1000, 1000)

	hc := metrics.NewHostCollector(1 * time.Second)
	cfg := &config.Config{
		ServerPort:                ":8080",
		AllowedOrigins:            []string{"*"},
		DatabasePath:              dbPath,
		APIKey:                    apiKey,
		MetricsCollectionInterval: 30 * time.Second,
		StacksDir:                 stacksDir,
		ArchivesDir:               archivesDir,
		ArchiveRetentionDays:      90,
		DefaultUID:                1000,
		DefaultGID:                1000,
	}

	server := NewServer(cfg, dm, hc, db, ce, "0.2.0-test")
	return server, db, ce, cfg
}

func TestHealthEndpoint(t *testing.T) {
	server, _, _, _ := setupTestServer(t, "")

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
	server, _, _, _ := setupTestServer(t, "")

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
	server, _, _, _ := setupTestServer(t, "master-secret-key")

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
	server, _, _, _ := setupTestServer(t, "master-secret-key")

	// Test with X-API-Key header
	req := httptest.NewRequest(http.MethodPost, "/api/v1/containers/nonexistent-container/start", nil)
	req.Header.Set("X-API-Key", "master-secret-key")
	w := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(w, req)

	// Passed auth; since container doesn't exist in local docker daemon, it should return 404 or 500, not 401
	if w.Code == http.StatusUnauthorized {
		t.Fatalf("request was unexpectedly rejected with 401 Unauthorized")
	}
}

func TestAuthMiddleware_AuthorizedWithDatabaseKey(t *testing.T) {
	server, db, _, _ := setupTestServer(t, "") // No master key in config

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

func TestCreateProject_PreflightValidationFailure(t *testing.T) {
	server, _, _, _ := setupTestServer(t, "test-key")

	// Test with empty compose payload
	payload := []byte(`{"name": "broken-stack", "compose": "   "}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(w, req)

	// Should be rejected without touching the filesystem
	if w.Code != http.StatusBadRequest && w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 400 or 422 for empty compose, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateProject_SuccessAndProvisioning(t *testing.T) {
	server, db, ce, cfg := setupTestServer(t, "test-key")

	composeContent := `services:
  atm10:
    image: itzg/minecraft-server:latest
    ports:
      - "25565:25565"
    volumes:
      - ./data:/data
`

	payload, _ := json.Marshal(map[string]string{
		"name":        "atm10",
		"compose":     composeContent,
		"description": "All The Mods 10 Minecraft Server",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted on project creation, got %d: %s", w.Code, w.Body.String())
	}

	// 1. Verify directory /stacks/atm10/ and data/ were created on disk
	projectDir := filepath.Join(cfg.StacksDir, "atm10")
	dataDir := filepath.Join(projectDir, "data")
	if stat, err := os.Stat(dataDir); err != nil || !stat.IsDir() {
		t.Fatalf("expected data directory to exist on disk: %v", err)
	}

	// 2. Verify compose.yml was committed
	savedCompose, err := os.ReadFile(filepath.Join(projectDir, "compose.yml"))
	if err != nil {
		t.Fatalf("failed to read compose.yml: %v", err)
	}
	if strings.TrimSpace(string(savedCompose)) != strings.TrimSpace(composeContent) {
		t.Errorf("saved compose mismatch: got %q, expected %q", string(savedCompose), composeContent)
	}

	// 3. Verify SQLite project record exists
	p, err := db.GetProject(context.Background(), "atm10")
	if err != nil {
		t.Fatalf("expected project in database: %v", err)
	}
	if p.Name != "atm10" || p.DirectoryPath != projectDir {
		t.Errorf("unexpected project record: %+v", p)
	}

	// 4. Test GET /api/v1/projects/{name}
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects/atm10", nil)
	getW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on GET project, got %d", getW.Code)
	}

	var getResp APIResponse
	_ = json.NewDecoder(getW.Body).Decode(&getResp)
	dataMap := getResp.Data.(map[string]any)
	if dataMap["hasDataDir"] != true {
		t.Errorf("expected hasDataDir=true")
	}

	_ = ce
}

func TestProjectTeardown_ArchiveAndCleanup(t *testing.T) {
	server, db, ce, cfg := setupTestServer(t, "test-key")

	// Provision a project directory on disk with data
	projectDir, err := ce.ProvisionStack(context.Background(), "atm10", "services:\n  minecraft:\n    image: test\n")
	if err != nil {
		t.Fatalf("failed to provision stack: %v", err)
	}

	// Add dummy save data into data/
	dataFile := filepath.Join(projectDir, "data", "world.txt")
	_ = os.WriteFile(dataFile, []byte("world-save-data"), 0644)

	// Register in DB
	p := &model.Project{
		Name:          "atm10",
		DirectoryPath: projectDir,
		ComposeFile:   "compose.yml",
		Status:        "running",
	}
	_ = db.CreateProject(context.Background(), p)

	// Trigger DELETE /api/v1/projects/atm10
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/atm10", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on delete, got %d: %s", w.Code, w.Body.String())
	}

	// 1. Verify project directory removed from stacks/
	if _, err := os.Stat(projectDir); !os.IsNotExist(err) {
		t.Errorf("expected project directory to be removed from stacks")
	}

	// 2. Verify project removed from SQLite
	_, err = db.GetProject(context.Background(), "atm10")
	if err == nil {
		t.Errorf("expected project to be removed from SQLite")
	}

	// 3. Verify .zip archive was created in archives/
	archives, err := db.ListArchives(context.Background(), "atm10")
	if err != nil {
		t.Fatalf("failed to list archives: %v", err)
	}
	if len(archives) != 1 {
		t.Fatalf("expected 1 archive created on teardown, got %d", len(archives))
	}

	archivePath := archives[0].ArchivePath
	if stat, err := os.Stat(archivePath); err != nil || stat.Size() == 0 {
		t.Errorf("archive zip file does not exist or is empty on disk: %v", err)
	}

	// Verify days until deletion countdown
	if archives[0].DaysUntilDeletion <= 0 || archives[0].DaysUntilDeletion > 90 {
		t.Errorf("expected countdown between 1 and 90 days, got %d", archives[0].DaysUntilDeletion)
	}

	_ = cfg
}

func TestArchiveListingAndRestore(t *testing.T) {
	server, db, ce, cfg := setupTestServer(t, "test-key")

	// Provision stack and dummy file
	projectDir, _ := ce.ProvisionStack(context.Background(), "restorable", "services:\n  web:\n    image: nginx\n")
	_ = os.WriteFile(filepath.Join(projectDir, "data", "app.log"), []byte("log entry"), 0644)

	// Manually archive it
	archiveMeta, err := db.GetArchive(context.Background(), 999)
	_ = err
	_ = archiveMeta

	// Trigger teardown
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/restorable", nil)
	delReq.Header.Set("X-API-Key", "test-key")
	delW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(delW, delReq)

	// List archives via GET /api/v1/archives
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/archives", nil)
	listW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(listW, listReq)

	if listW.Code != http.StatusOK {
		t.Fatalf("expected 200 on GET archives, got %d", listW.Code)
	}

	var listResp APIResponse
	_ = json.NewDecoder(listW.Body).Decode(&listResp)
	archivesList := listResp.Data.([]any)
	if len(archivesList) == 0 {
		t.Fatalf("expected at least 1 archive in list")
	}

	firstArch := archivesList[0].(map[string]any)
	archiveID := int64(firstArch["id"].(float64))

	// Verify countdown field is present in response
	if _, ok := firstArch["daysUntilDeletion"]; !ok {
		t.Errorf("expected 'daysUntilDeletion' countdown in archive listing")
	}

	// Restore via POST /api/v1/archives/{id}/restore
	restoreReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/archives/%d/restore", archiveID), nil)
	restoreReq.Header.Set("X-API-Key", "test-key")
	restoreW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(restoreW, restoreReq)

	if restoreW.Code != http.StatusOK {
		t.Fatalf("expected 200 on restore, got %d: %s", restoreW.Code, restoreW.Body.String())
	}

	// Verify files restored back to stacks/restorable/
	restoredLog := filepath.Join(cfg.StacksDir, "restorable", "data", "app.log")
	if _, err := os.Stat(restoredLog); err != nil {
		t.Fatalf("restored file does not exist: %v", err)
	}
}

func TestSettingsEndpoints(t *testing.T) {
	apiKey := "settings-admin-key"
	server, _, _, _ := setupTestServer(t, apiKey)

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
}

func TestPortCollisionValidationAndLifecycle(t *testing.T) {
	apiKey := "ports-test-key"
	server, _, engine, _ := setupTestServer(t, apiKey)

	composeAlpha := `services:
  mc:
    image: itzg/minecraft-server
    ports:
      - "25565:25565"
      - "19132:19132/udp"
`

	// 1. Create project "mc-alpha"
	createAlphaReq := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(fmt.Sprintf(`{
		"name": "mc-alpha",
		"compose": %q
	}`, composeAlpha)))
	createAlphaReq.Header.Set("Content-Type", "application/json")
	createAlphaReq.Header.Set("X-API-Key", apiKey)
	createAlphaW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(createAlphaW, createAlphaReq)

	if createAlphaW.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted on creating mc-alpha, got %d: %s", createAlphaW.Code, createAlphaW.Body.String())
	}

	// 2. Query GET /api/v1/ports to verify active port tracking
	portsReq := httptest.NewRequest(http.MethodGet, "/api/v1/ports", nil)
	portsW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(portsW, portsReq)

	if portsW.Code != http.StatusOK {
		t.Fatalf("expected 200 on GET /api/v1/ports, got %d", portsW.Code)
	}

	var portsResp APIResponse
	_ = json.NewDecoder(portsW.Body).Decode(&portsResp)
	portsList, ok := portsResp.Data.([]any)
	if !ok || len(portsList) != 2 {
		t.Fatalf("expected 2 active ports allocated, got %v", portsResp.Data)
	}

	// 3. Attempt to create project "mc-beta" colliding on host port 25565/tcp
	composeBeta := `services:
  mc2:
    image: itzg/minecraft-server
    ports:
      - "25565:25565"
`
	createBetaReq := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(fmt.Sprintf(`{
		"name": "mc-beta",
		"compose": %q
	}`, composeBeta)))
	createBetaReq.Header.Set("Content-Type", "application/json")
	createBetaReq.Header.Set("X-API-Key", apiKey)
	createBetaW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(createBetaW, createBetaReq)

	// Must be rejected immediately with HTTP 400 Bad Request
	if createBetaW.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request on port collision, got %d: %s", createBetaW.Code, createBetaW.Body.String())
	}

	var betaErrResp APIResponse
	_ = json.NewDecoder(createBetaW.Body).Decode(&betaErrResp)
	if !strings.Contains(betaErrResp.Error, "port conflict: host port 25565/tcp is already allocated to project \"mc-alpha\"") {
		t.Errorf("expected conflict message identifying 25565/tcp and mc-alpha, got: %s", betaErrResp.Error)
	}

	// Verify disk for mc-beta was NOT created (host remains untouched)
	betaDir, _ := engine.ProjectPath("mc-beta")
	if _, err := os.Stat(betaDir); !os.IsNotExist(err) {
		t.Fatalf("expected mc-beta directory not to exist, but stat returned err: %v", err)
	}

	// 4. Down the mc-alpha stack to release the port
	downReq := httptest.NewRequest(http.MethodPost, "/api/v1/projects/mc-alpha/down", nil)
	downReq.Header.Set("X-API-Key", apiKey)
	downW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(downW, downReq)

	if downW.Code != http.StatusOK {
		t.Fatalf("expected 200 on down, got %d: %s", downW.Code, downW.Body.String())
	}

	// 5. Verify ports list is now freed
	portsAfterDownReq := httptest.NewRequest(http.MethodGet, "/api/v1/ports", nil)
	portsAfterDownW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(portsAfterDownW, portsAfterDownReq)

	var portsAfterDownResp APIResponse
	_ = json.NewDecoder(portsAfterDownW.Body).Decode(&portsAfterDownResp)
	portsAfterDownList, _ := portsAfterDownResp.Data.([]any)
	if len(portsAfterDownList) != 0 {
		t.Fatalf("expected 0 active ports after down, got %d", len(portsAfterDownList))
	}

	// 6. Now creating mc-beta on 25565 succeeds because port was freed
	createBetaRetryReq := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(fmt.Sprintf(`{
		"name": "mc-beta",
		"compose": %q
	}`, composeBeta)))
	createBetaRetryReq.Header.Set("Content-Type", "application/json")
	createBetaRetryReq.Header.Set("X-API-Key", apiKey)
	createBetaRetryW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(createBetaRetryW, createBetaRetryReq)

	if createBetaRetryW.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted on retrying mc-beta after port release, got %d: %s", createBetaRetryW.Code, createBetaRetryW.Body.String())
	}
}

