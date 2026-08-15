package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func TestWritablePoolInitializesPragmasOnEveryConnection(t *testing.T) {
	t.Parallel()

	st, err := OpenWithOptions(filepath.Join(t.TempDir(), "brain.db"), OpenOptions{
		MaxOpenConns: 4,
		MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatalf("open writable pool: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	conns := make([]*sql.Conn, 0, 4)
	for i := 0; i < 4; i++ {
		conn, err := st.db.Conn(ctx)
		if err != nil {
			t.Fatalf("checkout connection %d: %v", i, err)
		}
		conns = append(conns, conn)
		t.Cleanup(func() { _ = conn.Close() })
	}
	for i, conn := range conns {
		var foreignKeys, busyTimeout, synchronous int
		if err := conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatalf("connection %d foreign_keys: %v", i, err)
		}
		if err := conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatalf("connection %d busy_timeout: %v", i, err)
		}
		if err := conn.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous); err != nil {
			t.Fatalf("connection %d synchronous: %v", i, err)
		}
		if foreignKeys != 1 || busyTimeout != 60000 || synchronous != 1 {
			t.Fatalf("connection %d pragmas foreign_keys=%d busy_timeout=%d synchronous=%d", i, foreignKeys, busyTimeout, synchronous)
		}
	}
}

func TestLinkCaptureAdmissionPoolUsesShortBusyTimeout(t *testing.T) {
	t.Parallel()

	st, err := OpenWithOptions(filepath.Join(t.TempDir(), "brain.db"), OpenOptions{})
	if err != nil {
		t.Fatalf("open writable store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.EnqueueLinkCapture(t.Context(), linkCaptureTestCandidate(), time.Now().UTC()); err != nil {
		t.Fatalf("initialize admission pool: %v", err)
	}
	if st.linkCaptureAdmissionDB == nil {
		t.Fatal("link capture admission pool is nil")
	}

	var foreignKeys, busyTimeout, synchronous int
	conn, err := st.linkCaptureAdmissionDB.Conn(t.Context())
	if err != nil {
		t.Fatalf("checkout admission connection: %v", err)
	}
	defer func() { _ = conn.Close() }()
	for _, check := range []struct {
		name string
		dst  *int
		stmt string
	}{
		{name: "foreign_keys", dst: &foreignKeys, stmt: "PRAGMA foreign_keys"},
		{name: "busy_timeout", dst: &busyTimeout, stmt: "PRAGMA busy_timeout"},
		{name: "synchronous", dst: &synchronous, stmt: "PRAGMA synchronous"},
	} {
		if err := conn.QueryRowContext(t.Context(), check.stmt).Scan(check.dst); err != nil {
			t.Fatalf("read admission %s: %v", check.name, err)
		}
	}
	if foreignKeys != 1 || busyTimeout != linkCaptureAdmissionBusyTimeoutMillis || synchronous != 1 {
		t.Fatalf("admission pragmas foreign_keys=%d busy_timeout=%d synchronous=%d", foreignKeys, busyTimeout, synchronous)
	}
}

func TestReadOnlyPoolInitializesPragmasOnEveryConnection(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "brain.db")
	writer := openStoreFromEmptyDatabase(t, path)
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	reader, err := OpenReadOnlyWithOptions(path, OpenOptions{MaxOpenConns: 4, MaxIdleConns: 4})
	if err != nil {
		t.Fatalf("open read-only pool: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	ctx := context.Background()
	conns := make([]*sql.Conn, 0, 4)
	for i := 0; i < 4; i++ {
		conn, err := reader.db.Conn(ctx)
		if err != nil {
			t.Fatalf("checkout connection %d: %v", i, err)
		}
		conns = append(conns, conn)
		t.Cleanup(func() { _ = conn.Close() })
	}
	for i, conn := range conns {
		var foreignKeys, busyTimeout, queryOnly int
		if err := conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatalf("connection %d foreign_keys: %v", i, err)
		}
		if err := conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatalf("connection %d busy_timeout: %v", i, err)
		}
		if err := conn.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&queryOnly); err != nil {
			t.Fatalf("connection %d query_only: %v", i, err)
		}
		if foreignKeys != 1 || busyTimeout != 1000 || queryOnly != 1 {
			t.Fatalf("connection %d pragmas foreign_keys=%d busy_timeout=%d query_only=%d", i, foreignKeys, busyTimeout, queryOnly)
		}
	}
}

func TestWritablePoolRecyclesSnapshotConnectionForWrites(t *testing.T) {
	t.Parallel()

	st, err := OpenWithOptions(filepath.Join(t.TempDir(), "brain.db"), OpenOptions{
		MaxOpenConns: 4,
		MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatalf("open writable pool: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	snapshot, err := st.BeginAuditReadSnapshot(t.Context())
	if err != nil {
		t.Fatalf("begin audit snapshot: %v", err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatalf("close audit snapshot: %v", err)
	}
	conn, err := st.db.Conn(t.Context())
	if err != nil {
		t.Fatalf("checkout recycled snapshot connection: %v", err)
	}
	defer func() { _ = conn.Close() }()
	var queryOnly int
	if err := conn.QueryRowContext(t.Context(), `PRAGMA query_only`).Scan(&queryOnly); err != nil {
		t.Fatalf("read recycled snapshot query_only: %v", err)
	}
	if queryOnly != 0 {
		t.Fatalf("recycled snapshot connection retained query_only=%d", queryOnly)
	}
	if _, err := conn.ExecContext(t.Context(), `UPDATE schema_migrations SET name = name WHERE version = 1`); err != nil {
		t.Fatalf("write after snapshot recycle: %v", err)
	}
}

func TestWritablePoolAllowsConcurrentWritesWithSQLiteSerialization(t *testing.T) {
	t.Parallel()

	st, err := OpenWithOptions(filepath.Join(t.TempDir(), "brain.db"), OpenOptions{
		MaxOpenConns: 4,
		MaxIdleConns: 4,
	})
	if err != nil {
		t.Fatalf("open writable pool: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			candidate := model.SourceCandidate{
				OriginalURL:   "https://example.com/concurrent-" + string(rune('a'+i)),
				CanonicalURL:  "https://example.com/concurrent-" + string(rune('a'+i)),
				NormalizedURL: "https://example.com/concurrent-" + string(rune('a'+i)),
				SourceType:    "web", Domain: "example.com", SourceKey: "src:concurrent-" + string(rune('a'+i)),
				NotePath: "sources/web/concurrent.md",
			}
			_, err := st.UpsertSource(t.Context(), candidate)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent write: %v", err)
		}
	}
}
