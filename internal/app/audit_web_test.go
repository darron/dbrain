package app

import (
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

func TestBuildLocalWebAuditDependenciesWhenSchedulerDisabled(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	dependencies, err := buildWebAuditHandlerDependencies(t.Context(), cfg, auditConfigMeta{Layout: "explicit_root", Source: "flag"})
	if err != nil {
		t.Fatal(err)
	}
	if dependencies.Reports == nil || dependencies.Runs == nil || dependencies.SyncInterval <= 0 || dependencies.StandardInterval <= 0 {
		t.Fatalf("local web audit dependencies incomplete: %#v", dependencies)
	}
}
