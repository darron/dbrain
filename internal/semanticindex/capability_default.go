//go:build !usearch || !cgo

package semanticindex

func RuntimeCapability() Capability {
	return Capability{State: CapabilityUnsupported}
}
