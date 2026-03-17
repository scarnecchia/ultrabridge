//go:build integration

package tests

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// connectTestDB connects to MariaDB using .dbenv credentials.
// It reads from TEST_DBENV_PATH (default /mnt/supernote/.dbenv).
// Returns nil and skips the test if the database is unreachable.
func connectTestDB(t *testing.T) *sql.DB {
	dbenvPath := os.Getenv("TEST_DBENV_PATH")
	if dbenvPath == "" {
		dbenvPath = "/mnt/supernote/.dbenv"
	}

	// Load .dbenv
	dbenv, err := loadDBEnv(dbenvPath)
	if err != nil {
		t.Skipf("cannot read %s: %v", dbenvPath, err)
	}

	dbHost := "localhost"
	if host := os.Getenv("TEST_DB_HOST"); host != "" {
		dbHost = host
	}

	dbPort := "3306"
	if port := os.Getenv("TEST_DB_PORT"); port != "" {
		dbPort = port
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		dbenv["MYSQL_USER"],
		dbenv["MYSQL_PASSWORD"],
		dbHost,
		dbPort,
		dbenv["MYSQL_DATABASE"])

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Skipf("cannot open database: %v", err)
	}

	// Test connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("database unreachable: %v", err)
	}

	return db
}

// loadDBEnv reads a .dbenv file and returns a map of key=value pairs.
func loadDBEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	env := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if ok {
			env[key] = val
		}
	}
	return env, scanner.Err()
}

// discoverTestUserID discovers the user ID from u_user.
func discoverTestUserID(t *testing.T, db *sql.DB) int64 {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var userID int64
	err := db.QueryRowContext(ctx, "SELECT user_id FROM u_user LIMIT 1").Scan(&userID)
	if err != nil {
		t.Fatalf("discover user_id: %v", err)
	}
	return userID
}

// createTestTask inserts a task directly into the database for test setup.
func createTestTask(t *testing.T, db *sql.DB, userID int64, title string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	now := time.Now().UnixMilli()
	// Generate a simple deterministic task ID using a hash of title+timestamp
	taskID := fmt.Sprintf("test_%d_%d", userID, now)

	_, err := db.ExecContext(ctx, `
		INSERT INTO t_schedule_task
		(task_id, task_list_id, user_id, title, detail, last_modified,
		 recurrence, is_reminder_on, status, importance, due_time, completed_time,
		 links, is_deleted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, 0, userID, title, "", now,
		"", "N", "needsAction", "", nil, now,
		"", "N")
	if err != nil {
		t.Fatalf("create test task: %v", err)
	}

	return taskID
}

// cleanupTestTasks deletes all tasks with titles starting with the given prefix.
func cleanupTestTasks(t *testing.T, db *sql.DB, userID int64, prefix string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := db.ExecContext(ctx, `
		DELETE FROM t_schedule_task
		WHERE user_id = ? AND title LIKE ?`,
		userID, prefix+"%")
	if err != nil {
		t.Logf("cleanup test tasks: %v (non-fatal)", err)
	}
}
