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

func TestUSearchSegmentBuilderStreamsReopenablePayload(t *testing.T) {
	builder, err := NewUSearchSegmentBuilder(USearchSegmentBuilderOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	session, err := builder.Begin(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Add(context.Background(), store.RetrievalEmbeddingRow{VectorBytes: embedding.EncodeDenseF32([]float32{1, 0}), Dimensions: 2}); err != nil {
		t.Fatal(err)
	}
	if err := session.Add(context.Background(), store.RetrievalEmbeddingRow{VectorBytes: embedding.EncodeDenseF32([]float32{0, 1}), Dimensions: 2}); err != nil {
		t.Fatal(err)
	}
	payload, err := session.Finish(context.Background())
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
	hits, err := reopened.Search([]float32{1, 0}, 2)
	if err != nil || len(hits) != 2 || hits[0].Ordinal != 0 {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
	if err := session.Add(context.Background(), store.RetrievalEmbeddingRow{VectorBytes: embedding.EncodeDenseF32([]float32{1, 0}), Dimensions: 2}); err == nil {
		t.Fatal("Add succeeded after Finish")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUSearchSegmentBuilderStreamingSessionRejectsInvalidLifecycle(t *testing.T) {
	builder, err := NewUSearchSegmentBuilder(USearchSegmentBuilderOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Begin(context.Background(), 0); err == nil {
		t.Fatal("Begin accepted zero expected rows")
	}
	session, err := builder.Begin(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Finish(context.Background()); err == nil {
		t.Fatal("Finish accepted underfilled session")
	}
	if err := session.Add(context.Background(), store.RetrievalEmbeddingRow{VectorBytes: []byte{1}, Dimensions: 2}); err == nil {
		t.Fatal("Add accepted malformed vector")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := session.Add(ctx, store.RetrievalEmbeddingRow{VectorBytes: embedding.EncodeDenseF32([]float32{1, 0}), Dimensions: 2}); err == nil {
		t.Fatal("Add ignored cancelled context")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Add(context.Background(), store.RetrievalEmbeddingRow{VectorBytes: embedding.EncodeDenseF32([]float32{1, 0}), Dimensions: 2}); err == nil {
		t.Fatal("Add succeeded after Close")
	}
	full, err := builder.Begin(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := full.Add(context.Background(), store.RetrievalEmbeddingRow{VectorBytes: embedding.EncodeDenseF32([]float32{1, 0}), Dimensions: 2}); err != nil {
		t.Fatal(err)
	}
	if err := full.Add(context.Background(), store.RetrievalEmbeddingRow{VectorBytes: embedding.EncodeDenseF32([]float32{0, 1}), Dimensions: 2}); err == nil {
		t.Fatal("Add accepted more than expected rows")
	}
	if err := full.Close(); err != nil {
		t.Fatal(err)
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
