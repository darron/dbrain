// semantic_ann_bakeoff screens an ANN graph payload with deterministic vectors.
// It does not open dbrain's database, call an embedding provider, or enable
// semantic retrieval.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/darron/dbrain/internal/annbakeoff"
)

type bakeoffDeps struct {
	run         func(context.Context, annbakeoff.Options) (annbakeoff.Report, error)
	writeReport func(string, annbakeoff.Report) error
}

func main() {
	if err := execute(context.Background(), os.Args[1:], bakeoffDeps{}); err != nil {
		fmt.Fprintln(os.Stderr, "semantic ANN bakeoff:", err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, args []string, deps bakeoffDeps) error {
	flags := flag.NewFlagSet("semantic_ann_bakeoff", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	reportPath := flags.String("report", "", "write JSON report to this path")
	sizes := flags.String("sizes", "25000,100000,286619", "strictly increasing vector-count stages")
	dimensions := flags.Int("dimensions", 768, "synthetic vector dimensions")
	queries := flags.Int("queries", 10, "exact-oracle query samples per stage")
	warmRepetitions := flags.Int("warm-repetitions", 3, "ANN timing repetitions per query")
	seed := flags.Uint64("seed", 20260721, "deterministic corpus and graph seed")
	recallAt := flags.Int("recall-at", 20, "top-k used for exact-versus-ANN recall")
	minimumRecall := flags.Float64("minimum-recall", 0.95, "minimum sampled recall-at threshold")
	neighborDegree := flags.Int("m", 16, "HNSW maximum neighbors per node")
	efSearch := flags.Int("ef-search", 256, "HNSW construction and query breadth")
	maxHeapGiB := flags.Float64("max-heap-sys-gib", 4, "stop after a stage above this Go heap-system ceiling")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*reportPath) == "" {
		return errors.New("--report is required")
	}
	stageSizes, err := parseStages(*sizes)
	if err != nil {
		return err
	}
	maxHeapBytes, err := gibibytes(*maxHeapGiB)
	if err != nil {
		return err
	}
	if deps.run == nil {
		deps.run = annbakeoff.Run
	}
	if deps.writeReport == nil {
		deps.writeReport = writeReport
	}
	report, runErr := deps.run(ctx, annbakeoff.Options{
		Sizes:           stageSizes,
		Dimensions:      *dimensions,
		QueryCount:      *queries,
		WarmRepetitions: *warmRepetitions,
		Seed:            *seed,
		RecallAt:        *recallAt,
		MinimumRecall:   *minimumRecall,
		MaxHeapSysBytes: maxHeapBytes,
		EfSearch:        *efSearch,
		M:               *neighborDegree,
	})
	if writeErr := deps.writeReport(*reportPath, report); writeErr != nil {
		return fmt.Errorf("write bakeoff report: %w", writeErr)
	}
	if runErr != nil {
		return runErr
	}
	if report.Status == annbakeoff.StatusRejected {
		return fmt.Errorf("backend screening rejected; inspect %s", *reportPath)
	}
	fmt.Printf("semantic ANN bakeoff passed: %d stage(s); report=%s\n", len(report.Stages), *reportPath)
	return nil
}

func parseStages(raw string) ([]int, error) {
	var stages []int
	previous := 0
	for _, part := range strings.Split(raw, ",") {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value <= 0 {
			return nil, fmt.Errorf("invalid stage size %q", part)
		}
		if value <= previous {
			return nil, errors.New("stage sizes must be strictly increasing")
		}
		stages = append(stages, value)
		previous = value
	}
	if len(stages) == 0 {
		return nil, errors.New("at least one stage is required")
	}
	return stages, nil
}

func gibibytes(value float64) (uint64, error) {
	if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, errors.New("max heap GiB must be positive")
	}
	bytes := value * (1 << 30)
	if bytes > math.MaxUint64 {
		return 0, errors.New("max heap GiB overflows bytes")
	}
	return uint64(bytes), nil
}

type reportFileOps struct {
	writeFile func(string, []byte, os.FileMode) error
	rename    func(string, string) error
	remove    func(string) error
}

func writeReport(path string, report annbakeoff.Report) error {
	return writeReportWithOps(path, report, reportFileOps{writeFile: os.WriteFile, rename: os.Rename, remove: os.Remove})
}

func writeReportWithOps(path string, report annbakeoff.Report, ops reportFileOps) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	defer func() { _ = ops.remove(temporary) }()
	if err := ops.writeFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return ops.rename(temporary, path)
}
