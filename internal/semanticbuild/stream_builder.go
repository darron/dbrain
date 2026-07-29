package semanticbuild

import (
	"context"
	"io"

	"github.com/darron/dbrain/internal/store"
)

// StreamingSegmentPayloadBuilder creates a bounded session that accepts
// source embeddings one at a time. It is separate from SegmentPayloadBuilder
// so the established flush contract remains stable while compaction can avoid
// materializing its full source-row slice.
type StreamingSegmentPayloadBuilder interface {
	Begin(context.Context, int) (StreamingSegmentPayloadSession, error)
}

// StreamingSegmentPayloadSession accepts exactly the count declared at Begin.
// Finish returns a self-contained opaque payload writer and releases any native
// state; Close is idempotent and releases a session abandoned before Finish.
type StreamingSegmentPayloadSession interface {
	Add(context.Context, store.RetrievalEmbeddingRow) error
	Finish(context.Context) (func(io.Writer) error, error)
	Close() error
}
