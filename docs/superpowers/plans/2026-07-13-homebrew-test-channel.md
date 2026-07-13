# Homebrew Test Release Channel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an owner-dispatched GitHub Actions channel that builds an exact dbrain commit, publishes durable prerelease assets, and updates one moving `dbrain-test` Homebrew formula without changing stable Homebrew distribution or deleting runtime data.

**Architecture:** Put deterministic candidate identity and formula generation in a tested Go package plus a non-distributed devtool CLI. Keep candidate execution in read-only jobs with no tap secret; later jobs publish opaque archives and update only the test formula through an exact path allowlist. Add a final-release tag guard to the stable workflow and serialize both tap writers.

**Tech Stack:** Go 1.26, standard-library `flag`/JSON/text generation, GitHub Actions YAML, GitHub CLI, Homebrew Ruby formula syntax, existing Taskfile gates.

## Global Constraints

- Work only in `/private/tmp/dbrain-homebrew-test-channel` on branch `codex/homebrew-test-channel`.
- Preserve the existing `security-pass` checkout and PR #88; do not merge that branch into this worktree.
- The manual workflow accepts `sha` and `label`; `sha` is exactly 40 hexadecimal characters and is normalized to lowercase.
- The trimmed display label is at most 64 bytes and contains no control characters; its safe slug is at most 32 characters and falls back to `test`.
- Formula versions are `0.0.<GITHUB_RUN_NUMBER>.<GITHUB_RUN_ATTEMPT>`.
- Binary release versions are `test/<slug>@<12-character-sha>`.
- Candidate tags are `homebrew-test-<run-number>-<run-attempt>-<slug>-<12-character-sha>`.
- The reviewed workflow permits only `github.actor == 'darron'` and `github.triggering_actor == 'darron'`; the write-capable `darvisf` bot is trusted not to replace the workflow or misuse secrets.
- Candidate code receives no tap token and no repository write permission.
- Secret-bearing jobs never execute candidate binaries or candidate-controlled scripts.
- The moving formula is `Formula/dbrain-test.rb`, defines `DbrainTest`, conflicts with stable `dbrain`, and installs the normal `dbrain` executable.
- The test formula contains no uninstall, post-uninstall, cleanup, zap, migration, launchd, XDG, database, vault, cache, or log hooks.
- Tap mutation is limited to `Formula/dbrain-test.rb` and `audit_exceptions/github_prerelease_allowlist.json`; `Formula/dbrain.rb` must remain byte-identical.
- Stable release tags must match exact final form `^v[0-9]+\.[0-9]+\.[0-9]+$`.
- Stable and test tap writers share non-cancelling concurrency group `dbrain-homebrew-tap-update`.
- Do not create a real tag, prerelease, tap commit, Homebrew installation, or deployment during local verification.
- Every behavior change follows red-green TDD. Final code verification is `task fmt`, `task lint`, `task test-ci`, and `task build` plus workflow-specific checks.

---

## File Structure

- `internal/releaseautomation/candidate.go`: validate inputs and derive canonical candidate identity.
- `internal/releaseautomation/candidate_test.go`: candidate validation, normalization, ordering, and injection regression tests.
- `internal/releaseautomation/formula.go`: render `dbrain-test.rb`, merge the prerelease audit exception, and validate changed tap paths.
- `internal/releaseautomation/formula_test.go`: exact formula, allowlist, and no-cleanup-hook regressions.
- `internal/releaseautomation/workflows_test.go`: workflow trigger, identity, permission, credential-boundary, stable-tag, and concurrency policy tests.
- `cmd/devtools/homebrew_test_release/main.go`: non-distributed CLI used by trusted workflow jobs.
- `cmd/devtools/homebrew_test_release/main_test.go`: CLI output and error contract tests.
- `.github/workflows/homebrew-test.yaml`: manual candidate verify/build/publish/tap pipeline.
- `.github/workflows/release.yaml`: exact stable-tag guard and shared tap concurrency group.
- `Taskfile.yml`: focused `test-release-automation` task.
- `docs/release-build.md`: operator runbook, safety boundary, and recovery instructions.
- `CHANGELOG.md`: maintainer-facing release-channel entry.

---

### Task 1: Candidate Metadata And Input Safety

**Files:**
- Create: `internal/releaseautomation/candidate.go`
- Create: `internal/releaseautomation/candidate_test.go`

