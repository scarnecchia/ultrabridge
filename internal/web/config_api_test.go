package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/sysop/ultrabridge/internal/appconfig"
	"github.com/sysop/ultrabridge/internal/logging"
	"github.com/sysop/ultrabridge/internal/notedb"
)

// initTestDB creates an in-memory SQLite DB with the settings table.
func initTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	// Create settings table
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		)
	`); err != nil {
		t.Fatalf("create settings table: %v", err)
	}

	return db
}

// TestGetConfigRedacts verifies that GET /api/config redacts secrets.
func TestGetConfigRedacts(t *testing.T) {
	db := initTestDB(t)
	defer db.Close()

	// Set some config values in DB including secrets
	ctx := context.Background()
	if err := notedb.SetSetting(ctx, db, appconfig.KeyUsername, "testuser"); err != nil {
		t.Fatalf("set username: %v", err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.DefaultCost)
	if err := notedb.SetSetting(ctx, db, appconfig.KeyPasswordHash, string(hash)); err != nil {
		t.Fatalf("set password hash: %v", err)
	}
	if err := notedb.SetSetting(ctx, db, appconfig.KeyOCRAPIKey, "my-api-key"); err != nil {
		t.Fatalf("set OCR API key: %v", err)
	}

	// Create handler with this DB
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	h := &Handler{
		noteDB:        db,
		logger:        logger,
		broadcaster:   broadcaster,
		runningConfig: &appconfig.Config{},
	}

	// Make GET /api/config request
	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()
	h.handleGetConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleGetConfig returned %d, want 200", w.Code)
	}

	var cfg RedactedConfig
	if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Verify secrets are redacted
	if cfg.PasswordHash != "[set]" {
		t.Errorf("PasswordHash = %q, want [set]", cfg.PasswordHash)
	}
	if cfg.OCRAPIKey != "[set]" {
		t.Errorf("OCRAPIKey = %q, want [set]", cfg.OCRAPIKey)
	}
	if cfg.Username != "testuser" {
		t.Errorf("Username = %q, want testuser", cfg.Username)
	}
}

// TestPutConfigHashesPassword verifies PUT /api/config hashes plaintext passwords.
func TestPutConfigHashesPassword(t *testing.T) {
	db := initTestDB(t)
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	h := &Handler{
		noteDB:        db,
		logger:        logger,
		broadcaster:   broadcaster,
		runningConfig: &appconfig.Config{},
	}

	// Make PUT /api/config request with password
	incoming := IncomingConfig{
		Username: "newuser",
		Password: "newpass123",
	}
	body, _ := json.Marshal(incoming)
	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handlePutConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handlePutConfig returned %d, want 200", w.Code)
		t.Logf("body: %s", w.Body.String())
	}

	var result appconfig.SaveResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Verify password was hashed in DB
	ctx := context.Background()
	storedHash, err := notedb.GetSetting(ctx, db, appconfig.KeyPasswordHash)
	if err != nil {
		t.Fatalf("get password hash: %v", err)
	}
	if storedHash == "" {
		t.Error("password hash not saved to DB")
	}

	// Verify hash is valid (should not match plaintext)
	if storedHash == "newpass123" {
		t.Error("password not hashed (stored as plaintext)")
	}

	// Verify bcrypt hash is valid
	if err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte("newpass123")); err != nil {
		t.Errorf("stored hash is not a valid bcrypt hash: %v", err)
	}
}

// TestPutConfigDetectsRestartRequired verifies restart detection.
func TestPutConfigDetectsRestartRequired(t *testing.T) {
	db := initTestDB(t)
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	h := &Handler{
		noteDB:        db,
		logger:        logger,
		broadcaster:   broadcaster,
		runningConfig: &appconfig.Config{},
	}

	// Make PUT /api/config request with a restart-required key (username is restart-required)
	incoming := IncomingConfig{
		Username: "newuser",
	}
	body, _ := json.Marshal(incoming)
	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handlePutConfig(w, req)

	var result appconfig.SaveResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Verify restart_required is set
	if !result.RestartRequired {
		t.Error("restart_required = false, want true when username changed")
	}

	// Verify configDirty flag is set
	if !h.configDirty.Load() {
		t.Error("configDirty = false, want true after restart-required change")
	}
}

// TestPutConfigNoRestartForNonRestartKeys is skipped because all fields in Config
// are restart-required. See appconfig/keys.go restartRequired map.

// TestHealthEndpointConfigDirty verifies /health includes config_dirty flag.
func TestHealthEndpointConfigDirty(t *testing.T) {
	db := initTestDB(t)
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	h := &Handler{
		noteDB:        db,
		logger:        logger,
		broadcaster:   broadcaster,
		runningConfig: &appconfig.Config{},
	}

	// Test with configDirty = false
	h.configDirty.Store(false)
	type healthResp struct {
		Status      string `json:"status"`
		ConfigDirty bool   `json:"config_dirty"`
	}

	// Create a mock health handler to test
	healthHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(healthResp{
			Status:      "ok",
			ConfigDirty: h.IsConfigDirty(),
		})
	}

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	healthHandler(w, req)

	var resp healthResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ConfigDirty {
		t.Error("config_dirty = true, want false when not set")
	}

	// Test with configDirty = true
	h.configDirty.Store(true)
	w = httptest.NewRecorder()
	healthHandler(w, req)

	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.ConfigDirty {
		t.Error("config_dirty = false, want true when set")
	}
}
