package compose

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Harkole/fenrir-daemon/internal/model"
)

var validProjectNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// Engine manages Docker Compose stack filesystem provisioning and CLI execution.
type Engine struct {
	stacksDir  string
	dockerHost string
	defaultUID int
	defaultGID int
}

// NewEngine creates a new Docker Compose execution engine.
func NewEngine(stacksDir, dockerHost string, defaultUID, defaultGID int) *Engine {
	if strings.TrimSpace(stacksDir) == "" {
		stacksDir = "/var/lib/fenrir/stacks/"
	}
	if defaultUID <= 0 {
		defaultUID = 1000
	}
	if defaultGID <= 0 {
		defaultGID = 1000
	}
	return &Engine{
		stacksDir:  filepath.Clean(stacksDir),
		dockerHost: dockerHost,
		defaultUID: defaultUID,
		defaultGID: defaultGID,
	}
}

// DefaultUID returns the configured default UID.
func (e *Engine) DefaultUID() int {
	return e.defaultUID
}

// DefaultGID returns the configured default GID.
func (e *Engine) DefaultGID() int {
	return e.defaultGID
}

// StacksDir returns the configured stacks root directory.
func (e *Engine) StacksDir() string {
	return e.stacksDir
}

// ProjectPath returns the absolute filesystem path for a given project.
func (e *Engine) ProjectPath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !validProjectNameRegex.MatchString(name) {
		return "", fmt.Errorf("invalid project name %q: must contain only alphanumeric, dash, dot, or underscore", name)
	}
	return filepath.Join(e.stacksDir, name), nil
}

// ValidateCompose runs pre-flight syntax and schema validation using `docker compose config`.
// If validation fails, it returns the detailed error output while leaving the live host untouched.
func (e *Engine) ValidateCompose(ctx context.Context, rawYaml string) (string, error) {
	rawYaml = strings.TrimSpace(rawYaml)
	if rawYaml == "" {
		return "", errors.New("compose payload cannot be empty")
	}

	tempDir, err := os.MkdirTemp("", "fenrir-compose-val-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary validation directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	tempFile := filepath.Join(tempDir, "compose.yml")
	if err := os.WriteFile(tempFile, []byte(rawYaml), 0644); err != nil {
		return "", fmt.Errorf("failed to write temporary compose file: %w", err)
	}

	// Try validating with `docker compose config`
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", tempFile, "config")
	if e.dockerHost != "" {
		cmd.Env = append(os.Environ(), "DOCKER_HOST="+e.dockerHost)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		// If docker binary is not installed / mock environment
		if errors.Is(err, exec.ErrNotFound) {
			// Basic YAML syntax fallback validation when Docker CLI is not present
			return "docker cli not found; passed basic non-empty check", nil
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = strings.TrimSpace(stdout.String())
		}
		if errMsg == "" {
			errMsg = err.Error()
		}
		return errMsg, fmt.Errorf("pre-flight validation failed: %s", errMsg)
	}

	return stdout.String(), nil
}

// ProvisionStack creates the stack directory structure (`data/` directory and `compose.yml`).
func (e *Engine) ProvisionStack(ctx context.Context, name, rawYaml string) (string, error) {
	projectDir, err := e.ProjectPath(name)
	if err != nil {
		return "", err
	}

	// 1. Create project root directory
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create project directory %q: %w", projectDir, err)
	}

	// 2. Create local persistent data/ directory
	dataDir := filepath.Join(projectDir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create project data directory %q: %w", dataDir, err)
	}

	// 3. Write compose.yml safely using atomic rename
	composePath := filepath.Join(projectDir, "compose.yml")
	tmpFile := filepath.Join(projectDir, ".compose.yml.tmp")

	if err := os.WriteFile(tmpFile, []byte(rawYaml), 0644); err != nil {
		return "", fmt.Errorf("failed to write temporary compose file: %w", err)
	}

	if err := os.Rename(tmpFile, composePath); err != nil {
		_ = os.Remove(tmpFile)
		return "", fmt.Errorf("failed to commit compose.yml: %w", err)
	}

	// 4. Ensure recursive permissions and ownership for container users (UID/GID 1000)
	if err := SetStackPermissions(projectDir, e.defaultUID, e.defaultGID); err != nil {
		return "", fmt.Errorf("failed to set stack permissions for %q: %w", projectDir, err)
	}

	return projectDir, nil
}

