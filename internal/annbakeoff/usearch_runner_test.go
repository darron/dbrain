//go:build usearch && cgo

package annbakeoff

import (
	"context"
	"testing"
)

func TestRunUSearchRecordsNativeParameters(t *testing.T) {
	report, err := RunUSearch(context.Background(), Options{
		Sizes:           []int{32},
		Dimensions:      8,
		QueryCount:      2,
		WarmRepetitions: 1,
		Seed:            17,
		RecallAt:        5,
		MinimumRecall:   0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Backend != "usearch" || report.Parameters["connectivity"] <= 0 || report.Parameters["expansion_add"] <= 0 || report.Parameters["expansion_search"] <= 0 {
		t.Fatalf("report=%+v", report)
	}
	if report.EfSearch != 0 || report.M != 0 || report.Stages[0].EfSearch != 0 || report.Stages[0].M != 0 {
		t.Fatalf("native report leaked HNSW parameters: %+v", report)
	}
}
