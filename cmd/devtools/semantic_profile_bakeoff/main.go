// semantic_profile_bakeoff evaluates an explicit restored dbrain SQLite corpus
// against candidate local embedding dimensions. It is deliberately read-only:
// it never opens the configured production database and never writes vectors.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/retrievalchunk"
	"github.com/darron/dbrain/internal/store"
)

type profileResult struct {
	Dimensions int    `json:"dimensions"`
	Status     string `json:"status"`
	Vectors    int    `json:"vectors"`
	Error      string `json:"error,omitempty"`
}
type report struct {
	Database          string          `json:"database"`
	Model             string          `json:"model"`
	MaxBytes          int             `json:"max_bytes"`
	Parents           int             `json:"parents"`
	UniqueChunks      int             `json:"unique_chunks"`
	Occurrences       int             `json:"occurrences"`
	ContextFailures   int             `json:"context_failures"`
	OversizedWindows  int             `json:"oversized_windows"`
	ByteDistribution  map[string]int  `json:"byte_distribution"`
	CandidateProfiles []profileResult `json:"candidate_profiles"`
}

type bakeoffOptions struct {
	database   string
	reportPath string
	baseURL    string
	model      string
	dimensions []int
	maxBytes   int
}

type parentCorpus interface {
	ListRetrievalParents(context.Context, string, int) ([]retrievalchunk.Parent, error)
}

type bakeoffDeps struct {
	newProvider func(embedding.OllamaOptions) (embedding.Provider, error)
	writeReport func(string, report) error
}

func main() {
	ctx := context.Background()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "semantic profile bakeoff:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("semantic_profile_bakeoff", flag.ContinueOnError)
	dbPath := flags.String("db", "", "explicit restored SQLite database path")
	model := flags.String("model", "embeddinggemma:300m-bf16", "Ollama embedding model")
	dimensionList := flags.String("dimensions", "768,384", "comma-separated requested embedding dimensions")
	maxBytes := flags.Int("max-bytes", retrievalchunk.MaxUTF8Bytes, "required maximum UTF-8 bytes per projected window")
	reportPath := flags.String("report", "", "write JSON report to this path")
	baseURL := flags.String("base-url", "http://127.0.0.1:11434", "Ollama base URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*dbPath) == "" || strings.TrimSpace(*reportPath) == "" {
		return errors.New("--db and --report are required")
	}
	if *maxBytes != retrievalchunk.MaxUTF8Bytes {
		return fmt.Errorf("--max-bytes must be %d for chunker %s", retrievalchunk.MaxUTF8Bytes, retrievalchunk.Version)
	}
	production, err := refusesLiveProductionDB(*dbPath)
	if err != nil {
		return fmt.Errorf("resolve production database boundary: %w", err)
	}
	if production {
		return fmt.Errorf("refusing live production XDG database %s; pass an explicit restored corpus copy", *dbPath)
	}
	dimensions, err := parseDimensions(*dimensionList)
	if err != nil {
		return err
	}

	st, err := store.OpenReadOnlyContext(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("open explicit database read-only: %w", err)
	}
	defer func() { _ = st.Close() }()
	return executeBakeoff(ctx, bakeoffOptions{
		database: *dbPath, reportPath: *reportPath, baseURL: *baseURL,
		model: *model, dimensions: dimensions, maxBytes: *maxBytes,
	}, st, bakeoffDeps{})
}

