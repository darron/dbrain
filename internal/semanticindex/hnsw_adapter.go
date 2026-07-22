package semanticindex

import (
	"fmt"
	"io"
	"math/rand"

	"github.com/coder/hnsw"
)

const BackendHNSW = "hnsw"

// HNSWOptions configure one in-memory HNSW graph. Segment persistence and
// publication remain the caller's responsibility.
type HNSWOptions struct {
	Dimensions int
	Seed       uint64
	M          int
	EfSearch   int
}

// HNSWNode identifies one graph vector by its dense, segment-local ordinal.
type HNSWNode struct {
	Ordinal uint64
	Vector  []float32
}

// HNSWHit is an approximate graph candidate. Callers must exactly rerank and
// validate it against authoritative SQLite state before it becomes evidence.
type HNSWHit struct {
	Ordinal uint64
}

// HNSW is the narrow backend adapter used by the segmented-index bakeoff.
// It owns no files and is not a serving Searcher.
type HNSW struct {
	graph      *hnsw.Graph[uint64]
	dimensions int
}

func NewHNSW(opts HNSWOptions) (*HNSW, error) {
	if opts.Dimensions <= 0 {
		return nil, fmt.Errorf("hnsw dimensions must be positive")
	}
	if opts.M < 0 {
		return nil, fmt.Errorf("hnsw neighbor degree cannot be negative")
	}
	graph := hnsw.NewGraph[uint64]()
	graph.Rng = rand.New(rand.NewSource(int64(opts.Seed)))
	if opts.M > 0 {
		graph.M = opts.M
	}
	if opts.EfSearch > 0 {
		graph.EfSearch = opts.EfSearch
	}
	return &HNSW{graph: graph, dimensions: opts.Dimensions}, nil
}

func (h *HNSW) Add(nodes ...HNSWNode) error {
	if h == nil || h.graph == nil {
		return fmt.Errorf("hnsw graph is nil")
	}
	converted := make([]hnsw.Node[uint64], 0, len(nodes))
	for _, node := range nodes {
		if len(node.Vector) != h.dimensions {
			return fmt.Errorf("hnsw ordinal %d dimensions %d want %d", node.Ordinal, len(node.Vector), h.dimensions)
		}
		vector := append([]float32(nil), node.Vector...)
		converted = append(converted, hnsw.Node[uint64]{Key: node.Ordinal, Value: vector})
	}
	h.graph.Add(converted...)
	return nil
}

func (h *HNSW) Search(query []float32, limit int) ([]HNSWHit, error) {
	if h == nil || h.graph == nil {
		return nil, fmt.Errorf("hnsw graph is nil")
	}
	if len(query) != h.dimensions {
		return nil, fmt.Errorf("hnsw query dimensions %d want %d", len(query), h.dimensions)
	}
	if limit <= 0 {
		return []HNSWHit{}, nil
	}
	nodes := h.graph.Search(query, limit)
	hits := make([]HNSWHit, 0, len(nodes))
	for _, node := range nodes {
		hits = append(hits, HNSWHit{Ordinal: node.Key})
	}
	return hits, nil
}

func (h *HNSW) Export(w io.Writer) error {
	if h == nil || h.graph == nil {
		return fmt.Errorf("hnsw graph is nil")
	}
	return h.graph.Export(w)
}

func (h *HNSW) Import(r io.Reader) error {
	if h == nil || h.graph == nil {
		return fmt.Errorf("hnsw graph is nil")
	}
	if err := h.graph.Import(r); err != nil {
		return err
	}
	if got := h.graph.Dims(); got != 0 && got != h.dimensions {
		return fmt.Errorf("hnsw imported dimensions %d want %d", got, h.dimensions)
	}
	return nil
}
