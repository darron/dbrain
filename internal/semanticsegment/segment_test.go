package semanticsegment

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishSegmentReopensVerifiedPayload(t *testing.T) {
	t.Parallel()
	cache := t.TempDir()
	segment, err := PublishSegment(cache, SegmentInput{
		DatabaseID:     "db-1",
		ProfileID:      "profile-1",
		Backend:        "usearch",
		BackendVersion: "2.26.0",
		DistanceMetric: "cosine",
		Dimensions:     2,
		Members: []Member{{
			Ordinal: 0, ChunkID: "chunk-a", Revision: 7, VectorHash: "hash-a",
		}},
		Payload: func(w io.Writer) error {
			_, err := io.WriteString(w, "opaque payload")
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenSegment(cache, "db-1", "profile-1", segment.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Manifest.Members; len(got) != 1 || got[0].ChunkID != "chunk-a" || got[0].Revision != 7 {
		t.Fatalf("members = %#v", got)
	}
}

func TestOpenSegmentRejectsTamperedPayload(t *testing.T) {
	t.Parallel()
	cache := t.TempDir()
	segment, err := PublishSegment(cache, SegmentInput{
		DatabaseID: "db-1", ProfileID: "profile-1", Backend: "usearch", BackendVersion: "2.26.0",
		DistanceMetric: "cosine", Dimensions: 2,
		Members: []Member{{Ordinal: 0, ChunkID: "chunk-a", Revision: 1, VectorHash: "hash-a"}},
		Payload: func(w io.Writer) error { _, err := io.WriteString(w, "opaque payload"); return err },
	})
	if err != nil {
		t.Fatal(err)
	}
	payloadPath := filepath.Join(cache, filepath.FromSlash(segment.RelativePath), PayloadFileName)
	if err := os.WriteFile(payloadPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSegment(cache, "db-1", "profile-1", segment.Hash); err == nil {
		t.Fatal("OpenSegment succeeded after payload tampering")
	}
}

func TestPublishRootReopensReferencedSegment(t *testing.T) {
	t.Parallel()
	cache := t.TempDir()
	segment, err := PublishSegment(cache, SegmentInput{
		DatabaseID: "db-1", ProfileID: "profile-1", Backend: "usearch", BackendVersion: "2.26.0",
		DistanceMetric: "cosine", Dimensions: 2,
		Members: []Member{{Ordinal: 0, ChunkID: "chunk-a", Revision: 1, VectorHash: "hash-a"}},
		Payload: func(w io.Writer) error { _, err := io.WriteString(w, "opaque payload"); return err },
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := PublishRoot(cache, RootInput{
		DatabaseID: "db-1", ProfileID: "profile-1", GenerationID: "generation-1", SnapshotRevision: 1,
		Segments: []RootSegment{{Hash: segment.Hash, RelativePath: segment.RelativePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenRoot(cache, "db-1", "profile-1", "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Manifest.Segments; len(got) != 1 || got[0].Hash != segment.Hash || root.RelativePath == "" {
		t.Fatalf("root = %#v reopened = %#v", root, reopened)
	}
}
