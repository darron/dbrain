//go:build usearch && cgo

// semantic_usearch_segment_flush evaluates one explicit restored SQLite corpus
// through the optional native USearch lifecycle. It is not a dbrain command and
// cannot enable semantic serving.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/embedding"
	"github.com/darron/dbrain/internal/semanticbuild"
	"github.com/darron/dbrain/internal/store"
)

type flushOptions struct {
	database, cache, provider, model, reportPath            string
	dimensions, connectivity, expansionAdd, expansionSearch int
	apply                                                   bool
}

type flushStore interface {
	semanticbuild.FlushStore
	Close() error
}

type flushReport struct {
	Database        string                     `json:"database"`
	Cache           string                     `json:"cache"`
	ProfileID       string                     `json:"profile_id"`
	Status          string                     `json:"status"`
	ReadyVectors    int                        `json:"ready_vectors"`
	RequiredVectors int                        `json:"required_vectors"`
	Result          *semanticbuild.FlushResult `json:"result,omitempty"`
	GeneratedAt     time.Time                  `json:"generated_at"`
}

type flushDeps struct {
	refusesProduction func(string) (bool, error)
	openReadOnly      func(string) (flushStore, error)
	openReadWrite     func(string) (flushStore, error)
	newBuilder        func(semanticbuild.USearchSegmentBuilderOptions) (*semanticbuild.USearchSegmentBuilder, error)
	flush             func(context.Context, semanticbuild.FlushStore, semanticbuild.SegmentPayloadBuilder, semanticbuild.FlushOptions) (semanticbuild.FlushResult, error)
	writeReport       func(string, flushReport) error
}

func main() {
	if err := runWithDeps(context.Background(), os.Args[1:], flushDeps{}); err != nil {
		fmt.Fprintln(os.Stderr, "semantic USearch segment flush:", err)
		os.Exit(1)
	}
}

