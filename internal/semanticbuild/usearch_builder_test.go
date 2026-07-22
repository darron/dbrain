//go:build usearch && cgo

package semanticbuild

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/store"
)

func TestUSearchSegmentBuilderExportsReopenablePayload(t *testing.T) {
	builder, err := NewUSearchSegmentBuilder(USearchSegmentBuilderOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := builder.Build(context.Background(), []store.RetrievalEmbeddingRow{
		{VectorBytes: embedding.EncodeDenseF32([]float32{1, 0}), Dimensions: 2},
		{VectorBytes: embedding.EncodeDenseF32([]float32{0, 1}), Dimensions: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := payload(&encoded); err != nil {
		t.Fatal(err)
	}
	reopened, err := semanticindex.NewUSearch(semanticindex.USearchOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Import(&encoded); err != nil {
		t.Fatal(err)
	}
	hits, err := reopened.Search([]float32{1, 0}, 1)
	if err != nil || len(hits) != 1 || hits[0].Ordinal != 0 {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
}

func TestUSearchSegmentBuilderRejectsInvalidRowsAndCancellation(t *testing.T) {
	builder, err := NewUSearchSegmentBuilder(USearchSegmentBuilderOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Build(context.Background(), nil); err == nil {
		t.Fatal("Build accepted empty rows")
	}
	if _, err := builder.Build(context.Background(), []store.RetrievalEmbeddingRow{{VectorBytes: []byte{1}, Dimensions: 2}}); err == nil {
		t.Fatal("Build accepted malformed vector")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := builder.Build(ctx, []store.RetrievalEmbeddingRow{{VectorBytes: embedding.EncodeDenseF32([]float32{1, 0}), Dimensions: 2}}); err == nil {
		t.Fatal("Build ignored cancelled context")
	}
}

func TestUSearchSegmentBuilderPayloadWriterPropagatesShortWrite(t *testing.T) {
	builder, err := NewUSearchSegmentBuilder(USearchSegmentBuilderOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := builder.Build(context.Background(), []store.RetrievalEmbeddingRow{{VectorBytes: embedding.EncodeDenseF32([]float32{1, 0}), Dimensions: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if err := payload(shortPayloadWriter{}); err == nil || !strings.Contains(err.Error(), "short write") {
		t.Fatalf("payload short write error = %v", err)
	}
}

type shortPayloadWriter struct{}

func (shortPayloadWriter) Write(value []byte) (int, error) { return len(value) - 1, nil }

var _ io.Writer = shortPayloadWriter{}
