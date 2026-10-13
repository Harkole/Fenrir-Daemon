package docker

import (
	"testing"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/model"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
)

func TestParseFenrirMetadata(t *testing.T) {
	m := &Monitor{
		gameKeywords: []string{"minecraft", "mc", "itzg", "valheim"},
	}

	tests := []struct {
		name            string
		names           []string
		image           string
		labels          map[string]string
		ports           []types.Port
		wantEnabled     bool
		wantType        string
		wantDisplayName string
	}{
		{
			name:  "Explicit game server with Minecraft formatted in display name",
			names: []string{"mc-server-1"},
			image: "itzg/minecraft-server:latest",
			labels: map[string]string{
				"fenrir.enable":       "true",
				"fenrir.type":         "game",
				"fenrir.display_name": "Minecraft - Survival SMP",
			},
			ports:           nil,
			wantEnabled:     true,
			wantType:        "game",
			wantDisplayName: "Minecraft - Survival SMP",
		},
		{
			name:  "Explicit game server for Valheim formatted in display name",
			names: []string{"valheim-server"},
			image: "lloesche/valheim-server:latest",
			labels: map[string]string{
				"fenrir.enable":       "true",
				"fenrir.type":         "game",
				"fenrir.display_name": "Valheim - Odin Realm",
			},
			ports:           nil,
			wantEnabled:     true,
			wantType:        "game",
			wantDisplayName: "Valheim - Odin Realm",
		},
		{
			name:  "Database container",
			names: []string{"postgres-db"},
			image: "postgres:16-alpine",
			labels: map[string]string{
				"fenrir.enable":       "true",
				"fenrir.type":         "database",
				"fenrir.display_name": "Postgres - Primary DB",
			},
			ports:           nil,
			wantEnabled:     true,
			wantType:        "database",
			wantDisplayName: "Postgres - Primary DB",
		},
		{
			name:  "Web app container with default display name fallback",
			names: []string{"frontend-app"},
			image: "nginx:alpine",
			labels: map[string]string{
				"fenrir.enable": "true",
				"fenrir.type":   "webapp",
			},
			ports:           nil,
			wantEnabled:     true,
			wantType:        "webapp",
			wantDisplayName: "frontend-app",
		},
		{
			name:            "Untracked generic container",
			names:           []string{"random-busybox"},
			image:           "busybox:latest",
			labels:          nil,
			ports:           nil,
			wantEnabled:     false,
			wantType:        "",
			wantDisplayName: "random-busybox",
		},
		{
			name:  "Tracked container without explicit type or display name infers game and formats display name",
			names: []string{"vanilla-mc"},
			image: "openjdk:21-slim",
			labels: map[string]string{
				"fenrir.enable": "true",
			},
			ports:           []types.Port{{PrivatePort: 25565, PublicPort: 25565, Type: "tcp"}},
			wantEnabled:     true,
			wantType:        "game",
			wantDisplayName: "Minecraft - vanilla-mc",
		},
		{
			name:  "User All The Mods 10 (ATM10) compose container with fenrir.type=minecraft",
			names: []string{"atm10"},
			image: "itzg/minecraft-server:java21",
			labels: map[string]string{
				"fenrir.enable":       "true",
				"fenrir.type":         "minecraft",
				"fenrir.display_name": "All The Mods 10 (ATM10)",
				"fenrir.modpack":      "all-the-mods-10",
			},
			ports:           []types.Port{{PrivatePort: 25565, PublicPort: 25565, Type: "tcp"}},
			wantEnabled:     true,
			wantType:        "game",
			wantDisplayName: "All The Mods 10 (ATM10)",
		},
		{
			name:  "User All The Mods 10 (ATM10) compose container with fenrir.type=game",
			names: []string{"atm10"},
			image: "itzg/minecraft-server:java21",
			labels: map[string]string{
				"fenrir.enable":       "true",
				"fenrir.type":         "game",
				"fenrir.display_name": "All The Mods 10 (ATM10)",
				"fenrir.modpack":      "all-the-mods-10",
			},
			ports:           []types.Port{{PrivatePort: 25565, PublicPort: 25565, Type: "tcp"}},
			wantEnabled:     true,
			wantType:        "game",
			wantDisplayName: "All The Mods 10 (ATM10)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := m.ParseFenrirMetadata(tt.names, tt.image, tt.labels, tt.ports)
			if meta.Enabled != tt.wantEnabled {
				t.Errorf("Enabled = %v, want %v", meta.Enabled, tt.wantEnabled)
			}
			if meta.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", meta.Type, tt.wantType)
			}
			if meta.DisplayName != tt.wantDisplayName {
				t.Errorf("DisplayName = %q, want %q", meta.DisplayName, tt.wantDisplayName)
			}
		})
	}
}

