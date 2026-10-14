package compose

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEngineProvisionAndFiles(t *testing.T) {
	tempDir := t.TempDir()
	stacksDir := filepath.Join(tempDir, "stacks")

	engine := NewEngine(stacksDir, "")
	ctx := context.Background()

	rawCompose := `services:
  atm10:
    image: itzg/minecraft-server:latest
    ports:
      - "25565:25565"
    volumes:
      - ./data:/data
`

	// 1. Provision stack
	projectDir, err := engine.ProvisionStack(ctx, "atm10", rawCompose)
	if err != nil {
		t.Fatalf("failed to provision stack: %v", err)
	}

	expectedDir := filepath.Join(stacksDir, "atm10")
	if projectDir != expectedDir {
		t.Errorf("expected %q, got %q", expectedDir, projectDir)
	}

	// 2. Verify data/ directory exists
	dataDir := filepath.Join(projectDir, "data")
	stat, err := os.Stat(dataDir)
	if err != nil || !stat.IsDir() {
		t.Fatalf("expected data directory to exist and be a directory: %v", err)
	}

	// 3. Read compose file
	readContent, err := engine.ReadComposeFile("atm10")
	if err != nil {
		t.Fatalf("failed to read compose file: %v", err)
	}
	if readContent != rawCompose {
		t.Errorf("expected raw compose to match, got %q", readContent)
	}

	// 4. List project files
	files, hasData, err := engine.ListProjectFiles("atm10")
	if err != nil {
		t.Fatalf("failed to list project files: %v", err)
	}
	if !hasData {
		t.Errorf("expected hasData to be true")
	}
	if len(files) < 2 {
		t.Errorf("expected at least compose.yml and data, got %v", files)
	}
}

func TestEngineValidateEmptyPayload(t *testing.T) {
	tempDir := t.TempDir()
	engine := NewEngine(tempDir, "")

	ctx := context.Background()
	_, err := engine.ValidateCompose(ctx, "   ")
	if err == nil {
		t.Fatalf("expected validation error for empty payload")
	}
}

func TestInvalidProjectName(t *testing.T) {
	tempDir := t.TempDir()
	engine := NewEngine(tempDir, "")

	invalidNames := []string{"../escape", "foo/bar", "foo\\bar", "", "  ", "space in name"}
	for _, name := range invalidNames {
		_, err := engine.ProjectPath(name)
		if err == nil {
			t.Errorf("expected error for invalid project name %q, got nil", name)
		}
	}
}
