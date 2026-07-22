// Package annbakeoff evaluates a candidate ANN payload without making it part
// of dbrain's serving or cache lifecycle.
package annbakeoff

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"time"

	"github.com/darron/dbrain/internal/semanticindex"
)

const (
	SchemaVersion = "dbrain.semantic-ann-bakeoff.v1"

	StatusPassed   = "passed"
	StatusRejected = "rejected"

	ReasonHeapLimit   = "heap_system_limit_exceeded"
	ReasonRecallFloor = "recall_floor_not_met"
)

// Index is the narrow candidate contract used only by the content-free ANN
// bakeoff. It deliberately contains no SQLite, segment, or serving behavior.
type Index interface {
	Reserve(int) error
	Add(...semanticindex.HNSWNode) error
	Search([]float32, int) ([]semanticindex.HNSWHit, error)
	Export(io.Writer) error
	Import(io.Reader) error
	Close() error
}

// Factory creates one empty candidate index for a screening stage.
type Factory func(Options) (Index, error)

// Options bounds one deterministic candidate screening run.
type Options struct {
	Sizes           []int
	Dimensions      int
	QueryCount      int
	WarmRepetitions int
	Seed            uint64
	RecallAt        int
	MinimumRecall   float64
	MaxHeapSysBytes uint64
	EfSearch        int
	M               int
	Connectivity    int
	ExpansionAdd    int
	ExpansionSearch int
}

// Report contains content-free evidence for a single backend screening run.
type Report struct {
	SchemaVersion string         `json:"schema_version"`
	Backend       string         `json:"backend"`
	Dimensions    int            `json:"dimensions"`
	Seed          uint64         `json:"seed"`
	RecallAt      int            `json:"recall_at"`
	MinimumRecall float64        `json:"minimum_recall"`
	EfSearch      int            `json:"ef_search,omitempty"`
	M             int            `json:"m,omitempty"`
	Parameters    map[string]int `json:"parameters"`
	Status        string         `json:"status"`
	Stages        []StageReport  `json:"stages"`
}

// StageReport records one vector-count stage. Durations are nanoseconds so a
// downstream report can derive milliseconds without losing short query times.
type StageReport struct {
	VectorCount      int            `json:"vector_count"`
	EfSearch         int            `json:"ef_search,omitempty"`
	M                int            `json:"m,omitempty"`
	Parameters       map[string]int `json:"parameters"`
	Status           string         `json:"status"`
	Reason           string         `json:"reason,omitempty"`
	CorpusBuildNanos int64          `json:"corpus_build_nanos"`
	GraphBuildNanos  int64          `json:"graph_build_nanos"`
	ExportNanos      int64          `json:"export_nanos"`
	ReopenNanos      int64          `json:"reopen_nanos"`
	ExactQueryNanos  int64          `json:"exact_query_nanos"`
	QueryP50Nanos    int64          `json:"query_p50_nanos"`
	QueryP95Nanos    int64          `json:"query_p95_nanos"`
	PayloadBytes     int            `json:"payload_bytes"`
	RecallAtK        float64        `json:"recall_at_k"`
	ReopenRecallAtK  float64        `json:"reopen_recall_at_k"`
	BuildHeapAlloc   uint64         `json:"build_heap_alloc_bytes"`
	BuildHeapSys     uint64         `json:"build_heap_sys_bytes"`
	LoadedHeapAlloc  uint64         `json:"loaded_heap_alloc_bytes"`
	LoadedHeapSys    uint64         `json:"loaded_heap_sys_bytes"`
}

// Run builds every requested stage in order. Gate failures are reported in the
// returned report rather than becoming opaque errors, allowing callers to save
// the exact evidence before stopping the next stage.
func Run(ctx context.Context, opts Options) (Report, error) {
	report, err := RunWith(ctx, opts, semanticindex.BackendHNSW, map[string]int{
		"ef_search": effectiveEfSearch(opts),
		"m":         effectiveM(opts),
	}, func(opts Options) (Index, error) {
		return semanticindex.NewHNSW(semanticindex.HNSWOptions{
			Dimensions: opts.Dimensions,
			Seed:       opts.Seed,
			M:          effectiveM(opts),
			EfSearch:   effectiveEfSearch(opts),
		})
	})
	annotateHNSWParameters(&report, opts)
	return report, err
}

