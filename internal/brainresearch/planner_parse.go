package brainresearch

import (
	"encoding/json"
	"fmt"
	"strings"
)

func parseModelResearchPlan(text string) (modelResearchPlan, error) {
	payload := extractJSONObject(text)
	if payload == "" {
		return modelResearchPlan{}, fmt.Errorf("planner response did not contain a JSON object")
	}
	var raw modelResearchPlan
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return modelResearchPlan{}, fmt.Errorf("parse planner JSON: %w", err)
	}
	return sanitizeModelResearchPlan(raw), nil
}

func sanitizeModelResearchPlan(raw modelResearchPlan) modelResearchPlan {
	var out modelResearchPlan
	out.Concepts = sanitizeModelConcepts(raw.Concepts)
	out.QueryVariants = sanitizeModelQueryVariants(raw.QueryVariants)
	return out
}

func extractJSONObject(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return ""
	}
	return text[start : end+1]
}
