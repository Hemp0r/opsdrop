package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Entry type constants.
const (
	EntryTypeFile      = "file"
	EntryTypeClipboard = "clipboard"

	ClipboardTTL = 30 * 24 * time.Hour
)

// FileRecord describes a stored file or clipboard entry.
type FileRecord struct {
	ID              int64
	EntryType       string
	UserID          sql.NullInt64
	Filename        string
	StoredPath      string
	Size            int64
	IsPublic        bool
	PublicToken     sql.NullString
	CreatedAt       time.Time
	ExpiresAt       time.Time
	IsEncrypted     bool
	EncryptionSalt  sql.NullString
	EncryptionNonce sql.NullString
	Content         sql.NullString
	Checksum        sql.NullString
}

// ErrFileNotFound indicates a record does not exist.
var ErrFileNotFound = errors.New("file not found")

const fileColumns = `id, entry_type, user_id, filename, stored_path, size, is_public, public_token, created_at, expires_at, is_encrypted, encryption_salt, encryption_nonce, content, checksum`

// CreateFile adds a new file record to the database.
func (d *Database) CreateFile(ctx context.Context, userID *int64, filename, storedPath string, size int64, isPublic bool, publicToken *string, expiresAt time.Time, isEncrypted bool, encryptionSalt, encryptionNonce, checksum *string) (*FileRecord, error) {
	const stmt = `INSERT INTO files (entry_type, user_id, filename, stored_path, size, is_public, public_token, created_at, expires_at, is_encrypted, encryption_salt, encryption_nonce, checksum)
				  VALUES ('file', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	now := NowUTC()
	expires := expiresAt.UTC().Format(time.RFC3339Nano)

	var token interface{}
	if publicToken != nil {
		token = *publicToken
	}

	var user interface{}
	if userID != nil {
		user = *userID
	}

	var salt interface{}
	if encryptionSalt != nil {
		salt = *encryptionSalt
	}
	var nonce interface{}
	if encryptionNonce != nil {
		nonce = *encryptionNonce
	}

	var chk interface{}
	if checksum != nil {
		chk = *checksum
	}

	res, err := d.conn.ExecContext(ctx, stmt, user, filename, storedPath, size, boolToInt(isPublic), token, now, expires, boolToInt(isEncrypted), salt, nonce, chk)
	if err != nil {
		return nil, fmt.Errorf("insert file: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	rec := &FileRecord{
		ID:          id,
		EntryType:   EntryTypeFile,
		Filename:    filename,
		StoredPath:  storedPath,
		Size:        size,
		IsPublic:    isPublic,
		CreatedAt:   parseTime(now),
		ExpiresAt:   expiresAt.UTC(),
		IsEncrypted: isEncrypted,
	}
	if userID != nil {
		rec.UserID = sql.NullInt64{Int64: *userID, Valid: true}
	}
	if publicToken != nil {
		rec.PublicToken = sql.NullString{String: *publicToken, Valid: true}
	}
	if encryptionSalt != nil {
		rec.EncryptionSalt = sql.NullString{String: *encryptionSalt, Valid: true}
	}
	if encryptionNonce != nil {
		rec.EncryptionNonce = sql.NullString{String: *encryptionNonce, Valid: true}
	}
	if checksum != nil {
		rec.Checksum = sql.NullString{String: *checksum, Valid: true}
	}
	return rec, nil
}

// CreateClipboardEntry inserts a clipboard entry into the unified files table.
func (d *Database) CreateClipboardEntry(ctx context.Context, userID int64, content string) (*FileRecord, error) {
	const stmt = `INSERT INTO files (entry_type, user_id, filename, stored_path, size, is_public, created_at, expires_at, is_encrypted, content)
				  VALUES ('clipboard', ?, '', '', ?, 0, ?, ?, 0, ?)`
	now := NowUTC()
	expires := time.Now().UTC().Add(ClipboardTTL).Format(time.RFC3339Nano)

	res, err := d.conn.ExecContext(ctx, stmt, userID, len(content), now, expires, content)
	if err != nil {
		return nil, fmt.Errorf("insert clipboard entry: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return &FileRecord{
		ID:        id,
		EntryType: EntryTypeClipboard,
		UserID:    sql.NullInt64{Int64: userID, Valid: true},
		Size:      int64(len(content)),
		CreatedAt: parseTime(now),
		ExpiresAt: parseTime(expires),
		Content:   sql.NullString{String: content, Valid: true},
	}, nil
}

// ListFilesByUser lists non-expired file entries for a user.
func (d *Database) ListFilesByUser(ctx context.Context, userID int64) ([]FileRecord, error) {
	stmt := `SELECT ` + fileColumns + ` FROM files WHERE user_id = ? AND entry_type = 'file' AND expires_at > ? ORDER BY created_at DESC`
	rows, err := d.conn.QueryContext(ctx, stmt, userID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("query files: %w", err)
	}
	defer rows.Close()
	return scanFileRows(rows)
}

// ListClipboardByUser lists non-expired clipboard entries for a user.
func (d *Database) ListClipboardByUser(ctx context.Context, userID int64, limit int) ([]FileRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	stmt := `SELECT ` + fileColumns + ` FROM files WHERE user_id = ? AND entry_type = 'clipboard' AND expires_at > ? ORDER BY created_at DESC LIMIT ?`
	rows, err := d.conn.QueryContext(ctx, stmt, userID, time.Now().UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, fmt.Errorf("query clipboard entries: %w", err)
	}
	defer rows.Close()
	return scanFileRows(rows)
}

// GetLatestClipboard returns the most recent non-expired clipboard entry for a user.
func (d *Database) GetLatestClipboard(ctx context.Context, userID int64) (*FileRecord, error) {
	stmt := `SELECT ` + fileColumns + ` FROM files WHERE user_id = ? AND entry_type = 'clipboard' AND expires_at > ? ORDER BY created_at DESC LIMIT 1`
	row := d.conn.QueryRowContext(ctx, stmt, userID, time.Now().UTC().Format(time.RFC3339Nano))
	return scanFileRecord(row)
}

// GetFileByID fetches a record by id (file or clipboard).
func (d *Database) GetFileByID(ctx context.Context, id int64) (*FileRecord, error) {
	stmt := `SELECT ` + fileColumns + ` FROM files WHERE id = ?`
	row := d.conn.QueryRowContext(ctx, stmt, id)
	return scanFileRecord(row)
}

// GetFileByPublicToken fetches a non-expired public file by token.
func (d *Database) GetFileByPublicToken(ctx context.Context, token string) (*FileRecord, error) {
	stmt := `SELECT ` + fileColumns + ` FROM files WHERE public_token = ? AND is_public = 1 AND expires_at > ?`
	row := d.conn.QueryRowContext(ctx, stmt, token, time.Now().UTC().Format(time.RFC3339Nano))
	return scanFileRecord(row)
}

// UpdateFilePublicity updates the public flag and token.
func (d *Database) UpdateFilePublicity(ctx context.Context, id int64, isPublic bool, publicToken *string) error {
	const stmt = `UPDATE files SET is_public = ?, public_token = ? WHERE id = ?`
	var token interface{}
	if publicToken != nil {
		token = *publicToken
	}
	res, err := d.conn.ExecContext(ctx, stmt, boolToInt(isPublic), token, id)
	if err != nil {
		return fmt.Errorf("update file publicity: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrFileNotFound
	}
	return nil
}

// DeleteFile removes a record (file or clipboard).
func (d *Database) DeleteFile(ctx context.Context, id int64) error {
	res, err := d.conn.ExecContext(ctx, `DELETE FROM files WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrFileNotFound
	}
	return nil
}