// RunWith evaluates one named candidate against the deterministic corpus and
// exact oracle. Candidate errors are returned; gate failures stay in Report so
// callers can persist the evidence before stopping.
func RunWith(ctx context.Context, opts Options, backend string, parameters map[string]int, factory Factory) (Report, error) {
	if err := validateOptions(opts); err != nil {
		return Report{}, err
	}
	if backend == "" {
		return Report{}, fmt.Errorf("backend is required")
	}
	if factory == nil {
		return Report{}, fmt.Errorf("candidate factory is required")
	}
	report := Report{
		SchemaVersion: SchemaVersion,
		Backend:       backend,
		Dimensions:    opts.Dimensions,
		Seed:          opts.Seed,
		RecallAt:      opts.RecallAt,
		MinimumRecall: opts.MinimumRecall,
		Parameters:    cloneParameters(parameters),
		Status:        StatusPassed,
		Stages:        make([]StageReport, 0, len(opts.Sizes)),
	}
	for _, size := range opts.Sizes {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		stage, err := runStage(ctx, size, opts, parameters, factory)
		if err != nil {
			return report, err
		}
		report.Stages = append(report.Stages, stage)
		if stage.Status == StatusRejected {
			report.Status = StatusRejected
			return report, nil
		}
	}
	return report, nil
}

func validateOptions(opts Options) error {
	if len(opts.Sizes) == 0 {
		return fmt.Errorf("at least one vector-count stage is required")
	}
	if opts.Dimensions <= 0 {
		return fmt.Errorf("dimensions must be positive")
	}
	if opts.QueryCount <= 0 || opts.WarmRepetitions <= 0 {
		return fmt.Errorf("query count and warm repetitions must be positive")
	}
	if opts.RecallAt <= 0 {
		return fmt.Errorf("recall-at must be positive")
	}
	if opts.EfSearch < 0 {
		return fmt.Errorf("ef search cannot be negative")
	}
	if opts.M < 0 {
		return fmt.Errorf("neighbor degree cannot be negative")
	}
	if opts.MinimumRecall < 0 || opts.MinimumRecall > 1 || math.IsNaN(opts.MinimumRecall) {
		return fmt.Errorf("minimum recall must be between zero and one")
	}
	previous := 0
	for _, size := range opts.Sizes {
		if size < opts.RecallAt {
			return fmt.Errorf("stage size %d is smaller than recall-at %d", size, opts.RecallAt)
		}
		if size <= previous {
			return fmt.Errorf("stages must be strictly increasing")
		}
		previous = size
	}
	return nil
}

