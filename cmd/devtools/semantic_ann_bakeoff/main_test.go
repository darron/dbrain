package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/annbakeoff"
)

func TestExecuteRequiresReportPath(t *testing.T) {
	err := execute(context.Background(), []string{"--sizes", "20"}, bakeoffDeps{})
	if err == nil || !strings.Contains(err.Error(), "--report is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseStagesRejectsDuplicatesAndInvalidCounts(t *testing.T) {
	for _, raw := range []string{"20,20", "20,0", "20,nope"} {
		if _, err := parseStages(raw); err == nil {
			t.Fatalf("parseStages(%q) succeeded", raw)
		}
	}
	got, err := parseStages("20,100")
	if err != nil || len(got) != 2 || got[0] != 20 || got[1] != 100 {
		t.Fatalf("stages=%v err=%v", got, err)
	}
}

func TestExecutePersistsRejectedReportBeforeReturningGateError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	rejected := annbakeoff.Report{Status: annbakeoff.StatusRejected, Stages: []annbakeoff.StageReport{{VectorCount: 20, Status: annbakeoff.StatusRejected, Reason: annbakeoff.ReasonHeapLimit}}}
	err := execute(context.Background(), []string{"--report", path, "--sizes", "20"}, bakeoffDeps{
		run: func(context.Context, annbakeoff.Options) (annbakeoff.Report, error) { return rejected, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("error = %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var got annbakeoff.Report
	if err := json.Unmarshal(data, &got); err != nil || got.Status != annbakeoff.StatusRejected || len(got.Stages) != 1 {
		t.Fatalf("report=%+v err=%v", got, err)
	}
}

func TestExecutePassesEfSearchToRunner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	var got annbakeoff.Options
	err := execute(context.Background(), []string{"--report", path, "--sizes", "20", "--ef-search", "1024"}, bakeoffDeps{
		run: func(_ context.Context, options annbakeoff.Options) (annbakeoff.Report, error) {
			got = options
			return annbakeoff.Report{Status: annbakeoff.StatusPassed}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.EfSearch != 1024 {
		t.Fatalf("ef search = %d", got.EfSearch)
	}
}

func TestExecutePassesNeighborDegreeToRunner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	var got annbakeoff.Options
	err := execute(context.Background(), []string{"--report", path, "--sizes", "20", "--m", "48"}, bakeoffDeps{
		run: func(_ context.Context, options annbakeoff.Options) (annbakeoff.Report, error) {
			got = options
			return annbakeoff.Report{Status: annbakeoff.StatusPassed}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.M != 48 {
		t.Fatalf("M = %d", got.M)
	}
}

func TestWriteReportRemovesTemporaryFileAfterFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	err := writeReportWithOps(path, annbakeoff.Report{}, reportFileOps{
		writeFile: func(path string, data []byte, mode os.FileMode) error {
			_ = os.WriteFile(path, data, mode)
			return errors.New("write failed")
		},
		rename: os.Rename,
		remove: os.Remove,
	})
	if err == nil {
		t.Fatal("expected write failure")
	}
	if _, err := os.Lstat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary report remains: %v", err)
	}
}
