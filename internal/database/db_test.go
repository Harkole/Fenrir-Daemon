package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/model"
)

func setupTestDB(t *testing.T) *DB {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_fenrir.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

func TestDatabaseInitAndPing(t *testing.T) {
	db := setupTestDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Ping(ctx); err != nil {
		t.Fatalf("database ping failed: %v", err)
	}
}

func TestContainerMetricsHistory(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC()

	stats1 := &model.ContainerResourceStats{
		CPUPercent:      15.5,
		MemoryUsage:     1024 * 1024 * 512, // 512MB
		MemoryLimit:     1024 * 1024 * 2048,
		MemoryPercent:   25.0,
		NetworkRxBytes:  12345,
		NetworkTxBytes:  67890,
		BlockReadBytes:  1000,
		BlockWriteBytes: 2000,
	}

	stats2 := &model.ContainerResourceStats{
		CPUPercent:      22.1,
		MemoryUsage:     1024 * 1024 * 600,
		MemoryLimit:     1024 * 1024 * 2048,
		MemoryPercent:   29.3,
		NetworkRxBytes:  23456,
		NetworkTxBytes:  78901,
		BlockReadBytes:  1500,
		BlockWriteBytes: 2500,
	}

	containerID := "a1b2c3d4e5f6789012345678"
	containerName := "atm10-minecraft"

	if err := db.RecordContainerMetric(ctx, containerID, containerName, stats1, now.Add(-5*time.Minute)); err != nil {
		t.Fatalf("failed to record metric 1: %v", err)
	}

	if err := db.RecordContainerMetric(ctx, containerID, containerName, stats2, now); err != nil {
		t.Fatalf("failed to record metric 2: %v", err)
	}

	// Query by container name
	historyByName, err := db.GetContainerMetricsHistory(ctx, containerName, 10, nil)
	if err != nil {
		t.Fatalf("failed to get history by name: %v", err)
	}
	if len(historyByName) != 2 {
		t.Fatalf("expected 2 records, got %d", len(historyByName))
	}
	// Verify ordered by recorded_at DESC
	if historyByName[0].CPUPercent != 22.1 {
		t.Errorf("expected newest record first (CPU 22.1), got %f", historyByName[0].CPUPercent)
	}

	// Query by container ID prefix
	historyByIDPrefix, err := db.GetContainerMetricsHistory(ctx, "a1b2c3d4", 10, nil)
	if err != nil {
		t.Fatalf("failed to get history by ID prefix: %v", err)
	}
	if len(historyByIDPrefix) != 2 {
		t.Fatalf("expected 2 records, got %d", len(historyByIDPrefix))
	}

	// Query latest metric
	latest, err := db.GetLatestContainerMetric(ctx, containerName)
	if err != nil {
		t.Fatalf("failed to get latest metric: %v", err)
	}
	if latest.CPUPercent != 22.1 {
		t.Errorf("expected latest CPU 22.1, got %f", latest.CPUPercent)
	}

	// Test Prune
	pruned, err := db.PruneMetrics(ctx, 2*time.Minute)
	if err != nil {
		t.Fatalf("failed to prune metrics: %v", err)
	}
	if pruned != 1 {
		t.Errorf("expected 1 record pruned, got %d", pruned)
	}

	historyAfterPrune, err := db.GetContainerMetricsHistory(ctx, containerName, 10, nil)
	if err != nil {
		t.Fatalf("failed to get history after prune: %v", err)
	}
	if len(historyAfterPrune) != 1 {
		t.Fatalf("expected 1 record remaining, got %d", len(historyAfterPrune))
	}
}

func TestAppSettings(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// 1. Initial get should return sql.ErrNoRows
	_, err := db.GetSetting(ctx, "minecraft_backup_enabled")
	if err == nil {
		t.Fatalf("expected error for non-existent setting, got nil")
	}

	// 2. Set setting
	if err := db.SetSetting(ctx, "minecraft_backup_enabled", "true"); err != nil {
		t.Fatalf("failed to set setting: %v", err)
	}

	// 3. Get setting
	val, err := db.GetSetting(ctx, "minecraft_backup_enabled")
	if err != nil {
		t.Fatalf("failed to get setting: %v", err)
	}
	if val != "true" {
		t.Errorf("expected 'true', got %q", val)
	}

	// 4. Update setting (upsert)
	if err := db.SetSetting(ctx, "minecraft_backup_enabled", "false"); err != nil {
		t.Fatalf("failed to update setting: %v", err)
	}
	val, err = db.GetSetting(ctx, "minecraft_backup_enabled")
	if err != nil {
		t.Fatalf("failed to get updated setting: %v", err)
	}
	if val != "false" {
		t.Errorf("expected 'false', got %q", val)
	}

	// 5. Add another setting and test GetAllSettings
	if err := db.SetSetting(ctx, "max_memory_alloc", "12G"); err != nil {
		t.Fatalf("failed to set second setting: %v", err)
	}

	all, err := db.GetAllSettings(ctx)
	if err != nil {
		t.Fatalf("failed to get all settings: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 settings, got %d", len(all))
	}
	if all["max_memory_alloc"] != "12G" {
		t.Errorf("expected 12G, got %q", all["max_memory_alloc"])
	}

	// 6. Delete setting
	if err := db.DeleteSetting(ctx, "minecraft_backup_enabled"); err != nil {
		t.Fatalf("failed to delete setting: %v", err)
	}
	_, err = db.GetSetting(ctx, "minecraft_backup_enabled")
	if err == nil {
		t.Fatalf("expected error after deletion, got nil")
	}
}

func TestAPIKeyValidation(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	rawKey, err := GenerateRandomKey("fn_admin")
	if err != nil {
		t.Fatalf("failed to generate random key: %v", err)
	}

	meta, err := db.CreateAPIKey(ctx, "Admin CLI Key", rawKey)
	if err != nil {
		t.Fatalf("failed to create api key: %v", err)
	}
	if meta.ID == 0 {
		t.Errorf("expected non-zero ID")
	}

	// Validate correct key
	valid, err := db.ValidateAPIKey(ctx, rawKey)
	if err != nil {
		t.Fatalf("validation error: %v", err)
	}
	if !valid {
		t.Errorf("expected key to be valid")
	}

	// Validate incorrect key
	invalid, err := db.ValidateAPIKey(ctx, "fn_admin_invalid_key_value")
	if err != nil {
		t.Fatalf("validation error: %v", err)
	}
	if invalid {
		t.Errorf("expected invalid key to return false")
	}

	// Validate empty key
	emptyValid, err := db.ValidateAPIKey(ctx, "")
	if err != nil {
		t.Fatalf("validation error: %v", err)
	}
	if emptyValid {
		t.Errorf("expected empty key to return false")
	}

	// List keys
	keys, err := db.ListAPIKeys(ctx)
	if err != nil {
		t.Fatalf("failed to list keys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	if keys[0].Revoked {
		t.Errorf("expected key to not be revoked")
	}

	// Revoke key
	if err := db.RevokeAPIKey(ctx, meta.Prefix); err != nil {
		t.Fatalf("failed to revoke key: %v", err)
	}

	// Validate revoked key should now fail
	validAfterRevoke, err := db.ValidateAPIKey(ctx, rawKey)
	if err != nil {
		t.Fatalf("validation error after revoke: %v", err)
	}
	if validAfterRevoke {
		t.Errorf("expected revoked key to be invalid")
	}
}

func TestProjectsCRUD(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	p := &model.Project{
		Name:          "atm10",
		DirectoryPath: "/var/lib/fenrir/stacks/atm10",
		ComposeFile:   "compose.yml",
		Description:   "ATM10 Minecraft Server",
		Status:        "created",
	}

	// 1. Create project
	if err := db.CreateProject(ctx, p); err != nil {
		t.Fatalf("failed to create project: %v", err)
	}
	if p.ID == 0 {
		t.Errorf("expected non-zero ID")
	}

	// 2. Get project
	fetched, err := db.GetProject(ctx, "atm10")
	if err != nil {
		t.Fatalf("failed to get project: %v", err)
	}
	if fetched.Name != "atm10" || fetched.DirectoryPath != "/var/lib/fenrir/stacks/atm10" {
		t.Errorf("unexpected project fetched: %+v", fetched)
	}

	// 3. Update status
	if err := db.UpdateProjectStatus(ctx, "atm10", "running"); err != nil {
		t.Fatalf("failed to update project status: %v", err)
	}
	fetched, _ = db.GetProject(ctx, "atm10")
	if fetched.Status != "running" {
		t.Errorf("expected status 'running', got %q", fetched.Status)
	}

	// 4. List projects
	list, err := db.ListProjects(ctx)
	if err != nil {
		t.Fatalf("failed to list projects: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 project in list, got %d", len(list))
	}

	// 5. Delete project
	if err := db.DeleteProject(ctx, "atm10"); err != nil {
		t.Fatalf("failed to delete project: %v", err)
	}
	_, err = db.GetProject(ctx, "atm10")
	if err == nil {
		t.Fatalf("expected error after project deletion, got nil")
	}
}

func TestArchivesCRUD(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	now := time.Now().UTC()
	archive := &model.ArchiveMetadata{
		ProjectName: "atm10",
		ArchivePath: "/var/lib/fenrir/archives/atm10-20261014.zip",
		SizeBytes:   1024 * 1024 * 50, // 50MB
		CreatedAt:   now,
		ExpiresAt:   now.Add(90 * 24 * time.Hour),
	}

	// 1. Create archive
	if err := db.CreateArchive(ctx, archive); err != nil {
		t.Fatalf("failed to create archive: %v", err)
	}
	if archive.ID == 0 {
		t.Errorf("expected non-zero ID")
	}
	if archive.DaysUntilDeletion <= 0 {
		t.Errorf("expected positive days until deletion, got %d", archive.DaysUntilDeletion)
	}

	// 2. Get archive
	fetched, err := db.GetArchive(ctx, archive.ID)
	if err != nil {
		t.Fatalf("failed to get archive: %v", err)
	}
	if fetched.ProjectName != "atm10" || fetched.SizeBytes != 1024*1024*50 {
		t.Errorf("unexpected archive fetched: %+v", fetched)
	}

	// 3. List archives
	list, err := db.ListArchives(ctx, "")
	if err != nil {
		t.Fatalf("failed to list all archives: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 archive in list, got %d", len(list))
	}

	projList, err := db.ListArchives(ctx, "atm10")
	if err != nil {
		t.Fatalf("failed to list atm10 archives: %v", err)
	}
	if len(projList) != 1 {
		t.Fatalf("expected 1 archive for atm10, got %d", len(projList))
	}

	// 4. Query expired archives (currently none expired)
	expiredNone, err := db.GetExpiredArchives(ctx, now)
	if err != nil {
		t.Fatalf("failed to query expired: %v", err)
	}
	if len(expiredNone) != 0 {
		t.Errorf("expected 0 expired archives now, got %d", len(expiredNone))
	}

	// In future +91 days, it should be expired
	expiredFuture, err := db.GetExpiredArchives(ctx, now.Add(91*24*time.Hour))
	if err != nil {
		t.Fatalf("failed to query expired in future: %v", err)
	}
	if len(expiredFuture) != 1 {
		t.Errorf("expected 1 expired archive in future, got %d", len(expiredFuture))
	}

	// 5. Delete archive
	if err := db.DeleteArchive(ctx, archive.ID); err != nil {
		t.Fatalf("failed to delete archive: %v", err)
	}
	_, err = db.GetArchive(ctx, archive.ID)
	if err == nil {
		t.Fatalf("expected error getting deleted archive, got nil")
	}
}

