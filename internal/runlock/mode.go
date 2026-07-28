package runlock

import "fmt"

type Mode uint8

const (
	Shared Mode = iota + 1
	Exclusive
)

type AcquireOptions struct {
	Mode     Mode
	Metadata string
}

func (m Mode) validate() error {
	switch m {
	case Shared, Exclusive:
		return nil
	default:
		return fmt.Errorf("invalid run lock mode %d", m)
	}
}
