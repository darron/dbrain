package semanticindex

import (
	"bytes"
	"reflect"
	"testing"
)

func TestHNSWAdapterSearchAndReopenPreservesNearestOrdinals(t *testing.T) {
	index, err := NewHNSW(HNSWOptions{Dimensions: 2, Seed: 7, EfSearch: 64})
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Add(
		HNSWNode{Ordinal: 11, Vector: []float32{1, 0}},
		HNSWNode{Ordinal: 22, Vector: []float32{0.8, 0.6}},
		HNSWNode{Ordinal: 33, Vector: []float32{0, 1}},
	); err != nil {
		t.Fatal(err)
	}
	assertHNSWOrdinals(t, index, []uint64{11, 22})

	var payload bytes.Buffer
	if err := index.Export(&payload); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewHNSW(HNSWOptions{Dimensions: 2, Seed: 7, EfSearch: 64})
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Import(&payload); err != nil {
		t.Fatal(err)
	}
	assertHNSWOrdinals(t, reopened, []uint64{11, 22})
}

func assertHNSWOrdinals(t *testing.T, index *HNSW, want []uint64) {
	t.Helper()
	hits, err := index.Search([]float32{1, 0}, len(want))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]uint64, len(hits))
	for i, hit := range hits {
		got[i] = hit.Ordinal
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ordinals=%v want=%v", got, want)
	}
}
