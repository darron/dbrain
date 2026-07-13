package releaseautomation

import (
	"strings"
	"testing"
)

func TestNewCandidate(t *testing.T) {
	sha := "84B3CC07B1A4DF8B2CDEBE24F9982548FD60E805"
	got, err := NewCandidate(sha, "  Security pass / DB  ", 184, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA != strings.ToLower(sha) || got.ShortSHA != "84b3cc07b1a4" {
		t.Fatalf("SHA identity = %#v", got)
	}
	if got.Label != "Security pass / DB" || got.Slug != "security-pass-db" {
		t.Fatalf("label identity = %#v", got)
	}
	if got.FormulaVersion != "0.0.184.2" {
		t.Fatalf("FormulaVersion = %q", got.FormulaVersion)
	}
	if got.RunNumber != 184 || got.RunAttempt != 2 {
		t.Fatalf("run identity = %#v", got)
	}
	if got.ReleaseVersion != "test/security-pass-db@84b3cc07b1a4" {
		t.Fatalf("ReleaseVersion = %q", got.ReleaseVersion)
	}
	if got.ReleaseTag != "homebrew-test-184-2-security-pass-db-84b3cc07b1a4" {
		t.Fatalf("ReleaseTag = %q", got.ReleaseTag)
	}
}

func TestNewCandidateUsesFallbackSlug(t *testing.T) {
	got, err := NewCandidate(strings.Repeat("a", 40), "🧠", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Slug != "test" {
		t.Fatalf("Slug = %q, want test", got.Slug)
	}
}

func TestNewCandidateNormalizesConsecutivePeriods(t *testing.T) {
	got, err := NewCandidate(strings.Repeat("a", 40), "db..test", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Slug != "db.test" {
		t.Fatalf("Slug = %q, want db.test", got.Slug)
	}
	if strings.Contains(got.ReleaseTag, "..") {
		t.Fatalf("ReleaseTag = %q, must not contain consecutive periods", got.ReleaseTag)
	}
}

func TestNewCandidateRejectsInvalidInput(t *testing.T) {
	validSHA := strings.Repeat("a", 40)
	tests := []struct {
		name    string
		sha     string
		label   string
		run     int64
		attempt int64
	}{
		{name: "empty sha", sha: "", label: "test", run: 1, attempt: 1},
		{name: "short sha", sha: strings.Repeat("a", 39), label: "test", run: 1, attempt: 1},
		{name: "non hex sha", sha: strings.Repeat("z", 40), label: "test", run: 1, attempt: 1},
		{name: "padded sha", sha: " " + validSHA + " ", label: "test", run: 1, attempt: 1},
		{name: "empty label", sha: validSHA, label: "   ", run: 1, attempt: 1},
		{name: "control label", sha: validSHA, label: "bad\nlabel", run: 1, attempt: 1},
		{name: "leading newline", sha: validSHA, label: "\nbuild", run: 1, attempt: 1},
		{name: "trailing newline", sha: validSHA, label: "build\n", run: 1, attempt: 1},
		{name: "leading carriage return", sha: validSHA, label: "\rbuild", run: 1, attempt: 1},
		{name: "trailing carriage return", sha: validSHA, label: "build\r", run: 1, attempt: 1},
		{name: "leading tab", sha: validSHA, label: "\tbuild", run: 1, attempt: 1},
		{name: "trailing tab", sha: validSHA, label: "build\t", run: 1, attempt: 1},
		{name: "internal line separator", sha: validSHA, label: "bad\u2028label", run: 1, attempt: 1},
		{name: "leading line separator", sha: validSHA, label: "\u2028build", run: 1, attempt: 1},
		{name: "trailing line separator", sha: validSHA, label: "build\u2028", run: 1, attempt: 1},
		{name: "internal paragraph separator", sha: validSHA, label: "bad\u2029label", run: 1, attempt: 1},
		{name: "leading paragraph separator", sha: validSHA, label: "\u2029build", run: 1, attempt: 1},
		{name: "trailing paragraph separator", sha: validSHA, label: "build\u2029", run: 1, attempt: 1},
		{name: "oversize label", sha: validSHA, label: strings.Repeat("x", 65), run: 1, attempt: 1},
		{name: "zero run", sha: validSHA, label: "test", run: 0, attempt: 1},
		{name: "zero attempt", sha: validSHA, label: "test", run: 1, attempt: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewCandidate(tt.sha, tt.label, tt.run, tt.attempt); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestCandidateGitHubOutput(t *testing.T) {
	got, err := NewCandidate(strings.Repeat("a", 40), "test=one", 9, 1)
	if err != nil {
		t.Fatal(err)
	}
	output := got.GitHubOutput()
	for _, want := range []string{
		"sha=" + strings.Repeat("a", 40),
		"short_sha=aaaaaaaaaaaa",
		"label=test=one",
		"slug=test-one",
		"run_number=9",
		"run_attempt=1",
		"formula_version=0.0.9.1",
		"release_version=test/test-one@aaaaaaaaaaaa",
		"release_tag=homebrew-test-9-1-test-one-aaaaaaaaaaaa",
	} {
		if !strings.Contains(output, want+"\n") {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestCandidateRerunIdentitySemantics(t *testing.T) {
	sha := strings.Repeat("a", 40)
	original, err := NewCandidate(sha, "security pass", 184, 1)
	if err != nil {
		t.Fatal(err)
	}
	partialRetry, err := NewCandidate(sha, "security pass", original.RunNumber, original.RunAttempt)
	if err != nil {
		t.Fatal(err)
	}
	if partialRetry != original {
		t.Fatalf("partial retry changed canonical identity: got %#v want %#v", partialRetry, original)
	}
	fullRerun, err := NewCandidate(sha, "security pass", 184, 2)
	if err != nil {
		t.Fatal(err)
	}
	if fullRerun.ReleaseTag == original.ReleaseTag || fullRerun.FormulaVersion == original.FormulaVersion {
		t.Fatalf("full prepare rerun reused original identity: original=%#v rerun=%#v", original, fullRerun)
	}
}