func executeBakeoff(ctx context.Context, opts bakeoffOptions, corpus parentCorpus, deps bakeoffDeps) error {
	if deps.newProvider == nil {
		deps.newProvider = func(options embedding.OllamaOptions) (embedding.Provider, error) {
			return embedding.NewOllama(options)
		}
	}
	if deps.writeReport == nil {
		deps.writeReport = writeReport
	}
	result := report{Database: opts.database, Model: opts.model, MaxBytes: opts.maxBytes, ByteDistribution: map[string]int{"0-450": 0, "451-900": 0, "901-1350": 0, "1351-1800": 0}}
	persist := func() error { return deps.writeReport(opts.reportPath, result) }
	// The initial report is durable before the first potentially expensive page.
	if err := persist(); err != nil {
		return err
	}
	if err := forEachParentPage(ctx, corpus, func(parent retrievalchunk.Parent) error {
		projection, err := retrievalchunk.BuildProjection(parent, retrievalchunk.DefaultOptions())
		if err != nil {
			return fmt.Errorf("project %s %s: %w", parent.Kind, parent.SourceKey, err)
		}
		result.Parents++
		result.UniqueChunks += len(projection.Chunks)
		result.Occurrences += len(projection.Occurrences)
		for _, chunk := range projection.Chunks {
			byteCount := len([]byte(chunk.Text))
			if byteCount > opts.maxBytes {
				result.OversizedWindows++
			}
			result.ByteDistribution[bucket(byteCount)]++
		}
		return nil
	}, persist); err != nil {
		_ = persist()
		return err
	}
	if err := persist(); err != nil {
		return err
	}
	for _, dimensions := range opts.dimensions {
		result.CandidateProfiles = append(result.CandidateProfiles, profileResult{Dimensions: dimensions, Status: "running"})
		resultIndex := len(result.CandidateProfiles) - 1
		candidate := &result.CandidateProfiles[resultIndex]
		if err := persist(); err != nil {
			return err
		}
		provider, err := deps.newProvider(embedding.OllamaOptions{BaseURL: opts.baseURL, Model: opts.model, Dimensions: dimensions})
		if err != nil {
			candidate.Status, candidate.Error = "failed", err.Error()
			if err := persist(); err != nil {
				return err
			}
			continue
		}
		batch := make([]string, 0, 64)
		embedBatch := func() error {
			if len(batch) == 0 {
				return nil
			}
			request := embedding.Request{Purpose: embedding.PurposeDocument, Texts: batch}
			response, embedErr := provider.Embed(ctx, request)
			if embedErr != nil {
				if isDimensionRejection(embedErr, dimensions) {
					candidate.Status = "unsupported"
				} else {
					candidate.Status = "failed"
				}
				candidate.Error = embedErr.Error()
				if embedding.IsBlocked(embedErr) {
					result.ContextFailures++
				}
				_ = persist()
				return embedErr
			}
			if validateErr := embedding.ValidateResponse(provider.Info(), request, response); validateErr != nil {
				candidate.Status, candidate.Error = "failed", validateErr.Error()
				_ = persist()
				return validateErr
			}
			for _, vector := range response.Vectors {
				if err := finiteL2(vector); err != nil {
					candidate.Status, candidate.Error = "failed", err.Error()
					_ = persist()
					return err
				}
			}
			candidate.Vectors += len(response.Vectors)
			batch = batch[:0]
			return persist()
		}
		err = forEachParentPage(ctx, corpus, func(parent retrievalchunk.Parent) error {
			projection, projectErr := retrievalchunk.BuildProjection(parent, retrievalchunk.DefaultOptions())
			if projectErr != nil {
				return projectErr
			}
			for _, chunk := range projection.Chunks {
				batch = append(batch, chunk.Text)
				if len(batch) == cap(batch) {
					if err := embedBatch(); err != nil {
						return err
					}
				}
			}
			return nil
		}, nil)
		if err == nil {
			err = embedBatch()
		}
		if err != nil {
			if candidate.Status == "running" {
				candidate.Status, candidate.Error = "failed", err.Error()
			}
			if persistErr := persist(); persistErr != nil {
				return persistErr
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		candidate.Status = "ready"
		if err := persist(); err != nil {
			return err
		}
	}
	if err := persist(); err != nil {
		return err
	}
	for _, candidate := range result.CandidateProfiles {
		if candidate.Status == "failed" {
			return fmt.Errorf("%d-dimensional candidate failed: %s", candidate.Dimensions, candidate.Error)
		}
	}
	if result.OversizedWindows != 0 || result.ContextFailures != 0 {
		return fmt.Errorf("bakeoff found oversized=%d context_failures=%d", result.OversizedWindows, result.ContextFailures)
	}
	for _, candidate := range result.CandidateProfiles {
		if candidate.Dimensions == 768 && candidate.Status != "ready" {
			return fmt.Errorf("768-dimensional foundation profile failed: %s", candidate.Error)
		}
	}
	return nil
}
func isDimensionRejection(err error, dimensions int) bool {
	if !embedding.IsFatalConfig(err) {
		return false
	}
	message := strings.ToLower(err.Error())
	dimension := strconv.Itoa(dimensions)
	httpRejection := strings.Contains(message, "ollama embedding endpoint returned http 4")
	explicitDimension := strings.Contains(message, "dimension "+dimension) || strings.Contains(message, "dimensions "+dimension) || strings.Contains(message, `"dimensions":`+dimension) || strings.Contains(message, `"dimensions": `+dimension)
	unsupported := strings.Contains(message, "unsupported") || strings.Contains(message, "not supported") || strings.Contains(message, "must be")
	return httpRejection && explicitDimension && unsupported
}

func forEachParentPage(ctx context.Context, corpus parentCorpus, visit func(retrievalchunk.Parent) error, afterPage func() error) error {
	after := ""
	for {
		page, err := corpus.ListRetrievalParents(ctx, after, 500)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		for _, parent := range page {
			if err := visit(parent); err != nil {
				return err
			}
		}
		after = page[len(page)-1].SourceKey
		if afterPage != nil {
			if err := afterPage(); err != nil {
				return err
			}
		}
	}
}
func parseDimensions(raw string) ([]int, error) {
	seen := map[int]bool{}
	var result []int
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid embedding dimension %q", part)
		}
		if !seen[n] {
			seen[n] = true
			result = append(result, n)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("at least one embedding dimension is required")
	}
	return result, nil
}
func bucket(bytes int) string {
	switch {
	case bytes <= 450:
		return "0-450"
	case bytes <= 900:
		return "451-900"
	case bytes <= 1350:
		return "901-1350"
	default:
		return "1351-1800"
	}
}
func finiteL2(vector []float32) error {
	if len(vector) == 0 {
		return errors.New("empty embedding vector")
	}
	var norm float64
	for _, v := range vector {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return errors.New("embedding vector is not finite")
		}
		norm += float64(v) * float64(v)
	}
	if math.Abs(math.Sqrt(norm)-1) > 1e-4 {
		return fmt.Errorf("embedding vector is not L2 normalized: %.9g", math.Sqrt(norm))
	}
	return nil
}
func verifyEmbedding(baseURL, model string, dimensions int, text string) error {
	provider, err := embedding.NewOllama(embedding.OllamaOptions{BaseURL: baseURL, Model: model, Dimensions: dimensions})
	if err != nil {
		return err
	}
	request := embedding.Request{Purpose: embedding.PurposeDocument, Texts: []string{text}}
	response, err := provider.Embed(context.Background(), request)
	if err != nil {
		return err
	}
	if err := embedding.ValidateResponse(provider.Info(), request, response); err != nil {
		return err
	}
	return finiteL2(response.Vectors[0])
}

