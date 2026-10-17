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

	engine := NewEngine(stacksDir, "", 1000, 1000)
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
	engine := NewEngine(tempDir, "", 1000, 1000)

	ctx := context.Background()
	_, err := engine.ValidateCompose(ctx, "   ")
	if err == nil {
		t.Fatalf("expected validation error for empty payload")
	}
}

func TestInvalidProjectName(t *testing.T) {
	tempDir := t.TempDir()
	engine := NewEngine(tempDir, "", 1000, 1000)

	invalidNames := []string{"../escape", "foo/bar", "foo\\bar", "", "  ", "space in name"}
	for _, name := range invalidNames {
		_, err := engine.ProjectPath(name)
		if err == nil {
			t.Errorf("expected error for invalid project name %q, got nil", name)
		}
	}
}

func TestEngineYAMLFileManagement(t *testing.T) {
	tempDir := t.TempDir()
	stacksDir := filepath.Join(tempDir, "stacks")
	engine := NewEngine(stacksDir, "", 1000, 1000)
	ctx := context.Background()

	// Provision with docker-compose.yml instead of default compose.yml
	initialYAML := "services:\n  valheim:\n    image: lloesche/valheim-server\n"
	_, err := engine.WriteComposeFile(ctx, "valheim-stack", "docker-compose.yml", initialYAML)
	if err != nil {
		t.Fatalf("failed to write docker-compose.yml: %v", err)
	}

	// 1. Verify FindComposeFile discovers docker-compose.yml
	filename, path, err := engine.FindComposeFile("valheim-stack")
	if err != nil {
		t.Fatalf("unexpected error finding compose file: %v", err)
	}
	if filename != "docker-compose.yml" {
		t.Errorf("expected docker-compose.yml, got %q", filename)
	}
	if filepath.Base(path) != "docker-compose.yml" {
		t.Errorf("expected path to end with docker-compose.yml, got %q", path)
	}

	// 2. ReadComposeFile should find and read docker-compose.yml
	content, err := engine.ReadComposeFile("valheim-stack")
	if err != nil {
		t.Fatalf("failed to read compose file: %v", err)
	}
	if content != initialYAML {
		t.Errorf("expected initialYAML, got %q", content)
	}

	// 3. Write another YAML config file (e.g., config.yaml)
	configYAML := "server_name: MyValheim\nworld: Dedicated\n"
	if err := engine.WriteProjectFile("valheim-stack", "config.yaml", configYAML); err != nil {
		t.Fatalf("failed to write config.yaml: %v", err)
	}

	// 4. ListProjectYAMLFiles should list both docker-compose.yml and config.yaml
	yamlFiles, err := engine.ListProjectYAMLFiles("valheim-stack")
	if err != nil {
		t.Fatalf("failed to list YAML files: %v", err)
	}
	if len(yamlFiles) != 2 {
		t.Fatalf("expected 2 YAML files, got %d: %v", len(yamlFiles), yamlFiles)
	}

	// 5. ReadProjectFile should read config.yaml accurately
	readConfig, err := engine.ReadProjectFile("valheim-stack", "config.yaml")
	if err != nil {
		t.Fatalf("failed to read config.yaml: %v", err)
	}
	if readConfig != configYAML {
		t.Errorf("expected configYAML, got %q", readConfig)
	}

	// 6. Edit existing YAML file (update config.yaml)
	updatedConfig := "server_name: UpdatedValheim\nworld: Hardcore\n"
	if err := engine.WriteProjectFile("valheim-stack", "config.yaml", updatedConfig); err != nil {
		t.Fatalf("failed to update config.yaml: %v", err)
	}
	readUpdated, err := engine.ReadProjectFile("valheim-stack", "config.yaml")
	if err != nil {
		t.Fatalf("failed to read updated config.yaml: %v", err)
	}
	if readUpdated != updatedConfig {
		t.Errorf("expected updatedConfig, got %q", readUpdated)
	}
}

func TestEnginePathTraversalSecurity(t *testing.T) {
	tempDir := t.TempDir()
	stacksDir := filepath.Join(tempDir, "stacks")
	engine := NewEngine(stacksDir, "", 1000, 1000)

	maliciousPaths := []string{
		"../secret.txt",
		"..\\secret.txt",
		"/etc/passwd",
		"sub/../../secret.txt",
	}

	for _, p := range maliciousPaths {
		_, err := engine.ResolveProjectFilePath("my-project", p)
		if err == nil {
			t.Errorf("expected traversal error for path %q, got nil", p)
		}
	}
}

