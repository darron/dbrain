package retrievalchunk

import "testing"

func TestRetrievalProjectionAndChunkVersionsAreExported(t *testing.T) {
	t.Parallel()
	if ProjectionVersion == "" {
		t.Fatal("ProjectionVersion must be a stable exported identity")
	}
	if Version == "" {
		t.Fatal("Version must be a stable exported identity")
	}
}

func TestProjectionV2AndChunkerV3VersionsAreExact(t *testing.T) {
	if ProjectionVersion != "retrieval-projection-v2" {
		t.Fatalf("ProjectionVersion = %q", ProjectionVersion)
	}
	if Version != "retrieval-chunker-v3" {
		t.Fatalf("Version = %q", Version)
	}
	if MaxUTF8Bytes != 1800 {
		t.Fatalf("MaxUTF8Bytes = %d", MaxUTF8Bytes)
	}
}
