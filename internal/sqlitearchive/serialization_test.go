package sqlitearchive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

type blockingOperationStore struct {
	putStarted chan struct{}
	getStarted chan struct{}
	release    chan struct{}
}

func (s *blockingOperationStore) PutObject(ctx context.Context, _ string, _ io.Reader, _ string, _ int64) (string, error) {
	close(s.putStarted)
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-s.release:
		return "etag", nil
	}
}

func (*blockingOperationStore) ListObjects(context.Context, string) ([]Object, error) {
	return nil, nil
}

func (s *blockingOperationStore) GetObject(ctx context.Context, _ string) (io.ReadCloser, error) {
	close(s.getStarted)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.release:
		return io.NopCloser(bytes.NewReader(nil)), errors.New("synthetic restore stop")
	}
}

func TestSQLiteArchiveAndRestoreUseSameCrossProcessLease(t *testing.T) {
	t.Run("archive blocks restore", func(t *testing.T) {
		cfg := serializationTestConfig(t)
		remote := &blockingOperationStore{putStarted: make(chan struct{}), getStarted: make(chan struct{}), release: make(chan struct{})}
		done := make(chan error, 1)
		go func() {
			_, err := Archive(t.Context(), cfg, Options{Store: remote})
			done <- err
		}()
		awaitArchiveSignal(t, remote.putStarted)
		_, err := Restore(t.Context(), cfg, RestorePlan{Object: Object{Key: "archive/db/brain-20260714T000000Z.db.gz"}}, Options{Store: remote})
		if !errors.Is(err, ErrOperationLocked) {
			t.Fatalf("Restore error = %v, want ErrOperationLocked", err)
		}
		close(remote.release)
		if err := <-done; err != nil {
			t.Fatalf("Archive: %v", err)
		}
	})

	t.Run("restore blocks archive", func(t *testing.T) {
		cfg := serializationTestConfig(t)
		remote := &blockingOperationStore{putStarted: make(chan struct{}), getStarted: make(chan struct{}), release: make(chan struct{})}
		done := make(chan error, 1)
		go func() {
			_, err := Restore(t.Context(), cfg, RestorePlan{Object: Object{Key: "archive/db/brain-20260714T000000Z.db.gz"}}, Options{Store: remote})
			done <- err
		}()
		awaitArchiveSignal(t, remote.getStarted)
		_, err := Archive(t.Context(), cfg, Options{Store: remote})
		if !errors.Is(err, ErrOperationLocked) {
			t.Fatalf("Archive error = %v, want ErrOperationLocked", err)
		}
		close(remote.release)
		if err := <-done; err == nil {
			t.Fatal("expected synthetic restore stop")
		}
	})
}

func serializationTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func awaitArchiveSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for archive operation")
	}
}
