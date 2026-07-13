package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSHA = "84b3cc07b1a4df8b2cdebe24f9982548fd60e805"

func TestRunMetadata(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"metadata", "--sha", strings.Repeat("a", 40), "--label", "Security pass",
		"--run-number", "184", "--run-attempt", "1",
	}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	want := "sha=" + strings.Repeat("a", 40) + "\n" +
		"short_sha=aaaaaaaaaaaa\n" +
		"label=Security pass\n" +
		"slug=security-pass\n" +
		"formula_version=0.0.184.1\n" +
		"release_version=test/security-pass@aaaaaaaaaaaa\n" +
		"release_tag=homebrew-test-184-1-security-pass-aaaaaaaaaaaa\n"
	if stdout.String() != want {
		t.Fatalf("stdout=%q, want %q", stdout.String(), want)
	}
}

func TestRunMetadataRejectsBadSHA(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"metadata", "--sha", "abc", "--label", "test", "--run-number", "1", "--run-attempt", "1"}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "40 hexadecimal") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunFormulaWritesRequestedFile(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "generated", "dbrain-test.rb")
	args := formulaArgs(output)
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`version "0.0.184.1"`,
		"homebrew-test-184-1-security-pass-84b3cc07b1a4",
		"test/security-pass@84b3cc07b1a4",
		testSHA,
		strings.Repeat("1", 64), strings.Repeat("2", 64),
		strings.Repeat("3", 64), strings.Repeat("4", 64),
	} {
		if !bytes.Contains(got, []byte(want)) {
			t.Fatalf("formula missing %q:\n%s", want, got)
		}
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("mode=%o, want 644", info.Mode().Perm())
	}
}

func TestRunFormulaReconstructsCandidate(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "dbrain-test.rb")
	args := formulaArgs(output)
	for _, forbidden := range []string{"--release-tag", "caller-tag", "--version", "9.9.9"} {
		args = append(args, forbidden)
	}
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("formula was written after usage error: %v", err)
	}
}

func TestRunFormulaRejectsStableFormulaPath(t *testing.T) {
	root := t.TempDir()
	stable := filepath.Join(root, "Formula", "dbrain.rb")
	args := formulaArgs(stable)
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "must not write stable Formula/dbrain.rb") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(stable); !os.IsNotExist(err) {
		t.Fatalf("stable formula was touched: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(stable)); !os.IsNotExist(err) {
		t.Fatalf("stable formula parent was created: %v", err)
	}
}

func TestRunAllowlistPreservesExistingEntry(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "existing.json")
	output := filepath.Join(root, "audit_exceptions", "github_prerelease_allowlist.json")
	if err := os.WriteFile(input, []byte(`{"other":"1.2.3"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"allowlist", "--input", input, "--output", output, "--version", "0.0.184.1"}, &stdout, &stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]string
	if err := json.Unmarshal(got, &values); err != nil {
		t.Fatal(err)
	}
	if values["other"] != "1.2.3" || values["dbrain-test"] != "0.0.184.1" {
		t.Fatalf("allowlist=%#v", values)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("mode=%o, want 644", info.Mode().Perm())
	}
}

func TestRunAllowlistSupportsInPlaceUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowlist.json")
	if err := os.WriteFile(path, []byte(`{"other":"1.2.3"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"allowlist", "--input", path, "--output", path, "--version", "0.0.184.1"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`"dbrain-test": "0.0.184.1"`)) {
		t.Fatalf("allowlist=%s", got)
	}
}

func TestRunValidatePaths(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"validate-paths",
		"Formula/dbrain-test.rb",
		"audit_exceptions/github_prerelease_allowlist.json",
	}, &stdout, &stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"validate-paths", "Formula/dbrain.rb"}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `forbidden path "Formula/dbrain.rb"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "no command", want: "usage: homebrew_test_release"},
		{name: "unknown command", args: []string{"wat"}, want: `unknown command "wat"`},
		{name: "metadata missing flag", args: []string{"metadata", "--sha", testSHA}, want: "metadata requires --sha, --label, --run-number, and --run-attempt"},
		{name: "metadata extra argument", args: []string{"metadata", "--sha", testSHA, "--label", "test", "--run-number", "1", "--run-attempt", "1", "extra"}, want: "metadata does not accept positional arguments"},
		{name: "formula missing flags", args: []string{"formula", "--output", "x"}, want: "formula requires"},
		{name: "formula bad integer", args: []string{"formula", "--run-number", "nope"}, want: "invalid value"},
		{name: "allowlist missing flags", args: []string{"allowlist", "--input", "x"}, want: "allowlist requires --input, --output, and --version"},
		{name: "allowlist extra argument", args: []string{"allowlist", "--input", "x", "--output", "y", "--version", "0.0.1.1", "extra"}, want: "allowlist does not accept positional arguments"},
		{name: "validate paths empty", args: []string{"validate-paths"}, want: "validate-paths requires at least one path"},
		{name: "validate paths flag", args: []string{"validate-paths", "--bad"}, want: "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q, want stderr containing %q", code, stdout.String(), stderr.String(), tt.want)
			}
		})
	}
}

func TestRunOperationalErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "formula invalid checksum", args: replaceArg(formulaArgs(filepath.Join(t.TempDir(), "dbrain-test.rb")), "--darwin-amd64-sha", "bad"), want: "checksum must contain 64 lowercase hexadecimal"},
		{name: "allowlist missing input", args: []string{"allowlist", "--input", filepath.Join(t.TempDir(), "missing.json"), "--output", filepath.Join(t.TempDir(), "out.json"), "--version", "0.0.1.1"}, want: "read allowlist input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q, want stderr containing %q", code, stdout.String(), stderr.String(), tt.want)
			}
		})
	}
}

func formulaArgs(output string) []string {
	releaseTag := "homebrew-test-184-1-security-pass-84b3cc07b1a4"
	return []string{
		"formula", "--output", output,
		"--sha", testSHA,
		"--label", "Security pass",
		"--run-number", "184",
		"--run-attempt", "1",
		"--release-base", "https://github.com/darron/dbrain/releases/download/" + releaseTag,
		"--darwin-amd64-sha", strings.Repeat("1", 64),
		"--darwin-arm64-sha", strings.Repeat("2", 64),
		"--linux-amd64-sha", strings.Repeat("3", 64),
		"--linux-arm64-sha", strings.Repeat("4", 64),
	}
}

func replaceArg(args []string, flagName, value string) []string {
	result := append([]string(nil), args...)
	for i := range result {
		if result[i] == flagName && i+1 < len(result) {
			result[i+1] = value
			return result
		}
	}
	return result
}
