package brainresearch

import (
	"strings"

	"github.com/darron/dbrain/internal/ask"
)

func buildNextSteps(evidence []ask.Evidence, query string) []SuggestedAction {
	if len(evidence) == 0 {
		return nil
	}
	query = strings.TrimSpace(query)
	steps := make([]SuggestedAction, 0, 2)
	if len(evidence) > 1 {
		lookups := make([]string, 0, min(len(evidence), 5))
		for _, doc := range evidence {
			if strings.TrimSpace(doc.SourceKey) == "" {
				continue
			}
			lookups = append(lookups, doc.SourceKey)
			if len(lookups) >= 5 {
				break
			}
		}
		if len(lookups) > 0 {
			params := map[string]interface{}{
				"lookups":      lookups,
				"content_mode": "evidence",
			}
			if query != "" {
				params["query"] = query
			}
			steps = append(steps, SuggestedAction{
				Action: "inspect_top_evidence",
				Label:  "Inspect top evidence",
				Reason: "Inspect the strongest evidence notes before making detailed claims.",
				Params: params,
			})
		}
	} else {
		params := map[string]interface{}{
			"lookup":       evidence[0].SourceKey,
			"content_mode": "evidence",
		}
		if query != "" {
			params["query"] = query
		}
		steps = append(steps, SuggestedAction{
			Action: "inspect_top_evidence",
			Label:  "Inspect top evidence",
			Reason: "Inspect the strongest evidence note before making detailed claims.",
			Params: params,
		})
	}
	for _, doc := range evidence {
		if doc.Kind == "item" || doc.Kind == "source" {
			steps = append(steps, SuggestedAction{
				Action: "expand_related",
				Label:  "Expand related context",
				Reason: "Follow linked sources or backlinks around a high-signal evidence node.",
				Params: map[string]interface{}{
					"lookup": doc.SourceKey,
				},
			})
			break
		}
	}
	return steps
}
