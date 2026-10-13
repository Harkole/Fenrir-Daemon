package metrics

import (
	"context"
	"testing"
	"time"
)

func TestHostCollectorCollect(t *testing.T) {
	collector := NewHostCollector(1 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	metrics, err := collector.Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error collecting host metrics: %v", err)
	}

	if metrics == nil {
		t.Fatalf("expected non-nil metrics")
	}

	// Host OS should not be empty
	if metrics.Host.OS == "" {
		t.Errorf("expected non-empty Host OS")
	}

	// CPU cores should be greater than 0
	if metrics.CPU.CoresLogical <= 0 {
		t.Errorf("expected logical cores > 0, got %d", metrics.CPU.CoresLogical)
	}

	// Memory total should be greater than 0
	if metrics.Memory.TotalBytes <= 0 {
		t.Errorf("expected memory total bytes > 0, got %d", metrics.Memory.TotalBytes)
	}

	// Test caching behavior: second call should return cached instance within TTL
	metrics2, err := collector.Collect(ctx)
	if err != nil {
		t.Fatalf("unexpected error on second collect: %v", err)
	}
	if metrics != metrics2 {
		t.Errorf("expected cached pointer instance within cacheTTL")
	}
}
