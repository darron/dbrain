package brainresearch

import (
	"regexp"
	"strings"
)

const (
	queryFamilyEntityTopicOverview = "entity_topic_overview"
	queryFamilyPeopleEventLookup   = "person_news_event_lookup"
	queryFamilyModelToolSelection  = "model_tool_selection"
	queryFamilySoftwareProject     = "software_project_lookup"
	queryFamilyComparison          = "comparison"
	queryFamilyTimeline            = "timeline_history"
	queryFamilyMediaEvidence       = "media_transcript_ocr_lookup"
	queryFamilyCorrectiveFollowup  = "corrective_followup"
	queryFamilyExactLookup         = "exact_title_source_lookup"
)

var sourceKeyCandidateRE = regexp.MustCompile(`(?i)\b(?:src|x|apple-note|feed-entry|gh-star|github_star|yt|youtube|safari-tab|manual):[a-z0-9][a-z0-9:_./-]*`)

func classifyQueryFamily(question string, terms []string, concepts []QueryConcept) string {
	lowerQuestion := strings.ToLower(question)
	switch {
	case len(sourceKeyCandidates(question)) > 0 || hasAnyTerm(terms, "exact", "title", "sourcekey", "lookup"):
		return queryFamilyExactLookup
	case hasAnyTerm(terms, "not", "wrong", "exclude", "without", "instead", "correct", "correction") ||
		strings.Contains(lowerQuestion, "not ") ||
		strings.Contains(lowerQuestion, " instead") ||
		strings.Contains(lowerQuestion, "wrong"):
		return queryFamilyCorrectiveFollowup
	case hasPeopleEventShape(concepts):
		return queryFamilyPeopleEventLookup
	case hasAnyTerm(terms, "transcript", "video", "audio", "ocr", "screenshot", "image", "photo", "recording"):
		return queryFamilyMediaEvidence
	case hasAnyTerm(terms, "timeline", "history", "historical", "evolution", "recent", "latest"):
		return queryFamilyTimeline
	case hasAnyTerm(terms, "compare", "comparison", "versus", "vs") || strings.Contains(lowerQuestion, " vs ") || strings.Contains(lowerQuestion, " versus ") || hasConcept(concepts, "alternative"):
		return queryFamilyComparison
	case hasConcept(concepts, "model") && (hasConcept(concepts, "agent") || hasAnyTerm(terms, "tool", "tools", "stack", "runtime")):
		return queryFamilyModelToolSelection
	case strings.Contains(lowerQuestion, "github") || strings.Contains(lowerQuestion, " repo") || strings.Contains(lowerQuestion, " repository") || hasAnyTerm(terms, "mcp", "devtools", "library", "package", "project", "tool"):
		return queryFamilySoftwareProject
	default:
		return queryFamilyEntityTopicOverview
	}
}

func hasPeopleEventShape(concepts []QueryConcept) bool {
	hasParent := hasConcept(concepts, "father") || hasConcept(concepts, "mother")
	return (hasParent && hasConcept(concepts, "children") && hasConcept(concepts, "kill")) ||
		(hasConcept(concepts, "kill") && hasConcept(concepts, "charge"))
}

func hasAnyTerm(terms []string, candidates ...string) bool {
	for _, term := range terms {
		for _, candidate := range candidates {
			if strings.EqualFold(term, candidate) {
				return true
			}
		}
	}
	return false
}

func sourceKeyCandidates(question string) []string {
	text := strings.TrimSpace(question)
	indices := sourceKeyCandidateRE.FindAllStringIndex(text, -1)
	matches := make([]string, 0, len(indices))
	for _, idx := range indices {
		if len(idx) != 2 {
			continue
		}
		if idx[0] > 0 && text[idx[0]-1] == '/' {
			continue
		}
		value := strings.TrimRight(text[idx[0]:idx[1]], ".,;!?)]}`")
		if value == "" {
			continue
		}
		matches = append(matches, value)
	}
	return uniqueStrings(matches)
}
