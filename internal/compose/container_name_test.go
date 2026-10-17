package compose

import (
	"strings"
	"testing"
)

func TestSanitizeContainerName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple valid name",
			input:    "atm10",
			expected: "atm10",
		},
		{
			name:     "name with whitespace",
			input:    "ATM 10 Server",
			expected: "ATM10Server",
		},
		{
			name:     "tabs and newlines",
			input:    "my\tserver\nname ",
			expected: "myservername",
		},
		{
			name:     "special characters and symbols stripped",
			input:    "My Minecraft Server! #1 [Modded] (1.20.1)",
			expected: "MyMinecraftServer1Modded1.20.1",
		},
		{
			name:     "allowed characters preserved (dot, dash, underscore)",
			input:    "mc.server-01_v2",
			expected: "mc.server-01_v2",
		},
		{
			name:     "leading disallowed characters (leading dash/dot/underscore)",
			input:    "-._-atm10-server",
			expected: "atm10-server",
		},
		{
			name:     "name with only disallowed characters",
			input:    "  !@#$%^&*()   ",
			expected: "",
		},
		{
			name:     "name starting with number is valid",
			input:    "123-game-server",
			expected: "123-game-server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := SanitizeContainerName(tt.input)
			if actual != tt.expected {
				t.Errorf("SanitizeContainerName(%q) = %q, expected %q", tt.input, actual, tt.expected)
			}
		})
	}
}

func TestApplyContainerName_OverrideExisting(t *testing.T) {
	inputYAML := `services:
  minecraft:
    image: itzg/minecraft-server:latest
    container_name: old-name
    ports:
      - "25565:25565"
`

	result, err := ApplyContainerName(inputYAML, "ATM 10 Server!")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "container_name: ATM10Server") {
		t.Errorf("expected container_name to be ATM10Server, got:\n%s", result)
	}

	if strings.Contains(result, "old-name") {
		t.Errorf("expected old-name to be replaced, got:\n%s", result)
	}
}

func TestApplyContainerName_AddNew(t *testing.T) {
	inputYAML := `services:
  minecraft:
    image: itzg/minecraft-server:latest
    ports:
      - "25565:25565"
`

	result, err := ApplyContainerName(inputYAML, "All The Mods 10")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "container_name: AllTheMods10") {
		t.Errorf("expected container_name to be AllTheMods10, got:\n%s", result)
	}
}

func TestApplyContainerName_PreservesComments(t *testing.T) {
	inputYAML := `# Top level comment
services:
  # Minecraft service comment
  minecraft:
    image: itzg/minecraft-server:latest
    container_name: placeholder # inline comment
`

	result, err := ApplyContainerName(inputYAML, "atm10")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "container_name: atm10") {
		t.Errorf("expected container_name: atm10, got:\n%s", result)
	}

	if !strings.Contains(result, "Top level comment") {
		t.Errorf("expected top level comment to be preserved, got:\n%s", result)
	}
}

func TestApplyContainerName_MultiService(t *testing.T) {
	inputYAML := `services:
  web:
    image: nginx:alpine
    container_name: old-web
  db:
    image: postgres:16
    container_name: old-db
`

	result, err := ApplyContainerName(inputYAML, "Portal Stack")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "container_name: PortalStack") {
		t.Errorf("expected first service to get PortalStack, got:\n%s", result)
	}

	if !strings.Contains(result, "container_name: PortalStack-db") {
		t.Errorf("expected db service to get PortalStack-db, got:\n%s", result)
	}
}
