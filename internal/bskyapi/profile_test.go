package bskyapi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveChromeProfileDirDefaultsAndSelectsNamedProfile(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"Default", "Profile 1"} {
		if err := os.Mkdir(filepath.Join(base, name), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}

	got, err := resolveChromeProfileDir(base, "")
	if err != nil {
		t.Fatalf("resolve default profile: %v", err)
	}
	if want := filepath.Join(base, "Default"); got != want {
		t.Fatalf("default profile = %q, want %q", got, want)
	}

	got, err = resolveChromeProfileDir(base, "Profile 1")
	if err != nil {
		t.Fatalf("resolve named profile: %v", err)
	}
	if want := filepath.Join(base, "Profile 1"); got != want {
		t.Fatalf("named profile = %q, want %q", got, want)
	}
}

func TestResolveChromeProfileDirRejectsMissingProfile(t *testing.T) {
	if _, err := resolveChromeProfileDir(t.TempDir(), "Profile 9"); err == nil {
		t.Fatal("expected missing profile error")
	}
}
