package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/embedding"
)

func TestStreamRetrievalActiveSegmentMembersStreamsSelectedRootMembersInOrder(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	seedActiveCompactionRoot(t, st)
	request := activeCompactionStreamRequest(t, st, "segment-alpha", "segment-bravo")

	var got []RetrievalActiveSegmentMember
	count, err := st.StreamRetrievalActiveSegmentMembers(context.Background(), request, func(member RetrievalActiveSegmentMember) error {
		got = append(got, member)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 || len(got) != 3 {
		t.Fatalf("count=%d members=%+v", count, got)
	}
	for index, want := range []struct {
		hash    string
		ordinal uint64
		chunk   string
	}{
		{"segment-alpha", 0, "chunk-a"},
		{"segment-alpha", 1, "chunk-b"},
		{"segment-bravo", 0, "chunk-c"},
	} {
		if member := got[index]; member.SegmentHash != want.hash || member.Ordinal != want.ordinal || member.Embedding.ChunkID != want.chunk || len(member.Embedding.VectorBytes) == 0 {
			t.Fatalf("member %d = %+v, want %s/%d/%s with vector", index, member, want.hash, want.ordinal, want.chunk)
		}
	}
}

func TestStreamRetrievalActiveSegmentMembersOmitsStaleMembership(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	seedActiveCompactionRoot(t, st)
	changed := testEmbedding("chunk-b", "flush-profile", "hash-b")
	changed.VectorBytes = embedding.EncodeDenseF32([]float32{0, 1})
	if err := st.PutRetrievalEmbedding(context.Background(), changed); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE retrieval_parent_projections SET status='pending' WHERE parent_kind='item' AND parent_source_key='item:two'`); err != nil {
		t.Fatal(err)
	}
	request := activeCompactionStreamRequest(t, st, "segment-alpha", "segment-bravo")

	var chunks []string
	count, err := st.StreamRetrievalActiveSegmentMembers(context.Background(), request, func(member RetrievalActiveSegmentMember) error {
		chunks = append(chunks, member.Embedding.ChunkID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(chunks) != 1 || chunks[0] != "chunk-a" {
		t.Fatalf("count=%d chunks=%v", count, chunks)
	}
}

func TestStreamRetrievalActiveSegmentMembersRejectsChangedRootOrInactiveSegment(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	seedActiveCompactionRoot(t, st)
	request := activeCompactionStreamRequest(t, st, "segment-alpha")
	called := false
	request.ExpectedActiveSnapshotRevision++
	if _, err := st.StreamRetrievalActiveSegmentMembers(context.Background(), request, func(RetrievalActiveSegmentMember) error { called = true; return nil }); err == nil || called {
		t.Fatalf("changed root stream err=%v called=%t", err, called)
	}
	request = activeCompactionStreamRequest(t, st, "segment-missing")
	if _, err := st.StreamRetrievalActiveSegmentMembers(context.Background(), request, func(RetrievalActiveSegmentMember) error { called = true; return nil }); err == nil || called {
		t.Fatalf("inactive segment stream err=%v called=%t", err, called)
	}
}

func TestStreamRetrievalActiveSegmentMembersStopsAtVisitorErrorAndCloses(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	seedActiveCompactionRoot(t, st)
	want := errors.New("stop stream")
	count, err := st.StreamRetrievalActiveSegmentMembers(context.Background(), activeCompactionStreamRequest(t, st, "segment-alpha"), func(RetrievalActiveSegmentMember) error {
		return want
	})
	if !errors.Is(err, want) || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := st.RetrievalEmbeddingProfile(ctx, "flush-profile"); err != nil {
		t.Fatalf("read after visitor failure: %v", err)
	}
}

func TestStreamRetrievalActiveSegmentMembersRejectsCorruptCurrentEmbedding(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	seedActiveCompactionRoot(t, st)
	if _, err := st.db.Exec(`UPDATE retrieval_embeddings SET vector_bytes=x'00' WHERE chunk_id='chunk-a' AND profile_id='flush-profile'`); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err := st.StreamRetrievalActiveSegmentMembers(context.Background(), activeCompactionStreamRequest(t, st, "segment-alpha"), func(RetrievalActiveSegmentMember) error {
		called = true
		return nil
	})
	var corruption *RetrievalEmbeddingCorruptionError
	if !errors.As(err, &corruption) || corruption.ChunkID != "chunk-a" || called {
		t.Fatalf("err=%v corruption=%+v called=%t", err, corruption, called)
	}
}

func TestStreamRetrievalActiveSegmentMembersRejectsSelectedCatalogDrift(t *testing.T) {
	t.Parallel()
	st := openCurrentTestStoreAtPath(t, filepath.Join(t.TempDir(), "brain.db"))
	defer func() { _ = st.Close() }()
	seedActiveCompactionRoot(t, st)
	if _, err := st.db.Exec(`UPDATE retrieval_index_segments SET indexed_chunk_count=99 WHERE segment_hash='segment-alpha'`); err != nil {
		t.Fatal(err)
	}
	called := false
	if _, err := st.StreamRetrievalActiveSegmentMembers(context.Background(), activeCompactionStreamRequest(t, st, "segment-alpha"), func(RetrievalActiveSegmentMember) error {
		called = true
		return nil
	}); err == nil || called {
		t.Fatalf("catalog-drift stream err=%v called=%t", err, called)
	}
}

func activeCompactionStreamRequest(t *testing.T, st *Store, hashes ...string) RetrievalActiveSegmentMemberStreamRequest {
	t.Helper()
	profile, err := st.RetrievalEmbeddingProfile(context.Background(), "flush-profile")
	if err != nil {
		t.Fatal(err)
	}
	return RetrievalActiveSegmentMemberStreamRequest{
		ProfileID: "flush-profile", ExpectedActiveGenerationID: profile.ActiveGenerationID,
		ExpectedPurgeEpoch: profile.PurgeEpoch, ExpectedActiveSnapshotRevision: profile.ActiveSnapshotRevision,
		SegmentHashes: hashes,
	}
}
