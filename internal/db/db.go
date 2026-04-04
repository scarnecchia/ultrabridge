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

// ResolveUserID returns the user ID to operate on.
// If explicitID is non-zero, it verifies that user exists in u_user.
// If explicitID is zero, it auto-discovers — but fails if multiple users exist
// (set UB_USER_ID to pick one).
func ResolveUserID(ctx context.Context, db *sql.DB, explicitID int64) (int64, error) {
	if explicitID != 0 {
		var exists int64
		err := db.QueryRowContext(ctx, "SELECT user_id FROM u_user WHERE user_id = ?", explicitID).Scan(&exists)
		if err != nil {
			return 0, fmt.Errorf("user_id %d not found in u_user: %w", explicitID, err)
		}
		return exists, nil
	}

	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM u_user").Scan(&count); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	if count == 0 {
		return 0, fmt.Errorf("no users found in u_user — has the Supernote device synced yet?")
	}
	if count > 1 {
		return 0, fmt.Errorf("multiple users found in u_user (%d) — set UB_USER_ID to specify which user", count)
	}

	var userID int64
	if err := db.QueryRowContext(ctx, "SELECT user_id FROM u_user LIMIT 1").Scan(&userID); err != nil {
		return 0, fmt.Errorf("discover user_id: %w", err)
	}
	return userID, nil
}

// ResolveEquipmentNo returns the device serial number for a given user ID.
func ResolveEquipmentNo(ctx context.Context, database *sql.DB, userID int64) (string, error) {
	var equipNo string
	err := database.QueryRowContext(ctx,
		"SELECT equipment_number FROM e_user_equipment WHERE user_id = ? LIMIT 1",
		userID).Scan(&equipNo)
	if err != nil {
		return "", fmt.Errorf("resolve equipment_number for user %d: %w", userID, err)
	}
	return equipNo, nil
}
