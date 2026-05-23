package researcheval

import (
	"encoding/json"
	"fmt"
	"os"
)

func LoadCases(path string) ([]Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read research eval cases %s: %w", path, err)
	}

	var cases []Case
	if err := json.Unmarshal(data, &cases); err == nil {
		if len(cases) == 0 {
			return nil, fmt.Errorf("research eval cases file %s contains no cases", path)
		}
		return cases, nil
	}

	var proposal Proposal
	if err := json.Unmarshal(data, &proposal); err != nil {
		return nil, fmt.Errorf("parse research eval cases %s: %w", path, err)
	}
	if len(proposal.Cases) == 0 {
		return nil, fmt.Errorf("research eval cases file %s contains no cases", path)
	}
	return proposal.Cases, nil
}

func ExampleCases() []Case {
	return []Case{
		{
			Name:                     "known topic retrieves and cites expected source",
			Question:                 "What does my brain know about Example Topic?",
			Limit:                    8,
			IncludeRelated:           true,
			RelatedLimit:             2,
			MinEvidence:              2,
			ExpectPlanner:            "deterministic",
			ExpectQueryFamily:        "entity_topic_overview",
			ExpectQueryTerms:         []string{"example", "topic"},
			ExpectAnySourceKeys:      []string{"src:replace-with-known-source"},
			ExpectCitationSourceKeys: []string{"src:replace-with-known-source"},
			ExpectAnswerStatus:       "ok",
			MinRetrievalSignals:      1,
			MaxLatencyMS:             5000,
		},
		{
			Name:                "known topic planner-disabled baseline",
			Question:            "What does my brain know about Example Topic?",
			Limit:               8,
			DisablePlanner:      true,
			MinEvidence:         1,
			ExpectPlanner:       "deterministic",
			ExpectQueryFamily:   "entity_topic_overview",
			ExpectQueryTerms:    []string{"example", "topic"},
			ExpectAnySourceKeys: []string{"src:replace-with-known-source"},
			MinRetrievalSignals: 1,
			MaxLatencyMS:        5000,
		},
	}
}
