package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Harkole/fenrir-daemon/internal/model"
)

// CheckPortConflicts checks whether any of the requested ports conflict with existing allocations
// owned by other projects.
func (db *DB) CheckPortConflicts(ctx context.Context, projectName string, ports []model.ProjectPort) ([]model.PortConflict, error) {
	projectName = strings.TrimSpace(projectName)
	var conflicts []model.PortConflict

	query := `
	SELECT project_name 
	FROM project_ports 
	WHERE host_port = ? AND LOWER(protocol) = LOWER(?) AND project_name != ?
	LIMIT 1;
	`

	for _, p := range ports {
		proto := strings.ToLower(strings.TrimSpace(p.Protocol))
		if proto == "" {
			proto = "tcp"
		}

		var owner string
		err := db.conn.QueryRowContext(ctx, query, p.HostPort, proto, projectName).Scan(&owner)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return nil, fmt.Errorf("failed checking port conflict for %d/%s: %w", p.HostPort, proto, err)
		}

		conflicts = append(conflicts, model.PortConflict{
			HostPort:    p.HostPort,
			Protocol:    proto,
			AllocatedTo: owner,
		})
	}

	return conflicts, nil
}

// AllocateProjectPorts atomically replaces all ports allocated to projectName with the provided list.
func (db *DB) AllocateProjectPorts(ctx context.Context, projectName string, ports []model.ProjectPort) error {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return fmt.Errorf("project name cannot be empty")
	}

	tx, err := db.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// First remove any existing ports allocated to this project
	_, err = tx.ExecContext(ctx, "DELETE FROM project_ports WHERE project_name = ?;", projectName)
	if err != nil {
		return fmt.Errorf("failed to clear existing ports for %q: %w", projectName, err)
	}

	insertQuery := `
	INSERT INTO project_ports (project_name, host_port, container_port, protocol)
	VALUES (?, ?, ?, ?);
	`

	for _, p := range ports {
		proto := strings.ToLower(strings.TrimSpace(p.Protocol))
		if proto == "" {
			proto = "tcp"
		}

		_, err := tx.ExecContext(ctx, insertQuery, projectName, p.HostPort, p.ContainerPort, proto)
		if err != nil {
			return fmt.Errorf("failed to allocate port %d/%s for %q: %w", p.HostPort, proto, projectName, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit port allocations: %w", err)
	}

	return nil
}

// ReleaseProjectPorts frees all ports allocated to projectName.
func (db *DB) ReleaseProjectPorts(ctx context.Context, projectName string) error {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return fmt.Errorf("project name cannot be empty")
	}

	_, err := db.conn.ExecContext(ctx, "DELETE FROM project_ports WHERE project_name = ?;", projectName)
	if err != nil {
		return fmt.Errorf("failed to release ports for project %q: %w", projectName, err)
	}

	return nil
}

// GetProjectPorts returns all ports allocated to a specific project.
func (db *DB) GetProjectPorts(ctx context.Context, projectName string) ([]model.ProjectPort, error) {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return nil, fmt.Errorf("project name cannot be empty")
	}

	query := `
	SELECT id, project_name, host_port, container_port, protocol
	FROM project_ports
	WHERE project_name = ?
	ORDER BY host_port ASC;
	`

	rows, err := db.conn.QueryContext(ctx, query, projectName)
	if err != nil {
		return nil, fmt.Errorf("failed to query ports for %q: %w", projectName, err)
	}
	defer rows.Close()

	var ports []model.ProjectPort
	for rows.Next() {
		var p model.ProjectPort
		if err := rows.Scan(&p.ID, &p.ProjectName, &p.HostPort, &p.ContainerPort, &p.Protocol); err != nil {
			return nil, fmt.Errorf("failed to scan port row: %w", err)
		}
		ports = append(ports, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading port rows: %w", err)
	}

	return ports, nil
}

// ListActivePorts returns all allocated ports across all projects.
func (db *DB) ListActivePorts(ctx context.Context) ([]model.ProjectPort, error) {
	query := `
	SELECT id, project_name, host_port, container_port, protocol
	FROM project_ports
	ORDER BY host_port ASC, protocol ASC;
	`

	rows, err := db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query active ports: %w", err)
	}
	defer rows.Close()

	var ports []model.ProjectPort
	for rows.Next() {
		var p model.ProjectPort
		if err := rows.Scan(&p.ID, &p.ProjectName, &p.HostPort, &p.ContainerPort, &p.Protocol); err != nil {
			return nil, fmt.Errorf("failed to scan active port row: %w", err)
		}
		ports = append(ports, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading active port rows: %w", err)
	}

	return ports, nil
}
