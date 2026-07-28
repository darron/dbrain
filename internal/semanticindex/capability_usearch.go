//go:build usearch && cgo

package semanticindex

import (
	"fmt"
	"sync"
)

type capabilityProbeIndex interface {
	Reserve(int) error
	Add(...HNSWNode) error
	Search([]float32, int) ([]HNSWHit, error)
	Close() error
}

type capabilityIndexFactory func(USearchOptions) (capabilityProbeIndex, error)

var (
	runtimeCapabilityOnce sync.Once
	runtimeCapability     Capability
)

func RuntimeCapability() Capability {
	runtimeCapabilityOnce.Do(func() {
		runtimeCapability = probeUSearch(func(options USearchOptions) (capabilityProbeIndex, error) {
			return NewUSearch(options)
		})
	})
	return runtimeCapability
}

func probeUSearch(factory capabilityIndexFactory) Capability {
	capability := Capability{Backend: BackendUSearch, Version: USearchVersion}
	index, err := factory(USearchOptions{Dimensions: 2})
	if err != nil {
		return capability.broken(err)
	}

	var probeErr error
	if err := index.Reserve(2); err != nil {
		probeErr = fmt.Errorf("reserve probe index: %w", err)
	} else if err := index.Add(
		HNSWNode{Ordinal: 0, Vector: []float32{1, 0}},
		HNSWNode{Ordinal: 1, Vector: []float32{0, 1}},
	); err != nil {
		probeErr = fmt.Errorf("add probe vectors: %w", err)
	} else if hits, err := index.Search([]float32{1, 0}, 1); err != nil {
		probeErr = fmt.Errorf("search probe vector: %w", err)
	} else if len(hits) == 0 || hits[0].Ordinal != 0 {
		probeErr = fmt.Errorf("probe nearest ordinal %d, want ordinal 0", firstProbeOrdinal(hits))
	}

	if closeErr := index.Close(); closeErr != nil {
		if probeErr != nil {
			probeErr = fmt.Errorf("%v; close probe index: %w", probeErr, closeErr)
		} else {
			probeErr = fmt.Errorf("close probe index: %w", closeErr)
		}
	}
	if probeErr != nil {
		return capability.broken(probeErr)
	}
	capability.State = CapabilitySupportedReady
	return capability
}

func firstProbeOrdinal(hits []HNSWHit) uint64 {
	if len(hits) == 0 {
		return 0
	}
	return hits[0].Ordinal
}

func (c Capability) broken(err error) Capability {
	c.State = CapabilitySupportedBroken
	if err != nil {
		c.Reason = sanitizeCapabilityReason(err.Error())
	}
	return c
}