func TestDeriveGameServer(t *testing.T) {
	m := &Monitor{
		gameKeywords: []string{"minecraft", "mc", "itzg", "valheim"},
	}

	tests := []struct {
		name         string
		meta         model.FenrirMetadata
		names        []string
		labels       map[string]string
		wantIsGame   bool
		wantGameType string
	}{
		{
			name: "Type game with Minecraft in display name",
			meta: model.FenrirMetadata{
				Enabled:     true,
				Type:        "game",
				DisplayName: "Minecraft - Survival SMP",
			},
			names:        []string{"mc-server"},
			wantIsGame:   true,
			wantGameType: "minecraft",
		},
		{
			name: "Type game with Valheim in display name",
			meta: model.FenrirMetadata{
				Enabled:     true,
				Type:        "game",
				DisplayName: "Valheim - Dedicated Server",
			},
			names:        []string{"valheim-box"},
			wantIsGame:   true,
			wantGameType: "valheim",
		},
		{
			name: "Type database is not game server",
			meta: model.FenrirMetadata{
				Enabled:     true,
				Type:        "database",
				DisplayName: "Postgres - Primary DB",
			},
			names:        []string{"postgres-db"},
			wantIsGame:   false,
			wantGameType: "",
		},
		{
			name: "ATM10 container with label fenrir.type=minecraft",
			meta: model.FenrirMetadata{
				Enabled:     true,
				Type:        "game",
				DisplayName: "All The Mods 10 (ATM10)",
			},
			names:        []string{"atm10"},
			labels:       map[string]string{"fenrir.type": "minecraft"},
			wantIsGame:   true,
			wantGameType: "minecraft",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isGame, gameType := m.deriveGameServer(tt.meta, tt.names, "", tt.labels, nil)
			if isGame != tt.wantIsGame {
				t.Errorf("isGame = %v, want %v", isGame, tt.wantIsGame)
			}
			if gameType != tt.wantGameType {
				t.Errorf("gameType = %q, want %q", gameType, tt.wantGameType)
			}
		})
	}
}

func TestDetectGameServer(t *testing.T) {
	m := &Monitor{
		gameKeywords: []string{"minecraft", "mc", "itzg", "valheim"},
	}

	tests := []struct {
		name         string
		names        []string
		image        string
		labels       map[string]string
		ports        []types.Port
		wantIsGame   bool
		wantGameType string
	}{
		{
			name:         "Explicit label fenrir.game=minecraft",
			names:        []string{"my-server"},
			image:        "custom-image:latest",
			labels:       map[string]string{"fenrir.game": "minecraft"},
			ports:        nil,
			wantIsGame:   true,
			wantGameType: "minecraft",
		},
		{
			name:         "Itzg Minecraft Server Image",
			names:        []string{"survival"},
			image:        "itzg/minecraft-server:latest",
			labels:       nil,
			ports:        nil,
			wantIsGame:   true,
			wantGameType: "minecraft",
		},
		{
			name:         "Default Minecraft port 25565",
			names:        []string{"unlabeled-server"},
			image:        "ubuntu:22.04",
			labels:       nil,
			ports:        []types.Port{{PrivatePort: 25565, PublicPort: 25565, Type: "tcp"}},
			wantIsGame:   true,
			wantGameType: "minecraft",
		},
		{
			name:         "Minecraft Bedrock port 19132",
			names:        []string{"bedrock-server"},
			image:        "ubuntu:22.04",
			labels:       nil,
			ports:        []types.Port{{PrivatePort: 19132, PublicPort: 19132, Type: "udp"}},
			wantIsGame:   true,
			wantGameType: "minecraft-bedrock",
		},
		{
			name:         "Valheim keyword in name",
			names:        []string{"valheim-dedicated"},
			image:        "generic/game:latest",
			labels:       nil,
			ports:        nil,
			wantIsGame:   true,
			wantGameType: "valheim",
		},
		{
			name:         "Generic nginx container",
			names:        []string{"web-nginx"},
			image:        "nginx:alpine",
			labels:       nil,
			ports:        []types.Port{{PrivatePort: 80, PublicPort: 8080, Type: "tcp"}},
			wantIsGame:   false,
			wantGameType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotIsGame, gotType := m.detectGameServer(tt.names, tt.image, tt.labels, tt.ports)
			if gotIsGame != tt.wantIsGame {
				t.Errorf("detectGameServer() isGame = %v, want %v", gotIsGame, tt.wantIsGame)
			}
			if gotType != tt.wantGameType {
				t.Errorf("detectGameServer() gameType = %v, want %v", gotType, tt.wantGameType)
			}
		})
	}
}

