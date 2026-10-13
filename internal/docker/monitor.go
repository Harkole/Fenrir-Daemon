package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/model"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// Monitor provides container inspection and resource monitoring using the official Docker SDK.
type Monitor struct {
	cli          *client.Client
	gameKeywords []string
}

// NewMonitor initializes a new Docker monitor with client options.
func NewMonitor(dockerHost string, gameKeywords []string) (*Monitor, error) {
	opts := []client.Opt{
		client.WithAPIVersionNegotiation(),
	}

	if dockerHost != "" {
		opts = append(opts, client.WithHost(dockerHost))
	} else {
		opts = append(opts, client.FromEnv)
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize docker client: %w", err)
	}

	return &Monitor{
		cli:          cli,
		gameKeywords: gameKeywords,
	}, nil
}

// Close closes the Docker client connection.
func (m *Monitor) Close() error {
	if m.cli != nil {
		return m.cli.Close()
	}
	return nil
}

// Ping verifies connectivity to the Docker daemon.
func (m *Monitor) Ping(ctx context.Context) error {
	_, err := m.cli.Ping(ctx)
	return err
}

// ListContainers retrieves a list of containers, optionally including stopped containers.
func (m *Monitor) ListContainers(ctx context.Context, all bool) ([]model.ContainerSummary, error) {
	containers, err := m.cli.ContainerList(ctx, container.ListOptions{All: all})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	summaries := make([]model.ContainerSummary, 0, len(containers))
	for _, c := range containers {
		cleanNames := make([]string, 0, len(c.Names))
		for _, name := range c.Names {
			cleanNames = append(cleanNames, strings.TrimPrefix(name, "/"))
		}

		fenrirMeta := m.ParseFenrirMetadata(cleanNames, c.Image, c.Labels, c.Ports)
		isGame, gameType := m.deriveGameServer(fenrirMeta, cleanNames, c.Image, c.Labels, c.Ports)

		ports := make([]model.PortMapping, 0, len(c.Ports))
		for _, p := range c.Ports {
			ports = append(ports, model.PortMapping{
				IP:          p.IP,
				PrivatePort: p.PrivatePort,
				PublicPort:  p.PublicPort,
				Type:        p.Type,
			})
		}

		summary := model.ContainerSummary{
			ID:           c.ID,
			Names:        cleanNames,
			Image:        c.Image,
			ImageID:      c.ImageID,
			Command:      c.Command,
			CreatedAt:    time.Unix(c.Created, 0).UTC(),
			State:        c.State,
			Status:       c.Status,
			Ports:        ports,
			Labels:       c.Labels,
			Fenrir:       fenrirMeta,
			IsGameServer: isGame,
			GameType:     gameType,
		}

		summaries = append(summaries, summary)
	}

	return summaries, nil
}

