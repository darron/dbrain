package releaseautomation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"text/template"
)

var (
	sha256Regexp        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	formulaVersionRegex = regexp.MustCompile(`^0\.0\.([1-9][0-9]*)\.([1-9][0-9]*)$`)
	testFormulaClass    = regexp.MustCompile(`(?m)^class DbrainTest < Formula\s*$`)
	formulaVersionLine  = regexp.MustCompile(`(?m)^  version "([^"]+)"\s*$`)
)

var homebrewTargets = []string{
	"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64",
}

type FormulaInput struct {
	Candidate   Candidate
	ReleaseBase string
	Checksums   map[string]string
}

func RenderTestFormula(input FormulaInput) ([]byte, error) {
	if err := validateCandidate(input.Candidate); err != nil {
		return nil, err
	}
	expectedReleaseBase := "https://github.com/darron/dbrain/releases/download/" + input.Candidate.ReleaseTag
	if input.ReleaseBase != expectedReleaseBase {
		return nil, fmt.Errorf("release base must be %q", expectedReleaseBase)
	}
	if len(input.Checksums) != len(homebrewTargets) {
		return nil, fmt.Errorf("checksums must contain exactly %d Homebrew targets", len(homebrewTargets))
	}
	for _, target := range homebrewTargets {
		if !sha256Regexp.MatchString(input.Checksums[target]) {
			return nil, fmt.Errorf("%s checksum must contain 64 lowercase hexadecimal characters", target)
		}
	}

	var output bytes.Buffer
	if err := testFormulaTemplate.Execute(&output, input); err != nil {
		return nil, fmt.Errorf("render test formula: %w", err)
	}
	return output.Bytes(), nil
}

func validateCandidate(candidate Candidate) error {
	runNumber, runAttempt, err := parseFormulaVersion(candidate.FormulaVersion)
	if err != nil {
		return fmt.Errorf("invalid candidate: %w", err)
	}
	expected, err := NewCandidate(candidate.SHA, candidate.Label, runNumber, runAttempt)
	if err != nil {
		return fmt.Errorf("invalid candidate: %w", err)
	}
	if candidate != expected {
		return fmt.Errorf("candidate metadata is inconsistent with its validated identity")
	}
	return nil
}

func parseFormulaVersion(version string) (int64, int64, error) {
	matches := formulaVersionRegex.FindStringSubmatch(version)
	if matches == nil {
		return 0, 0, fmt.Errorf("formula version must match 0.0.<run-number>.<run-attempt>")
	}
	runNumber, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse formula run number: %w", err)
	}
	runAttempt, err := strconv.ParseInt(matches[2], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("parse formula run attempt: %w", err)
	}
	return runNumber, runAttempt, nil
}

// ValidateFormulaAdvance prevents an older workflow run from overwriting a
// newer moving test formula. A nil existing formula denotes first publication;
// every existing file must otherwise be a recognizable DbrainTest formula.
func ValidateFormulaAdvance(existing []byte, incoming string) error {
	incomingRun, incomingAttempt, err := parseFormulaVersion(incoming)
	if err != nil {
		return fmt.Errorf("incoming formula version: %w", err)
	}
	if existing == nil {
		return nil
	}
	if len(testFormulaClass.FindAll(existing, -1)) != 1 {
		return fmt.Errorf("existing formula must contain exactly one class DbrainTest < Formula declaration")
	}
	matches := formulaVersionLine.FindAllSubmatch(existing, -1)
	if len(matches) != 1 {
		return fmt.Errorf("existing formula must contain exactly one version declaration")
	}
	existingVersion := string(matches[0][1])
	existingRun, existingAttempt, err := parseFormulaVersion(existingVersion)
	if err != nil {
		return fmt.Errorf("existing formula version %q: %w", existingVersion, err)
	}
	if incomingRun < existingRun || (incomingRun == existingRun && incomingAttempt <= existingAttempt) {
		return fmt.Errorf("incoming formula version %q must be greater than existing %q", incoming, existingVersion)
	}
	return nil
}

func UpdatePrereleaseAllowlist(existing []byte, version string) ([]byte, error) {
	if _, _, err := parseFormulaVersion(version); err != nil {
		return nil, err
	}

	values := map[string]string{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &values); err != nil {
			return nil, fmt.Errorf("decode prerelease allowlist: %w", err)
		}
		if values == nil {
			return nil, fmt.Errorf("decode prerelease allowlist: expected a JSON object")
		}
	}
	values["dbrain-test"] = version
	encoded, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode prerelease allowlist: %w", err)
	}
	return append(encoded, '\n'), nil
}

func ValidateTapPaths(paths []string) error {
	allowed := map[string]bool{
		"Formula/dbrain-test.rb":                            true,
		"audit_exceptions/github_prerelease_allowlist.json": true,
	}
	ordered := append([]string(nil), paths...)
	sort.Strings(ordered)
	for _, path := range ordered {
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
  license all_of: ["MIT", "Apache-2.0"]

  conflicts_with "dbrain", because: "both install the dbrain binary"

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
    pkgshare.install "THIRD_PARTY_NOTICES.md", "LICENSE-USearch"
  end

  test do
    output = shell_output("#{bin}/dbrain version")
    assert_match "release_version: {{.Candidate.ReleaseVersion}}", output
    assert_match "commit: {{.Candidate.SHA}}", output

    status = shell_output(
      "#{bin}/dbrain --root #{testpath}/dbrain --no-debug semantic status --json",
    )
    if OS.mac? && Hardware::CPU.arm?
      assert_match '"state": "supported_ready"', status
      assert_match '"backend": "usearch"', status
      assert_match '"version": "2.26.0"', status
    else
      assert_match '"state": "unsupported"', status
    end
  end
end
`))
