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

// GetSetting retrieves the value of a setting by its key.
func (db *DB) GetSetting(ctx context.Context, key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("setting key cannot be empty")
	}

	query := "SELECT value FROM app_settings WHERE key = ?;"
	var value string
	err := db.conn.QueryRowContext(ctx, query, key).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		return "", fmt.Errorf("failed to get setting %q: %w", key, err)
	}

	return value, nil
}

// SetSetting stores or updates an application setting.
func (db *DB) SetSetting(ctx context.Context, key, value string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("setting key cannot be empty")
	}

	query := `
	INSERT INTO app_settings (key, value, updated_at)
	VALUES (?, ?, ?)
	ON CONFLICT(key) DO UPDATE SET
		value = excluded.value,
		updated_at = excluded.updated_at;
	`

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.conn.ExecContext(ctx, query, key, value, now)
	if err != nil {
		return fmt.Errorf("failed to save setting %q: %w", key, err)
	}

	return nil
}

// GetAllSettings retrieves all key-value application settings.
func (db *DB) GetAllSettings(ctx context.Context) (map[string]string, error) {
	query := "SELECT key, value FROM app_settings ORDER BY key ASC;"
	rows, err := db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query settings: %w", err)
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("failed to scan setting row: %w", err)
		}
		settings[k] = v
	}

	return settings, rows.Err()
}

// ListSettingsDetails retrieves all settings with their last updated timestamp.
func (db *DB) ListSettingsDetails(ctx context.Context) ([]model.AppSetting, error) {
	query := "SELECT key, value, updated_at FROM app_settings ORDER BY key ASC;"
	rows, err := db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query settings: %w", err)
	}
	defer rows.Close()

	var list []model.AppSetting
	for rows.Next() {
		var s model.AppSetting
		var updatedRaw string
		if err := rows.Scan(&s.Key, &s.Value, &updatedRaw); err != nil {
			return nil, fmt.Errorf("failed to scan setting row: %w", err)
		}
		t, err := time.Parse(time.RFC3339Nano, updatedRaw)
		if err != nil {
			t, _ = time.Parse(time.RFC3339, updatedRaw)
		}
		s.UpdatedAt = t.UTC()
		list = append(list, s)
	}

	return list, rows.Err()
}

// DeleteSetting removes an application setting by key.
func (db *DB) DeleteSetting(ctx context.Context, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("setting key cannot be empty")
	}

	query := "DELETE FROM app_settings WHERE key = ?;"
	res, err := db.conn.ExecContext(ctx, query, key)
	if err != nil {
		return fmt.Errorf("failed to delete setting %q: %w", key, err)
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
