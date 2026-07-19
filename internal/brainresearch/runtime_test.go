package brainresearch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/semanticconfig"
)

func TestNewRuntimeBuilderModeConstructionAndForceOff(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{RootDir: root}
	b, err := NewRuntimeBuilder(cfg, nil, "", false, false)
	if err != nil || b.semanticMode != semanticconfig.ModeOff || b.semanticRetriever != nil {
		t.Fatalf("off builder = %#v, err=%v", b, err)
	}

	writeRuntimeSemanticConfig(t, root, "shadow", "embed-model", 2)
	_, st := inspectionTestStore(t)
	b, err = NewRuntimeBuilder(cfg, st, "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if b.semanticMode != semanticconfig.ModeShadow || b.semanticRetriever == nil || b.semanticOptions.Limit != 50 || b.semanticOptions.MaxChunks != 25000 {
		t.Fatalf("shadow builder = %#v", b)
	}
	if b.semanticOptions.Profile.Model != "embed-model" || b.semanticOptions.Profile.Dimensions != 2 || b.semanticOptions.Profile.ProjectionVersion == "" || b.semanticOptions.Profile.ChunkerVersion == "" {
		t.Fatalf("canonical profile = %#v", b.semanticOptions.Profile)
	}

	writeRuntimeSemanticConfig(t, root, "on", "", 0)
	if _, err := NewRuntimeBuilder(cfg, st, "", false, false); err == nil {
		t.Fatal("effective on must reject incomplete profile")
	}
	b, err = NewRuntimeBuilder(cfg, st, "", false, true)
	if err != nil || b.semanticMode != semanticconfig.ModeOff || b.semanticRetriever != nil {
		t.Fatalf("force-off incomplete builder = %#v, err=%v", b, err)
	}
	if _, err := NewRuntimeBuilder(cfg, st, "", true, true); !errors.Is(err, semanticconfig.ErrConflictingOverrides) {
		t.Fatalf("conflict error = %v", err)
	}
}

func writeRuntimeSemanticConfig(t *testing.T, root, mode, model string, dimensions int) {
	t.Helper()
	data := []byte("research:\n  semantic:\n    mode: " + mode + "\n    model: " + model + "\n    dimensions: " + fmt.Sprint(dimensions) + "\n")
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
