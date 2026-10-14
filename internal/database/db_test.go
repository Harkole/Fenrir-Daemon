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