**Interfaces:**
- Consumes: raw SHA, raw label, GitHub run number, and run attempt.
- Produces: `Candidate`, `NewCandidate(string, string, int64, int64) (Candidate, error)`, and `GitHubOutput() string`.

- [ ] **Step 1: Write the failing candidate tests**

Create `internal/releaseautomation/candidate_test.go`:

```go
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
	if got.SHA != strings.ToLower(sha) || got.ShortSHA != "84b3cc07b1a" {
		t.Fatalf("SHA identity = %#v", got)
	}
	if got.Label != "Security pass / DB" || got.Slug != "security-pass-db" {
		t.Fatalf("label identity = %#v", got)
	}
	if got.FormulaVersion != "0.0.184.2" {
		t.Fatalf("FormulaVersion = %q", got.FormulaVersion)
	}
	if got.ReleaseVersion != "test/security-pass-db@84b3cc07b1a" {
		t.Fatalf("ReleaseVersion = %q", got.ReleaseVersion)
	}
	if got.ReleaseTag != "homebrew-test-184-2-security-pass-db-84b3cc07b1a" {
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

func TestNewCandidateRejectsInvalidInput(t *testing.T) {
	validSHA := strings.Repeat("a", 40)
	tests := []struct {
		name    string
		sha     string
		label   string
		run     int64
		attempt int64
	}{
		{name: "short sha", sha: strings.Repeat("a", 39), label: "test", run: 1, attempt: 1},
		{name: "non hex sha", sha: strings.Repeat("z", 40), label: "test", run: 1, attempt: 1},
		{name: "empty label", sha: validSHA, label: "   ", run: 1, attempt: 1},
		{name: "control label", sha: validSHA, label: "bad\nlabel", run: 1, attempt: 1},
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
		"label=test=one",
		"slug=test-one",
		"formula_version=0.0.9.1",
		"release_version=test/test-one@aaaaaaaaaaaa",
	} {
		if !strings.Contains(output, want+"\n") {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}
```

- [ ] **Step 2: Run the tests and verify RED**

Run:

```sh
go test ./internal/releaseautomation -run 'Test(NewCandidate|CandidateGitHubOutput)' -count=1
```

Expected: FAIL because `Candidate` and `NewCandidate` do not exist.

- [ ] **Step 3: Implement minimal candidate identity**

Create `internal/releaseautomation/candidate.go`:

```go
package releaseautomation

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	fullSHARegexp = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	unsafeSlugRun = regexp.MustCompile(`[^a-z0-9._-]+`)
)

type Candidate struct {
	SHA            string
	ShortSHA       string
	Label          string
	Slug           string
	FormulaVersion string
	ReleaseVersion string
	ReleaseTag     string
}

func NewCandidate(rawSHA, rawLabel string, runNumber, runAttempt int64) (Candidate, error) {
	if !fullSHARegexp.MatchString(rawSHA) {
		return Candidate{}, fmt.Errorf("sha must contain exactly 40 hexadecimal characters")
	}
	label := strings.TrimSpace(rawLabel)
	if label == "" {
		return Candidate{}, fmt.Errorf("label must not be empty")
	}
	if len([]byte(label)) > 64 {
		return Candidate{}, fmt.Errorf("label must be at most 64 bytes")
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return Candidate{}, fmt.Errorf("label must not contain control characters")
		}
	}
	if runNumber < 1 || runAttempt < 1 {
		return Candidate{}, fmt.Errorf("run number and attempt must be positive")
	}

	sha := strings.ToLower(rawSHA)
	slug := unsafeSlugRun.ReplaceAllString(strings.ToLower(label), "-")
	slug = strings.Trim(slug, "-._")
	if len(slug) > 32 {
		slug = strings.Trim(slug[:32], "-._")
	}
	if slug == "" {
		slug = "test"
	}
	shortSHA := sha[:12]
	formulaVersion := fmt.Sprintf("0.0.%d.%d", runNumber, runAttempt)

	return Candidate{
		SHA:            sha,
		ShortSHA:       shortSHA,
		Label:          label,
		Slug:           slug,
		FormulaVersion: formulaVersion,
		ReleaseVersion: fmt.Sprintf("test/%s@%s", slug, shortSHA),
		ReleaseTag:     fmt.Sprintf("homebrew-test-%d-%d-%s-%s", runNumber, runAttempt, slug, shortSHA),
	}, nil
}

func (c Candidate) GitHubOutput() string {
	return fmt.Sprintf(
		"sha=%s\nshort_sha=%s\nlabel=%s\nslug=%s\nformula_version=%s\nrelease_version=%s\nrelease_tag=%s\n",
		c.SHA, c.ShortSHA, c.Label, c.Slug, c.FormulaVersion, c.ReleaseVersion, c.ReleaseTag,
	)
}
```

