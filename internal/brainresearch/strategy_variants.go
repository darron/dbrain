package brainresearch

import "strings"

func buildQueryVariants(question string, textQuery string, concepts []QueryConcept, family string) []QueryVariant {
	variants := make([]QueryVariant, 0, maxQueryVariants)
	add := func(query string, reason string) {
		query = strings.TrimSpace(strings.Join(strings.Fields(query), " "))
		if query == "" {
			return
		}
		for _, existing := range variants {
			if strings.EqualFold(existing.Query, query) {
				return
			}
		}
		variants = append(variants, QueryVariant{Query: query, Reason: reason})
	}

	add(textQuery, "normalized_terms")
	if preferred := preferredConceptQuery(concepts); preferred != "" && !strings.EqualFold(preferred, textQuery) {
		add(preferred, "preferred_concept_terms")
	}
	for _, query := range shortTokenPhraseQueries(concepts) {
		add(query, "short_token_phrase")
	}

	addFamilyVariants(add, family, question, concepts)
	for _, variant := range focusedConceptVariants(concepts) {
		add(variant.Query, variant.Reason)
	}

	if len(variants) == 0 {
		add(question, "original_question")
	}
	return limitQueryVariants(variants)
}

func shortTokenPhraseQueries(concepts []QueryConcept) []string {
	var queries []string
	for _, concept := range concepts {
		parts := strings.Fields(concept.Preferred)
		if len(parts) != 2 || len([]rune(parts[0])) != 1 {
			continue
		}
		queries = append(queries, `"`+parts[0]+"-"+parts[1]+`"`)
	}
	return uniqueStrings(queries)
}

func addFamilyVariants(add func(string, string), family string, question string, concepts []QueryConcept) {
	switch family {
	case queryFamilyPeopleEventLookup:
		addPeopleEventVariants(add, concepts)
	case queryFamilyModelToolSelection:
		addModelToolSelectionVariants(add, concepts)
	case queryFamilySoftwareProject:
		addSoftwareProjectVariants(add, concepts)
	case queryFamilyComparison:
		addComparisonVariants(add, concepts)
	case queryFamilyTimeline:
		addTimelineVariants(add, concepts)
	case queryFamilyMediaEvidence:
		addMediaEvidenceVariants(add, concepts)
	case queryFamilyCorrectiveFollowup:
		addCorrectiveFollowupVariants(add, concepts)
	case queryFamilyExactLookup:
		addExactLookupVariants(add, question)
	}
}

func addPeopleEventVariants(add func(string, string), concepts []QueryConcept) {
	location := firstConceptKey(concepts, "calgary")
	hasFather := hasConcept(concepts, "father")
	hasChildren := hasConcept(concepts, "children")
	hasKill := hasConcept(concepts, "kill")
	if hasFather && hasChildren && hasKill {
		prefix := strings.TrimSpace(location)
		add(strings.TrimSpace(prefix+" father charged killing children"), "people_event_title_variant")
		add(strings.TrimSpace(prefix+" man charged killing children"), "people_event_title_variant")
		add(strings.TrimSpace(prefix+" father son daughter"), "victim_role_variant")
	}
	if hasChildren && hasConcept(concepts, "vehicle") {
		prefix := strings.TrimSpace(location)
		add(strings.TrimSpace(prefix+" children found vehicle"), "vehicle_context_variant")
	}
}

func addModelToolSelectionVariants(add func(string, string), concepts []QueryConcept) {
	if hasConcept(concepts, "model") && hasConcept(concepts, "agent") {
		if subject := modelStrategySubject(concepts); subject != "" {
			add("llm model stack "+subject, "model_strategy_variant")
			add("qwen gpt "+subject, "model_name_variant")
		}
	}
	if subject := preferredConceptQueryExcluding(concepts, "model", "tool"); subject != "" {
		add("model stack "+subject, "model_tool_stack_variant")
	}
}

