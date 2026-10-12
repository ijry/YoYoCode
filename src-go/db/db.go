// Package db mirrors upstream src-tauri/src/db: SQLite storage.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Database wraps the global SQLite connection.
type Database struct {
	Conn *sql.DB
	Path string
}

// AppDatabase mirrors upstream AppDatabase wrapping AppState.db.conn.
type AppDatabase struct {
	DB *Database
}

// Open opens (and creates) the SQLite database at dataDir/yoyocode.db.
func Open(dataDir string) (*Database, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "yoyocode.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	d := &Database{Conn: conn, Path: path}
	if err := d.migrate(); err != nil {
		return nil, err
	}
	return d, nil
}

// extraMigrations holds schema contributed by individual feature modules.
//
// Modules add their own tables by creating a separate file in this package with
// an `init()` that calls RegisterMigration — e.g. db/migration_forge.go. That
// keeps each feature's schema next to nothing else it has to touch, so two
// people (or two agents) adding features in parallel never collide in
// migration.go.
var extraMigrations []func(*Database) error

// RegisterMigration appends a module migration. Call from init().
func RegisterMigration(fn func(*Database) error) {
	extraMigrations = append(extraMigrations, fn)
}
