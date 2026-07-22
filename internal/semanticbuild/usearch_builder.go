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

// Build returns a copied opaque payload writer. It closes all native state
// before returning, so the caller owns only a Go byte slice and cannot retain
// a live native index beyond this build operation.
func (b *USearchSegmentBuilder) Build(ctx context.Context, rows []store.RetrievalEmbeddingRow) (func(io.Writer) error, error) {
	if b == nil {
		return nil, fmt.Errorf("usearch segment builder is nil")
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("usearch segment rows are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	index, err := semanticindex.NewUSearch(b.options)
	if err != nil {
		return nil, err
	}
	defer func() { _ = index.Close() }()
	if err := index.Reserve(len(rows)); err != nil {
		return nil, err
	}
	for ordinal, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if row.Dimensions != b.options.Dimensions {
			return nil, fmt.Errorf("usearch segment row %s dimensions %d want %d", row.ChunkID, row.Dimensions, b.options.Dimensions)
		}
		vector, err := embedding.DecodeDenseF32(row.VectorBytes, b.options.Dimensions)
		if err != nil {
			return nil, fmt.Errorf("decode usearch segment row %s: %w", row.ChunkID, err)
		}
		if err := index.Add(semanticindex.HNSWNode{Ordinal: uint64(ordinal), Vector: vector}); err != nil {
			return nil, err
		}
	}
	var encoded bytes.Buffer
	if err := index.Export(&encoded); err != nil {
		return nil, err
	}
	payload := append([]byte(nil), encoded.Bytes()...)
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