- [ ] **Step 4: Run focused GREEN tests**

Run:

```sh
go test ./internal/releaseautomation -run 'Test(NewCandidate|CandidateGitHubOutput)' -count=2
```

Expected: PASS twice.

- [ ] **Step 5: Commit candidate metadata**

```sh
git add internal/releaseautomation/candidate.go internal/releaseautomation/candidate_test.go
git commit -m "ci: add Homebrew test candidate identity"
```

---

### Task 2: Safe Formula Generation And Tap Allowlisting

**Files:**
- Create: `internal/releaseautomation/formula.go`
- Create: `internal/releaseautomation/formula_test.go`

**Interfaces:**
- Consumes: `Candidate`, GitHub release base URL, four SHA-256 values, and an optional existing audit-exception JSON document.
- Produces: `RenderTestFormula(FormulaInput) ([]byte, error)`, `UpdatePrereleaseAllowlist([]byte, string) ([]byte, error)`, and `ValidateTapPaths([]string) error`.

- [ ] **Step 1: Write failing formula and path-policy tests**

Create `internal/releaseautomation/formula_test.go` with a fixture candidate and these assertions:

```go
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

func TestRenderTestFormulaRejectsInvalidChecksum(t *testing.T) {
	input := formulaFixture(t)
	input.Checksums["darwin_arm64"] = "bad"
	if _, err := RenderTestFormula(input); err == nil {
		t.Fatal("expected checksum error")
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
}

func TestValidateTapPaths(t *testing.T) {
	if err := ValidateTapPaths([]string{
		"Formula/dbrain-test.rb",
		"audit_exceptions/github_prerelease_allowlist.json",
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"Formula/dbrain.rb", "README.md", "Casks/dbrain.rb"} {
		if err := ValidateTapPaths([]string{path}); err == nil {
			t.Fatalf("expected %s to be rejected", path)
		}
	}
}
```

- [ ] **Step 2: Run the formula tests and verify RED**

Run:

```sh
go test ./internal/releaseautomation -run 'Test(RenderTestFormula|UpdatePrereleaseAllowlist|ValidateTapPaths)' -count=1
```

Expected: FAIL because formula interfaces do not exist.

- [ ] **Step 3: Implement deterministic rendering**

Create `internal/releaseautomation/formula.go` with:

```go
package releaseautomation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"text/template"
)

var sha256Regexp = regexp.MustCompile(`^[0-9a-f]{64}$`)

var homebrewTargets = []string{
	"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64",
}

type FormulaInput struct {
	Candidate   Candidate
	ReleaseBase string
	Checksums   map[string]string
}

func RenderTestFormula(input FormulaInput) ([]byte, error) {
	for _, target := range homebrewTargets {
		if !sha256Regexp.MatchString(input.Checksums[target]) {
			return nil, fmt.Errorf("%s checksum must contain 64 lowercase hexadecimal characters", target)
		}
	}
	if !strings.HasPrefix(input.ReleaseBase, "https://github.com/darron/dbrain/releases/download/") {
		return nil, fmt.Errorf("unexpected release base")
	}
	data := struct {
		FormulaInput
		Targets []string
	}{FormulaInput: input, Targets: homebrewTargets}
	var output bytes.Buffer
	if err := testFormulaTemplate.Execute(&output, data); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func UpdatePrereleaseAllowlist(existing []byte, version string) ([]byte, error) {
	values := map[string]string{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &values); err != nil {
			return nil, err
		}
	}
	values["dbrain-test"] = version
	return json.MarshalIndent(values, "", "  ")
}

func ValidateTapPaths(paths []string) error {
	allowed := map[string]bool{
		"Formula/dbrain-test.rb": true,
		"audit_exceptions/github_prerelease_allowlist.json": true,
	}
	sort.Strings(paths)
	for _, path := range paths {
		if !allowed[path] {
			return fmt.Errorf("tap update changed forbidden path %q", path)
		}
	}
	return nil
}

var testFormulaTemplate = template.Must(template.New("formula").Parse(`class DbrainTest < Formula
  desc "Local-first second-brain CLI test candidate"
  homepage "https://github.com/darron/dbrain"
  version "{{.Candidate.FormulaVersion}}"
  license "MIT"

  conflicts_with "dbrain", because: "both install the dbrain binary"

  {{/* Render the same nested OS/CPU shape as the stable formula. */}}
  if OS.mac?
    if Hardware::CPU.arm?
      url "{{.ReleaseBase}}/dbrain_{{.Candidate.ReleaseTag}}_darwin_arm64.tar.gz"
      sha256 "{{index .Checksums "darwin_arm64"}}"
    else
      url "{{.ReleaseBase}}/dbrain_{{.Candidate.ReleaseTag}}_darwin_amd64.tar.gz"
      sha256 "{{index .Checksums "darwin_amd64"}}"
    end
  elsif OS.linux?
    if Hardware::CPU.arm?
      url "{{.ReleaseBase}}/dbrain_{{.Candidate.ReleaseTag}}_linux_arm64.tar.gz"
      sha256 "{{index .Checksums "linux_arm64"}}"
    else
      url "{{.ReleaseBase}}/dbrain_{{.Candidate.ReleaseTag}}_linux_amd64.tar.gz"
      sha256 "{{index .Checksums "linux_amd64"}}"
    end
  end

  def install
    bin.install "dbrain"
  end

  test do
    output = shell_output("#{bin}/dbrain version")
    assert_match "release_version: {{.Candidate.ReleaseVersion}}", output
    assert_match "commit: {{.Candidate.SHA}}", output
  end
