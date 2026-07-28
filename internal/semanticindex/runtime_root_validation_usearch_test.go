//go:build usearch && cgo

package semanticindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/darron/dbrain/internal/semanticsegment"
)

func TestValidateUSearchRuntimeRoot(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, usearchRootFixture)
		wantOK bool
	}{
		{name: "healthy admitted root remains valid", wantOK: true},
		{
			name: "missing root manifest is rejected",
			mutate: func(t *testing.T, cache string, fixture usearchRootFixture) {
				t.Helper()
				if err := os.Remove(filepath.Join(cache, filepath.FromSlash(fixture.root.RelativePath), semanticsegment.RootFileName)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing segment manifest is rejected",
			mutate: func(t *testing.T, cache string, fixture usearchRootFixture) {
				t.Helper()
				if err := os.Remove(filepath.Join(cache, filepath.FromSlash(fixture.segment.RelativePath), semanticsegment.ManifestFileName)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "damaged payload is rejected",
			mutate: func(t *testing.T, cache string, fixture usearchRootFixture) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(cache, filepath.FromSlash(fixture.segment.RelativePath), semanticsegment.PayloadFileName), []byte("damaged"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cache := t.TempDir()
			fixture := publishUSearchRootFixture(t, cache, "profile", usearchRootFixtureOptions{})
			if tc.mutate != nil {
				tc.mutate(t, cache, fixture)
			}
			err := ValidateUSearchRuntimeRoot(context.Background(), cache, "db", "profile", fixture.root.Manifest.GenerationID, 2, 7, 3, USearchVersion, fixture.root.Manifest.DescriptorSHA256)
			if (err == nil) != tc.wantOK {
				t.Fatalf("ValidateUSearchRuntimeRoot() error=%v want_ok=%t", err, tc.wantOK)
			}
		})
	}
}
