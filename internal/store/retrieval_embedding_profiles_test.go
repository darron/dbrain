package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestRetrievalEmbeddingVerificationStateReportsMissingProfile(t *testing.T) {
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()

	_, err := st.RetrievalEmbeddingVerificationState(context.Background(), "embedding-profile-v1:missing")
	if !errors.Is(err, ErrRetrievalEmbeddingProfileNotFound) {
		t.Fatalf("err=%v, want ErrRetrievalEmbeddingProfileNotFound", err)
	}
}

func TestRetrievalEmbeddingProfileReportsTypedMissingProfile(t *testing.T) {
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()

	_, err := st.RetrievalEmbeddingProfile(context.Background(), "embedding-profile-v1:missing")
	if !errors.Is(err, ErrRetrievalEmbeddingProfileNotFound) {
		t.Fatalf("err=%v, want ErrRetrievalEmbeddingProfileNotFound", err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err=%v must not expose sql.ErrNoRows", err)
	}
}
