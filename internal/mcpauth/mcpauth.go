package mcpauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// TokenInfo holds metadata about a token for listing. Raw token is never stored.
type TokenInfo struct {
	TokenHash string // full SHA-256 hex (used as ID for revocation)
	Label     string
	CreatedAt int64 // millisecond UTC unix timestamp
	LastUsed  int64 // 0 = never used
}

// ErrInvalidToken is returned when a token is not found or has been revoked.
var ErrInvalidToken = errors.New("invalid or revoked token")

// Migrate creates the mcp_tokens table if it does not exist.
// Safe to call on every startup (idempotent).
func Migrate(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS mcp_tokens (
			token_hash TEXT PRIMARY KEY,
			label      TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			last_used  INTEGER NOT NULL DEFAULT 0
		)`,
	}
	for i, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("mcpauth migration statement %d: %w", i, err)
		}
	}
	return nil
}

// CreateToken generates a new bearer token, stores its SHA-256 hash, and
// returns the raw token (displayed once) and the hash (used as identifier).
func CreateToken(ctx context.Context, db *sql.DB, label string) (rawToken, tokenHash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("mcpauth generate: %w", err)
	}
	rawToken = base64.RawURLEncoding.EncodeToString(b)

	h := sha256.Sum256([]byte(rawToken))
	tokenHash = hex.EncodeToString(h[:])

	now := time.Now().UnixMilli()
	_, err = db.ExecContext(ctx,
		`INSERT INTO mcp_tokens (token_hash, label, created_at) VALUES (?, ?, ?)`,
		tokenHash, label, now,
	)
	if err != nil {
		return "", "", fmt.Errorf("mcpauth store: %w", err)
	}
	return rawToken, tokenHash, nil
}

// ValidateToken hashes the raw token and checks the database.
// Returns the token's label on success. Updates last_used timestamp.
// Returns ErrInvalidToken if the token is not found or was revoked.
func ValidateToken(ctx context.Context, db *sql.DB, rawToken string) (string, error) {
	h := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(h[:])

	var label string
	err := db.QueryRowContext(ctx,
		`SELECT label FROM mcp_tokens WHERE token_hash = ?`, tokenHash,
	).Scan(&label)
	if err == sql.ErrNoRows {
		return "", ErrInvalidToken
	}
	if err != nil {
		return "", fmt.Errorf("mcpauth validate: %w", err)
	}

	now := time.Now().UnixMilli()
	_, _ = db.ExecContext(ctx,
		`UPDATE mcp_tokens SET last_used = ? WHERE token_hash = ?`,
		now, tokenHash,
	)
	return label, nil
}

// ListTokens returns metadata for all tokens, ordered by creation time (newest first).
func ListTokens(ctx context.Context, db *sql.DB) ([]TokenInfo, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT token_hash, label, created_at, last_used FROM mcp_tokens ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("mcpauth list: %w", err)
	}
	defer rows.Close()

	var tokens []TokenInfo
	for rows.Next() {
		var t TokenInfo
		if err := rows.Scan(&t.TokenHash, &t.Label, &t.CreatedAt, &t.LastUsed); err != nil {
			return nil, fmt.Errorf("mcpauth scan: %w", err)
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// RevokeToken deletes a token by its hash. Idempotent — no error if already gone.
func RevokeToken(ctx context.Context, db *sql.DB, tokenHash string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM mcp_tokens WHERE token_hash = ?`, tokenHash,
	)
	if err != nil {
		return fmt.Errorf("mcpauth revoke: %w", err)
	}
	return nil
}
