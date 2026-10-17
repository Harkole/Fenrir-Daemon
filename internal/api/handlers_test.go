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

	// 2. Verify compose.yml was committed with container_name overridden
	savedCompose, err := os.ReadFile(filepath.Join(projectDir, "compose.yml"))
	if err != nil {
		t.Fatalf("failed to read compose.yml: %v", err)
	}
	if !strings.Contains(string(savedCompose), "container_name: atm10") {
		t.Errorf("saved compose missing container_name atm10: got %q", string(savedCompose))
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

func TestCreateProject_ContainerNameOverrideAndSanitization(t *testing.T) {
	server, _, _, cfg := setupTestServer(t, "test-key")

	// Compose file with existing placeholder container_name
	composeContent := `services:
  minecraft:
    image: itzg/minecraft-server:latest
    container_name: old-placeholder-name
    ports:
      - "25566:25565"
`

	// Skoll passes a name with spaces and special characters
	payload, _ := json.Marshal(map[string]string{
		"name":        "ATM 10 - Server! [Hardcore]",
		"compose":     composeContent,
		"description": "Modded Minecraft Server",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d: %s", w.Code, w.Body.String())
	}

	var resp APIResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	respMap := resp.Data.(map[string]any)

	// Sanitized name should be ATM10-ServerHardcore
	expectedName := "ATM10-ServerHardcore"
	if respMap["project"] != expectedName {
		t.Errorf("expected project name %q, got %q", expectedName, respMap["project"])
	}

	projectDir := filepath.Join(cfg.StacksDir, expectedName)
	savedCompose, err := os.ReadFile(filepath.Join(projectDir, "compose.yml"))
	if err != nil {
		t.Fatalf("failed to read compose.yml: %v", err)
	}

	if !strings.Contains(string(savedCompose), "container_name: ATM10-ServerHardcore") {
		t.Errorf("expected container_name: ATM10-ServerHardcore in compose.yml, got:\n%s", string(savedCompose))
	}
	if strings.Contains(string(savedCompose), "old-placeholder-name") {
		t.Errorf("expected old-placeholder-name to be replaced, got:\n%s", string(savedCompose))
	}
}

func TestUpdateProjectCompose_ContainerNameOverride(t *testing.T) {
	server, _, ce, cfg := setupTestServer(t, "test-key")

	// Provision initial stack
	initialCompose := `services:
  minecraft:
    image: itzg/minecraft-server:latest
    container_name: mc-server
`
	_, err := ce.ProvisionStack(context.Background(), "mc-server", initialCompose)
	if err != nil {
		t.Fatalf("failed to provision stack: %v", err)
	}

	// Update compose with new template lacking container_name or having different one
	updatedContent := `services:
  minecraft:
    image: itzg/minecraft-server:nightly
    container_name: another-name
`
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/projects/mc-server/compose", strings.NewReader(updatedContent))
	updateReq.Header.Set("X-API-Key", "test-key")
	updateW := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(updateW, updateReq)

	if updateW.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted on update compose, got %d: %s", updateW.Code, updateW.Body.String())
	}

	savedCompose, err := os.ReadFile(filepath.Join(cfg.StacksDir, "mc-server", "compose.yml"))
	if err != nil {
		t.Fatalf("failed to read updated compose.yml: %v", err)
	}

	if !strings.Contains(string(savedCompose), "container_name: mc-server") {
		t.Errorf("expected container_name: mc-server in updated compose.yml, got:\n%s", string(savedCompose))
	}
	if strings.Contains(string(savedCompose), "another-name") {
		t.Errorf("expected another-name to be replaced, got:\n%s", string(savedCompose))
	}
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

func TestUpdateProjectCompose_JSONPayloadAndDescription(t *testing.T) {
	server, db, ce, cfg := setupTestServer(t, "test-key")

	// 1. Provision initial stack and DB record
	initialCompose := `services:
  valheim:
    image: lloesche/valheim-server:latest
`
	_, err := ce.ProvisionStack(context.Background(), "valheim", initialCompose)
	if err != nil {
		t.Fatalf("failed to provision stack: %v", err)
	}
	p := &model.Project{
		Name:          "valheim",
		DirectoryPath: filepath.Join(cfg.StacksDir, "valheim"),
		ComposeFile:   "compose.yml",
		Description:   "Initial description",
		Status:        "running",
	}
	_ = db.CreateProject(context.Background(), p)

	// 2. Update with JSON payload including updated description and redeploy=false
	updatePayload := map[string]any{
		"compose": `services:
  valheim:
    image: lloesche/valheim-server:v2
    ports:
      - "2456:2456/udp"
`,
		"description": "Updated Valheim Dedicated Server",
		"redeploy":    false,
	}
	payloadBytes, _ := json.Marshal(updatePayload)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/projects/valheim/compose", bytes.NewReader(payloadBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK with redeploy=false, got %d: %s", w.Code, w.Body.String())
	}

	// 3. Verify file on disk is updated with container_name
	saved, err := os.ReadFile(filepath.Join(cfg.StacksDir, "valheim", "compose.yml"))
	if err != nil {
		t.Fatalf("failed to read updated compose: %v", err)
	}
	if !strings.Contains(string(saved), "lloesche/valheim-server:v2") {
		t.Errorf("expected updated image in compose.yml, got:\n%s", string(saved))
	}
	if !strings.Contains(string(saved), "container_name: valheim") {
		t.Errorf("expected container_name: valheim, got:\n%s", string(saved))
	}

	// 4. Verify DB description updated
	updatedProj, err := db.GetProject(context.Background(), "valheim")
	if err != nil {
		t.Fatalf("failed to get project from DB: %v", err)
	}
	if updatedProj.Description != "Updated Valheim Dedicated Server" {
		t.Errorf("expected updated description, got %q", updatedProj.Description)
	}
	if updatedProj.Status != "updated" {
		t.Errorf("expected status 'updated', got %q", updatedProj.Status)
	}

	// 5. Verify ports allocated in DB
	ports, _ := db.GetProjectPorts(context.Background(), "valheim")
	if len(ports) != 1 || ports[0].HostPort != 2456 || ports[0].Protocol != "udp" {
		t.Errorf("expected port 2456/udp allocated, got %v", ports)
	}
}

