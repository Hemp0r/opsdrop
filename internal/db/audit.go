package db

import (
	"context"
	"fmt"
	"time"
)

// AuditRecord captures user activity for traceability.
type AuditRecord struct {
	ID        int64
	UserID    *int64
	MachineID string
	Action    string
	Resource  string
	Metadata  string
	CreatedAt time.Time
}

// CreateAuditRecord persists an audit entry.
func (d *Database) CreateAuditRecord(ctx context.Context, userID *int64, machineID, action, resource, metadata string) error {
	const stmt = `INSERT INTO audit_logs (user_id, machine_id, action, resource, metadata, created_at)
				  VALUES (?, ?, ?, ?, ?, ?)`
	var user interface{}
	if userID != nil {
		user = *userID
	}
	if _, err := d.conn.ExecContext(ctx, stmt, user, machineID, action, resource, metadata, NowUTC()); err != nil {
		return fmt.Errorf("insert audit record: %w", err)
	}
	return nil
}
