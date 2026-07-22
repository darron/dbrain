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
	periodRun     = regexp.MustCompile(`\.+`)
)

type Candidate struct {
	SHA            string
	ShortSHA       string
	Label          string
	Slug           string
	RunNumber      int64
	RunAttempt     int64
	FormulaVersion string
	ReleaseVersion string
	ReleaseTag     string
}

func NewCandidate(rawSHA, rawLabel string, runNumber, runAttempt int64) (Candidate, error) {
	if !fullSHARegexp.MatchString(rawSHA) {
		return Candidate{}, fmt.Errorf("sha must contain exactly 40 hexadecimal characters")
	}
	for _, r := range rawLabel {
		if unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp) {
			return Candidate{}, fmt.Errorf("label must not contain control characters or line separators")
		}
	}
	label := strings.TrimSpace(rawLabel)
	if label == "" {
		return Candidate{}, fmt.Errorf("label must not be empty")
	}
	if len([]byte(label)) > 64 {
		return Candidate{}, fmt.Errorf("label must be at most 64 bytes")
	}
	if runNumber < 1 || runAttempt < 1 {
		return Candidate{}, fmt.Errorf("run number and attempt must be positive")
	}

	sha := strings.ToLower(rawSHA)
	slug := unsafeSlugRun.ReplaceAllString(strings.ToLower(label), "-")
	slug = periodRun.ReplaceAllString(slug, ".")
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
		RunNumber:      runNumber,
		RunAttempt:     runAttempt,
		FormulaVersion: formulaVersion,
		ReleaseVersion: fmt.Sprintf("test/%s@%s", slug, shortSHA),
		ReleaseTag:     fmt.Sprintf("homebrew-test-%d-%d-%s-%s", runNumber, runAttempt, slug, shortSHA),
	}, nil
}

func (c Candidate) GitHubOutput() string {
	return fmt.Sprintf(
		"sha=%s\nshort_sha=%s\nlabel=%s\nslug=%s\nrun_number=%d\nrun_attempt=%d\nformula_version=%s\nrelease_version=%s\nrelease_tag=%s\n",
		c.SHA, c.ShortSHA, c.Label, c.Slug, c.RunNumber, c.RunAttempt, c.FormulaVersion, c.ReleaseVersion, c.ReleaseTag,
	)
}
