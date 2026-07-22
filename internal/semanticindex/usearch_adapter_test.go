//go:build usearch && cgo

package semanticindex

import (
	"bytes"
	"reflect"
	"testing"
)

func TestUSearchAdapterSearchAndReopenPreservesNearestOrdinals(t *testing.T) {
	index, err := NewUSearch(USearchOptions{Dimensions: 2, Connectivity: 16, ExpansionAdd: 128, ExpansionSearch: 128})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = index.Close() })
	if err := index.Reserve(3); err != nil {
		t.Fatal(err)
	}
	if err := index.Add(
		HNSWNode{Ordinal: 11, Vector: []float32{1, 0}},
		HNSWNode{Ordinal: 22, Vector: []float32{0.8, 0.6}},
		HNSWNode{Ordinal: 33, Vector: []float32{0, 1}},
	); err != nil {
		t.Fatal(err)
	}
	assertUSearchOrdinals(t, index, []uint64{11, 22})

	var payload bytes.Buffer
	if err := index.Export(&payload); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewUSearch(USearchOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Import(&payload); err != nil {
		t.Fatal(err)
	}
	assertUSearchOrdinals(t, reopened, []uint64{11, 22})
}

func TestUSearchAdapterRejectsInvalidState(t *testing.T) {
	if _, err := NewUSearch(USearchOptions{Dimensions: 0}); err == nil {
		t.Fatal("expected zero dimensions to be rejected")
	}
	if _, err := NewUSearch(USearchOptions{Dimensions: 2, Connectivity: -1}); err == nil {
		t.Fatal("expected negative connectivity to be rejected")
	}
	index, err := NewUSearch(USearchOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	if err := index.Add(HNSWNode{Ordinal: 1, Vector: []float32{1, 0}}); err == nil {
		t.Fatal("expected closed index add to be rejected")
	}
	if err := index.Import(bytes.NewReader([]byte("not a usearch payload"))); err == nil {
		t.Fatal("expected closed index import to be rejected")
	}
	fresh, err := NewUSearch(USearchOptions{Dimensions: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	if err := fresh.Import(bytes.NewReader([]byte("not a usearch payload"))); err == nil {
		t.Fatal("expected malformed payload to be rejected")
	}
}

func assertUSearchOrdinals(t *testing.T, index *USearch, want []uint64) {
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