// GetContainer returns comprehensive details for a specific container by ID or name.
func (m *Monitor) GetContainer(ctx context.Context, id string) (*model.ContainerDetail, error) {
	inspect, err := m.cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container %q: %w", id, err)
	}

	ports := make([]model.PortMapping, 0)
	if inspect.NetworkSettings != nil {
		for portProto, bindings := range inspect.NetworkSettings.Ports {
			privatePort := portProto.Int()
			proto := portProto.Proto()
			if len(bindings) == 0 {
				ports = append(ports, model.PortMapping{
					PrivatePort: uint16(privatePort),
					Type:        proto,
				})
			} else {
				for _, b := range bindings {
					var pubPort uint16
					fmt.Sscanf(b.HostPort, "%d", &pubPort)
					ports = append(ports, model.PortMapping{
						IP:          b.HostIP,
						PrivatePort: uint16(privatePort),
						PublicPort:  pubPort,
						Type:        proto,
					})
				}
			}
		}
	}

	var rawPorts []types.Port
	for _, p := range ports {
		rawPorts = append(rawPorts, types.Port{
			IP:          p.IP,
			PrivatePort: p.PrivatePort,
			PublicPort:  p.PublicPort,
			Type:        p.Type,
		})
	}

	name := strings.TrimPrefix(inspect.Name, "/")
	cleanNames := []string{name}
	fenrirMeta := m.ParseFenrirMetadata(cleanNames, inspect.Config.Image, inspect.Config.Labels, rawPorts)
	isGame, gameType := m.deriveGameServer(fenrirMeta, cleanNames, inspect.Config.Image, inspect.Config.Labels, rawPorts)

	mounts := make([]model.VolumeMount, 0, len(inspect.Mounts))
	for _, mount := range inspect.Mounts {
		mounts = append(mounts, model.VolumeMount{
			Type:        string(mount.Type),
			Name:        mount.Name,
			Source:      mount.Source,
			Destination: mount.Destination,
			Driver:      mount.Driver,
			Mode:        mount.Mode,
			RW:          mount.RW,
		})
	}

	networks := make([]string, 0)
	ipAddress := ""
	if inspect.NetworkSettings != nil {
		ipAddress = inspect.NetworkSettings.IPAddress
		for netName := range inspect.NetworkSettings.Networks {
			networks = append(networks, netName)
		}
	}

	created, _ := time.Parse(time.RFC3339Nano, inspect.Created)
	started, _ := time.Parse(time.RFC3339Nano, inspect.State.StartedAt)
	finished, _ := time.Parse(time.RFC3339Nano, inspect.State.FinishedAt)

	healthStatus := "none"
	if inspect.State.Health != nil {
		healthStatus = inspect.State.Health.Status
	}

	detail := &model.ContainerDetail{
		ContainerSummary: model.ContainerSummary{
			ID:           inspect.ID,
			Names:        cleanNames,
			Image:        inspect.Config.Image,
			ImageID:      inspect.Image,
			Command:      strings.Join(inspect.Config.Cmd, " "),
			CreatedAt:    created,
			State:        inspect.State.Status,
			Status:       formatContainerState(inspect.State),
			Ports:        ports,
			Labels:       inspect.Config.Labels,
			Fenrir:       fenrirMeta,
			IsGameServer: isGame,
			GameType:     gameType,
		},
		Platform:     inspect.Platform,
		RestartCount: inspect.RestartCount,
		StartedAt:    started,
		FinishedAt:   finished,
		HealthStatus: healthStatus,
		Environment:  sanitizeEnv(inspect.Config.Env),
		Mounts:       mounts,
		Networks:     networks,
		IPAddress:    ipAddress,
	}

	// Fetch live stats if running
	if inspect.State.Running {
		if stats, err := m.GetContainerStats(ctx, inspect.ID); err == nil {
			detail.Stats = stats
		}
	}

	return detail, nil
}

// GetContainerStats captures a single snapshot of resource usage for a container.
func (m *Monitor) GetContainerStats(ctx context.Context, id string) (*model.ContainerResourceStats, error) {
	resp, err := m.cli.ContainerStats(ctx, id, false)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch container stats: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read container stats payload: %w", err)
	}

	var stats container.StatsResponse
	if err := json.Unmarshal(body, &stats); err != nil {
		return nil, fmt.Errorf("failed to parse container stats json: %w", err)
	}

	return calculateResourceStats(&stats), nil
}

// calculateResourceStats derives human-usable CPU, Memory, and Network numbers.
func calculateResourceStats(stats *container.StatsResponse) *model.ContainerResourceStats {
	// CPU percentage calculation
	var cpuPercent float64
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage) - float64(stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemUsage) - float64(stats.PreCPUStats.SystemUsage)
	onlineCPUs := float64(stats.CPUStats.OnlineCPUs)
	if onlineCPUs == 0.0 {
		onlineCPUs = float64(len(stats.CPUStats.CPUUsage.PercpuUsage))
	}
	if onlineCPUs == 0.0 {
		onlineCPUs = 1.0
	}
	if systemDelta > 0.0 && cpuDelta > 0.0 {
		cpuPercent = (cpuDelta / systemDelta) * onlineCPUs * 100.0
	}

	// Memory usage calculation
	memUsage := stats.MemoryStats.Usage
	if cache, ok := stats.MemoryStats.Stats["inactive_file"]; ok && memUsage > cache {
		memUsage -= cache
	} else if cache, ok := stats.MemoryStats.Stats["total_inactive_file"]; ok && memUsage > cache {
		memUsage -= cache
	}
	memLimit := stats.MemoryStats.Limit
	var memPercent float64
	if memLimit > 0 {
		memPercent = (float64(memUsage) / float64(memLimit)) * 100.0
	}

	// Network I/O
	var rxBytes, txBytes uint64
	for _, net := range stats.Networks {
		rxBytes += net.RxBytes
		txBytes += net.TxBytes
	}

	// Block I/O
	var readBytes, writeBytes uint64
	for _, bio := range stats.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(bio.Op) {
		case "read":
			readBytes += bio.Value
		case "write":
			writeBytes += bio.Value
		}
	}

	return &model.ContainerResourceStats{
		CPUPercent:      math.Round(cpuPercent*100) / 100,
		MemoryUsage:     memUsage,
		MemoryLimit:     memLimit,
		MemoryPercent:   math.Round(memPercent*100) / 100,
		NetworkRxBytes:  rxBytes,
		NetworkTxBytes:  txBytes,
		BlockReadBytes:  readBytes,
		BlockWriteBytes: writeBytes,
	}
}