end
`))
```

- [ ] **Step 4: Run focused GREEN tests**

```sh
go test ./internal/releaseautomation -run 'Test(RenderTestFormula|UpdatePrereleaseAllowlist|ValidateTapPaths)' -count=2
```

Expected: PASS twice.

- [ ] **Step 5: Commit formula policy**

```sh
git add internal/releaseautomation/formula.go internal/releaseautomation/formula_test.go
git commit -m "ci: generate isolated Homebrew test formula"
```

---

### Task 3: Trusted Release Helper CLI

**Files:**
- Create: `cmd/devtools/homebrew_test_release/main.go`
- Create: `cmd/devtools/homebrew_test_release/main_test.go`
- Modify: `Taskfile.yml`

**Interfaces:**
- Consumes: `metadata`, `formula`, `allowlist`, or `validate-paths` subcommands.
- Produces: GitHub output lines, generated files, or a nonzero exit with a precise stderr message.

- [ ] **Step 1: Write failing CLI tests**

Structure the CLI as `run(args []string, stdout, stderr io.Writer) int`. Tests
must call `run` directly and cover:

```go
func TestRunMetadata(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"metadata", "--sha", strings.Repeat("a", 40), "--label", "Security pass",
		"--run-number", "184", "--run-attempt", "1",
	}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "release_tag=homebrew-test-184-1-security-pass-aaaaaaaaaaaa\n") {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestRunMetadataRejectsBadSHA(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"metadata", "--sha", "abc", "--label", "test", "--run-number", "1", "--run-attempt", "1"}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "40 hexadecimal") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}
```

Add formula tests using `t.TempDir()` and exact checksum flags, an allowlist test
that preserves an existing entry, and `validate-paths` tests that accept the two
allowed paths but reject `Formula/dbrain.rb`.

- [ ] **Step 2: Run CLI tests and verify RED**

```sh
go test ./cmd/devtools/homebrew_test_release -count=1
```

Expected: FAIL because the command package does not exist.

- [ ] **Step 3: Implement the CLI**

Use only the standard `flag` package. `main` calls `os.Exit(run(os.Args[1:],
os.Stdout, os.Stderr))`. Each subcommand uses a private `flag.FlagSet` with
`ContinueOnError` and writes parse errors to the provided stderr writer.

The top-level dispatcher is:

```go
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homebrew_test_release <metadata|formula|allowlist|validate-paths>")
		return 2
	}
	switch args[0] {
	case "metadata":
		return runMetadata(args[1:], stdout, stderr)
	case "formula":
		return runFormula(args[1:], stdout, stderr)
	case "allowlist":
		return runAllowlist(args[1:], stdout, stderr)
	case "validate-paths":
		return runValidatePaths(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
}
```

Implement the four private runners with signatures
`func runX(args []string, stdout, stderr io.Writer) int`. Return `2` for flag
usage errors and `1` for validated operational errors.

Exact commands:

