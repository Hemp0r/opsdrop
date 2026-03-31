package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// User represents an authenticated account.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

// ErrUserNotFound is returned when a user cannot be located.
var ErrUserNotFound = errors.New("user not found")

// CreateUser inserts a new user with the supplied password hash.
func (d *Database) CreateUser(ctx context.Context, username, passwordHash string) (*User, error) {
	const stmt = `INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)`
	now := NowUTC()
	res, err := d.conn.ExecContext(ctx, stmt, username, passwordHash, now)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &User{
		ID:           id,
		Username:     username,
		PasswordHash: passwordHash,
		CreatedAt:    parseTime(now),
	}, nil
}

// GetUserByUsername fetches a user by username.
func (d *Database) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	const stmt = `SELECT id, username, password_hash, created_at FROM users WHERE username = ?`
	row := d.conn.QueryRowContext(ctx, stmt, username)
	return scanUser(row)
}

// GetUserByID fetches a user by id.
func (d *Database) GetUserByID(ctx context.Context, id int64) (*User, error) {
	const stmt = `SELECT id, username, password_hash, created_at FROM users WHERE id = ?`
	row := d.conn.QueryRowContext(ctx, stmt, id)
	return scanUser(row)
}

func scanUser(row *sql.Row) (*User, error) {
	u := &User{}
	var created string
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	u.CreatedAt = parseTime(created)
	return u, nil
}

func parseTime(v string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}
	}
	return t
}
