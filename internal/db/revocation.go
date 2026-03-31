package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// RevokeToken stores a token hash so it cannot be reused before its natural expiry.
func (d *Database) RevokeToken(ctx context.Context, token string, expiresAt time.Time) error {
	const stmt = `INSERT OR IGNORE INTO revoked_tokens (token_hash, expires_at) VALUES (?, ?)`
	_, err := d.conn.ExecContext(ctx, stmt, hashToken(token), expiresAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	return nil
}

// IsTokenRevoked checks whether a token has been revoked.
func (d *Database) IsTokenRevoked(ctx context.Context, token string) (bool, error) {
	const stmt = `SELECT COUNT(*) FROM revoked_tokens WHERE token_hash = ? AND expires_at > ?`
	var count int
	err := d.conn.QueryRowContext(ctx, stmt, hashToken(token), time.Now().UTC().Format(time.RFC3339Nano)).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check revocation: %w", err)
	}
	return count > 0, nil
}

// CleanExpiredRevocations removes revocation entries whose tokens have naturally expired.
func (d *Database) CleanExpiredRevocations(ctx context.Context) (int64, error) {
	const stmt = `DELETE FROM revoked_tokens WHERE expires_at <= ?`
	res, err := d.conn.ExecContext(ctx, stmt, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("clean revocations: %w", err)
	}
	return res.RowsAffected()
}
