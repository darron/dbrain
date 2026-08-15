package app

import (
	"testing"

	"github.com/darron/dbrain/internal/store"
)

func TestInteractiveStoreOpenOptionsUsesSharedPoolSize(t *testing.T) {
	opts := interactiveStoreOpenOptions()
	if opts.MaxOpenConns != store.InteractivePoolSize || opts.MaxIdleConns != store.InteractivePoolSize {
		t.Fatalf("interactive store options = %+v, want pool size %d", opts, store.InteractivePoolSize)
	}
}
