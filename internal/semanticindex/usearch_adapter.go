//go:build usearch && cgo

package semanticindex

import (
	"fmt"
	"io"
	"math"

	usearch "github.com/unum-cloud/usearch/golang"
)

const BackendUSearch = "usearch"

// USearchOptions configure one in-memory USearch graph. The caller owns the
// enclosing segment format and all payload persistence.
type USearchOptions struct {
	Dimensions      int
	Connectivity    int
	ExpansionAdd    int
	ExpansionSearch int
}

// USearch is a tag-gated native candidate adapter. It is intentionally not a
// serving Searcher and owns no dbrain files.
type USearch struct {
	index      *usearch.Index
	dimensions int
}

func NewUSearch(opts USearchOptions) (*USearch, error) {
	if err := validateUSearchOptions(opts); err != nil {
		return nil, err
	}
	config := usearch.DefaultConfig(uint(opts.Dimensions))
	config.Metric = usearch.Cosine
	config.Quantization = usearch.F32
	config.Connectivity = uint(opts.Connectivity)
	config.ExpansionAdd = uint(opts.ExpansionAdd)
	config.ExpansionSearch = uint(opts.ExpansionSearch)
	index, err := usearch.NewIndex(config)
	if err != nil {
		return nil, fmt.Errorf("create usearch index: %w", err)
	}
	return &USearch{index: index, dimensions: opts.Dimensions}, nil
}

func validateUSearchOptions(opts USearchOptions) error {
	if opts.Dimensions <= 0 {
		return fmt.Errorf("usearch dimensions must be positive")
	}
	if opts.Connectivity < 0 || opts.ExpansionAdd < 0 || opts.ExpansionSearch < 0 {
		return fmt.Errorf("usearch parameters cannot be negative")
	}
	return nil
}

func (u *USearch) Reserve(capacity int) error {
	if err := u.available(); err != nil {
		return err
	}
	if capacity < 0 {
		return fmt.Errorf("usearch capacity cannot be negative")
	}
	if uint64(capacity) > uint64(math.MaxUint) {
		return fmt.Errorf("usearch capacity overflows native size")
	}
	if err := u.index.Reserve(uint(capacity)); err != nil {
		return fmt.Errorf("reserve usearch capacity: %w", err)
	}
	return nil
}

func (u *USearch) Add(nodes ...HNSWNode) error {
	if err := u.available(); err != nil {
		return err
	}
	for _, node := range nodes {
		if len(node.Vector) != u.dimensions {
			return fmt.Errorf("usearch ordinal %d dimensions %d want %d", node.Ordinal, len(node.Vector), u.dimensions)
		}
		if err := u.index.Add(usearch.Key(node.Ordinal), node.Vector); err != nil {
			return fmt.Errorf("add usearch ordinal %d: %w", node.Ordinal, err)
		}
	}
	return nil
}

func (u *USearch) Search(query []float32, limit int) ([]HNSWHit, error) {
	if err := u.available(); err != nil {
		return nil, err
	}
	if len(query) != u.dimensions {
		return nil, fmt.Errorf("usearch query dimensions %d want %d", len(query), u.dimensions)
	}
	if limit <= 0 {
		return []HNSWHit{}, nil
	}
	keys, _, err := u.index.Search(query, uint(limit))
	if err != nil {
		return nil, fmt.Errorf("search usearch: %w", err)
	}
	hits := make([]HNSWHit, 0, len(keys))
	for _, key := range keys {
		hits = append(hits, HNSWHit{Ordinal: uint64(key)})
	}
	return hits, nil
}

func (u *USearch) Export(w io.Writer) error {
	if err := u.available(); err != nil {
		return err
	}
	if w == nil {
		return fmt.Errorf("usearch export writer is nil")
	}
	size, err := u.index.SerializedLength()
	if err != nil {
		return fmt.Errorf("measure usearch payload: %w", err)
	}
	if uint64(size) > uint64(math.MaxInt) {
		return fmt.Errorf("usearch payload exceeds Go allocation limit")
	}
	payload := make([]byte, int(size))
	if err := u.index.SaveBuffer(payload, size); err != nil {
		return fmt.Errorf("save usearch payload: %w", err)
	}
	n, err := w.Write(payload)
	if err != nil {
		return fmt.Errorf("write usearch payload: %w", err)
	}
	if n != len(payload) {
		return fmt.Errorf("write usearch payload: %w", io.ErrShortWrite)
	}
	return nil
}

func (u *USearch) Import(r io.Reader) error {
	if err := u.available(); err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("usearch import reader is nil")
	}
	payload, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read usearch payload: %w", err)
	}
	if len(payload) == 0 {
		return fmt.Errorf("usearch payload is empty")
	}
	if err := u.index.LoadBuffer(payload, uint(len(payload))); err != nil {
		return fmt.Errorf("load usearch payload: %w", err)
	}
	dimensions, err := u.index.Dimensions()
	if err != nil {
		return fmt.Errorf("read imported usearch dimensions: %w", err)
	}
	if int(dimensions) != u.dimensions {
		return fmt.Errorf("usearch imported dimensions %d want %d", dimensions, u.dimensions)
	}
	return nil
}

func (u *USearch) Close() error {
	if u == nil || u.index == nil {
		return nil
	}
	index := u.index
	u.index = nil
	if err := index.Close(); err != nil {
		return fmt.Errorf("close usearch index: %w", err)
	}
	return nil
}

func (u *USearch) available() error {
	if u == nil || u.index == nil {
		return fmt.Errorf("usearch index is closed")
	}
	return nil
}