func TestCalculateResourceStats(t *testing.T) {
	stats := &container.StatsResponse{
		Stats: container.Stats{
			Read:    time.Now(),
			PreRead: time.Now().Add(-1 * time.Second),
			CPUStats: container.CPUStats{
				CPUUsage: container.CPUUsage{
					TotalUsage:  2000000000, // 2s
					PercpuUsage: []uint64{1000000000, 1000000000},
				},
				SystemUsage: 10000000000, // 10s
				OnlineCPUs:  2,
			},
			PreCPUStats: container.CPUStats{
				CPUUsage: container.CPUUsage{
					TotalUsage: 1000000000, // 1s
				},
				SystemUsage: 5000000000, // 5s
			},
			MemoryStats: container.MemoryStats{
				Usage: 1024 * 1024 * 512, // 512 MB
				Limit: 1024 * 1024 * 1024, // 1024 MB
				Stats: map[string]uint64{
					"inactive_file": 1024 * 1024 * 12, // 12 MB cache
				},
			},
		},
		Networks: map[string]container.NetworkStats{
			"eth0": {
				RxBytes: 1500,
				TxBytes: 3000,
			},
		},
	}

	res := calculateResourceStats(stats)

	// CPU delta: 1s, System delta: 5s, 2 CPUs -> (1 / 5) * 2 * 100 = 40%
	if res.CPUPercent != 40.0 {
		t.Errorf("expected CPU percent 40.0, got %f", res.CPUPercent)
	}

	// Memory usage: 512MB - 12MB = 500MB
	expectedMem := uint64(500 * 1024 * 1024)
	if res.MemoryUsage != expectedMem {
		t.Errorf("expected MemoryUsage %d, got %d", expectedMem, res.MemoryUsage)
	}

	if res.NetworkRxBytes != 1500 || res.NetworkTxBytes != 3000 {
		t.Errorf("expected net RX 1500, TX 3000, got RX %d, TX %d", res.NetworkRxBytes, res.NetworkTxBytes)
	}
}

func TestSanitizeEnv(t *testing.T) {
	envs := []string{
		"EULA=TRUE",
		"SERVER_PORT=25565",
		"RCON_PASSWORD=supersecret",
		"DB_AUTH_TOKEN=abcdef123",
		"CF_API_KEY=$2a$10$.6zL8XxXlH3LJmmqOJfiL.mO7pcK7kSHZue2hXBgTX2l0GmlzmMVa",
	}

	sanitized := sanitizeEnv(envs)
	if sanitized[0] != "EULA=TRUE" || sanitized[1] != "SERVER_PORT=25565" {
		t.Errorf("unexpected sanitization for non-sensitive env: %v", sanitized)
	}
	if sanitized[2] != "RCON_PASSWORD=********" {
		t.Errorf("password not masked: %s", sanitized[2])
	}
	if sanitized[3] != "DB_AUTH_TOKEN=********" {
		t.Errorf("token not masked: %s", sanitized[3])
	}
	if sanitized[4] != "CF_API_KEY=********" {
		t.Errorf("CF_API_KEY not masked: %s", sanitized[4])
	}
}
