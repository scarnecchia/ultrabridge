package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Connect opens a connection pool to MariaDB and verifies connectivity.
func Connect(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("db ping: %w", err)
	}
	return db, nil
}

// DiscoverUserID returns the single user's ID from u_user.
// The Supernote Private Cloud is single-user; this fails if no users exist.
func DiscoverUserID(ctx context.Context, db *sql.DB) (int64, error) {
	var userID int64
	err := db.QueryRowContext(ctx, "SELECT user_id FROM u_user LIMIT 1").Scan(&userID)
	if err != nil {
		return 0, fmt.Errorf("discover user_id: %w", err)
	}
	return userID, nil
}
