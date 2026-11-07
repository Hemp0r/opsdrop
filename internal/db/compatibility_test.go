package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// Exercise the persisted pre-clipboard-unification schema, not just a fresh DB.
func TestLegacySQLiteMigrationAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL, created_at TEXT NOT NULL)`,
		`INSERT INTO users VALUES (1, 'legacy', 'hash', '2026-01-01T00:00:00Z')`,
		`CREATE TABLE files (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL, filename TEXT NOT NULL, stored_path TEXT NOT NULL, size INTEGER NOT NULL, is_public INTEGER NOT NULL DEFAULT 0, public_token TEXT UNIQUE, created_at TEXT NOT NULL, expires_at TEXT NOT NULL, is_encrypted INTEGER NOT NULL DEFAULT 0, encryption_salt TEXT, encryption_nonce TEXT)`,
		`INSERT INTO files VALUES (1, 1, 'private.txt', '/private', 7, 0, NULL, '2026-01-01T00:00:00Z', '2027-01-01T00:00:00Z', 1, 'salt', 'nonce')`,
		`CREATE TABLE clipboard_entries (id INTEGER PRIMARY KEY, user_id INTEGER, content TEXT, created_at TEXT)`,
		`INSERT INTO clipboard_entries VALUES (1, 1, 'clipboard content', '2026-01-01T00:00:00Z')`,
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	d, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for i := 0; i < 2; i++ {
		if err := EnsureMigrations(ctx, d.Conn()); err != nil {
			t.Fatalf("migration pass %d: %v", i, err)
		}
	}
	var filename, salt, nonce string
	var encrypted int
	if err := d.Conn().QueryRowContext(ctx, `SELECT filename, is_encrypted, encryption_salt, encryption_nonce FROM files WHERE id=1`).Scan(&filename, &encrypted, &salt, &nonce); err != nil {
		t.Fatal(err)
	}
	if filename != "private.txt" || encrypted != 1 || salt != "salt" || nonce != "nonce" {
		t.Fatalf("legacy metadata lost: %q %d %q %q", filename, encrypted, salt, nonce)
	}
	var n int
	if err := d.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE entry_type='clipboard' AND content='clipboard content'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("clipboard migration: count=%d err=%v", n, err)
	}
	if _, err := d.Conn().ExecContext(ctx, `INSERT INTO files (filename, stored_path, size, is_public, public_token, created_at, expires_at, checksum) VALUES ('public.txt', '/public', 3, 1, 'public-token', '2026-01-01T00:00:00Z', '2027-01-01T00:00:00Z', 'checksum')`); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := EnsureMigrations(ctx, reopened.Conn()); err != nil {
		t.Fatal(err)
	}
	var checksum string
	var owner sql.NullInt64
	if err := reopened.Conn().QueryRowContext(ctx, `SELECT user_id, checksum FROM files WHERE public_token='public-token'`).Scan(&owner, &checksum); err != nil {
		t.Fatal(err)
	}
	if owner.Valid || checksum != "checksum" {
		t.Fatalf("public record changed: owner=%v checksum=%q", owner, checksum)
	}
	var integrity string
	if err := reopened.Conn().QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q err=%v", integrity, err)
	}
}

func TestFreshSQLiteConstraints(t *testing.T) {
	d, err := New(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	if err := EnsureMigrations(ctx, d.Conn()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Conn().ExecContext(ctx, `INSERT INTO files (user_id, created_at, expires_at) VALUES (999, 'now', 'later')`); err == nil {
		t.Fatal("missing user accepted: foreign keys disabled")
	}
	if _, err := d.Conn().ExecContext(ctx, `INSERT INTO revoked_tokens (token_hash, expires_at) VALUES ('revoked', 'later')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Conn().ExecContext(ctx, `INSERT INTO revoked_tokens (token_hash, expires_at) VALUES ('revoked', 'later')`); err == nil {
		t.Fatal("duplicate revocation accepted")
	}
	if d.Conn().Stats().MaxOpenConnections != 1 {
		t.Fatal("SQLite single-connection invariant changed")
	}
}