```text
homebrew_test_release metadata --sha SHA --label LABEL --run-number N --run-attempt N
homebrew_test_release formula --output PATH --sha SHA --label LABEL --run-number N --run-attempt N --release-base URL --darwin-amd64-sha HEX --darwin-arm64-sha HEX --linux-amd64-sha HEX --linux-arm64-sha HEX
homebrew_test_release allowlist --input PATH --output PATH --version VERSION
homebrew_test_release validate-paths PATH [PATH...]
```

The `formula` command must reconstruct `Candidate` through `NewCandidate`; it
must not accept caller-provided release tag/version strings that could diverge
from metadata. Write generated files with mode `0644`, create only the direct
parent directory with `0755`, and never touch `Formula/dbrain.rb`.

- [ ] **Step 4: Add the focused Task target**

Add to `Taskfile.yml`:

```yaml
  test-release-automation:
    desc: Run deterministic release and workflow automation tests
    cmds:
      - GOCACHE={{.GOCACHE}} GOMODCACHE={{.GOMODCACHE}} go test ./internal/releaseautomation ./cmd/devtools/homebrew_test_release
```

- [ ] **Step 5: Run CLI and Task GREEN tests**

```sh
task test-release-automation
go run ./cmd/devtools/homebrew_test_release metadata \
  --sha 84b3cc07b1a4df8b2cdebe24f9982548fd60e805 \
  --label 'security pass' --run-number 184 --run-attempt 1
```

Expected: tests pass; command emits canonical GitHub output lines and no stderr.

- [ ] **Step 6: Commit the trusted CLI**

```sh
git add cmd/devtools/homebrew_test_release Taskfile.yml
git commit -m "ci: add Homebrew test release helper"
```

---

### Task 4: Manual Candidate Workflow And Policy Tests

**Files:**
- Create: `internal/releaseautomation/workflows_test.go`
- Create: `.github/workflows/homebrew-test.yaml`

**Interfaces:**
- Consumes: the trusted CLI, exact SHA/label inputs, `HOMEBREW_TAP_TOKEN`, and GitHub's run metadata.
- Produces: verified candidate archives, an immutable GitHub prerelease, a moving test formula, and a workflow summary.

- [ ] **Step 1: Write the failing workflow-policy test**

Create `internal/releaseautomation/workflows_test.go`. Read the workflow using a
repository-root helper based on `runtime.Caller`. Assert all of these exact
properties before the workflow exists:

```go
func readRepoFile(t *testing.T, relative string) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	data, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestHomebrewTestWorkflowPolicy(t *testing.T) {
	text := readRepoFile(t, ".github/workflows/homebrew-test.yaml")
	required := []string{
		"workflow_dispatch:", "sha:", "label:",
		"github.actor", "github.triggering_actor", "darron",
		"refs/heads/main", "persist-credentials: false",
		"permissions:\n  contents: read",
		"DBRAIN_RELEASE_VERSION", "--prerelease", "--target",
		"HOMEBREW_TAP_TOKEN", "Formula/dbrain-test.rb",
		"audit_exceptions/github_prerelease_allowlist.json",
		"git diff --exit-code -- Formula/dbrain.rb",
		"dbrain-homebrew-tap-update", "cancel-in-progress: false",
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Errorf("workflow missing %q", want)
		}
	}
	for _, forbidden := range []string{"push:\n", "pull_request:\n", "schedule:\n"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("manual workflow contains forbidden trigger %q", forbidden)
		}
	}
}
```

Also parse with `gopkg.in/yaml.v3` and assert the root is a mapping and the
`jobs` node contains exactly `prepare`, `verify`, `build`, `publish`, and
`update-homebrew-tap`. Add a policy assertion that only `publish` receives
`contents: write`, and that only `update-homebrew-tap` references the tap token.

- [ ] **Step 2: Run workflow tests and verify RED**

```sh
go test ./internal/releaseautomation -run TestHomebrewTestWorkflowPolicy -count=1
```

Expected: FAIL because `.github/workflows/homebrew-test.yaml` is missing.

- [ ] **Step 3: Add the manual workflow skeleton and prepare job**

Create `.github/workflows/homebrew-test.yaml` with only:

