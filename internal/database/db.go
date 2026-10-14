package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DB encapsulates the SQLite database connection pool and operations.
type DB struct {
	conn *sql.DB
	path string
}

const schema = `
CREATE TABLE IF NOT EXISTS container_metrics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    container_id TEXT NOT NULL,
    container_name TEXT NOT NULL,
    cpu_percent REAL NOT NULL,
    memory_usage_bytes INTEGER NOT NULL,
    memory_limit_bytes INTEGER NOT NULL,
    memory_percent REAL NOT NULL,
    network_rx_bytes INTEGER NOT NULL,
    network_tx_bytes INTEGER NOT NULL,
    block_read_bytes INTEGER NOT NULL,
    block_write_bytes INTEGER NOT NULL,
    recorded_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_metrics_container_time ON container_metrics(container_id, recorded_at);
CREATE INDEX IF NOT EXISTS idx_metrics_name_time ON container_metrics(container_name, recorded_at);
CREATE INDEX IF NOT EXISTS idx_metrics_recorded_at ON container_metrics(recorded_at);

CREATE TABLE IF NOT EXISTS app_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS api_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key_hash TEXT NOT NULL UNIQUE,
    prefix TEXT NOT NULL,
    name TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    revoked INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);

CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    directory_path TEXT NOT NULL,
    compose_file TEXT NOT NULL DEFAULT 'compose.yml',
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'created',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_projects_name ON projects(name);

CREATE TABLE IF NOT EXISTS archives (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_name TEXT NOT NULL,
    archive_path TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    created_at DATETIME NOT NULL,
    expires_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_archives_project ON archives(project_name);
CREATE INDEX IF NOT EXISTS idx_archives_expires ON archives(expires_at);

CREATE TABLE IF NOT EXISTS project_ports (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_name TEXT NOT NULL,
    host_port INTEGER NOT NULL,
    container_port INTEGER NOT NULL,
    protocol TEXT NOT NULL DEFAULT 'tcp',
    UNIQUE(host_port, protocol)
);

CREATE INDEX IF NOT EXISTS idx_project_ports_project ON project_ports(project_name);
CREATE INDEX IF NOT EXISTS idx_project_ports_lookup ON project_ports(host_port, protocol);
`

// Open initializes and migrates the SQLite database at the specified path.
func Open(path string) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		path = "fenrir.db"
	}

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database at %q: %w", path, err)
	}

	// Optimize connection settings for SQLite
	conn.SetMaxOpenConns(10)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(1 * time.Hour)

	// Apply WAL mode and busy timeout pragmas
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA foreign_keys=ON;",
	}
	for _, pragma := range pragmas {
		if _, err := conn.Exec(pragma); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("failed to apply pragma %q: %w", pragma, err)
		}
	}

	// Run initial schema migration
	if _, err := conn.Exec(schema); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to execute sqlite schema migration: %w", err)
	}

	return &DB{
		conn: conn,
		path: path,
	}, nil
}

// Close closes the underlying SQLite database connection.
func (db *DB) Close() error {
	if db.conn != nil {
		return db.conn.Close()
	}
	return nil
}

// Ping checks whether the database is accessible.
func (db *DB) Ping(ctx context.Context) error {
	if db.conn == nil {
		return fmt.Errorf("database connection is nil")
	}
	return db.conn.PingContext(ctx)
}

// Path returns the database filepath.
func (db *DB) Path() string {
	return db.path
}
