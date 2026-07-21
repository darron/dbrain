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
	report := report{Database: *dbPath, Model: *model, MaxBytes: *maxBytes, ByteDistribution: map[string]int{"0-450": 0, "451-900": 0, "901-1350": 0, "1351-1800": 0}}
	if err := forEachParent(ctx, st, func(parent retrievalchunk.Parent) error {
		projection, err := retrievalchunk.BuildProjection(parent, retrievalchunk.DefaultOptions())
		if err != nil {
			return fmt.Errorf("project %s %s: %w", parent.Kind, parent.SourceKey, err)
		}
		report.Parents++
		report.UniqueChunks += len(projection.Chunks)
		report.Occurrences += len(projection.Occurrences)
		for _, chunk := range projection.Chunks {
			bytes := len([]byte(chunk.Text))
			if bytes > *maxBytes {
				report.OversizedWindows++
			}
			report.ByteDistribution[bucket(bytes)]++
		}
		return nil
	}); err != nil {
		_ = writeReport(*reportPath, report)
		return err
	}
	if err := writeReport(*reportPath, report); err != nil {
		return err
	}
	for _, dimensions := range dimensions {
		result := profileResult{Dimensions: dimensions, Status: "ready"}
		provider, err := embedding.NewOllama(embedding.OllamaOptions{BaseURL: *baseURL, Model: *model, Dimensions: dimensions})
		if err != nil {
			result.Status, result.Error = "unsupported", err.Error()
			report.CandidateProfiles = append(report.CandidateProfiles, result)
			continue
		}
		batch := make([]string, 0, 64)
		embedBatch := func() error {
			if len(batch) == 0 {
				return nil
			}
			response, embedErr := provider.Embed(ctx, embedding.Request{Purpose: embedding.PurposeDocument, Texts: batch})
			if embedErr != nil {
				if embedding.IsBlocked(embedErr) {
					result.Status = "unsupported"
				} else {
					result.Status = "failed"
				}
				result.Error = embedErr.Error()
				if embedding.IsBlocked(embedErr) {
					report.ContextFailures++
				}
				return embedErr
			}
			for _, vector := range response.Vectors {
				if err := finiteL2(vector); err != nil {
					result.Status, result.Error = "failed", err.Error()
					return err
				}
			}
			result.Vectors += len(response.Vectors)
			batch = batch[:0]
			return writeReport(*reportPath, report)
		}
		err = forEachParent(ctx, st, func(parent retrievalchunk.Parent) error {
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
		})
		if err == nil {
			err = embedBatch()
		}
		if err != nil {
			_ = writeReport(*reportPath, report)
			if result.Status == "ready" {
				result.Status, result.Error = "failed", err.Error()
			}
		}
		report.CandidateProfiles = append(report.CandidateProfiles, result)
		if err := writeReport(*reportPath, report); err != nil {
			return err
		}
	}
	if err := writeReport(*reportPath, report); err != nil {
		return err
	}
	if report.OversizedWindows != 0 || report.ContextFailures != 0 {
		return fmt.Errorf("bakeoff found oversized=%d context_failures=%d", report.OversizedWindows, report.ContextFailures)
	}
	for _, candidate := range report.CandidateProfiles {
		if candidate.Dimensions == 768 && candidate.Status != "ready" {
			return fmt.Errorf("768-dimensional foundation profile failed: %s", candidate.Error)
		}
	}
	return nil
}

func forEachParent(ctx context.Context, st *store.Store, visit func(retrievalchunk.Parent) error) error {
	after := ""
	for {
		page, err := st.ListRetrievalParents(ctx, after, 500)
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
	response, err := provider.Embed(context.Background(), embedding.Request{Purpose: embedding.PurposeDocument, Texts: []string{text}})
	if err != nil {
		return err
	}
	return finiteL2(response.Vectors[0])
}
func defaultProductionDBPath() (string, error) {
	cfg, err := config.Load("")
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
func writeReport(path string, value report) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return os.Rename(tmp, path)
}
