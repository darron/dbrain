package metrics

import (
	"sync"

	"github.com/darron/dbrain/internal/runlock"
)

// The hook is intentionally package-private. Tests use it to place a
// deterministic barrier immediately after the real cross-process lock is
// acquired; normal builds leave it nil and pay only the read-lock cost.
var metricsLockHook struct {
	sync.RWMutex
	fn func(runlock.Mode)
}

func metricsLockAcquired(mode runlock.Mode) {
	metricsLockHook.RLock()
	hook := metricsLockHook.fn
	metricsLockHook.RUnlock()
	if hook != nil {
		hook(mode)
	}
}