// RemoveExpiredFiles deletes expired entries and returns the removed records for filesystem cleanup.
func (d *Database) RemoveExpiredFiles(ctx context.Context, now time.Time) ([]FileRecord, error) {
	selectStmt := `SELECT ` + fileColumns + ` FROM files WHERE expires_at <= ?`
	rows, err := d.conn.QueryContext(ctx, selectStmt, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("query expired files: %w", err)
	}
	defer rows.Close()

	expired, err := scanFileRows(rows)
	if err != nil {
		return nil, err
	}

	if len(expired) == 0 {
		return nil, nil
	}

	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	for _, rec := range expired {
		if _, err := tx.ExecContext(ctx, `DELETE FROM files WHERE id = ?`, rec.ID); err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("delete expired file: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit expired delete: %w", err)
	}
	return expired, nil
}

func scanFileRecord(row *sql.Row) (*FileRecord, error) {
	var rec FileRecord
	var created, expires string
	var isPublic, isEncrypted int
	if err := row.Scan(&rec.ID, &rec.EntryType, &rec.UserID, &rec.Filename, &rec.StoredPath, &rec.Size, &isPublic, &rec.PublicToken, &created, &expires, &isEncrypted, &rec.EncryptionSalt, &rec.EncryptionNonce, &rec.Content, &rec.Checksum); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	rec.CreatedAt = parseTime(created)
	rec.ExpiresAt = parseTime(expires)
	rec.IsPublic = isPublic == 1
	rec.IsEncrypted = isEncrypted == 1
	return &rec, nil
}

func scanFileRows(rows *sql.Rows) ([]FileRecord, error) {
	var out []FileRecord
	for rows.Next() {
		var rec FileRecord
		var created, expires string
		var isPublic, isEncrypted int
		if err := rows.Scan(&rec.ID, &rec.EntryType, &rec.UserID, &rec.Filename, &rec.StoredPath, &rec.Size, &isPublic, &rec.PublicToken, &created, &expires, &isEncrypted, &rec.EncryptionSalt, &rec.EncryptionNonce, &rec.Content, &rec.Checksum); err != nil {
			return nil, err
		}
		rec.CreatedAt = parseTime(created)
		rec.ExpiresAt = parseTime(expires)
		rec.IsPublic = isPublic == 1
		rec.IsEncrypted = isEncrypted == 1
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
