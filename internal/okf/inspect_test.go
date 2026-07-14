package okf

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darron/dbrain/internal/vaultfs"
)

func TestInspectBundleReturnsOnlyAggregateValidation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeInspectionManifest(t, dir, "2026-07-13T18:00:00Z", []ManifestConcept{{Path: "concepts/one.md", Title: "private title", SourceKey: "src:private"}})
	if err := os.MkdirAll(filepath.Join(dir, "concepts"), 0o755); err != nil {
		t.Fatalf("mkdir concepts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "concepts", "one.md"), []byte("---\ntype: note\ntitle: Private\n---\n[missing](two.md)\n"), 0o600); err != nil {
		t.Fatalf("write concept: %v", err)
	}
	root := openInspectionRoot(t, dir)
	got, err := InspectBundle(context.Background(), root)
	if err != nil {
		t.Fatalf("InspectBundle: %v", err)
	}
	if !got.ManifestValid || got.ExportedAt != time.Date(2026, 7, 13, 18, 0, 0, 0, time.UTC) || got.DocumentCount != 1 || got.BrokenLinkCount != 1 || got.ValidationErrorCount != 1 || !got.TraversalComplete {
		t.Fatalf("unexpected inspection summary: %+v", got)
	}
	payload, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	for _, private := range []string{"one.md", "private title", "src:private", "missing"} {
		if strings.Contains(string(payload), private) {
			t.Fatalf("aggregate summary leaked %q: %s", private, payload)
		}
	}
}

func TestInspectBundleClassifiesManifestAndTraversalFailures(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, string)
	}{
		{name: "missing_manifest", prepare: func(t *testing.T, dir string) {}},
		{name: "invalid_manifest", prepare: func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, manifestFileName), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing_exported_at", prepare: func(t *testing.T, dir string) { writeInspectionManifest(t, dir, "", nil) }},
		{name: "absolute_manifest_path", prepare: func(t *testing.T, dir string) {
			writeInspectionManifest(t, dir, "2026-07-13T18:00:00Z", []ManifestConcept{{Path: "/etc/passwd"}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.prepare(t, dir)
			got, err := InspectBundle(context.Background(), openInspectionRoot(t, dir))
			if err != nil {
				t.Fatalf("InspectBundle: %v", err)
			}
			if got.ManifestValid || got.ValidationErrorCount == 0 {
				t.Fatalf("expected sanitized invalid summary, got %+v", got)
			}
		})
	}

	dir := t.TempDir()
	writeInspectionManifest(t, dir, "2026-07-13T18:00:00Z", nil)
	outside := filepath.Join(filepath.Dir(dir), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.md")); err != nil {
		t.Fatalf("escaping symlink: %v", err)
	}
	got, err := InspectBundle(context.Background(), openInspectionRoot(t, dir))
	if err != nil {
		t.Fatalf("InspectBundle traversal: %v", err)
	}
	if got.TraversalComplete || got.ValidationErrorCount == 0 {
		t.Fatalf("expected traversal failure summary, got %+v", got)
	}
}

func TestInspectBundleRejectsManifestPathThroughEscapingDirectorySymlink(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	dir := filepath.Join(base, "bundle")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir bundle: %v", err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "concept.md"), []byte("---\ntype: note\n---\noutside\n"), 0o600); err != nil {
		t.Fatalf("write outside concept: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatalf("create escaping directory symlink: %v", err)
	}
	writeInspectionManifest(t, dir, "2026-07-13T18:00:00Z", []ManifestConcept{{Path: "escape/concept.md", Type: "note"}})

	root := openInspectionRoot(t, dir)
	got, err := InspectBundle(t.Context(), root)
	if err != nil {
		t.Fatalf("InspectBundle: %v", err)
	}
	if got.ManifestValid || got.TraversalComplete || got.ValidationErrorCount == 0 {
		t.Fatalf("escaping manifest concept path accepted: %+v", got)
	}

	validation, err := ValidateBundle(dir)
	if err != nil {
		t.Fatalf("ValidateBundle: %v", err)
	}
	if validation.Conformant {
		t.Fatalf("escaping manifest concept path was conformant: %+v", validation)
	}
}

func TestInspectBundleRejectsNonCanonicalManifestConceptPaths(t *testing.T) {
	t.Parallel()

	for _, conceptPath := range []string{
		"index.md",
		"log.md",
		".",
		"concepts/not-markdown.txt",
		"concepts/../concept.md",
	} {
		conceptPath := conceptPath
		t.Run(conceptPath, func(t *testing.T) {
			dir := t.TempDir()
			targetPath := conceptPath
			if conceptPath == "concepts/../concept.md" {
				targetPath = "concept.md"
			}
			if conceptPath != "." {
				fullTarget := filepath.Join(dir, filepath.FromSlash(targetPath))
				if err := os.MkdirAll(filepath.Dir(fullTarget), 0o755); err != nil {
					t.Fatalf("mkdir target parent: %v", err)
				}
				if err := os.WriteFile(fullTarget, []byte("---\ntype: note\n---\ninvalid manifest target\n"), 0o600); err != nil {
					t.Fatalf("write invalid target: %v", err)
				}
			}
			writeInspectionManifest(t, dir, "2026-07-13T18:00:00Z", []ManifestConcept{{Path: conceptPath, Type: "note"}})
			got, err := InspectBundle(t.Context(), openInspectionRoot(t, dir))
			if err != nil {
				t.Fatalf("InspectBundle: %v", err)
			}
			if got.ManifestValid || got.ValidationErrorCount == 0 {
				t.Fatalf("non-canonical manifest path %q accepted: %+v", conceptPath, got)
			}
		})
	}
}

func TestInspectBundleRejectsDuplicateManifestConceptPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "concepts"), 0o755); err != nil {
		t.Fatalf("mkdir concepts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "concepts", "one.md"), []byte("---\ntype: note\n---\none\n"), 0o600); err != nil {
		t.Fatalf("write concept: %v", err)
	}
	writeInspectionManifest(t, dir, "2026-07-13T18:00:00Z", []ManifestConcept{
		{Path: "concepts/one.md", Type: "note"},
		{Path: "concepts/ONE.md", Type: "note"},
	})

	got, err := InspectBundle(t.Context(), openInspectionRoot(t, dir))
	if err != nil {
		t.Fatalf("InspectBundle: %v", err)
	}
	if got.ManifestValid || got.ValidationErrorCount == 0 {
		t.Fatalf("duplicate manifest concept path accepted: %+v", got)
	}
}

