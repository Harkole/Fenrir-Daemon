package archive

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/database"
	"github.com/Harkole/fenrir-daemon/internal/model"
)

func TestCreateAndRestoreArchive(t *testing.T) {
	tempDir := t.TempDir()

	projectDir := filepath.Join(tempDir, "stacks", "atm10")
	dataDir := filepath.Join(projectDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("failed to create data dir: %v", err)
	}

	composeContent := "services:\n  minecraft:\n    image: itzg/minecraft-server\n"
	if err := os.WriteFile(filepath.Join(projectDir, "compose.yml"), []byte(composeContent), 0644); err != nil {
		t.Fatalf("failed to write compose.yml: %v", err)
	}

	worldData := "world-save-chunk-data-123456"
	if err := os.WriteFile(filepath.Join(dataDir, "level.dat"), []byte(worldData), 0644); err != nil {
		t.Fatalf("failed to write level.dat: %v", err)
	}

	archivesDir := filepath.Join(tempDir, "archives")
	ctx := context.Background()

	// 1. Create Archive
	meta, err := CreateZipArchive(ctx, "atm10", projectDir, archivesDir, 90)
	if err != nil {
		t.Fatalf("failed to create zip archive: %v", err)
	}

	if meta.ProjectName != "atm10" {
		t.Errorf("expected project name atm10, got %s", meta.ProjectName)
	}
	if meta.SizeBytes <= 0 {
		t.Errorf("expected non-zero archive size, got %d", meta.SizeBytes)
	}
	if meta.DaysUntilDeletion != 90 {
		t.Errorf("expected 90 days until deletion, got %d", meta.DaysUntilDeletion)
	}

	// Verify file exists on disk
	if _, err := os.Stat(meta.ArchivePath); err != nil {
		t.Fatalf("archive file does not exist on disk: %v", err)
	}

	// 2. Restore Archive into a fresh location
	restoreDir := filepath.Join(tempDir, "restored", "atm10")
	if err := RestoreZipArchive(ctx, meta.ArchivePath, restoreDir, 1000, 1000); err != nil {
		t.Fatalf("failed to restore archive: %v", err)
	}

	// Verify restored contents
	restoredCompose, err := os.ReadFile(filepath.Join(restoreDir, "compose.yml"))
	if err != nil {
		t.Fatalf("failed to read restored compose.yml: %v", err)
	}
	if string(restoredCompose) != composeContent {
		t.Errorf("compose content mismatch: got %q, expected %q", string(restoredCompose), composeContent)
	}

	restoredData, err := os.ReadFile(filepath.Join(restoreDir, "data", "level.dat"))
	if err != nil {
		t.Fatalf("failed to read restored level.dat: %v", err)
	}
	if string(restoredData) != worldData {
		t.Errorf("world data mismatch: got %q, expected %q", string(restoredData), worldData)
	}
}

func TestPruneExpiredArchives(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	archivesDir := filepath.Join(tempDir, "archives")
	_ = os.MkdirAll(archivesDir, 0755)

	// Create fake archive zip file on disk
	fakeZipPath := filepath.Join(archivesDir, "expired-atm10.zip")
	_ = os.WriteFile(fakeZipPath, []byte("fake-zip-data"), 0644)

	ctx := context.Background()
	now := time.Now().UTC()

	// Insert expired archive record into DB (expired 10 days ago)
	expiredMeta := &model.ArchiveMetadata{
		ProjectName: "atm10",
		ArchivePath: fakeZipPath,
		SizeBytes:   13,
		CreatedAt:   now.Add(-100 * 24 * time.Hour),
		ExpiresAt:   now.Add(-10 * 24 * time.Hour),
	}
	if err := db.CreateArchive(ctx, expiredMeta); err != nil {
		t.Fatalf("failed to insert expired archive: %v", err)
	}

	// Prune
	pruned, err := PruneExpiredArchives(ctx, db)
	if err != nil {
		t.Fatalf("pruning failed: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 pruned archive, got %d", pruned)
	}

	// Verify file was deleted from disk
	if _, err := os.Stat(fakeZipPath); !os.IsNotExist(err) {
		t.Errorf("expected archive file to be deleted from disk")
	}

	// Verify record was deleted from DB
	_, err = db.GetArchive(ctx, expiredMeta.ID)
	if err == nil {
		t.Errorf("expected DB record to be deleted")
	}
}
