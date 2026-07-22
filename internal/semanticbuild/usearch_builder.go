//go:build usearch && cgo

package semanticbuild

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/store"
)

const (
	defaultUSearchSegmentConnectivity    = 16
	defaultUSearchSegmentExpansionAdd    = 128
	defaultUSearchSegmentExpansionSearch = 256
)

// USearchSegmentBuilderOptions are intentionally explicit in derived segment
// provenance. Their defaults are the settings that passed the full-vector
// candidate screen; this does not enable the native backend in dbrain itself.
type USearchSegmentBuilderOptions struct {
	Dimensions, Connectivity, ExpansionAdd, ExpansionSearch int
}

type USearchSegmentBuilder struct {
	options semanticindex.USearchOptions
}

type usearchSegmentPayloadSession struct {
	index           *semanticindex.USearch
	dimensions      int
	expected, added int
}

func NewUSearchSegmentBuilder(options USearchSegmentBuilderOptions) (*USearchSegmentBuilder, error) {
	if options.Dimensions <= 0 {
		return nil, fmt.Errorf("usearch segment dimensions must be positive")
	}
	if options.Connectivity < 0 || options.ExpansionAdd < 0 || options.ExpansionSearch < 0 {
		return nil, fmt.Errorf("usearch segment parameters cannot be negative")
	}
	if options.Connectivity == 0 {
		options.Connectivity = defaultUSearchSegmentConnectivity
	}
	if options.ExpansionAdd == 0 {
		options.ExpansionAdd = defaultUSearchSegmentExpansionAdd
	}
	if options.ExpansionSearch == 0 {
		options.ExpansionSearch = defaultUSearchSegmentExpansionSearch
	}
	return &USearchSegmentBuilder{options: semanticindex.USearchOptions{
		Dimensions: options.Dimensions, Connectivity: options.Connectivity,
		ExpansionAdd: options.ExpansionAdd, ExpansionSearch: options.ExpansionSearch,
	}}, nil
}

// Begin reserves a bounded native index for one streaming payload build.
func (b *USearchSegmentBuilder) Begin(ctx context.Context, expectedRows int) (StreamingSegmentPayloadSession, error) {
	if b == nil {
		return nil, fmt.Errorf("usearch segment builder is nil")
	}
	if expectedRows <= 0 {
		return nil, fmt.Errorf("usearch segment expected rows must be positive")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	index, err := semanticindex.NewUSearch(b.options)
	if err != nil {
		return nil, err
	}
	if err := index.Reserve(expectedRows); err != nil {
		_ = index.Close()
		return nil, err
	}
	return &usearchSegmentPayloadSession{index: index, dimensions: b.options.Dimensions, expected: expectedRows}, nil
}

// Build adapts the established slice-based flush contract through the streaming
// session, keeping the current flush caller stable while avoiding duplicate
// builder implementations.
func (b *USearchSegmentBuilder) Build(ctx context.Context, rows []store.RetrievalEmbeddingRow) (func(io.Writer) error, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("usearch segment rows are required")
	}
	session, err := b.Begin(ctx, len(rows))
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()
	for _, row := range rows {
		if err := session.Add(ctx, row); err != nil {
			return nil, err
		}
	}
	return session.Finish(ctx)
}

func (s *usearchSegmentPayloadSession) Add(ctx context.Context, row store.RetrievalEmbeddingRow) error {
	if s == nil || s.index == nil {
		return fmt.Errorf("usearch segment payload session is closed")
	}
	if s.added >= s.expected {
		return fmt.Errorf("usearch segment payload session exceeds expected rows %d", s.expected)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if row.Dimensions != s.dimensions {
		return fmt.Errorf("usearch segment row %s dimensions %d want %d", row.ChunkID, row.Dimensions, s.dimensions)
	}
	vector, err := embedding.DecodeDenseF32(row.VectorBytes, s.dimensions)
	if err != nil {
		return fmt.Errorf("decode usearch segment row %s: %w", row.ChunkID, err)
	}
	if err := s.index.Add(semanticindex.HNSWNode{Ordinal: uint64(s.added), Vector: vector}); err != nil {
		return err
	}
	s.added++
	return nil
}

func (s *usearchSegmentPayloadSession) Finish(ctx context.Context) (func(io.Writer) error, error) {
	if s == nil || s.index == nil {
		return nil, fmt.Errorf("usearch segment payload session is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.added != s.expected {
		return nil, fmt.Errorf("usearch segment payload session has %d rows, want %d", s.added, s.expected)
	}
	var encoded bytes.Buffer
	if err := s.index.Export(&encoded); err != nil {
		_ = s.Close()
		return nil, err
	}
	if err := s.Close(); err != nil {
		return nil, err
	}
	payload := encoded.Bytes()
	return func(writer io.Writer) error {
		if writer == nil {
			return fmt.Errorf("usearch segment payload writer is nil")
		}
		count, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if count != len(payload) {
			return io.ErrShortWrite
		}
		return nil
	}, nil
}

func (s *usearchSegmentPayloadSession) Close() error {
	if s == nil || s.index == nil {
		return nil
	}
	index := s.index
	s.index = nil
	return index.Close()
}

var _ StreamingSegmentPayloadBuilder = (*USearchSegmentBuilder)(nil)
var _ StreamingSegmentPayloadSession = (*usearchSegmentPayloadSession)(nil)
