package source

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/sysop/ultrabridge/internal/notedb"
)

// testSource is a minimal stub implementation of Source for testing.
type testSource struct {
	sourceType string
	sourceName string
	startCalls int
	stopCalls  int
}

func (s *testSource) Type() string {
	return s.sourceType
}

func (s *testSource) Name() string {
	return s.sourceName
}

func (s *testSource) Start(ctx context.Context) error {
	s.startCalls++
	return nil
}

func (s *testSource) Stop() {
	s.stopCalls++
}

// TestSourceInterface verifies that testSource implements Source.
// AC2.1: Source interface defines Type(), Name(), Start(ctx), Stop() contract
func TestSourceInterface(t *testing.T) {
	var _ Source = (*testSource)(nil)

	ts := &testSource{sourceType: "test", sourceName: "test-instance"}
	if ts.Type() != "test" {
		t.Errorf("Type() = %q, want test", ts.Type())
	}
	if ts.Name() != "test-instance" {
		t.Errorf("Name() = %q, want test-instance", ts.Name())
	}

	ctx := context.Background()
	if err := ts.Start(ctx); err != nil {
		t.Errorf("Start: %v", err)
	}
	if ts.startCalls != 1 {
		t.Errorf("startCalls = %d, want 1", ts.startCalls)
	}

	ts.Stop()
	if ts.stopCalls != 1 {
		t.Errorf("stopCalls = %d, want 1", ts.stopCalls)
	}
}

// TestSourceRowRoundtrip verifies CRUD operations work correctly.
// AC2.4: Sources table CRUD works — add, update, enable/disable, remove sources via API
// AC2.6: Source-specific config stored as JSON in config_json column
func TestSourceRowRoundtrip(t *testing.T) {
	ctx := context.Background()
	db, err := notedb.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open DB: %v", err)
	}
	defer db.Close()

	// Test AddSource
	configData := map[string]interface{}{"apiKey": "secret", "region": "us-east"}
	configJSON, _ := json.Marshal(configData)

	row := SourceRow{
		Type:       "supernote",
		Name:       "primary",
		Enabled:    true,
		ConfigJSON: string(configJSON),
	}

	id, err := AddSource(ctx, db, row)
	if err != nil {
		t.Fatalf("AddSource: %v", err)
	}
	if id == 0 {
		t.Error("AddSource returned id=0")
	}

	// Test GetSource
	retrieved, err := GetSource(ctx, db, id)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if retrieved.Type != "supernote" {
		t.Errorf("Type = %q, want supernote", retrieved.Type)
	}
	if retrieved.Name != "primary" {
		t.Errorf("Name = %q, want primary", retrieved.Name)
	}
	if !retrieved.Enabled {
		t.Error("Enabled = false, want true")
	}
	if retrieved.ConfigJSON != string(configJSON) {
		t.Errorf("ConfigJSON mismatch")
	}

	// Verify created_at and updated_at are set
	if retrieved.CreatedAt == 0 {
		t.Error("CreatedAt = 0")
	}
	if retrieved.UpdatedAt == 0 {
		t.Error("UpdatedAt = 0")
	}

	// Test UpdateSource
	oldUpdatedAt := retrieved.UpdatedAt
	retrieved.Name = "secondary"
	retrieved.Enabled = false
	newConfigData := map[string]interface{}{"apiKey": "newsecret", "region": "eu-west"}
	newConfigJSON, _ := json.Marshal(newConfigData)
	retrieved.ConfigJSON = string(newConfigJSON)

	// Add small delay to ensure timestamp changes
	time.Sleep(1 * time.Millisecond)

	if err := UpdateSource(ctx, db, retrieved); err != nil {
		t.Fatalf("UpdateSource: %v", err)
	}

	// Verify update took effect
	updated, err := GetSource(ctx, db, id)
	if err != nil {
		t.Fatalf("GetSource after update: %v", err)
	}
	if updated.Name != "secondary" {
		t.Errorf("Name after update = %q, want secondary", updated.Name)
	}
	if updated.Enabled {
		t.Error("Enabled after update = true, want false")
	}
	if updated.ConfigJSON != string(newConfigJSON) {
		t.Errorf("ConfigJSON after update mismatch")
	}
	if updated.UpdatedAt <= oldUpdatedAt {
		t.Errorf("UpdatedAt not incremented: old=%d, new=%d", oldUpdatedAt, updated.UpdatedAt)
	}

	// Test RemoveSource
	if err := RemoveSource(ctx, db, id); err != nil {
		t.Fatalf("RemoveSource: %v", err)
	}

	// Verify removed
	_, err = GetSource(ctx, db, id)
	if err == nil {
		t.Error("GetSource after RemoveSource should fail")
	}
	if !errors.Is(err, ErrSourceNotFound) {
		t.Errorf("error = %v, want ErrSourceNotFound", err)
	}
}

// TestListSources verifies listing all sources.
func TestListSources(t *testing.T) {
	ctx := context.Background()
	db, err := notedb.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open DB: %v", err)
	}
	defer db.Close()

	// Add multiple sources
	sources := []SourceRow{
		{Type: "supernote", Name: "device1", Enabled: true, ConfigJSON: "{}"},
		{Type: "boox", Name: "device2", Enabled: true, ConfigJSON: "{}"},
		{Type: "supernote", Name: "device3", Enabled: false, ConfigJSON: "{}"},
	}

	for _, src := range sources {
		_, err := AddSource(ctx, db, src)
		if err != nil {
			t.Fatalf("AddSource: %v", err)
		}
	}

	// List all
	all, err := ListSources(ctx, db)
	if err != nil {
		t.Fatalf("ListSources: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListSources count = %d, want 3", len(all))
	}
}

