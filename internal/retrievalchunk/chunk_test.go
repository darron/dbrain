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
