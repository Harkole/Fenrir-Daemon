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

// CreateProject registers a new Docker Compose project in the database.
func (db *DB) CreateProject(ctx context.Context, p *model.Project) error {
	if p == nil {
		return fmt.Errorf("project cannot be nil")
	}

	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return fmt.Errorf("project name cannot be empty")
	}

	p.DirectoryPath = strings.TrimSpace(p.DirectoryPath)
	if p.DirectoryPath == "" {
		return fmt.Errorf("project directory path cannot be empty")
	}

	if p.ComposeFile == "" {
		p.ComposeFile = "compose.yml"
	}

	if p.Status == "" {
		p.Status = "created"
	}

	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now

	query := `
	INSERT INTO projects (name, directory_path, compose_file, description, status, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?);
	`

	res, err := db.conn.ExecContext(ctx, query,
		p.Name,
		p.DirectoryPath,
		p.ComposeFile,
		p.Description,
		p.Status,
		p.CreatedAt.Format(time.RFC3339Nano),
		p.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("failed to insert project %q: %w", p.Name, err)
	}

	id, _ := res.LastInsertId()
	p.ID = id
	return nil
}

// GetProject retrieves a project by its unique name.
func (db *DB) GetProject(ctx context.Context, name string) (*model.Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("project name cannot be empty")
	}

	query := `
	SELECT id, name, directory_path, compose_file, description, status, created_at, updated_at
	FROM projects
	WHERE name = ?
	LIMIT 1;
	`

	var p model.Project
	var createdRaw, updatedRaw string

	err := db.conn.QueryRowContext(ctx, query, name).Scan(
		&p.ID,
		&p.Name,
		&p.DirectoryPath,
		&p.ComposeFile,
		&p.Description,
		&p.Status,
		&createdRaw,
		&updatedRaw,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to query project %q: %w", name, err)
	}

	cTime, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		cTime, _ = time.Parse(time.RFC3339, createdRaw)
	}
	uTime, err := time.Parse(time.RFC3339Nano, updatedRaw)
	if err != nil {
		uTime, _ = time.Parse(time.RFC3339, updatedRaw)
	}
	p.CreatedAt = cTime.UTC()
	p.UpdatedAt = uTime.UTC()

	return &p, nil
}

// ListProjects retrieves all registered Docker Compose projects.
func (db *DB) ListProjects(ctx context.Context) ([]model.Project, error) {
	query := `
	SELECT id, name, directory_path, compose_file, description, status, created_at, updated_at
	FROM projects
	ORDER BY name ASC;
	`

	rows, err := db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query projects: %w", err)
	}
	defer rows.Close()

	var projects []model.Project
	for rows.Next() {
		var p model.Project
		var createdRaw, updatedRaw string

		if err := rows.Scan(
			&p.ID,
			&p.Name,
			&p.DirectoryPath,
			&p.ComposeFile,
			&p.Description,
			&p.Status,
			&createdRaw,
			&updatedRaw,
		); err != nil {
			return nil, fmt.Errorf("failed to scan project row: %w", err)
		}

		cTime, err := time.Parse(time.RFC3339Nano, createdRaw)
		if err != nil {
			cTime, _ = time.Parse(time.RFC3339, createdRaw)
		}
		uTime, err := time.Parse(time.RFC3339Nano, updatedRaw)
		if err != nil {
			uTime, _ = time.Parse(time.RFC3339, updatedRaw)
		}
		p.CreatedAt = cTime.UTC()
		p.UpdatedAt = uTime.UTC()

		projects = append(projects, p)
	}

	return projects, rows.Err()
}

// UpdateProject updates an existing project's metadata.
func (db *DB) UpdateProject(ctx context.Context, p *model.Project) error {
	if p == nil {
		return fmt.Errorf("project cannot be nil")
	}

	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return fmt.Errorf("project name cannot be empty")
	}

	p.UpdatedAt = time.Now().UTC()

	query := `
	UPDATE projects
	SET directory_path = ?, compose_file = ?, description = ?, status = ?, updated_at = ?
	WHERE name = ?;
	`

	res, err := db.conn.ExecContext(ctx, query,
		p.DirectoryPath,
		p.ComposeFile,
		p.Description,
		p.Status,
		p.UpdatedAt.Format(time.RFC3339Nano),
		p.Name,
	)
	if err != nil {
		return fmt.Errorf("failed to update project %q: %w", p.Name, err)
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

// UpdateProjectStatus updates only the runtime status and timestamp for a project.
func (db *DB) UpdateProjectStatus(ctx context.Context, name, status string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("project name cannot be empty")
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	query := "UPDATE projects SET status = ?, updated_at = ? WHERE name = ?;"

	res, err := db.conn.ExecContext(ctx, query, status, now, name)
	if err != nil {
		return fmt.Errorf("failed to update project status %q: %w", name, err)
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

// DeleteProject deletes a project record by name.
func (db *DB) DeleteProject(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("project name cannot be empty")
	}

	query := "DELETE FROM projects WHERE name = ?;"
	res, err := db.conn.ExecContext(ctx, query, name)
	if err != nil {
		return fmt.Errorf("failed to delete project %q: %w", name, err)
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
