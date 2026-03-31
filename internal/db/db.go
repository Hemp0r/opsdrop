package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Database wraps the application database handle.
type Database struct {
	conn *sql.DB
}

// New opens a SQLite database at the given path and ensures the schema exists.
func New(path string) (*Database, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	conn.SetMaxOpenConns(1) // SQLite is happy with a single writer connection.

	db := &Database{conn: conn}
	if err := db.initSchema(context.Background()); err != nil {
		conn.Close()
		return nil, err
	}
	return db, nil
}

// Close releases database resources.
func (d *Database) Close() error {
	return d.conn.Close()
}

func (d *Database) initSchema(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON;`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			entry_type TEXT NOT NULL DEFAULT 'file',
			user_id INTEGER,
			filename TEXT NOT NULL DEFAULT '',
			stored_path TEXT NOT NULL DEFAULT '',
			size INTEGER NOT NULL DEFAULT 0,
			is_public INTEGER NOT NULL DEFAULT 0,
			public_token TEXT UNIQUE,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			is_encrypted INTEGER NOT NULL DEFAULT 0,
			encryption_salt TEXT,
			encryption_nonce TEXT,
			content TEXT,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER,
			machine_id TEXT NOT NULL,
			action TEXT NOT NULL,
			resource TEXT NOT NULL,
			metadata TEXT,
			created_at TEXT NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE SET NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);`,
	}

	for _, stmt := range statements {
		if _, err := d.conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec schema stmt: %w", err)
		}
	}
	return nil
}

// NowUTC returns a timestamp string suitable for SQLite storage.
func NowUTC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// Conn exposes the raw sql.DB when direct access is required.
func (d *Database) Conn() *sql.DB {
	return d.conn
}
