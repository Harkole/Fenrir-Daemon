package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/model"
)

// CreateArchive records metadata for a new stack backup archive.
func (db *DB) CreateArchive(ctx context.Context, a *model.ArchiveMetadata) error {
	if a == nil {
		return fmt.Errorf("archive metadata cannot be nil")
	}

	a.ProjectName = strings.TrimSpace(a.ProjectName)
	if a.ProjectName == "" {
		return fmt.Errorf("archive project name cannot be empty")
	}

	a.ArchivePath = strings.TrimSpace(a.ArchivePath)
	if a.ArchivePath == "" {
		return fmt.Errorf("archive path cannot be empty")
	}

	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	if a.ExpiresAt.IsZero() {
		a.ExpiresAt = a.CreatedAt.Add(90 * 24 * time.Hour)
	}
	a.DaysUntilDeletion = model.CalculateDaysUntilDeletion(a.ExpiresAt)

	query := `
	INSERT INTO archives (project_name, archive_path, size_bytes, created_at, expires_at)
	VALUES (?, ?, ?, ?, ?);
	`

	res, err := db.conn.ExecContext(ctx, query,
		a.ProjectName,
		a.ArchivePath,
		a.SizeBytes,
		a.CreatedAt.Format(time.RFC3339Nano),
		a.ExpiresAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("failed to record archive: %w", err)
	}

	id, _ := res.LastInsertId()
	a.ID = id
	return nil
}

// GetArchive retrieves an archive record by ID.
func (db *DB) GetArchive(ctx context.Context, id int64) (*model.ArchiveMetadata, error) {
	query := `
	SELECT id, project_name, archive_path, size_bytes, created_at, expires_at
	FROM archives
	WHERE id = ?
	LIMIT 1;
	`

	var a model.ArchiveMetadata
	var createdRaw, expiresRaw string

	err := db.conn.QueryRowContext(ctx, query, id).Scan(
		&a.ID,
		&a.ProjectName,
		&a.ArchivePath,
		&a.SizeBytes,
		&createdRaw,
		&expiresRaw,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to get archive %d: %w", id, err)
	}

	cTime, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		cTime, _ = time.Parse(time.RFC3339, createdRaw)
	}
	eTime, err := time.Parse(time.RFC3339Nano, expiresRaw)
	if err != nil {
		eTime, _ = time.Parse(time.RFC3339, expiresRaw)
	}

	a.CreatedAt = cTime.UTC()
	a.ExpiresAt = eTime.UTC()
	a.DaysUntilDeletion = model.CalculateDaysUntilDeletion(a.ExpiresAt)

	return &a, nil
}

// ListArchives retrieves archives, optionally filtered by project name.
func (db *DB) ListArchives(ctx context.Context, projectName string) ([]model.ArchiveMetadata, error) {
	projectName = strings.TrimSpace(projectName)

	var query string
	var args []any

	if projectName != "" {
		query = `
		SELECT id, project_name, archive_path, size_bytes, created_at, expires_at
		FROM archives
		WHERE project_name = ?
		ORDER BY created_at DESC;
		`
		args = append(args, projectName)
	} else {
		query = `
		SELECT id, project_name, archive_path, size_bytes, created_at, expires_at
		FROM archives
		ORDER BY created_at DESC;
		`
	}

	rows, err := db.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list archives: %w", err)
	}
	defer rows.Close()

	var list []model.ArchiveMetadata
	for rows.Next() {
		var a model.ArchiveMetadata
		var createdRaw, expiresRaw string

		if err := rows.Scan(
			&a.ID,
			&a.ProjectName,
			&a.ArchivePath,
			&a.SizeBytes,
			&createdRaw,
			&expiresRaw,
		); err != nil {
			return nil, fmt.Errorf("failed to scan archive row: %w", err)
		}

		cTime, err := time.Parse(time.RFC3339Nano, createdRaw)
		if err != nil {
			cTime, _ = time.Parse(time.RFC3339, createdRaw)
		}
		eTime, err := time.Parse(time.RFC3339Nano, expiresRaw)
		if err != nil {
			eTime, _ = time.Parse(time.RFC3339, expiresRaw)
		}

		a.CreatedAt = cTime.UTC()
		a.ExpiresAt = eTime.UTC()
		a.DaysUntilDeletion = model.CalculateDaysUntilDeletion(a.ExpiresAt)

		list = append(list, a)
	}

	return list, rows.Err()
}

// GetExpiredArchives retrieves all archives whose expires_at is before the given cutoff time.
func (db *DB) GetExpiredArchives(ctx context.Context, now time.Time) ([]model.ArchiveMetadata, error) {
	query := `
	SELECT id, project_name, archive_path, size_bytes, created_at, expires_at
	FROM archives
	WHERE expires_at <= ?
	ORDER BY expires_at ASC;
	`

	rows, err := db.conn.QueryContext(ctx, query, now.Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("failed to query expired archives: %w", err)
	}
	defer rows.Close()

	var list []model.ArchiveMetadata
	for rows.Next() {
		var a model.ArchiveMetadata
		var createdRaw, expiresRaw string

		if err := rows.Scan(
			&a.ID,
			&a.ProjectName,
			&a.ArchivePath,
			&a.SizeBytes,
			&createdRaw,
			&expiresRaw,
		); err != nil {
			return nil, fmt.Errorf("failed to scan expired archive row: %w", err)
		}

		cTime, err := time.Parse(time.RFC3339Nano, createdRaw)
		if err != nil {
			cTime, _ = time.Parse(time.RFC3339, createdRaw)
		}
		eTime, err := time.Parse(time.RFC3339Nano, expiresRaw)
		if err != nil {
			eTime, _ = time.Parse(time.RFC3339, expiresRaw)
		}

		a.CreatedAt = cTime.UTC()
		a.ExpiresAt = eTime.UTC()
		a.DaysUntilDeletion = 0

		list = append(list, a)
	}

	return list, rows.Err()
}

// DeleteArchive removes an archive record from the database.
func (db *DB) DeleteArchive(ctx context.Context, id int64) error {
	query := "DELETE FROM archives WHERE id = ?;"
	res, err := db.conn.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete archive record %d: %w", id, err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return nil
	}
	if rows == 0 {
		return sql.ErrNoRows
	}

	return nil
}
