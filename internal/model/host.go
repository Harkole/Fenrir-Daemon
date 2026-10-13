package model

import "time"

// HostInfo holds static and boot-time operating system information.
type HostInfo struct {
	Hostname        string `json:"hostname"`
	OS              string `json:"os"`
	Platform        string `json:"platform"`
	PlatformFamily  string `json:"platformFamily"`
	PlatformVersion string `json:"platformVersion"`
	KernelVersion   string `json:"kernelVersion"`
	KernelArch      string `json:"kernelArch"`
	UptimeSeconds   uint64 `json:"uptimeSeconds"`
	BootTime        uint64 `json:"bootTime"`
	HostID          string `json:"hostId"`
}

// CPUMetrics holds processor metrics.
type CPUMetrics struct {
	ModelName     string    `json:"modelName,omitempty"`
	CoresLogical  int       `json:"coresLogical"`
	CoresPhysical int       `json:"coresPhysical"`
	TotalPercent  float64   `json:"totalPercent"`
	PerCorePercent []float64 `json:"perCorePercent"`
}

// MemoryMetrics holds RAM and Swap statistics.
type MemoryMetrics struct {
	TotalBytes       uint64  `json:"totalBytes"`
	AvailableBytes   uint64  `json:"availableBytes"`
	UsedBytes        uint64  `json:"usedBytes"`
	UsedPercent      float64 `json:"usedPercent"`
	SwapTotalBytes   uint64  `json:"swapTotalBytes"`
	SwapUsedBytes    uint64  `json:"swapUsedBytes"`
	SwapFreeBytes    uint64  `json:"swapFreeBytes"`
	SwapUsedPercent  float64 `json:"swapUsedPercent"`
}

// DiskPartitionMetrics holds storage stats for a single mounted filesystem.
type DiskPartitionMetrics struct {
	Device      string  `json:"device"`
	MountPoint  string  `json:"mountPoint"`
	FSType      string  `json:"fsType"`
	TotalBytes  uint64  `json:"totalBytes"`
	FreeBytes   uint64  `json:"freeBytes"`
	UsedBytes   uint64  `json:"usedBytes"`
	UsedPercent float64 `json:"usedPercent"`
}

// LoadMetrics holds system load averages (available on Unix-like hosts).
type LoadMetrics struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

// HostMetrics represents the full aggregate host hardware and system state.
type HostMetrics struct {
	Timestamp   time.Time              `json:"timestamp"`
	Host        HostInfo               `json:"host"`
	CPU         CPUMetrics             `json:"cpu"`
	Memory      MemoryMetrics          `json:"memory"`
	Disks       []DiskPartitionMetrics `json:"disks"`
	Load        *LoadMetrics           `json:"load,omitempty"`
}
