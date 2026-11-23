package db

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// SQLite 1.63 opens NOMUTEX connections. database/sql must continue to
// serialize concurrent callers through our deliberately single-connection pool.
func TestSQLiteConcurrentPoolAccess(t *testing.T) {
	// This test targets connection serialization, not filesystem throughput.
	// Persisted migrations/reopen/integrity are covered separately. In-memory
	// storage avoids making the guard deadline a per-commit fsync benchmark.
	d, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := d.Conn().ExecContext(ctx, `CREATE TABLE compatibility_counter (n INTEGER NOT NULL); INSERT INTO compatibility_counter VALUES (0)`); err != nil {
		t.Fatal(err)
	}
	const workers, iterations = 8, 20
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterations {
				if _, err := d.Conn().ExecContext(ctx, `UPDATE compatibility_counter SET n=n+1`); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var got int
	if err := d.Conn().QueryRowContext(ctx, `SELECT n FROM compatibility_counter`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != workers*iterations {
		t.Fatalf("lost updates: got %d, want %d", got, workers*iterations)
	}
	if d.Conn().Stats().MaxOpenConnections != 1 {
		t.Fatal("SQLite single-connection invariant changed")
	}
}

// Pin the upstream distinction between empty BLOB and SQL NULL. This is a
// driver compatibility assertion, not a claim that OpsDrop stores BLOB columns.
func TestSQLiteEmptyBlobIsNotNull(t *testing.T) {
	d, err := New(filepath.Join(t.TempDir(), "blob.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var empty, null any
	if err := d.Conn().QueryRow(`SELECT x'', NULL`).Scan(&empty, &null); err != nil {
		t.Fatal(err)
	}
	blob, ok := empty.([]byte)
	if !ok || blob == nil || len(blob) != 0 {
		t.Fatalf("empty BLOB: got %#v (%T)", empty, empty)
	}
	if null != nil {
		t.Fatalf("SQL NULL: got %#v", null)
	}
}