func TestUpdateProject_PutProjectRoute(t *testing.T) {
	server, _, ce, cfg := setupTestServer(t, "test-key")

	_, err := ce.ProvisionStack(context.Background(), "web-app", "services:\n  nginx:\n    image: nginx:alpine\n")
	if err != nil {
		t.Fatalf("failed to provision: %v", err)
	}

	// PUT /api/v1/projects/web-app
	newYAML := "services:\n  nginx:\n    image: nginx:latest\n"
	body, _ := json.Marshal(map[string]any{
		"yaml":     newYAML,
		"redeploy": false,
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/projects/web-app", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on PUT /api/v1/projects/web-app, got %d: %s", w.Code, w.Body.String())
	}

	saved, _ := os.ReadFile(filepath.Join(cfg.StacksDir, "web-app", "compose.yml"))
	if !strings.Contains(string(saved), "nginx:latest") {
		t.Errorf("expected updated image in compose.yml, got:\n%s", string(saved))
	}
}

func TestUpdateProjectCompose_DryRun(t *testing.T) {
	server, _, ce, cfg := setupTestServer(t, "test-key")

	initialYAML := "services:\n  redis:\n    image: redis:alpine\n"
	_, err := ce.ProvisionStack(context.Background(), "cache", initialYAML)
	if err != nil {
		t.Fatalf("failed to provision: %v", err)
	}

	dryRunYAML := "services:\n  redis:\n    image: redis:7-alpine\n    ports:\n      - \"6379:6379\"\n"
	body, _ := json.Marshal(map[string]any{
		"compose": dryRunYAML,
		"dryRun":  true,
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/projects/cache/compose", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on dry run, got %d: %s", w.Code, w.Body.String())
	}

	var resp APIResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	respMap, ok := resp.Data.(map[string]any)
	if !ok || respMap["valid"] != true {
		t.Fatalf("expected valid: true, got %v", resp.Data)
	}

	// Verify file on disk was NOT modified during dry run
	saved, _ := os.ReadFile(filepath.Join(cfg.StacksDir, "cache", "compose.yml"))
	if strings.Contains(string(saved), "redis:7-alpine") {
		t.Errorf("file should not have been overwritten during dry run!")
	}
}