```yaml
name: Homebrew Test Candidate

on:
  workflow_dispatch:
    inputs:
      sha:
        description: Full 40-character dbrain commit SHA
        required: true
        type: string
      label:
        description: Human-readable test label
        required: true
        type: string

permissions:
  contents: read

jobs:
  prepare:
    name: Validate candidate
    runs-on: ubuntu-latest
    outputs:
      sha: ${{ steps.metadata.outputs.sha }}
      short_sha: ${{ steps.metadata.outputs.short_sha }}
      label: ${{ steps.metadata.outputs.label }}
      slug: ${{ steps.metadata.outputs.slug }}
      formula_version: ${{ steps.metadata.outputs.formula_version }}
      release_version: ${{ steps.metadata.outputs.release_version }}
      release_tag: ${{ steps.metadata.outputs.release_tag }}
    steps:
      - name: Enforce owner and default branch
        shell: bash
        env:
          ACTOR: ${{ github.actor }}
          TRIGGERING_ACTOR: ${{ github.triggering_actor }}
          WORKFLOW_REF: ${{ github.ref }}
        run: |
          set -euo pipefail
          test "${ACTOR}" = "darron"
          test "${TRIGGERING_ACTOR}" = "darron"
          test "${WORKFLOW_REF}" = "refs/heads/main"

      - name: Check out trusted workflow source
        uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd
        with:
          ref: ${{ github.sha }}
          persist-credentials: false

      - name: Set up Go
        uses: actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c
        with:
          go-version-file: go.mod
          cache: false

      - name: Derive candidate metadata
        id: metadata
        env:
          INPUT_SHA: ${{ inputs.sha }}
          INPUT_LABEL: ${{ inputs.label }}
          RUN_NUMBER: ${{ github.run_number }}
          RUN_ATTEMPT: ${{ github.run_attempt }}
        run: |
          go run ./cmd/devtools/homebrew_test_release metadata \
            --sha "${INPUT_SHA}" --label "${INPUT_LABEL}" \
            --run-number "${RUN_NUMBER}" --run-attempt "${RUN_ATTEMPT}" \
            >> "${GITHUB_OUTPUT}"
```

- [ ] **Step 4: Add verify and build jobs**

Use these exact immutable actions and tool versions:

```text
actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd
actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c
actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020
go-task/setup-task@01a4adf9db2d14c1de7a560f09170b6e0df736aa
actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a
actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c
Node 22
Task 3.52.0
golangci-lint v2.12.2
```

The build matrix is exactly:

```yaml
        include:
          - {goos: darwin, goarch: amd64, archive_ext: tar.gz}
          - {goos: darwin, goarch: arm64, archive_ext: tar.gz}
          - {goos: linux, goarch: amd64, archive_ext: tar.gz}
          - {goos: linux, goarch: arm64, archive_ext: tar.gz}
          - {goos: windows, goarch: amd64, archive_ext: zip}
```

Both jobs check out `${{ needs.prepare.outputs.sha }}` with
`persist-credentials: false`. `verify` runs `npm ci`, `task web-build`,
`task lint`, `task test-ci`, and `task build`. `build` repeats UI generation,
sets `DBRAIN_RELEASE_VERSION: ${{ needs.prepare.outputs.release_version }}` and
the matrix `GOOS`/`GOARCH`, runs `task build`, packages README/LICENSE/notices,
and uploads archives named:

```text
dbrain_<release_tag>_<goos>_<goarch>.tar.gz
dbrain_<release_tag>_windows_amd64.zip
```

Do not pass `HOMEBREW_TAP_TOKEN`, `github.token`, or `contents: write` to either
job. Set action caches to `false` for candidate code.

- [ ] **Step 5: Add prerelease publication**

Add `publish` with `permissions: contents: write`. It downloads and checksums
the archives but does not check out candidate source. Before creation, query the
tag and fail if it already exists. Publish with:

```sh
gh release create "${RELEASE_TAG}" \
  --repo "${GITHUB_REPOSITORY}" \
  --target "${CANDIDATE_SHA}" \
  --title "dbrain test: ${DISPLAY_LABEL} (${SHORT_SHA})" \
  --notes "Homebrew test candidate built from ${CANDIDATE_SHA}. Not a stable release." \
  --prerelease
gh release upload "${RELEASE_TAG}" dist/dbrain_* \
  --repo "${GITHUB_REPOSITORY}"
```

Use environment variables for every expression-derived value. Never splice the
display label directly into shell source.

- [ ] **Step 6: Add tap publication with exact authority limits**

Add `update-homebrew-tap` with:

```yaml
    concurrency:
      group: dbrain-homebrew-tap-update
      cancel-in-progress: false
```