var loadProductionConfig = config.Load

func defaultProductionDBPath() (string, error) {
	cfg, err := loadProductionConfig("")
	if err != nil {
		return "", err
	}
	return cfg.DBPath, nil
}
func refusesLiveProductionDB(path string) (bool, error) {
	live, err := defaultProductionDBPath()
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(path) == "" {
		return false, errors.New("candidate database path is empty")
	}
	if strings.TrimSpace(live) == "" {
		return false, errors.New("configured production database path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	expected, err := filepath.Abs(live)
	if err != nil {
		return false, err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return false, err
	}
	expectedResolved, err := filepath.EvalSymlinks(expected)
	if err != nil {
		if os.IsNotExist(err) {
			if _, candidateErr := os.Stat(resolved); candidateErr != nil {
				return false, candidateErr
			}
			return false, nil
		}
		return false, err
	}
	if resolved == expectedResolved {
		return true, nil
	}
	a, err := os.Stat(resolved)
	if err != nil {
		return false, err
	}
	b, err := os.Stat(expectedResolved)
	if err != nil {
		return false, err
	}
	return os.SameFile(a, b), nil
}

type reportFileOps struct {
	writeFile func(string, []byte, os.FileMode) error
	rename    func(string, string) error
	remove    func(string) error
}

func writeReport(path string, value report) error {
	return writeReportWithOps(path, value, reportFileOps{writeFile: os.WriteFile, rename: os.Rename, remove: os.Remove})
}

func writeReportWithOps(path string, value report, ops reportFileOps) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	// Cleanup is unconditional: write implementations can create a partial file
	// before returning an error, and rename failures must not strand stale state.
	defer func() { _ = ops.remove(tmp) }()
	if err := ops.writeFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	if err := ops.rename(tmp, path); err != nil {
		return fmt.Errorf("rename report: %w", err)
	}
	return nil
}
