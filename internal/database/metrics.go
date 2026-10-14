package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/model"
)

// RecordContainerMetric inserts a new metric snapshot for a container.
func (db *DB) RecordContainerMetric(ctx context.Context, containerID, containerName string, stats *model.ContainerResourceStats, recordedAt time.Time) error {
	if stats == nil {
		return fmt.Errorf("cannot record nil container stats")
	}
	if recordedAt.IsZero() {
		recordedAt = time.Now().UTC()
	}

	query := `
	INSERT INTO container_metrics (
		container_id, container_name, cpu_percent,
		memory_usage_bytes, memory_limit_bytes, memory_percent,
		network_rx_bytes, network_tx_bytes, block_read_bytes, block_write_bytes,
		recorded_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`

	_, err := db.conn.ExecContext(ctx, query,
		containerID,
		containerName,
		stats.CPUPercent,
		stats.MemoryUsage,
		stats.MemoryLimit,
		stats.MemoryPercent,
		stats.NetworkRxBytes,
		stats.NetworkTxBytes,
		stats.BlockReadBytes,
		stats.BlockWriteBytes,
		recordedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("failed to insert container metric: %w", err)
	}

	return nil
}

// GetContainerMetricsHistory queries historical metrics for a container matching ID, prefix, or name.
func (db *DB) GetContainerMetricsHistory(ctx context.Context, nameOrID string, limit int, since *time.Time) ([]model.ContainerMetricRecord, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	cleanTarget := strings.TrimPrefix(strings.TrimSpace(nameOrID), "/")

	var conditions []string
	var args []any

	// Match container by exact ID, ID prefix (common in Docker), or container name
	conditions = append(conditions, "(container_id = ? OR container_id LIKE ? OR container_name = ?)")
	args = append(args, cleanTarget, cleanTarget+"%", cleanTarget)

	if since != nil && !since.IsZero() {
		conditions = append(conditions, "recorded_at >= ?")
		args = append(args, since.Format(time.RFC3339Nano))
	}

	args = append(args, limit)

	query := fmt.Sprintf(`
	SELECT id, container_id, container_name, cpu_percent,
	       memory_usage_bytes, memory_limit_bytes, memory_percent,
	       network_rx_bytes, network_tx_bytes, block_read_bytes, block_write_bytes,
	       recorded_at
	FROM container_metrics
	WHERE %s
	ORDER BY recorded_at DESC
	LIMIT ?;
	`, strings.Join(conditions, " AND "))

	rows, err := db.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query container metrics history: %w", err)
	}
	defer rows.Close()

	var records []model.ContainerMetricRecord
	for rows.Next() {
		var rec model.ContainerMetricRecord
		var recordedAtRaw string

		err := rows.Scan(
			&rec.ID,
			&rec.ContainerID,
			&rec.ContainerName,
			&rec.CPUPercent,
			&rec.MemoryUsage,
			&rec.MemoryLimit,
			&rec.MemoryPercent,
			&rec.NetworkRxBytes,
			&rec.NetworkTxBytes,
			&rec.BlockReadBytes,
			&rec.BlockWriteBytes,
			&recordedAtRaw,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan container metric row: %w", err)
		}

		t, err := time.Parse(time.RFC3339Nano, recordedAtRaw)
		if err != nil {
			// Fallback standard RFC3339 or ISO-8601
			t, _ = time.Parse(time.RFC3339, recordedAtRaw)
		}
		rec.RecordedAt = t.UTC()

		records = append(records, rec)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error during container metrics iteration: %w", err)
	}

	return records, nil
}

// PruneMetrics removes metric records older than a specified duration to prevent unbounded database growth.
func (db *DB) PruneMetrics(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	query := "DELETE FROM container_metrics WHERE recorded_at < ?;"

	res, err := db.conn.ExecContext(ctx, query, cutoff.Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("failed to prune old container metrics: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return rows, nil
}

// GetLatestContainerMetric retrieves the most recent recorded metric for a container.
func (db *DB) GetLatestContainerMetric(ctx context.Context, nameOrID string) (*model.ContainerMetricRecord, error) {
	records, err := db.GetContainerMetricsHistory(ctx, nameOrID, 1, nil)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, sql.ErrNoRows
	}
	return &records[0], nil
}
