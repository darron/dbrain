package annbakeoff

import (
	"context"
	"reflect"
	"testing"

	"github.com/darron/dbrain/internal/semanticindex"
)

func TestDeterministicCorpusAndExactOracle(t *testing.T) {
	first, err := newCorpus(64, 8, 17)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newCorpus(64, 8, 17)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.values, second.values) {
		t.Fatal("same seed produced different corpus vectors")
	}

	got := exactTopK(first, first.vector(7), 5)
	if len(got) != 5 || got[0] != 7 {
		t.Fatalf("exact top-k = %v, want self ordinal first", got)
	}
}

func TestRunReportsStageAndStopsBeforeLaterStageWhenHeapGateFails(t *testing.T) {
	report, err := Run(context.Background(), Options{
		Sizes:           []int{32, 64},
		Dimensions:      8,
		QueryCount:      4,
		WarmRepetitions: 3,
		Seed:            17,
		RecallAt:        5,
		MinimumRecall:   0,
		MaxHeapSysBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != StatusRejected || len(report.Stages) != 1 {
		t.Fatalf("report = %+v, want one rejected stage", report)
	}
	stage := report.Stages[0]
	if stage.Status != StatusRejected || stage.Reason != ReasonHeapLimit || stage.VectorCount != 32 {
		t.Fatalf("stage = %+v", stage)
	}
}

func TestRunRejectsInvalidOptions(t *testing.T) {
	_, err := Run(context.Background(), Options{
		Sizes:           []int{4},
		Dimensions:      2,
		QueryCount:      1,
		WarmRepetitions: 1,
		RecallAt:        5,
		MinimumRecall:   0.95,
	})
	if err == nil {
		t.Fatal("expected invalid recall limit error")
	}
}

func TestRunRecordsConfiguredEfSearch(t *testing.T) {
	report, err := Run(context.Background(), Options{
		Sizes:           []int{32},
		Dimensions:      8,
		QueryCount:      2,
		WarmRepetitions: 1,
		Seed:            17,
		RecallAt:        5,
		MinimumRecall:   0,
		EfSearch:        64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.EfSearch != 64 || len(report.Stages) != 1 || report.Stages[0].EfSearch != 64 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunRecordsConfiguredNeighborDegree(t *testing.T) {
	report, err := Run(context.Background(), Options{
		Sizes:           []int{32},
		Dimensions:      8,
		QueryCount:      2,
		WarmRepetitions: 1,
		Seed:            17,
		RecallAt:        5,
		MinimumRecall:   0,
		M:               32,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.M != 32 || report.Stages[0].M != 32 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunWithRecordsCandidateParametersAndClosesBothIndexes(t *testing.T) {
	var closed int
	report, err := RunWith(context.Background(), Options{
		Sizes:           []int{32},
		Dimensions:      8,
		QueryCount:      2,
		WarmRepetitions: 1,
		Seed:            17,
		RecallAt:        5,
		MinimumRecall:   0,
	}, "native-test", map[string]int{"connectivity": 16}, func(opts Options) (Index, error) {
		index, err := semanticindex.NewHNSW(semanticindex.HNSWOptions{Dimensions: opts.Dimensions, Seed: opts.Seed})
		if err != nil {
			return nil, err
		}
		return &trackedIndex{HNSW: index, onClose: func() { closed++ }}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Backend != "native-test" || report.Parameters["connectivity"] != 16 || closed != 2 {
		t.Fatalf("report=%+v closed=%d", report, closed)
	}
}

type trackedIndex struct {
	*semanticindex.HNSW
	onClose func()
}

func (i *trackedIndex) Reserve(int) error { return nil }

func (i *trackedIndex) Close() error {
	if i.onClose != nil {
		i.onClose()
	}
	return nil
}
