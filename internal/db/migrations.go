package db

import (
	"context"
	"database/sql"
	"fmt"
)

// EnsureMigrations upgrades the schema to the latest version.
func EnsureMigrations(ctx context.Context, conn *sql.DB) error {
	if err := addExpiresAtColumn(ctx, conn); err != nil {
		return err
	}
	if err := relaxUserIDNull(ctx, conn); err != nil {
		return err
	}
	if err := addEncryptionColumns(ctx, conn); err != nil {
		return err
	}
	if err := addRevocationsTable(ctx, conn); err != nil {
		return err
	}
	if err := addChecksumColumn(ctx, conn); err != nil {
		return err
	}
	if err := mergeClipboardIntoFiles(ctx, conn); err != nil {
		return err
	}
	return nil
}

func addChecksumColumn(ctx context.Context, conn *sql.DB) error {
	const pragma = `PRAGMA table_info(files);`
	rows, err := conn.QueryContext(ctx, pragma)
	if err != nil {
		return fmt.Errorf("check files schema for checksum: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var (
			cid      int
			name     string
			typeName string
			notNull  int
			defaultV any
			pk       int
		)
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultV, &pk); err != nil {
			return fmt.Errorf("scan pragma: %w", err)
		}
		if name == "checksum" {
			hasColumn = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("pragma iteration: %w", err)
	}
	if hasColumn {
		return nil
	}

	if _, err := conn.ExecContext(ctx, `ALTER TABLE files ADD COLUMN checksum TEXT`); err != nil {
		return fmt.Errorf("add checksum column: %w", err)
	}
	return nil
}

func addRevocationsTable(ctx context.Context, conn *sql.DB) error {
	_, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS revoked_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		token_hash TEXT NOT NULL UNIQUE,
		expires_at TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create revoked_tokens table: %w", err)
	}
	return nil
}

func addExpiresAtColumn(ctx context.Context, conn *sql.DB) error {
	const check = `PRAGMA table_info(files);`
	rows, err := conn.QueryContext(ctx, check)
	if err != nil {
		return fmt.Errorf("check files schema: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var (
			cid      int
			name     string
			typeName string
			notNull  int
			defaultV any
			pk       int
		)
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultV, &pk); err != nil {
			return fmt.Errorf("scan pragma: %w", err)
		}
		if name == "expires_at" {
			hasColumn = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("pragma iteration: %w", err)
	}
	if hasColumn {
		return nil
	}

	if _, err := conn.ExecContext(ctx, `ALTER TABLE files ADD COLUMN expires_at TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add expires_at column: %w", err)
	}
	return nil
}

func relaxUserIDNull(ctx context.Context, conn *sql.DB) error {
	const pragma = `PRAGMA table_info(files);`
	rows, err := conn.QueryContext(ctx, pragma)
	if err != nil {
		return fmt.Errorf("check files schema: %w", err)
	}
	defer rows.Close()

	userIDAllowsNull := false
	for rows.Next() {
		var (
			cid      int
			name     string
			typeName string
			notNull  int
			defaultV any
			pk       int
		)
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultV, &pk); err != nil {
			return fmt.Errorf("scan pragma: %w", err)
		}
		if name == "user_id" {
			userIDAllowsNull = notNull == 0
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("pragma iteration: %w", err)
	}

	if userIDAllowsNull {
		return nil
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS files_tmp (
			id INTEGER PRIMARY KEY,
			user_id INTEGER,
			filename TEXT NOT NULL,
			stored_path TEXT NOT NULL,
			size INTEGER NOT NULL,
			is_public INTEGER NOT NULL DEFAULT 0,
			public_token TEXT UNIQUE,
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			is_encrypted INTEGER NOT NULL DEFAULT 0,
			encryption_salt TEXT,
			encryption_nonce TEXT
		);`,
		`INSERT INTO files_tmp (id, user_id, filename, stored_path, size, is_public, public_token, created_at, expires_at, is_encrypted, encryption_salt, encryption_nonce)
		 SELECT id, user_id, filename, stored_path, size, is_public, public_token, created_at, COALESCE(NULLIF(expires_at, ''), created_at), COALESCE(is_encrypted, 0), encryption_salt, encryption_nonce
		 FROM files;`,
		`DROP TABLE files;`,
		`ALTER TABLE files_tmp RENAME TO files;`,
		`CREATE INDEX IF NOT EXISTS idx_files_public_token ON files(public_token);`,
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration step failed: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func addEncryptionColumns(ctx context.Context, conn *sql.DB) error {
	const pragma = `PRAGMA table_info(files);`
	rows, err := conn.QueryContext(ctx, pragma)
	if err != nil {
		return fmt.Errorf("check files encryption columns: %w", err)
	}
	defer rows.Close()

	required := map[string]string{
		"is_encrypted":     `ALTER TABLE files ADD COLUMN is_encrypted INTEGER NOT NULL DEFAULT 0`,
		"encryption_salt":  `ALTER TABLE files ADD COLUMN encryption_salt TEXT`,
		"encryption_nonce": `ALTER TABLE files ADD COLUMN encryption_nonce TEXT`,
	}

	for rows.Next() {
		var (
			cid      int
			name     string
			typeName string
			notNull  int
			defaultV any
			pk       int
		)
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultV, &pk); err != nil {
			return fmt.Errorf("scan pragma: %w", err)
		}
		delete(required, name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("pragma iteration: %w", err)
	}

	for name, stmt := range required {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("add %s column: %w", name, err)
		}
	}
	return nil
}

func mergeClipboardIntoFiles(ctx context.Context, conn *sql.DB) error {
	rows, err := conn.QueryContext(ctx, `PRAGMA table_info(files);`)
	if err != nil {
		return fmt.Errorf("check files schema for entry_type: %w", err)
	}
	defer rows.Close()

	hasEntryType := false
	for rows.Next() {
		var (
			cid      int
			name     string
			typeName string
			notNull  int
			defaultV any
			pk       int
		)
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultV, &pk); err != nil {
			return fmt.Errorf("scan pragma: %w", err)
		}
		if name == "entry_type" {
			hasEntryType = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("pragma iteration: %w", err)
	}

	if !hasEntryType {
		if _, err := conn.ExecContext(ctx, `ALTER TABLE files ADD COLUMN entry_type TEXT NOT NULL DEFAULT 'file'`); err != nil {
			return fmt.Errorf("add entry_type column: %w", err)
		}
		if _, err := conn.ExecContext(ctx, `ALTER TABLE files ADD COLUMN content TEXT`); err != nil {
			return fmt.Errorf("add content column: %w", err)
		}
	}

	// Migrate clipboard_entries data if table exists.
	var tableName string
	err = conn.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='clipboard_entries'`).Scan(&tableName)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check clipboard_entries table: %w", err)
	}

	const migrateStmt = `INSERT INTO files (entry_type, user_id, filename, stored_path, size, is_public, created_at, expires_at, is_encrypted, content)
		SELECT 'clipboard', user_id, '', '', LENGTH(content), 0, created_at, DATETIME(created_at, '+30 days'), 0, content
		FROM clipboard_entries`
	if _, err := conn.ExecContext(ctx, migrateStmt); err != nil {
		return fmt.Errorf("migrate clipboard entries: %w", err)
	}

	if _, err := conn.ExecContext(ctx, `DROP TABLE clipboard_entries`); err != nil {
		return fmt.Errorf("drop clipboard_entries table: %w", err)
	}

	return nil
}
