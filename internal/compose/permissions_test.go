package compose

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetStackPermissions(t *testing.T) {
	tempDir := t.TempDir()

	subDir := filepath.Join(tempDir, "data")
	if err := os.MkdirAll(subDir, 0700); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	testFile := filepath.Join(subDir, "server.properties")
	if err := os.WriteFile(testFile, []byte("motd=Fenrir Server"), 0600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Apply permissions for UID 1000, GID 1000
	if err := SetStackPermissions(tempDir, 1000, 1000); err != nil {
		t.Fatalf("SetStackPermissions returned error: %v", err)
	}

	// Verify file and directory can still be read and accessed
	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("failed to stat test file: %v", err)
	}

	if info.Size() == 0 {
		t.Fatalf("test file is empty")
	}
}