// TestListEnabledSources verifies listing only enabled sources.
func TestListEnabledSources(t *testing.T) {
	ctx := context.Background()
	db, err := notedb.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open DB: %v", err)
	}
	defer db.Close()

	// Add multiple sources with different enabled states
	sources := []SourceRow{
		{Type: "supernote", Name: "device1", Enabled: true, ConfigJSON: "{}"},
		{Type: "boox", Name: "device2", Enabled: true, ConfigJSON: "{}"},
		{Type: "supernote", Name: "device3", Enabled: false, ConfigJSON: "{}"},
	}

	for _, src := range sources {
		_, err := AddSource(ctx, db, src)
		if err != nil {
			t.Fatalf("AddSource: %v", err)
		}
	}

	// List enabled only
	enabled, err := ListEnabledSources(ctx, db)
	if err != nil {
		t.Fatalf("ListEnabledSources: %v", err)
	}
	if len(enabled) != 2 {
		t.Errorf("ListEnabledSources count = %d, want 2", len(enabled))
	}

	for _, src := range enabled {
		if !src.Enabled {
			t.Errorf("ListEnabledSources returned disabled source: %q", src.Name)
		}
	}
}

// TestGetSourceNotFound verifies ErrSourceNotFound is returned.
func TestGetSourceNotFound(t *testing.T) {
	ctx := context.Background()
	db, err := notedb.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open DB: %v", err)
	}
	defer db.Close()

	_, err = GetSource(ctx, db, 999)
	if err == nil {
		t.Error("GetSource should return error for nonexistent source")
	}
	if !errors.Is(err, ErrSourceNotFound) {
		t.Errorf("error = %v, want ErrSourceNotFound", err)
	}
}

// testFactory is a factory that parses JSON config into a typed struct.
type testConfig struct {
	APIKey string `json:"api_key"`
	Region string `json:"region"`
}

func testFactory(db *sql.DB, row SourceRow, deps SharedDeps) (Source, error) {
	var cfg testConfig
	if err := json.Unmarshal([]byte(row.ConfigJSON), &cfg); err != nil {
		return nil, err
	}
	return &testSource{
		sourceType: row.Type,
		sourceName: row.Name,
	}, nil
}

// TestRegistryCreate verifies Registry.Create with known type.
// AC2.6: config_json parsed by factory
// AC2.7: Unknown source type in DB logs warning and is skipped
func TestRegistryCreate(t *testing.T) {
	ctx := context.Background()
	db, err := notedb.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open DB: %v", err)
	}
	defer db.Close()

	reg := NewRegistry()
	reg.Register("supernote", testFactory)
	reg.Register("boox", testFactory)

	// Test known type
	config := testConfig{APIKey: "test-key", Region: "us-west"}
	configJSON, _ := json.Marshal(config)

	row := SourceRow{
		ID:         1,
		Type:       "supernote",
		Name:       "test-sn",
		Enabled:    true,
		ConfigJSON: string(configJSON),
	}

	mockDeps := SharedDeps{}
	source, err := reg.Create(db, row, mockDeps)
	if err != nil {
		t.Fatalf("Create known type: %v", err)
	}
	if source.Type() != "supernote" {
		t.Errorf("Source.Type() = %q, want supernote", source.Type())
	}
	if source.Name() != "test-sn" {
		t.Errorf("Source.Name() = %q, want test-sn", source.Name())
	}
}

// TestRegistryCreateUnknownType verifies unknown type returns error.
// AC2.7: Unknown source type in DB logs warning and is skipped (returns error)
func TestRegistryCreateUnknownType(t *testing.T) {
	ctx := context.Background()
	db, err := notedb.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open DB: %v", err)
	}
	defer db.Close()

	reg := NewRegistry()
	reg.Register("supernote", testFactory)

	row := SourceRow{
		ID:         1,
		Type:       "unknown",
		Name:       "test",
		Enabled:    true,
		ConfigJSON: "{}",
	}

	mockDeps := SharedDeps{}
	_, err = reg.Create(db, row, mockDeps)
	if err == nil {
		t.Error("Create with unknown type should fail")
	}
	if !contains(err.Error(), "unknown source type") {
		t.Errorf("error message = %q, want 'unknown source type'", err.Error())
	}
}

// TestRegistryCreateInvalidJSON verifies invalid config_json returns error.
// AC2.8: Source with invalid config_json logs error and is skipped (returns error)
func TestRegistryCreateInvalidJSON(t *testing.T) {
	ctx := context.Background()
	db, err := notedb.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open DB: %v", err)
	}
	defer db.Close()

	reg := NewRegistry()
	reg.Register("supernote", testFactory)

	row := SourceRow{
		ID:         1,
		Type:       "supernote",
		Name:       "test",
		Enabled:    true,
		ConfigJSON: "invalid json {",
	}

	mockDeps := SharedDeps{}
	_, err = reg.Create(db, row, mockDeps)
	if err == nil {
		t.Error("Create with invalid JSON should fail")
	}
}

// contains is a test helper.
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
