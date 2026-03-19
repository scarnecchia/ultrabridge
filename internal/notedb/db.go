package notedb

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Open opens (or creates) the SQLite database at path, applies schema migrations,
// and returns the connection pool. Enables WAL mode and foreign keys.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("notedb open: %w", err)
	}
	// SQLite performs best with a single writer.
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("notedb ping: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("notedb migrate: %w", err)
	}
	return db, nil
}
