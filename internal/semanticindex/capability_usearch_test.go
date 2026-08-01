//go:build usearch && cgo

package semanticindex

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestProbeUSearchReady(t *testing.T) {
	index := &capabilityProbeTestIndex{hits: []HNSWHit{{Ordinal: 0}, {Ordinal: 1}}}
	capability := probeUSearch(func(options USearchOptions) (capabilityProbeIndex, error) {
		if got, want := options.Dimensions, 2; got != want {
			t.Fatalf("probe dimensions = %d, want %d", got, want)
		}
		return index, nil
	})

	if got, want := capability, (Capability{State: CapabilitySupportedReady, Backend: BackendUSearch, Version: USearchVersion}); !reflect.DeepEqual(got, want) {
		t.Fatalf("probeUSearch() = %#v, want %#v", got, want)
	}
	if !index.reserved || !index.added || !index.searched || !index.closed {
		t.Fatalf("probe calls reserve=%t add=%t search=%t close=%t, want all true", index.reserved, index.added, index.searched, index.closed)
	}
}

func TestProbeUSearchReportsFailures(t *testing.T) {
	tests := []struct {
		name    string
		factory capabilityIndexFactory
		wants   []string
	}{
		{
			name: "constructor",
			factory: func(USearchOptions) (capabilityProbeIndex, error) {
				return nil, errors.New("constructor failed")
			},
			wants: []string{"constructor failed"},
		},
		{
			name: "reserve",
			factory: func(USearchOptions) (capabilityProbeIndex, error) {
				return &capabilityProbeTestIndex{reserveErr: errors.New("reserve failed")}, nil
			},
			wants: []string{"reserve failed"},
		},
		{
			name: "add",
			factory: func(USearchOptions) (capabilityProbeIndex, error) {
				return &capabilityProbeTestIndex{addErr: errors.New("add failed")}, nil
			},
			wants: []string{"add failed"},
		},
		{
			name: "search",
			factory: func(USearchOptions) (capabilityProbeIndex, error) {
				return &capabilityProbeTestIndex{searchErr: errors.New("search failed")}, nil
			},
			wants: []string{"search failed"},
		},
		{
			name: "close",
			factory: func(USearchOptions) (capabilityProbeIndex, error) {
				return &capabilityProbeTestIndex{hits: []HNSWHit{{Ordinal: 0}}, closeErr: errors.New("close failed")}, nil
			},
			wants: []string{"close failed"},
		},
		{
			name: "search and close",
			factory: func(USearchOptions) (capabilityProbeIndex, error) {
				return &capabilityProbeTestIndex{searchErr: errors.New("search failed"), closeErr: errors.New("close failed")}, nil
			},
			wants: []string{"search failed", "close failed"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capability := probeUSearch(test.factory)
			if capability.State != CapabilitySupportedBroken || capability.Backend != BackendUSearch || capability.Version != USearchVersion {
				t.Fatalf("probeUSearch() = %#v, want broken USearch capability", capability)
			}
			for _, want := range test.wants {
				if !strings.Contains(capability.Reason, want) {
					t.Fatalf("probe reason = %q, want it to contain %q", capability.Reason, want)
				}
			}
		})
	}
}

func TestProbeUSearchRejectsWrongNearestOrdinal(t *testing.T) {
	capability := probeUSearch(func(USearchOptions) (capabilityProbeIndex, error) {
		return &capabilityProbeTestIndex{hits: []HNSWHit{{Ordinal: 1}, {Ordinal: 0}}}, nil
	})
	if capability.State != CapabilitySupportedBroken || !strings.Contains(capability.Reason, "ordinal 0") {
		t.Fatalf("probeUSearch() = %#v, want ordinal validation failure", capability)
	}
}

func TestRuntimeCapabilityIsImmutable(t *testing.T) {
	first := RuntimeCapability()
	second := RuntimeCapability()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("RuntimeCapability results differ: %#v and %#v", first, second)
	}
}

type capabilityProbeTestIndex struct {
	reserveErr error
	addErr     error
	searchErr  error
	closeErr   error
	hits       []HNSWHit
	reserved   bool
	added      bool
	searched   bool
	closed     bool
}

func (i *capabilityProbeTestIndex) Reserve(int) error {
	i.reserved = true
	return i.reserveErr
}

func (i *capabilityProbeTestIndex) Add(...HNSWNode) error {
	i.added = true
	return i.addErr
}

func (i *capabilityProbeTestIndex) Search([]float32, int) ([]HNSWHit, error) {
	i.searched = true
	return i.hits, i.searchErr
}

func (i *capabilityProbeTestIndex) Close() error {
	i.closed = true
	return i.closeErr
}
