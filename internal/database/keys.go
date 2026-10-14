package database

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/model"
)

// HashKey computes a SHA-256 hex digest for an API key.
func HashKey(rawKey string) string {
	sum := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(sum[:])
}

// GenerateRandomKey generates a cryptographically secure API key with the given prefix.
func GenerateRandomKey(prefix string) (string, error) {
	if prefix == "" {
		prefix = "fn"
	}
	prefix = strings.TrimSuffix(prefix, "_")

	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(bytes)), nil
}

// CreateAPIKey stores a hashed API key with an associated name/description.
func (db *DB) CreateAPIKey(ctx context.Context, name, rawKey string) (*model.APIKeyMetadata, error) {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" {
		return nil, fmt.Errorf("api key cannot be empty")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = "default"
	}

	keyHash := HashKey(rawKey)

	prefix := rawKey
	if len(rawKey) > 8 {
		prefix = rawKey[:8] + "..."
	}

	now := time.Now().UTC()
	query := `
	INSERT INTO api_keys (key_hash, prefix, name, created_at, revoked)
	VALUES (?, ?, ?, ?, 0);
	`

	res, err := db.conn.ExecContext(ctx, query, keyHash, prefix, name, now.Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("failed to store api key: %w", err)
	}

	id, _ := res.LastInsertId()

	return &model.APIKeyMetadata{
		ID:        id,
		Prefix:    prefix,
		Name:      name,
		CreatedAt: now,
		Revoked:   false,
	}, nil
}

// ValidateAPIKey verifies whether the provided raw key exists and is not revoked.
func (db *DB) ValidateAPIKey(ctx context.Context, rawKey string) (bool, error) {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" {
		return false, nil
	}

	keyHash := HashKey(rawKey)

	query := "SELECT key_hash, revoked FROM api_keys WHERE key_hash = ? AND revoked = 0 LIMIT 1;"
	var storedHash string
	var revoked int
	err := db.conn.QueryRowContext(ctx, query, keyHash).Scan(&storedHash, &revoked)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("failed to validate api key: %w", err)
	}

	if revoked != 0 {
		return false, nil
	}

	// Constant-time comparison
	match := subtle.ConstantTimeCompare([]byte(keyHash), []byte(storedHash)) == 1
	return match, nil
}

// ListAPIKeys retrieves metadata for all generated API keys.
func (db *DB) ListAPIKeys(ctx context.Context) ([]model.APIKeyMetadata, error) {
	query := "SELECT id, prefix, name, created_at, revoked FROM api_keys ORDER BY id ASC;"
	rows, err := db.conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query api keys: %w", err)
	}
	defer rows.Close()

	var keys []model.APIKeyMetadata
	for rows.Next() {
		var k model.APIKeyMetadata
		var createdRaw string
		var revokedInt int
		if err := rows.Scan(&k.ID, &k.Prefix, &k.Name, &createdRaw, &revokedInt); err != nil {
			return nil, fmt.Errorf("failed to scan api key: %w", err)
		}
		t, err := time.Parse(time.RFC3339Nano, createdRaw)
		if err != nil {
			t, _ = time.Parse(time.RFC3339, createdRaw)
		}
		k.CreatedAt = t.UTC()
		k.Revoked = revokedInt != 0
		keys = append(keys, k)
	}

	return keys, rows.Err()
}

// RevokeAPIKey marks an API key as revoked by ID or prefix.
func (db *DB) RevokeAPIKey(ctx context.Context, idOrPrefix string) error {
	idOrPrefix = strings.TrimSpace(idOrPrefix)
	if idOrPrefix == "" {
		return fmt.Errorf("identifier cannot be empty")
	}

	query := "UPDATE api_keys SET revoked = 1 WHERE prefix = ? OR CAST(id AS TEXT) = ?;"
	res, err := db.conn.ExecContext(ctx, query, idOrPrefix, idOrPrefix)
	if err != nil {
		return fmt.Errorf("failed to revoke api key %q: %w", idOrPrefix, err)
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
