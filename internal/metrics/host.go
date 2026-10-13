package metrics

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/model"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
)

// HostCollector gathers host hardware metrics with optional caching.
type HostCollector struct {
	cacheTTL    time.Duration
	mu          sync.RWMutex
	lastMetrics *model.HostMetrics
	lastFetched time.Time
}

// NewHostCollector creates a new metrics collector instance.
func NewHostCollector(cacheTTL time.Duration) *HostCollector {
	return &HostCollector{
		cacheTTL: cacheTTL,
	}
}

// Collect retrieves current host hardware metrics.
func (c *HostCollector) Collect(ctx context.Context) (*model.HostMetrics, error) {
	c.mu.RLock()
	if c.lastMetrics != nil && time.Since(c.lastFetched) < c.cacheTTL {
		cached := c.lastMetrics
		c.mu.RUnlock()
		return cached, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double check after lock acquisition
	if c.lastMetrics != nil && time.Since(c.lastFetched) < c.cacheTTL {
		return c.lastMetrics, nil
	}

	metrics, err := c.collectFresh(ctx)
	if err != nil {
		return nil, err
	}

	c.lastMetrics = metrics
	c.lastFetched = time.Now()
	return metrics, nil
}

func (c *HostCollector) collectFresh(ctx context.Context) (*model.HostMetrics, error) {
	result := &model.HostMetrics{
		Timestamp: time.Now().UTC(),
	}

	// 1. Host Info
	if hInfo, err := host.InfoWithContext(ctx); err == nil && hInfo != nil {
		result.Host = model.HostInfo{
			Hostname:        hInfo.Hostname,
			OS:              hInfo.OS,
			Platform:        hInfo.Platform,
			PlatformFamily:  hInfo.PlatformFamily,
			PlatformVersion: hInfo.PlatformVersion,
			KernelVersion:   hInfo.KernelVersion,
			KernelArch:      hInfo.KernelArch,
			UptimeSeconds:   hInfo.Uptime,
			BootTime:        hInfo.BootTime,
			HostID:          hInfo.HostID,
		}
	}

	// 2. CPU Metrics
	var totalPct float64
	if totals, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(totals) > 0 {
		totalPct = roundTwoDecimals(totals[0])
	}
	var perCorePct []float64
	if perCores, err := cpu.PercentWithContext(ctx, 0, true); err == nil {
		perCorePct = make([]float64, len(perCores))
		for i, pct := range perCores {
			perCorePct[i] = roundTwoDecimals(pct)
		}
	}
	logicalCores, _ := cpu.CountsWithContext(ctx, true)
	physicalCores, _ := cpu.CountsWithContext(ctx, false)

	var modelName string
	if cpuInfos, err := cpu.InfoWithContext(ctx); err == nil && len(cpuInfos) > 0 {
		modelName = cpuInfos[0].ModelName
	}

	result.CPU = model.CPUMetrics{
		ModelName:      modelName,
		CoresLogical:   logicalCores,
		CoresPhysical:  physicalCores,
		TotalPercent:   totalPct,
		PerCorePercent: perCorePct,
	}

	// 3. Memory Metrics
	if vMem, err := mem.VirtualMemoryWithContext(ctx); err == nil && vMem != nil {
		result.Memory.TotalBytes = vMem.Total
		result.Memory.AvailableBytes = vMem.Available
		result.Memory.UsedBytes = vMem.Used
		result.Memory.UsedPercent = roundTwoDecimals(vMem.UsedPercent)
	}
	if sMem, err := mem.SwapMemoryWithContext(ctx); err == nil && sMem != nil {
		result.Memory.SwapTotalBytes = sMem.Total
		result.Memory.SwapUsedBytes = sMem.Used
		result.Memory.SwapFreeBytes = sMem.Free
		result.Memory.SwapUsedPercent = roundTwoDecimals(sMem.UsedPercent)
	}

	// 4. Disk Partitions
	if partitions, err := disk.PartitionsWithContext(ctx, false); err == nil {
		seenMounts := make(map[string]bool)
		for _, p := range partitions {
			if seenMounts[p.Mountpoint] {
				continue
			}
			seenMounts[p.Mountpoint] = true

			usage, err := disk.UsageWithContext(ctx, p.Mountpoint)
			if err != nil || usage == nil {
				continue
			}

			result.Disks = append(result.Disks, model.DiskPartitionMetrics{
				Device:      p.Device,
				MountPoint:  p.Mountpoint,
				FSType:      p.Fstype,
				TotalBytes:  usage.Total,
				FreeBytes:   usage.Free,
				UsedBytes:   usage.Used,
				UsedPercent: roundTwoDecimals(usage.UsedPercent),
			})
		}
	}

	// 5. System Load (primarily Linux / Unix)
	if lAvg, err := load.AvgWithContext(ctx); err == nil && lAvg != nil {
		result.Load = &model.LoadMetrics{
			Load1:  roundTwoDecimals(lAvg.Load1),
			Load5:  roundTwoDecimals(lAvg.Load5),
			Load15: roundTwoDecimals(lAvg.Load15),
		}
	}

	return result, nil
}

func roundTwoDecimals(val float64) float64 {
	return math.Round(val*100) / 100
}
