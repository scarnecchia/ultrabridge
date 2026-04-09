package mcpauth

import (
	"context"
	"database/sql"
	"fmt"
)

// TokenInfo holds metadata about a token for listing. Raw token is never stored.
type TokenInfo struct {
	TokenHash string // full SHA-256 hex (used as ID for revocation)
	Label     string
	CreatedAt int64 // millisecond UTC unix timestamp
	LastUsed  int64 // 0 = never used
}

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
