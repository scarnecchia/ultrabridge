package notedb

import (
	"context"
	"database/sql"
)

// GetSetting reads a setting value by key. Returns empty string if not set.
func GetSetting(ctx context.Context, db *sql.DB, key string) (string, error) {
	var val string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

// SetSetting writes a setting value. Creates or updates.
func SetSetting(ctx context.Context, db *sql.DB, key, value string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}