The job must:

1. fail if `HOMEBREW_TAP_TOKEN` is empty;
2. download archives without executing or extracting them;
3. check out trusted source at `${{ github.sha }}` with persisted credentials disabled;
4. check out `darron/homebrew-tap` using only the tap token;
5. hash `homebrew-tap/Formula/dbrain.rb` before generation;
6. run the trusted CLI to generate `Formula/dbrain-test.rb` and merge the audit exception;
7. hash the stable formula again and compare exact hashes;
8. run `git -C homebrew-tap diff --exit-code -- Formula/dbrain.rb`;
9. pass every changed path to `homebrew_test_release validate-paths`;
10. stage only the two allowed paths, commit, and push; and
11. write install, upgrade, rollback, and removal commands to `${GITHUB_STEP_SUMMARY}`.

- [ ] **Step 7: Run workflow and package GREEN tests**

```sh
task test-release-automation
go test ./internal/releaseautomation -run TestHomebrewTestWorkflowPolicy -count=2
git diff --check
```

If `actionlint` is installed, also run:

```sh
actionlint .github/workflows/homebrew-test.yaml
```

Expected: all available checks pass; no workflow is dispatched.

- [ ] **Step 8: Commit the candidate workflow**

```sh
git add .github/workflows/homebrew-test.yaml internal/releaseautomation/workflows_test.go
git commit -m "ci: add owner-only Homebrew test channel"
```

---

### Task 5: Stable Release Guard And Tap Serialization

**Files:**
- Modify: `internal/releaseautomation/workflows_test.go`
- Modify: `.github/workflows/release.yaml`

**Interfaces:**
- Consumes: stable `GITHUB_REF_NAME` and the existing tap-update job.
- Produces: fail-closed final-version tag validation and the shared concurrency lock.

- [ ] **Step 1: Add failing stable-workflow policy tests**

Add assertions that `.github/workflows/release.yaml` contains:

```text
Validate final release tag
^v[0-9]+\.[0-9]+\.[0-9]+$
dbrain-homebrew-tap-update
cancel-in-progress: false
```

Parse step order and require `Validate final release tag` to precede `Check out
repository` in the `verify` job. Add a table test for the exact regular
expression:

```go
var finalReleaseTagRegexp = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

func TestFinalReleaseTagPolicy(t *testing.T) {
	tests := map[string]bool{
		"v0.6.0": true, "v10.20.300": true,
		"v0.6.0-rc.1": false, "v0.6.0-test": false,
		"v0.6": false, "version-1": false, "v1.2.3.4": false,
	}
	for tag, want := range tests {
		if got := finalReleaseTagRegexp.MatchString(tag); got != want {
			t.Errorf("tag %q match=%v, want %v", tag, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the tests and verify RED**

```sh
go test ./internal/releaseautomation -run 'Test(FinalReleaseTagPolicy|StableReleaseWorkflowPolicy)' -count=1
```

Expected: FAIL because the stable workflow lacks the guard and concurrency group.

- [ ] **Step 3: Add the first-step stable tag guard**

Before checkout in `verify.steps`, add:

```yaml
      - name: Validate final release tag
        shell: bash
        env:
          RELEASE_TAG: ${{ github.ref_name }}
        run: |
          set -euo pipefail
          if [[ ! "${RELEASE_TAG}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            echo "stable releases require an exact vX.Y.Z tag; got ${RELEASE_TAG}" >&2
            exit 1
          fi
```

Because every later stable job already depends transitively on `verify`, guard
failure prevents builds, release publication, and tap mutation.

- [ ] **Step 4: Add shared tap concurrency**

Under the existing stable `update-homebrew-tap` job, add:

```yaml
    concurrency:
      group: dbrain-homebrew-tap-update
      cancel-in-progress: false
```

- [ ] **Step 5: Run stable policy GREEN tests**

```sh
task test-release-automation
git diff --check .github/workflows/release.yaml
```

If installed:

```sh
actionlint .github/workflows/release.yaml .github/workflows/homebrew-test.yaml
```

Expected: policy tests and available workflow lint pass.

- [ ] **Step 6: Commit stable release protection**

```sh
git add .github/workflows/release.yaml internal/releaseautomation/workflows_test.go
git commit -m "ci: guard stable Homebrew publication"
```

---

### Task 6: Documentation, Changelog, And Final Verification

**Files:**
- Modify: `docs/release-build.md`
- Modify: `CHANGELOG.md`
- Inspect: `docs/superpowers/specs/2026-07-13-homebrew-test-channel-design.md`
- Inspect: `.github/workflows/homebrew-test.yaml`
- Inspect: `.github/workflows/release.yaml`

**Interfaces:**
- Consumes: implemented commands, formula name, safety boundaries, and failure modes.
- Produces: exact maintainer runbook and a verified implementation handoff.

- [ ] **Step 1: Update release documentation**

Add a `Homebrew Test Candidates` section to `docs/release-build.md` containing:

```sh
# Run Homebrew Test Candidate from GitHub Actions with an exact SHA and label.
brew unlink dbrain
brew install darron/tap/dbrain-test
dbrain version

# Move to the next candidate.
brew update
brew upgrade dbrain-test
dbrain version

# Return to stable.
brew unlink dbrain-test
brew link dbrain
dbrain version

# Optionally remove only the test keg and link.
brew uninstall dbrain-test
brew link dbrain
```

State explicitly that unlink/uninstall does not touch config, XDG roots,
database, vault, media, cache, logs, launchd state, helpers, or models. Document
that only `darron` may dispatch/rerun the reviewed workflow and that `darvisf`
is a trusted write-capable bot account. Document recovery when the prerelease
exists but tap update fails: inspect the failed run, fix the workflow/code in a
PR, then start a new run; never retarget the old candidate tag.

- [ ] **Step 2: Add changelog entry**

Under the current dated heading, add concise bullets:

```markdown
- **Release testing**: Added an owner-dispatched Homebrew test channel that builds an exact commit into durable prerelease assets and one moving `dbrain-test` formula without changing stable `dbrain` distribution.
- **Release safety**: Stable publication now accepts only exact `vX.Y.Z` tags, serializes tap updates, and tests that candidate formula generation cannot modify `Formula/dbrain.rb` or add runtime-data cleanup hooks.
```

- [ ] **Step 3: Run focused verification**

```sh
task test-release-automation
go test ./internal/releaseautomation ./cmd/devtools/homebrew_test_release -count=2
git diff --check
```

Expected: all focused checks pass twice.

- [ ] **Step 4: Run standard repository gates**

```sh
task fmt
task lint
task test-ci
task build
```

Expected: every command exits zero.

- [ ] **Step 5: Validate workflow YAML and generated formula**

Use the repo's Go YAML dependency to parse both workflow files in tests. If
`actionlint` exists, run it against both files. Generate a synthetic formula and
audit exception into `t.TempDir()` or `/private/tmp`, run `ruby -c` on the
formula when Ruby is installed, and verify no generated file remains in the
worktree.

Do not dispatch the workflow, create tags/releases, modify the tap, or run
Homebrew installation as part of this step.

- [ ] **Step 6: Inspect the complete diff against the approved spec**

```sh
git status -sb
git diff --check main...HEAD
git diff --stat main...HEAD
git diff main...HEAD -- \
  .github/workflows/homebrew-test.yaml \
  .github/workflows/release.yaml \
  internal/releaseautomation \
  cmd/devtools/homebrew_test_release \
  Taskfile.yml docs/release-build.md CHANGELOG.md
```

Confirm every acceptance criterion and record any skipped optional tool as a
coverage gap rather than a pass.

- [ ] **Step 7: Commit documentation**

```sh
git add docs/release-build.md CHANGELOG.md
git commit -m "docs: document Homebrew test candidates"
```

- [ ] **Step 8: Update the existing draft PR**

Push `codex/homebrew-test-channel`, update PR #89 to describe the implemented
workflow rather than design-only scope, and leave it draft until GitHub CI and
an external code review pass. Do not dispatch the candidate workflow before
merge without a separate explicit approval.

---

## Plan Self-Review Checklist

- Candidate input, arbitrary-label normalization, numeric ordering, and SHA identity: Task 1.
- Formula shape, normal binary name, stable conflict, no cleanup hooks, audit exception, and tap allowlist: Task 2.
- Trusted helper CLI and repeatable local target: Task 3.
- Owner-only manual workflow, credential separation, immutable prerelease, build matrix, and tap update: Task 4.
- Exact stable tag guard and cross-workflow tap serialization: Task 5.
- Install/upgrade/rollback/removal docs, changelog, full gates, and draft PR handoff: Task 6.
- No production dispatch, tag, release, tap mutation, Homebrew install, or deployment is authorized by this plan.
