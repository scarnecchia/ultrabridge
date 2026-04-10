package source

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrSourceNotFound is returned when a source row is not found.
var ErrSourceNotFound = errors.New("source not found")

// ErrUnknownType is returned when a source type is not registered.
var ErrUnknownType = errors.New("unknown source type")

// Registry holds factories keyed by source type name.
type Registry struct {
	factories map[string]Factory
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]Factory),
	}
}

// Register adds a factory for a source type.
func (r *Registry) Register(typeName string, f Factory) {
	r.factories[typeName] = f
}

// Create looks up the factory for row.Type, calls it, and returns the Source.
// Returns an error if the type is unknown or the factory fails.
func (r *Registry) Create(db *sql.DB, row SourceRow, deps SharedDeps) (Source, error) {
	f, ok := r.factories[row.Type]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownType, row.Type)
	}
	return f(db, row, deps)
}

// ListSources returns all source rows from the DB.
func ListSources(ctx context.Context, db *sql.DB) ([]SourceRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, type, name, enabled, config_json, created_at, updated_at
		 FROM sources
		 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query sources: %w", err)
	}
	defer rows.Close()

	var sources []SourceRow
	for rows.Next() {
		var row SourceRow
		var enabled int
		if err := rows.Scan(&row.ID, &row.Type, &row.Name, &enabled, &row.ConfigJSON, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan sources: %w", err)
		}
		row.Enabled = enabled != 0
		sources = append(sources, row)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("sources rows error: %w", err)
	}

	return sources, nil
}

// ListEnabledSources returns only enabled source rows.
func ListEnabledSources(ctx context.Context, db *sql.DB) ([]SourceRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, type, name, enabled, config_json, created_at, updated_at
		 FROM sources
		 WHERE enabled = 1
		 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query enabled sources: %w", err)
	}
	defer rows.Close()

	var sources []SourceRow
	for rows.Next() {
		var row SourceRow
		var enabled int
		if err := rows.Scan(&row.ID, &row.Type, &row.Name, &enabled, &row.ConfigJSON, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan enabled sources: %w", err)
		}
		row.Enabled = enabled != 0
		sources = append(sources, row)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("enabled sources rows error: %w", err)
	}

	return sources, nil
}

// GetSource returns a single source by ID.
func GetSource(ctx context.Context, db *sql.DB, id int64) (SourceRow, error) {
	var row SourceRow
	var enabled int
	err := db.QueryRowContext(ctx,
		`SELECT id, type, name, enabled, config_json, created_at, updated_at
		 FROM sources
		 WHERE id = ?`,
		id).Scan(&row.ID, &row.Type, &row.Name, &enabled, &row.ConfigJSON, &row.CreatedAt, &row.UpdatedAt)
	if err == sql.ErrNoRows {
		return SourceRow{}, fmt.Errorf("%w", ErrSourceNotFound)
	}
	if err != nil {
		return SourceRow{}, fmt.Errorf("get source: %w", err)
	}
	row.Enabled = enabled != 0
	return row, nil
}

// AddSource inserts a new source row and returns the assigned ID.
func AddSource(ctx context.Context, db *sql.DB, row SourceRow) (int64, error) {
	now := time.Now().UnixMilli()
	enabled := 0
	if row.Enabled {
		enabled = 1
	}
	result, err := db.ExecContext(ctx,
		`INSERT INTO sources (type, name, enabled, config_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		row.Type, row.Name, enabled, row.ConfigJSON, now, now)
	if err != nil {
		return 0, fmt.Errorf("insert source: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

// UpdateSource updates an existing source row (name, enabled, config_json, updated_at).
func UpdateSource(ctx context.Context, db *sql.DB, row SourceRow) error {
	now := time.Now().UnixMilli()
	enabled := 0
	if row.Enabled {
		enabled = 1
	}
	result, err := db.ExecContext(ctx,
		`UPDATE sources
		 SET name = ?, enabled = ?, config_json = ?, updated_at = ?
		 WHERE id = ?`,
		row.Name, enabled, row.ConfigJSON, now, row.ID)
	if err != nil {
		return fmt.Errorf("update source: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w", ErrSourceNotFound)
	}
	return nil
}

// RemoveSource deletes a source row by ID.
func RemoveSource(ctx context.Context, db *sql.DB, id int64) error {
	result, err := db.ExecContext(ctx,
		`DELETE FROM sources WHERE id = ?`,
		id)
	if err != nil {
		return fmt.Errorf("delete source: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w", ErrSourceNotFound)
	}
	return nil
}