func runWithDeps(ctx context.Context, args []string, deps flushDeps) error {
	flags := flag.NewFlagSet("semantic_usearch_segment_flush", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	database := flags.String("db", "", "explicit restored SQLite database path")
	cache := flags.String("cache", "", "explicit derived-cache directory")
	provider := flags.String("provider", "ollama", "embedding provider identity")
	model := flags.String("model", "", "embedding model identity")
	dimensions := flags.Int("dimensions", 0, "embedding dimensions")
	reportPath := flags.String("report", "", "write JSON evaluation report")
	connectivity := flags.Int("connectivity", 16, "USearch maximum graph neighbors")
	expansionAdd := flags.Int("expansion-add", 128, "USearch construction breadth")
	expansionSearch := flags.Int("expansion-search", 256, "USearch query breadth")
	apply := flags.Bool("apply", false, "publish one derived segment/root and activate its SQLite generation")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*apply {
		return errors.New("--apply is required; this evaluator never writes by default")
	}
	return execute(ctx, flushOptions{
		database: *database, cache: *cache, provider: *provider, model: *model, dimensions: *dimensions,
		reportPath: *reportPath, connectivity: *connectivity, expansionAdd: *expansionAdd,
		expansionSearch: *expansionSearch, apply: *apply,
	}, deps)
}

func execute(ctx context.Context, options flushOptions, deps flushDeps) error {
	if err := validateOptions(options); err != nil {
		return err
	}
	setFlushDefaults(&deps)
	production, err := deps.refusesProduction(options.database)
	if err != nil {
		return fmt.Errorf("resolve configured production database: %w", err)
	}
	if production {
		return fmt.Errorf("refusing configured production database %s; pass an explicit restored corpus copy", options.database)
	}
	profile := semanticbuild.Profile(embedding.Info{Provider: options.provider, Model: options.model, Dimensions: options.dimensions})
	profileID, err := profile.ID()
	if err != nil {
		return fmt.Errorf("resolve embedding profile: %w", err)
	}
	report := flushReport{Database: options.database, Cache: options.cache, ProfileID: profileID,
		Status: "preflight", RequiredVectors: store.RetrievalSegmentTarget, GeneratedAt: time.Now().UTC()}
	readOnly, err := deps.openReadOnly(options.database)
	if err != nil {
		return fmt.Errorf("open restored database read-only: %w", err)
	}
	window, windowErr := readOnly.NextRetrievalFlushWindow(ctx, profileID, store.RetrievalSegmentTarget)
	closeErr := readOnly.Close()
	if windowErr != nil {
		return fmt.Errorf("read restored L0 window: %w", windowErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close restored read-only database: %w", closeErr)
	}
	report.ReadyVectors = len(window.Rows)
	if len(window.Rows) != store.RetrievalSegmentTarget {
		report.Status = "below_flush_threshold"
		if err := deps.writeReport(options.reportPath, report); err != nil {
			return fmt.Errorf("write threshold report: %w", err)
		}
		return fmt.Errorf("restored profile has %d ready vectors in L0; requires %d before a normal segment flush", len(window.Rows), store.RetrievalSegmentTarget)
	}
	if err := deps.writeReport(options.reportPath, report); err != nil {
		return fmt.Errorf("write preflight report: %w", err)
	}
	readWrite, err := deps.openReadWrite(options.database)
	if err != nil {
		return fmt.Errorf("open restored database read-write: %w", err)
	}
	defer func() { _ = readWrite.Close() }()
	builder, err := deps.newBuilder(semanticbuild.USearchSegmentBuilderOptions{
		Dimensions: options.dimensions, Connectivity: options.connectivity,
		ExpansionAdd: options.expansionAdd, ExpansionSearch: options.expansionSearch,
	})
	if err != nil {
		return err
	}
	result, err := deps.flush(ctx, readWrite, builder, semanticbuild.FlushOptions{
		Profile: profile, Backend: "usearch", BackendVersion: "2.26.0", DistanceMetric: "cosine",
		CacheDir: options.cache,
	})
	if err != nil {
		return fmt.Errorf("flush restored USearch segment: %w", err)
	}
	report.Status, report.Result, report.GeneratedAt = "completed", &result, time.Now().UTC()
	if err := deps.writeReport(options.reportPath, report); err != nil {
		return fmt.Errorf("write completion report: %w", err)
	}
	fmt.Printf("semantic USearch segment flush completed: generation=%s segment=%s indexed=%d l0=%d report=%s\n", result.GenerationID, result.SegmentHash, result.Indexed, result.L0Ready, options.reportPath)
	return nil
}

func setFlushDefaults(deps *flushDeps) {
	if deps.refusesProduction == nil {
		deps.refusesProduction = refusesProductionDatabase
	}
	if deps.openReadOnly == nil {
		deps.openReadOnly = func(path string) (flushStore, error) { return store.OpenReadOnly(path) }
	}
	if deps.openReadWrite == nil {
		deps.openReadWrite = func(path string) (flushStore, error) { return store.Open(path) }
	}
	if deps.newBuilder == nil {
		deps.newBuilder = semanticbuild.NewUSearchSegmentBuilder
	}
	if deps.flush == nil {
		deps.flush = semanticbuild.Flush
	}
	if deps.writeReport == nil {
		deps.writeReport = writeReport
	}
}

// refusesProductionDatabase checks all active configuration selectors as well
// as a candidate's own <root>/data/brain.db convention. The latter is needed
// for devtools launched outside direnv, where DBRAIN_ROOT is not inherited but
// the candidate still points at a configured repository root.
func refusesProductionDatabase(candidate string) (bool, error) {
	configured := make([]string, 0, 3)
	if configFile := strings.TrimSpace(os.Getenv("DBRAIN_CONFIG_FILE")); configFile != "" {
		cfg, err := config.LoadConfigFile(configFile)
		if err != nil {
			return false, err
		}
		configured = append(configured, cfg.DBPath)
	} else if root := strings.TrimSpace(os.Getenv("DBRAIN_ROOT")); root != "" {
		cfg, err := config.Load(root)
		if err != nil {
			return false, err
		}
		configured = append(configured, cfg.DBPath)
	} else {
		cfg, err := config.Load("")
		if err != nil {
			return false, err
		}
		configured = append(configured, cfg.DBPath)
	}
	if filepath.Base(candidate) == "brain.db" && filepath.Base(filepath.Dir(candidate)) == "data" {
		root := filepath.Dir(filepath.Dir(candidate))
		cfg, err := config.Load(root)
		if err != nil {
			return false, err
		}
		configured = append(configured, cfg.DBPath)
	}
	for _, database := range configured {
		same, err := samePath(candidate, database)
		if err != nil {
			return false, err
		}
		if same {
			return true, nil
		}
	}
	return false, nil
}

func validateOptions(options flushOptions) error {
	if !options.apply {
		return errors.New("--apply is required")
	}
	for _, field := range []struct{ name, value string }{
		{"--db", options.database}, {"--cache", options.cache}, {"--provider", options.provider}, {"--model", options.model}, {"--report", options.reportPath},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	if options.dimensions <= 0 {
		return errors.New("--dimensions must be positive")
	}
	if options.connectivity < 0 || options.expansionAdd < 0 || options.expansionSearch < 0 {
		return errors.New("USearch parameters cannot be negative")
	}
	return nil
}

func samePath(left, right string) (bool, error) {
	leftAbs, err := filepath.Abs(left)
	if err != nil {
		return false, err
	}
	rightAbs, err := filepath.Abs(right)
	if err != nil {
		return false, err
	}
	leftResolved, err := filepath.EvalSymlinks(leftAbs)
	if err == nil {
		leftAbs = leftResolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	rightResolved, err := filepath.EvalSymlinks(rightAbs)
	if err == nil {
		rightAbs = rightResolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return leftAbs == rightAbs, nil
}

func writeReport(path string, report flushReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	defer func() { _ = os.Remove(temporary) }()
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
