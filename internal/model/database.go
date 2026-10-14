package model

import "time"

// ContainerMetricRecord represents a persisted container resource usage snapshot.
type ContainerMetricRecord struct {
	ID              int64     `json:"id"`
	ContainerID     string    `json:"containerId"`
	ContainerName   string    `json:"containerName"`
	CPUPercent      float64   `json:"cpuPercent"`
	MemoryUsage     uint64    `json:"memoryUsageBytes"`
	MemoryLimit     uint64    `json:"memoryLimitBytes"`
	MemoryPercent   float64   `json:"memoryPercent"`
	NetworkRxBytes  uint64    `json:"networkRxBytes"`
	NetworkTxBytes  uint64    `json:"networkTxBytes"`
	BlockReadBytes  uint64    `json:"blockReadBytes"`
	BlockWriteBytes uint64    `json:"blockWriteBytes"`
	RecordedAt      time.Time `json:"recordedAt"`
}

// AppSetting represents a key-value application setting.
type AppSetting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// APIKeyMetadata represents public metadata for a stored API key.
type APIKeyMetadata struct {
	ID        int64     `json:"id"`
	Prefix    string    `json:"prefix"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	Revoked   bool      `json:"revoked"`
}