func TestUpdateProjectCompose_InvalidYAML(t *testing.T) {
	server, _, ce, _ := setupTestServer(t, "test-key")

	_, _ = ce.ProvisionStack(context.Background(), "test-syntax", "services:\n  app:\n    image: app\n")

	invalidYAML := "services:\n  invalid: [broken: {yaml"
	req := httptest.NewRequest(http.MethodPut, "/api/v1/projects/test-syntax/compose", strings.NewReader(invalidYAML))
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 Unprocessable Entity, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProjectYAMLAndFilesEndpoints(t *testing.T) {
	server, _, ce, _ := setupTestServer(t, "test-key")

	// 1. Provision stack
	_, err := ce.ProvisionStack(context.Background(), "my-stack", "services:\n  web:\n    image: nginx\n")
	if err != nil {
		t.Fatalf("failed to provision: %v", err)
	}

	// 2. Add extra YAML file (config.yaml) and non-YAML file (notes.txt)
	_ = ce.WriteProjectFile("my-stack", "config.yaml", "env: production\nworkers: 4\n")
	_ = ce.WriteProjectFile("my-stack", "notes.txt", "some important notes")

	// 3. GET /api/v1/projects/my-stack/yaml should list compose.yml and config.yaml
	yamlListReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects/my-stack/yaml", nil)
	yamlListW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(yamlListW, yamlListReq)

	if yamlListW.Code != http.StatusOK {
		t.Fatalf("expected 200 on list yaml, got %d: %s", yamlListW.Code, yamlListW.Body.String())
	}

	var yamlListResp APIResponse
	_ = json.NewDecoder(yamlListW.Body).Decode(&yamlListResp)
	yamlListData := yamlListResp.Data.(map[string]any)
	filesList := yamlListData["files"].([]any)
	if len(filesList) != 2 {
		t.Errorf("expected 2 YAML files, got %d: %v", len(filesList), filesList)
	}

	// 4. GET /api/v1/projects/my-stack/yaml/config.yaml should read content
	getYAMLReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects/my-stack/yaml/config.yaml", nil)
	getYAMLW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(getYAMLW, getYAMLReq)

	if getYAMLW.Code != http.StatusOK {
		t.Fatalf("expected 200 on get yaml file, got %d: %s", getYAMLW.Code, getYAMLW.Body.String())
	}

	var getYAMLResp APIResponse
	_ = json.NewDecoder(getYAMLW.Body).Decode(&getYAMLResp)
	getYAMLData := getYAMLResp.Data.(map[string]any)
	if !strings.Contains(getYAMLData["content"].(string), "workers: 4") {
		t.Errorf("expected workers: 4 in content, got %v", getYAMLData["content"])
	}

	// 5. PUT /api/v1/projects/my-stack/yaml/config.yaml should update content
	updatedYAMLContent := "env: production\nworkers: 8\n"
	putYAMLReq := httptest.NewRequest(http.MethodPut, "/api/v1/projects/my-stack/yaml/config.yaml", strings.NewReader(updatedYAMLContent))
	putYAMLReq.Header.Set("X-API-Key", "test-key")
	putYAMLW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(putYAMLW, putYAMLReq)

	if putYAMLW.Code != http.StatusOK {
		t.Fatalf("expected 200 on put yaml file, got %d: %s", putYAMLW.Code, putYAMLW.Body.String())
	}

	readBack, _ := ce.ReadProjectFile("my-stack", "config.yaml")
	if !strings.Contains(readBack, "workers: 8") {
		t.Errorf("expected workers: 8 in updated file, got:\n%s", readBack)
	}

	// 6. PUT /api/v1/projects/my-stack/yaml/config.yaml with invalid YAML syntax should fail with 422
	badYAMLReq := httptest.NewRequest(http.MethodPut, "/api/v1/projects/my-stack/yaml/config.yaml", strings.NewReader("bad: [broken"))
	badYAMLReq.Header.Set("X-API-Key", "test-key")
	badYAMLW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(badYAMLW, badYAMLReq)

	if badYAMLW.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on bad yaml syntax, got %d: %s", badYAMLW.Code, badYAMLW.Body.String())
	}

	// 7. Path traversal attempts should be rejected with 400 Bad Request
	traversalReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects/my-stack/yaml/../secret.yaml", nil)
	traversalW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(traversalW, traversalReq)

	if traversalW.Code != http.StatusBadRequest && traversalW.Code != http.StatusNotFound && traversalW.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 400, 404, or 307 on path traversal, got %d: %s", traversalW.Code, traversalW.Body.String())
	}

	// 8. GET /api/v1/projects/my-stack/files should list all files with metadata
	filesReq := httptest.NewRequest(http.MethodGet, "/api/v1/projects/my-stack/files", nil)
	filesW := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(filesW, filesReq)

	if filesW.Code != http.StatusOK {
		t.Fatalf("expected 200 on get files list, got %d: %s", filesW.Code, filesW.Body.String())
	}

	var filesResp APIResponse
	_ = json.NewDecoder(filesW.Body).Decode(&filesResp)
	filesData := filesResp.Data.(map[string]any)
	itemsList := filesData["files"].([]any)
	if len(itemsList) < 3 {
		t.Errorf("expected at least 3 files (compose.yml, config.yaml, notes.txt), got %d", len(itemsList))
	}
}