func TestInspectBundleRejectsNonRegularManifestConceptTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "concepts", "directory.md"), 0o755); err != nil {
		t.Fatalf("mkdir concept target: %v", err)
	}
	writeInspectionManifest(t, dir, "2026-07-13T18:00:00Z", []ManifestConcept{{Path: "concepts/directory.md", Type: "note"}})

	got, err := InspectBundle(t.Context(), openInspectionRoot(t, dir))
	if err != nil {
		t.Fatalf("InspectBundle: %v", err)
	}
	if got.ManifestValid || got.ValidationErrorCount == 0 {
		t.Fatalf("non-regular manifest concept target accepted: %+v", got)
	}
}

func TestInspectBundleValidatesManifestIdentity(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		version   string
		profile   string
		wantValid bool
	}{
		{name: "current_private", version: "0.1", profile: ProfilePrivate, wantValid: true},
		{name: "missing_version", version: "", profile: ProfilePrivate},
		{name: "unsupported_version", version: "9.9", profile: ProfilePrivate},
		{name: "missing_profile", version: "0.1", profile: ""},
		{name: "unsupported_profile", version: "0.1", profile: "public"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			payload, err := json.Marshal(Manifest{
				OKFVersion: tc.version,
				Profile:    tc.profile,
				ExportedAt: "2026-07-13T18:00:00Z",
			})
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, manifestFileName), payload, 0o600); err != nil {
				t.Fatalf("write manifest: %v", err)
			}

			got, err := InspectBundle(t.Context(), openInspectionRoot(t, dir))
			if err != nil {
				t.Fatalf("InspectBundle: %v", err)
			}
			if got.ManifestValid != tc.wantValid {
				t.Fatalf("ManifestValid = %t, want %t: %+v", got.ManifestValid, tc.wantValid, got)
			}
			validation, err := ValidateBundle(dir)
			if err != nil {
				t.Fatalf("ValidateBundle: %v", err)
			}
			if validation.Conformant != tc.wantValid {
				t.Fatalf("Conformant = %t, want %t: %+v", validation.Conformant, tc.wantValid, validation)
			}
		})
	}
}

func openInspectionRoot(t *testing.T, dir string) *vaultfs.Root {
	t.Helper()
	root, err := vaultfs.Open(dir)
	if err != nil {
		t.Fatalf("vaultfs.Open: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func writeInspectionManifest(t *testing.T, dir string, exportedAt string, concepts []ManifestConcept) {
	t.Helper()
	payload, err := json.Marshal(Manifest{OKFVersion: "0.1", Profile: ProfilePrivate, ExportedAt: exportedAt, Concepts: concepts})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestFileName), payload, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}
