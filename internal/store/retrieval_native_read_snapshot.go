package store

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
)

// RetrievalNativeReadSession keeps every authority read for one native search
// on a single SQLite snapshot. It is deliberately query-only: the native
// index remains derived data and SQLite remains the authority for both active
// root validation and exact L0 rows.
type RetrievalNativeReadSession interface {
	ReadRetrievalNativeCandidates(context.Context, RetrievalNativeCandidateRequest) ([]RetrievalEmbeddingRow, error)
	ReadRetrievalExactL0(context.Context, RetrievalActiveRootReadRequest, int) ([]RetrievalEmbeddingRow, error)
	Close() error
}

type retrievalNativeReadSnapshot struct {
	mu             sync.Mutex
	conn           *sql.Conn
	priorQueryOnly bool
	closed         bool
}

// BeginRetrievalNativeReadSnapshot pins one query-only SQLite read snapshot
// for a native semantic search. All methods still CAS-check the active root;
// the pinned view prevents a successful search from mixing L0 and validated
// candidates from different database snapshots.
func (s *Store) BeginRetrievalNativeReadSnapshot(ctx context.Context) (RetrievalNativeReadSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("open retrieval native read snapshot connection: %w", err)
	}
	priorQueryOnly, err := snapshotQueryOnly(ctx, conn)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read retrieval native snapshot query-only state: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA query_only = ON`); err != nil {
		restoreErr := restoreSnapshotQueryOnly(conn, priorQueryOnly)
		_ = conn.Close()
		if restoreErr != nil {
			return nil, fmt.Errorf("set retrieval native snapshot query-only: %w; restore query-only: %v", err, restoreErr)
		}
		return nil, fmt.Errorf("set retrieval native snapshot query-only: %w", err)
	}
	if err := beginReadSnapshotTransaction(ctx, conn); err != nil {
		restoreErr := restoreSnapshotQueryOnly(conn, priorQueryOnly)
		_ = conn.Close()
		if restoreErr != nil {
			return nil, fmt.Errorf("begin retrieval native read snapshot: %w; restore query-only: %v", err, restoreErr)
		}
		return nil, fmt.Errorf("begin retrieval native read snapshot: %w", err)
	}
	return &retrievalNativeReadSnapshot{conn: conn, priorQueryOnly: priorQueryOnly}, nil
}

func (s *retrievalNativeReadSnapshot) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	_, rollbackErr := s.conn.ExecContext(context.Background(), `ROLLBACK`)
	restoreErr := restoreSnapshotQueryOnly(s.conn, s.priorQueryOnly)
	connErr := s.conn.Close()
	if rollbackErr != nil {
		return rollbackErr
	}
	if restoreErr != nil {
		return restoreErr
	}
	return connErr
}

func (s *retrievalNativeReadSnapshot) ReadRetrievalNativeCandidates(ctx context.Context, input RetrievalNativeCandidateRequest) ([]RetrievalEmbeddingRow, error) {
	input = normalizeRetrievalNativeCandidateRequest(input)
	if err := validateRetrievalNativeCandidateRequest(input); err != nil {
		return nil, err
	}
	reader, err := s.query(ctx)
	if err != nil {
		return nil, err
	}
	return readRetrievalNativeCandidates(ctx, reader, input)
}

func (s *retrievalNativeReadSnapshot) ReadRetrievalExactL0(ctx context.Context, input RetrievalActiveRootReadRequest, limit int) ([]RetrievalEmbeddingRow, error) {
	input = normalizeRetrievalActiveRootReadRequest(input)
	if input.ProfileID == "" || input.ExpectedActiveGenerationID == "" || input.ExpectedPurgeEpoch < 0 || input.ExpectedActiveSnapshotRevision <= 0 {
		return nil, fmt.Errorf("retrieval exact L0 active-root request is invalid")
	}
	if limit <= 0 || limit > RetrievalSegmentHardLimit {
		return nil, fmt.Errorf("retrieval exact L0 limit must be between 1 and %d", RetrievalSegmentHardLimit)
	}
	reader, err := s.query(ctx)
	if err != nil {
		return nil, err
	}
	return readRetrievalExactL0(ctx, reader, input, limit)
}

func (s *retrievalNativeReadSnapshot) query(ctx context.Context) (sqlQueryer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("retrieval native read snapshot is closed")
	}
	return s.conn, nil
}