// ParseFenrirMetadata parses Docker labels to determine if a container is tracked,
// its abstracted category/type (game, database, webapp), and a human-readable display name
// formatted with the specific game and friendly name (e.g. "Minecraft - Survival SMP").
func (m *Monitor) ParseFenrirMetadata(names []string, image string, labels map[string]string, ports []types.Port) model.FenrirMetadata {
	primaryName := ""
	if len(names) > 0 {
		primaryName = names[0]
	}

	// 1. Look for fenrir.enable=true (also accepting fenrir.enabled=true or fenrir.monitor=true)
	enabled := false
	if val, ok := labels["fenrir.enable"]; ok && strings.EqualFold(val, "true") {
		enabled = true
	} else if val, ok := labels["fenrir.enabled"]; ok && strings.EqualFold(val, "true") {
		enabled = true
	} else if val, ok := labels["fenrir.monitor"]; ok && strings.EqualFold(val, "true") {
		enabled = true
	}

	// 2. Read fenrir.type (abstracted role: "game", "database", "webapp", etc.)
	cType := ""
	if val, ok := labels["fenrir.type"]; ok && strings.TrimSpace(val) != "" {
		rawType := strings.ToLower(strings.TrimSpace(val))
		// Normalize specific game types to the abstract "game" category
		if rawType == "game" || rawType == "minecraft" || rawType == "valheim" || strings.HasSuffix(rawType, "-server") {
			cType = "game"
		} else {
			cType = rawType
		}
	} else if val, ok := labels["fenrir.game"]; ok && strings.TrimSpace(val) != "" {
		cType = "game"
	}

	// 3. Read fenrir.display_name (e.g. "Minecraft - Survival SMP", "Valheim - Dedicated")
	displayName := ""
	if val, ok := labels["fenrir.display_name"]; ok && strings.TrimSpace(val) != "" {
		displayName = strings.TrimSpace(val)
	} else if val, ok := labels["fenrir.displayName"]; ok && strings.TrimSpace(val) != "" {
		displayName = strings.TrimSpace(val)
	}

	// 4. Infer type and display name formatting if not explicitly provided
	isDetectedGame, detectedGame := m.detectGameServer(names, image, labels, ports)
	if cType == "" {
		if isDetectedGame {
			cType = "game"
		} else if enabled {
			cType = "generic"
		}
	}

	// If display name was omitted, format with game title and friendly container name
	if displayName == "" {
		if cType == "game" || isDetectedGame {
			gameTitle := formatGameTitle(detectedGame)
			if primaryName != "" {
				displayName = fmt.Sprintf("%s - %s", gameTitle, primaryName)
			} else {
				displayName = gameTitle
			}
		} else {
			displayName = primaryName
		}
	}

	return model.FenrirMetadata{
		Enabled:     enabled,
		Type:        cType,
		DisplayName: displayName,
	}
}

