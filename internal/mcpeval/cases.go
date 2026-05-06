package mcpeval

import (
	"encoding/json"
	"fmt"
	"os"
)

func LoadCases(path string) ([]Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read eval cases %s: %w", path, err)
	}
	var cases []Case
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, fmt.Errorf("parse eval cases %s: %w", path, err)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("eval cases file %s contains no cases", path)
	}
	return cases, nil
}

func ExampleCases() []Case {
	return []Case{
		{
			Name:                   "known topic retrieves expected saved evidence",
			Question:               "What does my brain know about Example Topic?",
			Limit:                  8,
			SourceTypes:            []string{"web"},
			IncludeRelated:         true,
			RelatedLimit:           2,
			MinEvidence:            3,
			ExpectTopSourceKeys:    []string{"src:replace-with-a-known-good-source"},
			ExpectAnySourceKeys:    []string{"src:replace-with-a-known-good-source"},
			ExpectText:             []string{"replace with a phrase that should appear in retrieved evidence"},
			ExpectTopText:          []string{"replace with a phrase that should appear in the strongest evidence row"},
			ForbidText:             []string{"replace with known boilerplate or noisy phrase"},
			RequireTopMatchedTerms: []string{"replace-with-important-term"},
			ForbidTopMissingTerms:  []string{"replace-with-important-term"},
			MaxLatencyMS:           3000,
		},
		{
			Name:                "known tag exposes representative saved items",
			Question:            "What does my brain know about Example Person?",
			Limit:               8,
			MinEvidence:         1,
			MinExactTagEvidence: 1,
			ExpectAnyExactTagEvidenceSourceKeys: []string{
				"x:replace-with-tagged-item",
				"yt:replace-with-tagged-video",
			},
			ExpectExactTagEvidenceText: []string{"replace with phrase from a tagged saved item"},
			MaxLatencyMS:               3000,
		},
	}
}
