package model

import "time"

// PortMapping represents an exposed container port and its host binding.
type PortMapping struct {
	IP            string `json:"ip,omitempty"`
	PrivatePort   uint16 `json:"privatePort"`
	PublicPort    uint16 `json:"publicPort,omitempty"`
	Type          string `json:"type"` // tcp or udp
}

// ContainerResourceStats holds CPU, memory, and network usage metrics.
type ContainerResourceStats struct {
	CPUPercent     float64 `json:"cpuPercent"`
	MemoryUsage    uint64  `json:"memoryUsageBytes"`
	MemoryLimit    uint64  `json:"memoryLimitBytes"`
	MemoryPercent  float64 `json:"memoryPercent"`
	NetworkRxBytes uint64  `json:"networkRxBytes"`
	NetworkTxBytes uint64  `json:"networkTxBytes"`
	BlockReadBytes uint64  `json:"blockReadBytes"`
	BlockWriteBytes uint64 `json:"blockWriteBytes"`
}

// FenrirMetadata holds Fenrir-specific label configuration parsed from container labels.
type FenrirMetadata struct {
	Enabled     bool   `json:"enabled"`
	Type        string `json:"type,omitempty"`        // e.g. "minecraft", "database", "webapp"
	DisplayName string `json:"displayName,omitempty"` // e.g. "Survival SMP", "Postgres Primary"
}

// ContainerSummary holds high-level status for container listing.
type ContainerSummary struct {
	ID           string                  `json:"id"`
	Names        []string                `json:"names"`
	Image        string                  `json:"image"`
	ImageID      string                  `json:"imageId"`
	Command      string                  `json:"command"`
	CreatedAt    time.Time               `json:"createdAt"`
	State        string                  `json:"state"`  // "running", "exited", "paused", etc.
	Status       string                  `json:"status"` // e.g. "Up 2 hours", "Exited (0) 10 minutes ago"
	Ports        []PortMapping           `json:"ports"`
	Labels       map[string]string       `json:"labels"`
	Fenrir       FenrirMetadata          `json:"fenrir"`
	IsGameServer bool                    `json:"isGameServer"`
	GameType     string                  `json:"gameType,omitempty"` // e.g. "minecraft"
	Stats        *ContainerResourceStats `json:"stats,omitempty"`
}

// VolumeMount represents a bind mount or volume attached to a container.
type VolumeMount struct {
	Type        string `json:"type"` // bind, volume, tmpfs
	Name        string `json:"name,omitempty"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Driver      string `json:"driver,omitempty"`
	Mode        string `json:"mode"`
	RW          bool   `json:"rw"`
}

// ContainerDetail holds full inspection data for an individual container.
type ContainerDetail struct {
	ContainerSummary
	Platform      string            `json:"platform"`
	RestartCount  int               `json:"restartCount"`
	StartedAt     time.Time         `json:"startedAt,omitempty"`
	FinishedAt    time.Time         `json:"finishedAt,omitempty"`
	HealthStatus  string            `json:"healthStatus,omitempty"` // "healthy", "unhealthy", "starting", "none"
	Environment   []string          `json:"environment,omitempty"`
	Mounts        []VolumeMount     `json:"mounts"`
	Networks      []string          `json:"networks"`
	IPAddress     string            `json:"ipAddress,omitempty"`
}