// deriveGameServer determines if a container is a game server and identifies the specific game.
func (m *Monitor) deriveGameServer(meta model.FenrirMetadata, names []string, image string, labels map[string]string, ports []types.Port) (bool, string) {
	if meta.Type == "game" {
		// 1. If raw fenrir.type was an explicit game (e.g. "minecraft"), preserve it as the gameType
		if rawType, ok := labels["fenrir.type"]; ok && strings.ToLower(rawType) != "game" && strings.TrimSpace(rawType) != "" {
			return true, strings.ToLower(strings.TrimSpace(rawType))
		}
		// 2. Attempt to extract specific game name from the formatted display name ("<Game> - <Friendly Name>")
		if game := extractGameFromDisplayName(meta.DisplayName); game != "" {
			return true, strings.ToLower(game)
		}
		// 3. Fallback to heuristic detection
		isGame, detected := m.detectGameServer(names, image, labels, ports)
		if isGame && detected != "" {
			return true, detected
		}
		return true, "generic"
	}
	if meta.Type != "" && meta.Type != "generic" {
		// Explicit non-game container (e.g., database, webapp)
		return false, ""
	}
	return m.detectGameServer(names, image, labels, ports)
}

func extractGameFromDisplayName(displayName string) string {
	if strings.Contains(displayName, " - ") {
		parts := strings.SplitN(displayName, " - ", 2)
		return strings.TrimSpace(parts[0])
	}
	return ""
}

func formatGameTitle(game string) string {
	switch strings.ToLower(strings.TrimSpace(game)) {
	case "minecraft":
		return "Minecraft"
	case "minecraft-bedrock":
		return "Minecraft Bedrock"
	case "valheim":
		return "Valheim"
	case "palworld":
		return "Palworld"
	case "factorio":
		return "Factorio"
	case "terraria":
		return "Terraria"
	case "generic", "":
		return "Game"
	default:
		g := strings.TrimSpace(game)
		if len(g) > 0 {
			return strings.ToUpper(g[:1]) + g[1:]
		}
		return "Game"
	}
}

// detectGameServer checks whether a container is running a game server like Minecraft.
func (m *Monitor) detectGameServer(names []string, image string, labels map[string]string, ports []types.Port) (bool, string) {
	// 1. Explicit label check
	if val, ok := labels["fenrir.game"]; ok && val != "" {
		return true, strings.ToLower(val)
	}
	if val, ok := labels["fenrir.monitor"]; ok && strings.EqualFold(val, "true") {
		return true, "generic"
	}

	// 2. Known Minecraft ports: 25565 (Java), 19132 (Bedrock), 25575 (RCON)
	for _, p := range ports {
		if p.PrivatePort == 25565 || p.PublicPort == 25565 {
			return true, "minecraft"
		}
		if p.PrivatePort == 19132 || p.PublicPort == 19132 {
			return true, "minecraft-bedrock"
		}
	}

	// 3. Image and name keyword checks
	targets := []string{strings.ToLower(image)}
	for _, n := range names {
		targets = append(targets, strings.ToLower(n))
	}

	for _, target := range targets {
		for _, kw := range m.gameKeywords {
			if strings.Contains(target, kw) {
				if strings.Contains(kw, "valheim") {
					return true, "valheim"
				}
				return true, "minecraft"
			}
		}
	}

	return false, ""
}

// formatContainerState produces a readable status string from ContainerState.
func formatContainerState(s *types.ContainerState) string {
	if s == nil {
		return "unknown"
	}
	if s.Running {
		if s.Paused {
			return "paused"
		}
		if s.Restarting {
			return "restarting"
		}
		return "running"
	}
	if s.OOMKilled {
		return fmt.Sprintf("oom-killed (exit code %d)", s.ExitCode)
	}
	if s.Dead {
		return "dead"
	}
	return fmt.Sprintf("exited (%d)", s.ExitCode)
}

// sanitizeEnv masks sensitive credentials in environment variables (PASSWORDS, TOKENS, KEYS, SECRETS).
func sanitizeEnv(envs []string) []string {
	sanitized := make([]string, 0, len(envs))
	sensitiveKeys := []string{"PASS", "SECRET", "TOKEN", "KEY", "AUTH", "RCON_PASS"}

	for _, env := range envs {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			upperKey := strings.ToUpper(parts[0])
			isSensitive := false
			for _, s := range sensitiveKeys {
				if strings.Contains(upperKey, s) {
					isSensitive = true
					break
				}
			}
			if isSensitive {
				sanitized = append(sanitized, parts[0]+"=********")
				continue
			}
		}
		sanitized = append(sanitized, env)
	}

	return sanitized
}