func runStage(ctx context.Context, size int, opts Options, parameters map[string]int, factory Factory) (StageReport, error) {
	stage := StageReport{VectorCount: size, Parameters: cloneParameters(parameters), Status: StatusPassed}
	corpusStart := time.Now()
	corpus, err := newCorpus(size, opts.Dimensions, opts.Seed)
	if err != nil {
		return stage, err
	}
	stage.CorpusBuildNanos = time.Since(corpusStart).Nanoseconds()

	buildStart := time.Now()
	index, err := factory(opts)
	if err != nil {
		return stage, err
	}
	indexClosed := false
	defer func() {
		if !indexClosed {
			_ = index.Close()
		}
	}()
	if err := index.Reserve(size); err != nil {
		return stage, err
	}
	const batchSize = 512
	batch := make([]semanticindex.HNSWNode, 0, batchSize)
	for ordinal := 0; ordinal < size; ordinal++ {
		if ordinal%batchSize == 0 {
			if err := ctx.Err(); err != nil {
				return stage, err
			}
		}
		batch = append(batch, semanticindex.HNSWNode{Ordinal: uint64(ordinal), Vector: corpus.vector(ordinal)})
		if len(batch) == cap(batch) {
			if err := index.Add(batch...); err != nil {
				return stage, err
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		if err := index.Add(batch...); err != nil {
			return stage, err
		}
	}
	stage.GraphBuildNanos = time.Since(buildStart).Nanoseconds()
	stage.BuildHeapAlloc, stage.BuildHeapSys = heapMetrics()

	exportStart := time.Now()
	var payload bytes.Buffer
	if err := index.Export(&payload); err != nil {
		return stage, fmt.Errorf("export hnsw payload: %w", err)
	}
	stage.ExportNanos = time.Since(exportStart).Nanoseconds()
	stage.PayloadBytes = payload.Len()

	// Reopen only the exported graph. The caller-owned segment format will add
	// checksums and canonical membership around this opaque payload later.
	if err := index.Close(); err != nil {
		return stage, err
	}
	indexClosed = true
	runtime.GC()
	reopenStart := time.Now()
	reopened, err := factory(opts)
	if err != nil {
		return stage, err
	}
	defer func() { _ = reopened.Close() }()
	if err := reopened.Import(bytes.NewReader(payload.Bytes())); err != nil {
		return stage, fmt.Errorf("reopen candidate payload: %w", err)
	}
	stage.ReopenNanos = time.Since(reopenStart).Nanoseconds()
	payload.Reset()
	payload = bytes.Buffer{}
	runtime.GC()
	stage.LoadedHeapAlloc, stage.LoadedHeapSys = heapMetrics()

	queries := queryOrdinals(size, opts.QueryCount)
	exactStart := time.Now()
	exact := make([][]uint64, len(queries))
	for i, ordinal := range queries {
		if err := ctx.Err(); err != nil {
			return stage, err
		}
		exact[i] = exactTopK(corpus, corpus.vector(ordinal), opts.RecallAt)
	}
	stage.ExactQueryNanos = time.Since(exactStart).Nanoseconds()

	firstRecall, samples, err := measureSearch(ctx, reopened, corpus, queries, exact, opts)
	if err != nil {
		return stage, err
	}
	stage.RecallAtK, stage.ReopenRecallAtK = firstRecall, firstRecall
	stage.QueryP50Nanos = percentile(samples, 0.50).Nanoseconds()
	stage.QueryP95Nanos = percentile(samples, 0.95).Nanoseconds()

	if opts.MaxHeapSysBytes > 0 && stage.LoadedHeapSys > opts.MaxHeapSysBytes {
		stage.Status, stage.Reason = StatusRejected, ReasonHeapLimit
		return stage, nil
	}
	if stage.RecallAtK < opts.MinimumRecall {
		stage.Status, stage.Reason = StatusRejected, ReasonRecallFloor
	}
	return stage, nil
}

func measureSearch(ctx context.Context, index Index, corpus corpus, queries []int, exact [][]uint64, opts Options) (float64, []time.Duration, error) {
	var recallTotal float64
	samples := make([]time.Duration, 0, len(queries)*opts.WarmRepetitions)
	for iteration := 0; iteration < opts.WarmRepetitions; iteration++ {
		for queryIndex, ordinal := range queries {
			if err := ctx.Err(); err != nil {
				return 0, nil, err
			}
			started := time.Now()
			hits, err := index.Search(corpus.vector(ordinal), opts.RecallAt)
			samples = append(samples, time.Since(started))
			if err != nil {
				return 0, nil, err
			}
			if iteration == 0 {
				recallTotal += recall(exact[queryIndex], hits)
			}
		}
	}
	return recallTotal / float64(len(queries)), samples, nil
}

type corpus struct {
	values     []float32
	dimensions int
}

func newCorpus(count, dimensions int, seed uint64) (corpus, error) {
	if count <= 0 || dimensions <= 0 {
		return corpus{}, fmt.Errorf("corpus count and dimensions must be positive")
	}
	if count > math.MaxInt/dimensions {
		return corpus{}, fmt.Errorf("corpus dimensions overflow")
	}
	values := make([]float32, count*dimensions)
	rng := rand.New(rand.NewSource(int64(seed)))
	clusters := minInt(64, maxInt(1, count/8))
	bases := make([]float64, clusters*dimensions)
	for cluster := 0; cluster < clusters; cluster++ {
		normalize64(bases[cluster*dimensions:(cluster+1)*dimensions], rng)
	}
	for ordinal := 0; ordinal < count; ordinal++ {
		vector := values[ordinal*dimensions : (ordinal+1)*dimensions]
		base := bases[(ordinal%clusters)*dimensions : (ordinal%clusters+1)*dimensions]
		var squared float64
		for dimension := range vector {
			value := 0.93*base[dimension] + 0.07*(rng.Float64()*2-1)
			vector[dimension] = float32(value)
			squared += value * value
		}
		inverse := 1 / math.Sqrt(squared)
		for dimension := range vector {
			vector[dimension] = float32(float64(vector[dimension]) * inverse)
		}
	}
	return corpus{values: values, dimensions: dimensions}, nil
}

func normalize64(vector []float64, rng *rand.Rand) {
	var squared float64
	for i := range vector {
		vector[i] = rng.Float64()*2 - 1
		squared += vector[i] * vector[i]
	}
	inverse := 1 / math.Sqrt(squared)
	for i := range vector {
		vector[i] *= inverse
	}
}

func (c corpus) vector(ordinal int) []float32 {
	start := ordinal * c.dimensions
	return c.values[start : start+c.dimensions]
}

type scoredOrdinal struct {
	ordinal  uint64
	distance float64
}

func exactTopK(c corpus, query []float32, limit int) []uint64 {
	best := make([]scoredOrdinal, 0, limit)
	for ordinal := 0; ordinal*c.dimensions < len(c.values); ordinal++ {
		candidate := scoredOrdinal{ordinal: uint64(ordinal), distance: cosineDistance(query, c.vector(ordinal))}
		insert := sort.Search(len(best), func(index int) bool { return better(candidate, best[index]) })
		if insert == len(best) && len(best) == limit {
			continue
		}
		best = append(best, scoredOrdinal{})
		copy(best[insert+1:], best[insert:])
		best[insert] = candidate
		if len(best) > limit {
			best = best[:limit]
		}
	}
	result := make([]uint64, len(best))
	for i := range best {
		result[i] = best[i].ordinal
	}
	return result
}

func cosineDistance(a, b []float32) float64 {
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return 1 - dot
}

func better(a, b scoredOrdinal) bool {
	return a.distance < b.distance || (a.distance == b.distance && a.ordinal < b.ordinal)
}

func recall(exact []uint64, hits []semanticindex.HNSWHit) float64 {
	if len(exact) == 0 {
		return 1
	}
	want := make(map[uint64]struct{}, len(exact))
	for _, ordinal := range exact {
		want[ordinal] = struct{}{}
	}
	matched := 0
	for _, hit := range hits {
		if _, ok := want[hit.Ordinal]; ok {
			matched++
		}
	}
	return float64(matched) / float64(len(exact))
}

func queryOrdinals(count, queryCount int) []int {
	result := make([]int, queryCount)
	for index := range result {
		result[index] = index * count / queryCount
	}
	return result
}

func heapMetrics() (uint64, uint64) {
	var metrics runtime.MemStats
	runtime.ReadMemStats(&metrics)
	return metrics.HeapAlloc, metrics.HeapSys
}

func percentile(values []time.Duration, percentile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(math.Ceil(percentile*float64(len(sorted)))) - 1
	return sorted[maxInt(0, minInt(index, len(sorted)-1))]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func effectiveEfSearch(opts Options) int {
	if opts.EfSearch > 0 {
		return opts.EfSearch
	}
	return 256
}

func effectiveM(opts Options) int {
	if opts.M > 0 {
		return opts.M
	}
	return 16
}

func cloneParameters(parameters map[string]int) map[string]int {
	copy := make(map[string]int, len(parameters))
	for key, value := range parameters {
		copy[key] = value
	}
	return copy
}

func annotateHNSWParameters(report *Report, opts Options) {
	if report == nil {
		return
	}
	report.EfSearch = effectiveEfSearch(opts)
	report.M = effectiveM(opts)
	for index := range report.Stages {
		report.Stages[index].EfSearch = report.EfSearch
		report.Stages[index].M = report.M
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
