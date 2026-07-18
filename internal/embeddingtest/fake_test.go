package embeddingtest

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/darron/dbrain/internal/embedding"
)

func TestFakePreservesRequestOrderAndExactCardinality(t *testing.T) {
	t.Parallel()
	fake := New(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2}, map[string][]float32{
		"alpha": {1, 0},
		"bravo": {0, 1},
	})
	response, err := fake.Embed(context.Background(), embedding.Request{
		Purpose: embedding.PurposeDocument, Texts: []string{"bravo", "alpha", "bravo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Provider != "fake" || response.Model != "fake-v1" || response.Dimensions != 2 {
		t.Fatalf("response provenance = %+v", response)
	}
	want := [][]float32{{0, 1}, {1, 0}, {0, 1}}
	assertVectorsEqual(t, response.Vectors, want)
}

func TestFakeRejectsUnmappedTextByDefault(t *testing.T) {
	t.Parallel()
	fake := New(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2}, map[string][]float32{
		"alpha": {1, 0},
	})
	if _, err := fake.Embed(context.Background(), embedding.Request{
		Purpose: embedding.PurposeQuery, Texts: []string{"missing"},
	}); err == nil || !embedding.IsBlocked(err) {
		t.Fatalf("strict fake unmapped error = %v, want blocked classification", err)
	}
}

func TestFakeInvalidMappedVectorIsFatalConfiguration(t *testing.T) {
	t.Parallel()
	fake := New(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2}, map[string][]float32{
		"alpha": {1},
	})
	_, err := fake.Embed(context.Background(), embedding.Request{
		Purpose: embedding.PurposeDocument, Texts: []string{"alpha"},
	})
	if err == nil || !embedding.IsFatalConfig(err) {
		t.Fatalf("invalid fake mapping error = %v, want fatal config", err)
	}
}

func TestFakeDeepCopiesConfigurationResponsesAndCalls(t *testing.T) {
	t.Parallel()
	vectors := map[string][]float32{"alpha": {1, 0}}
	fake := New(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2}, vectors)
	vectors["alpha"][0] = 99
	req := embedding.Request{Purpose: embedding.PurposeDocument, Texts: []string{"alpha"}}
	response, err := fake.Embed(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	response.Vectors[0][0] = 88
	req.Texts[0] = "mutated"
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Texts[0] != "alpha" {
		t.Fatalf("recorded calls = %+v", calls)
	}
	calls[0].Texts[0] = "also mutated"

	response, err = fake.Embed(context.Background(), embedding.Request{
		Purpose: embedding.PurposeDocument, Texts: []string{"alpha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertVectorsEqual(t, response.Vectors, [][]float32{{1, 0}})
	if got := fake.Calls(); len(got) != 2 || got[0].Texts[0] != "alpha" || got[1].Texts[0] != "alpha" {
		t.Fatalf("deep-copied calls = %+v", got)
	}
}

func TestFakeRecordsConcurrentCallsRaceSafely(t *testing.T) {
	t.Parallel()
	const count = 64
	vectors := make(map[string][]float32, count)
	for i := 0; i < count; i++ {
		vectors[fmt.Sprintf("text-%02d", i)] = []float32{1, 0}
	}
	fake := New(embedding.Info{Provider: "fake", Model: "fake-v1", Dimensions: 2}, vectors)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := fake.Embed(context.Background(), embedding.Request{
				Purpose: embedding.PurposeQuery, Texts: []string{fmt.Sprintf("text-%02d", i)},
			})
			if err != nil {
				t.Errorf("embed %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if calls := fake.Calls(); len(calls) != count {
		t.Fatalf("recorded %d concurrent calls, want %d", len(calls), count)
	}
}

func assertVectorsEqual(t *testing.T, got, want [][]float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("vector count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("vector %d dimensions = %d, want %d", i, len(got[i]), len(want[i]))
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Fatalf("vector[%d][%d] = %v, want %v", i, j, got[i][j], want[i][j])
			}
		}
	}
}