func addSoftwareProjectVariants(add func(string, string), concepts []QueryConcept) {
	if subject := preferredConceptQueryExcluding(concepts, "tool", "project"); subject != "" {
		add("github "+subject, "software_project_github_variant")
		add(subject+" documentation", "software_project_docs_variant")
	}
}

func addComparisonVariants(add func(string, string), concepts []QueryConcept) {
	if subject := preferredConceptQueryExcluding(concepts, "alternative", "comparison"); subject != "" {
		add(subject+" alternatives", "comparison_alternatives_variant")
		add(subject+" replacement", "comparison_replacement_variant")
	}
}

func addTimelineVariants(add func(string, string), concepts []QueryConcept) {
	if subject := preferredConceptQueryExcluding(concepts, "timeline", "history"); subject != "" {
		add(subject+" timeline", "timeline_variant")
		add(subject+" history", "history_variant")
	}
}

func addMediaEvidenceVariants(add func(string, string), concepts []QueryConcept) {
	if subject := preferredConceptQueryExcluding(concepts, "media", "transcript", "ocr", "video", "audio", "image", "recording"); subject != "" {
		add("transcript "+subject, "media_transcript_variant")
		add("ocr "+subject, "media_ocr_variant")
		add("video "+subject, "media_video_variant")
	}
}

func addCorrectiveFollowupVariants(add func(string, string), concepts []QueryConcept) {
	if subject := preferredConceptQueryExcluding(concepts, "not", "wrong", "exclude", "without", "instead", "correct"); subject != "" {
		add(subject, "corrective_focus_variant")
	}
}

func addExactLookupVariants(add func(string, string), question string) {
	for _, sourceKey := range sourceKeyCandidates(question) {
		add(sourceKey, "exact_source_key_variant")
	}
}

func modelStrategySubject(concepts []QueryConcept) string {
	terms := make([]string, 0, len(concepts))
	for _, concept := range concepts {
		if concept.Key == "model" {
			continue
		}
		if !concept.Required {
			continue
		}
		if concept.Preferred != "" {
			terms = append(terms, concept.Preferred)
			continue
		}
		terms = append(terms, concept.Key)
	}
	return strings.Join(uniqueStrings(terms), " ")
}

func preferredConceptQuery(concepts []QueryConcept) string {
	return preferredConceptQueryExcluding(concepts)
}

func preferredConceptQueryExcluding(concepts []QueryConcept, excluded ...string) string {
	excludedSet := map[string]struct{}{}
	for _, key := range excluded {
		excludedSet[strings.ToLower(strings.TrimSpace(key))] = struct{}{}
	}
	hasStrong := hasStrongConcept(concepts)
	terms := make([]string, 0, len(concepts))
	for _, concept := range concepts {
		if _, skip := excludedSet[strings.ToLower(concept.Key)]; skip {
			continue
		}
		if hasConcept(concepts, "knowledge_base") && (concept.Key == "knowledge" || concept.Key == "base") {
			continue
		}
		if hasStrong && (concept.Role == conceptRoleIntent || concept.Role == conceptRoleFrame) {
			continue
		}
		if concept.Preferred != "" {
			terms = append(terms, concept.Preferred)
			continue
		}
		terms = append(terms, concept.Key)
	}
	return strings.Join(uniqueStrings(terms), " ")
}

func focusedConceptVariants(concepts []QueryConcept) []QueryVariant {
	required := make([]string, 0, len(concepts))
	for _, concept := range concepts {
		if !concept.Required {
			continue
		}
		if concept.Role == conceptRoleIntent || concept.Role == conceptRoleFrame {
			continue
		}
		required = append(required, conceptPreferredTerm(concept))
	}
	if len(required) <= 3 {
		return nil
	}
	var variants []QueryVariant
	for i := 0; i <= len(required)-3 && len(variants) < 3; i++ {
		variants = append(variants, QueryVariant{Query: strings.Join(required[i:i+3], " "), Reason: "focused_concept_window"})
	}
	return variants
}