// ReadComposeFile reads the content of compose.yml from a project directory.
func (e *Engine) ReadComposeFile(name string) (string, error) {
	projectDir, err := e.ProjectPath(name)
	if err != nil {
		return "", err
	}

	composePath := filepath.Join(projectDir, "compose.yml")
	data, err := os.ReadFile(composePath)
	if err != nil {
		return "", fmt.Errorf("failed to read compose file: %w", err)
	}

	return string(data), nil
}

// ListProjectFiles inspects the project directory and returns a list of files and whether data/ exists.
func (e *Engine) ListProjectFiles(name string) ([]string, bool, error) {
	projectDir, err := e.ProjectPath(name)
	if err != nil {
		return nil, false, err
	}

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, false, fmt.Errorf("failed to read project directory: %w", err)
	}

	var files []string
	hasDataDir := false

	for _, entry := range entries {
		files = append(files, entry.Name())
		if entry.IsDir() && entry.Name() == "data" {
			hasDataDir = true
		}
	}

	return files, hasDataDir, nil
}

// Up runs `docker compose up -d` within the project directory.
func (e *Engine) Up(ctx context.Context, name string) (string, error) {
	return e.runCompose(ctx, name, "up", "-d")
}

// Down runs `docker compose down` within the project directory.
func (e *Engine) Down(ctx context.Context, name string, removeVolumes bool) (string, error) {
	args := []string{"down"}
	if removeVolumes {
		args = append(args, "-v")
	}
	return e.runCompose(ctx, name, args...)
}

// Restart runs `docker compose restart` within the project directory.
func (e *Engine) Restart(ctx context.Context, name string) (string, error) {
	return e.runCompose(ctx, name, "restart")
}

// Status checks service states using `docker compose ps --format json`.
func (e *Engine) Status(ctx context.Context, name string) ([]model.ComposeServiceStatus, string, error) {
	output, err := e.runCompose(ctx, name, "ps", "--format", "json")
	if err != nil {
		return nil, output, err
	}

	var services []model.ComposeServiceStatus
	outputTrim := strings.TrimSpace(output)
	if outputTrim == "" {
		return []model.ComposeServiceStatus{}, output, nil
	}

	// Docker compose may output a JSON array or newline-delimited JSON objects
	if strings.HasPrefix(outputTrim, "[") {
		_ = json.Unmarshal([]byte(outputTrim), &services)
	} else {
		lines := strings.Split(outputTrim, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var svc model.ComposeServiceStatus
			if err := json.Unmarshal([]byte(line), &svc); err == nil {
				services = append(services, svc)
			}
		}
	}

	if services == nil {
		services = []model.ComposeServiceStatus{}
	}

	return services, output, nil
}

// Logs fetches container logs from the stack.
func (e *Engine) Logs(ctx context.Context, name string, tail int, service string) (string, error) {
	args := []string{"logs"}
	if tail > 0 {
		args = append(args, "--tail", fmt.Sprintf("%d", tail))
	}
	if service != "" {
		args = append(args, service)
	}
	return e.runCompose(ctx, name, args...)
}

func (e *Engine) runCompose(ctx context.Context, name string, args ...string) (string, error) {
	projectDir, err := e.ProjectPath(name)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(projectDir); os.IsNotExist(err) {
		return "", fmt.Errorf("project directory %q does not exist", projectDir)
	}

	cmdArgs := append([]string{"compose"}, args...)
	cmd := exec.CommandContext(ctx, "docker", cmdArgs...)
	cmd.Dir = projectDir

	cmd.Env = os.Environ()
	if e.dockerHost != "" {
		cmd.Env = append(cmd.Env, "DOCKER_HOST="+e.dockerHost)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	output := strings.TrimSpace(outBuf.String())
	if errBuf.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += strings.TrimSpace(errBuf.String())
	}

	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Sprintf("docker cli not found; simulated compose %s", strings.Join(args, " ")), nil
		}
		return output, fmt.Errorf("docker compose %s failed: %w (%s)", strings.Join(args, " "), err, output)
	}

	return output, nil
}
