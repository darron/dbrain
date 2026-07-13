package releaseautomation

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func formulaFixture(t *testing.T) FormulaInput {
	t.Helper()
	candidate, err := NewCandidate(strings.Repeat("a", 40), "security pass", 184, 1)
	if err != nil {
		t.Fatal(err)
	}
	return FormulaInput{
		Candidate:   candidate,
		ReleaseBase: "https://github.com/darron/dbrain/releases/download/" + candidate.ReleaseTag,
		Checksums: map[string]string{
			"darwin_amd64": strings.Repeat("1", 64),
			"darwin_arm64": strings.Repeat("2", 64),
			"linux_amd64":  strings.Repeat("3", 64),
			"linux_arm64":  strings.Repeat("4", 64),
		},
	}
}

func TestRenderTestFormula(t *testing.T) {
	input := formulaFixture(t)
	got, err := RenderTestFormula(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"class DbrainTest < Formula",
		`version "0.0.184.1"`,
		`conflicts_with "dbrain", because: "both install the dbrain binary"`,
		`bin.install "dbrain"`,
		input.Candidate.ReleaseTag,
		input.Candidate.ReleaseVersion,
		input.Candidate.SHA,
		strings.Repeat("1", 64), strings.Repeat("2", 64),
		strings.Repeat("3", 64), strings.Repeat("4", 64),
	} {
		if !bytes.Contains(got, []byte(want)) {
			t.Fatalf("formula missing %q:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{
		"uninstall do", "post_uninstall", "zap do", ".config/dbrain",
		".local/share/dbrain", "LaunchAgents", "launchctl", "rm_rf",
	} {
		if bytes.Contains(bytes.ToLower(got), bytes.ToLower([]byte(forbidden))) {
			t.Fatalf("formula contains forbidden cleanup text %q", forbidden)
		}
	}
}

func TestRenderTestFormulaUsesExactAssetURLs(t *testing.T) {
	input := formulaFixture(t)
	got, err := RenderTestFormula(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"} {
		want := input.ReleaseBase + "/dbrain_" + input.Candidate.ReleaseTag + "_" + target + ".tar.gz"
		if !bytes.Contains(got, []byte(`url "`+want+`"`)) {
			t.Fatalf("formula missing exact URL %q:\n%s", want, got)
		}
	}
}

func TestRenderTestFormulaRejectsInvalidChecksum(t *testing.T) {
	input := formulaFixture(t)
	input.Checksums["darwin_arm64"] = "bad"
	if _, err := RenderTestFormula(input); err == nil {
		t.Fatal("expected checksum error")
	}
}

func TestRenderTestFormulaRejectsUntrustedMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*FormulaInput)
	}{
		{
			name: "release base outside repository",
			mutate: func(input *FormulaInput) {
				input.ReleaseBase = "https://example.com/releases/" + input.Candidate.ReleaseTag
			},
		},
		{
			name: "release base with ruby injection",
			mutate: func(input *FormulaInput) {
				input.ReleaseBase += `\"\n  system \"touch\", \"/tmp/pwned\"`
			},
		},
		{
			name: "tampered formula version",
			mutate: func(input *FormulaInput) {
				input.Candidate.FormulaVersion = `0.0.184.1\"; system \"id`
			},
		},
		{
			name: "tampered release version",
			mutate: func(input *FormulaInput) {
				input.Candidate.ReleaseVersion += `\"; system \"id`
			},
		},
		{
			name: "tampered release tag",
			mutate: func(input *FormulaInput) {
				input.Candidate.ReleaseTag += "/../../stable"
				input.ReleaseBase = "https://github.com/darron/dbrain/releases/download/" + input.Candidate.ReleaseTag
			},
		},
		{
			name: "tampered sha",
			mutate: func(input *FormulaInput) {
				input.Candidate.SHA = strings.Repeat("a", 39) + `\"`
			},
		},
		{
			name: "uppercase checksum",
			mutate: func(input *FormulaInput) {
				input.Checksums["linux_arm64"] = strings.Repeat("A", 64)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := formulaFixture(t)
			tt.mutate(&input)
			if _, err := RenderTestFormula(input); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestUpdatePrereleaseAllowlistPreservesOtherFormulae(t *testing.T) {
	existing := []byte(`{"other":"1.2.3"}`)
	got, err := UpdatePrereleaseAllowlist(existing, "0.0.184.1")
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["other"] != "1.2.3" || decoded["dbrain-test"] != "0.0.184.1" {
		t.Fatalf("allowlist = %#v", decoded)
	}
	if !bytes.HasSuffix(got, []byte("\n")) {
		t.Fatalf("allowlist must end with a newline: %q", got)
	}
}

func TestUpdatePrereleaseAllowlistRejectsInvalidInput(t *testing.T) {
	for _, tt := range []struct {
		name     string
		existing []byte
		version  string
	}{
		{name: "invalid json", existing: []byte(`{"other":`), version: "0.0.1.1"},
		{name: "wrong json shape", existing: []byte(`[]`), version: "0.0.1.1"},
		{name: "invalid version", existing: []byte(`{}`), version: "1.2.3-rc1"},
		{name: "injected version", existing: []byte(`{}`), version: "0.0.1.1\nother"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := UpdatePrereleaseAllowlist(tt.existing, tt.version); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateTapPaths(t *testing.T) {
	if err := ValidateTapPaths([]string{
		"Formula/dbrain-test.rb",
		"audit_exceptions/github_prerelease_allowlist.json",
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"Formula/dbrain.rb", "README.md", "Casks/dbrain.rb",
		"Formula/../Formula/dbrain-test.rb", "/Formula/dbrain-test.rb",
	} {
		if err := ValidateTapPaths([]string{path}); err == nil {
			t.Fatalf("expected %s to be rejected", path)
		}
	}
}
