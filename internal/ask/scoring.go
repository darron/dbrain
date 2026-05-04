package ask

import (
	"sort"
	"strings"
)

func rankCandidates(candidates []evidenceCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].Kind != candidates[j].Kind {
			return candidates[i].Kind == "source"
		}
		return candidates[i].SourceKey < candidates[j].SourceKey
	})
}

func scoreCandidate(candidate *evidenceCandidate, question string, terms []string) {
	info := explainEvidenceScore(question, terms, candidate.Evidence, candidate.MatchText)
	candidate.Score = info.Score
	candidate.Retrieval = &info
}

func addRetrievalSignal(candidate *evidenceCandidate, name string, detail string, weight int) {
	if weight == 0 {
		return
	}
	if candidate.Retrieval == nil {
		candidate.Retrieval = &RetrievalInfo{}
	}
	candidate.Score += weight
	candidate.Retrieval.Score = candidate.Score
	candidate.Retrieval.Signals = append(candidate.Retrieval.Signals, RetrievalSignal{
		Name:   name,
		Detail: detail,
		Weight: weight,
	})
}

func explainEvidenceScore(question string, terms []string, evidence Evidence, matchText string) RetrievalInfo {
	score := 0
	signals := make([]RetrievalSignal, 0, len(terms)+4)
	add := func(name string, weight int, detail string) {
		if weight == 0 {
			return
		}
		score += weight
		signals = append(signals, RetrievalSignal{Name: name, Detail: detail, Weight: weight})
	}

	title := strings.ToLower(evidence.Title)
	summary := strings.ToLower(evidence.Summary)
	excerpt := strings.ToLower(evidence.Excerpt)
	url := strings.ToLower(evidence.URL)
	tags := strings.ToLower(evidence.UserTags)
	author := strings.ToLower(evidence.Author)
	fullText := strings.ToLower(matchText)

	joined := strings.ToLower(strings.Join(terms, " "))
	if joined != "" && strings.Contains(title, joined) {
		add("exact_phrase_title", 15, joined)
	}
	if joined != "" && strings.Contains(tags, joined) {
		add("exact_phrase_user_tags", 12, joined)
	}

	queryTerms := uniqueTerms(terms)
	matchedTerms := make([]string, 0, len(queryTerms))
	missingTerms := make([]string, 0)
	for _, term := range queryTerms {
		switch {
		case strings.Contains(title, term):
			add("query_term_title", 12, term)
			matchedTerms = append(matchedTerms, term)
		case strings.Contains(tags, term):
			add("query_term_user_tags", 10, term)
			matchedTerms = append(matchedTerms, term)
		case strings.Contains(summary, term):
			add("query_term_summary", 8, term)
			matchedTerms = append(matchedTerms, term)
		case strings.Contains(author, term):
			add("query_term_author", 6, term)
			matchedTerms = append(matchedTerms, term)
		case strings.Contains(excerpt, term):
			add("query_term_excerpt", 5, term)
			matchedTerms = append(matchedTerms, term)
		case strings.Contains(url, term):
			add("query_term_url", 2, term)
			matchedTerms = append(matchedTerms, term)
		case strings.Contains(fullText, term):
			add("query_term_full_text", 4, term)
			matchedTerms = append(matchedTerms, term)
		default:
			missingTerms = append(missingTerms, term)
		}
	}
	if len(queryTerms) > 1 && len(matchedTerms) == len(queryTerms) {
		add("all_query_terms_matched", 12, strings.Join(matchedTerms, ", "))
	}
	if len(queryTerms) >= 3 && len(missingTerms) > 0 {
		add("missing_query_terms", -8*len(missingTerms), strings.Join(missingTerms, ", "))
	}
	if strings.TrimSpace(evidence.Summary) != "" {
		add("has_summary", 3, "")
	}
	if evidence.Kind == "source" {
		add("source_document", 1, "")
	}
	if matchesSourceTypes([]string{"github"}, evidence.SourceType) && strings.Contains(strings.ToLower(question), "repo") {
		add("github_repo_query", 2, "")
	}
	if len(evidence.EntityMatches) > 0 {
		add("entity_matches", minInt(len(evidence.EntityMatches)*3, 9), strings.Join(evidence.EntityMatches, ", "))
	}
	return RetrievalInfo{
		Score:        score,
		Signals:      signals,
		MatchedTerms: matchedTerms,
		MissingTerms: missingTerms,
	}
}

func matchesSourceTypes(filters []string, sourceType string) bool {
	if len(filters) == 0 {
		return true
	}
	sourceType = strings.TrimSpace(strings.ToLower(sourceType))
	family := sourceTypeFamily(sourceType)
	for _, filter := range filters {
		filter = strings.TrimSpace(strings.ToLower(filter))
		if filter == "" {
			continue
		}
		if filter == sourceType || filter == family {
			return true
		}
	}
	return false
}

func sourceTypeFamily(value string) string {
	if idx := strings.IndexByte(value, '_'); idx > 0 {
		return value[:idx]
	}
	return value
}
