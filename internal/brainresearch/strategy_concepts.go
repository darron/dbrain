package brainresearch

import "strings"

func buildQueryConcepts(terms []string) []QueryConcept {
	concepts := make([]QueryConcept, 0, len(terms))
	seen := map[string]struct{}{}
	for _, term := range terms {
		concept := conceptForTerm(term)
		if concept.Key == "" {
			continue
		}
		if _, ok := seen[concept.Key]; ok {
			continue
		}
		seen[concept.Key] = struct{}{}
		concepts = append(concepts, concept)
	}
	return concepts
}

func conceptForTerm(term string) QueryConcept {
	term = strings.TrimSpace(strings.ToLower(term))
	switch term {
	case "father", "dad", "parent", "man":
		return QueryConcept{Key: "father", Preferred: "father", Terms: []string{"father", "dad", "parent", "man"}, Required: true}
	case "mother", "mom", "woman":
		return QueryConcept{Key: "mother", Preferred: "mother", Terms: []string{"mother", "mom", "parent", "woman"}, Required: true}
	case "children", "child", "kid", "kids", "son", "daughter":
		return QueryConcept{Key: "children", Preferred: "children", Terms: []string{"children", "child", "kid", "kids", "son", "daughter"}, Required: true}
	case "kill", "killed", "killing", "murder", "murdered", "murdering", "death", "deaths":
		return QueryConcept{Key: "kill", Preferred: "killing", Terms: []string{"kill", "killed", "killing", "kills", "murder", "murdered", "murdering", "death", "deaths", "dead"}, Required: true}
	case "charge", "charged", "charges", "charging":
		return QueryConcept{Key: "charge", Preferred: "charged", Terms: []string{"charge", "charged", "charges", "charging"}, Required: true}
	case "vehicle", "suv", "car":
		return QueryConcept{Key: "vehicle", Preferred: "vehicle", Terms: []string{"vehicle", "suv", "car", "automobile"}, Required: true}
	case "two", "2":
		return QueryConcept{Key: "two", Preferred: "two", Terms: []string{"two", "2"}, Required: false}
	case "k8s", "kubernetes":
		return QueryConcept{Key: "kubernetes", Preferred: "kubernetes", Terms: []string{"kubernetes", "k8s"}, Required: true}
	case "alternative", "alternatives", "replacement", "replacements":
		return QueryConcept{Key: "alternative", Preferred: "alternative", Terms: []string{"alternative", "alternatives", "replacement", "replacements", "instead of"}, Required: true}
	case "model", "models", "llm", "llms":
		return QueryConcept{Key: "model", Preferred: "model", Terms: []string{"model", "models", "llm", "llms", "qwen", "gpt", "claude", "gemini", "minimax", "deepseek", "ollama", "openrouter"}, Required: true}
	default:
		if len([]rune(term)) < 3 {
			return QueryConcept{}
		}
		return QueryConcept{Key: term, Preferred: term, Terms: conceptTermAliases(term), Required: true}
	}
}

func conceptTermAliases(term string) []string {
	terms := []string{term}
	switch {
	case strings.HasSuffix(term, "ies") && len(term) > 4:
		terms = append(terms, strings.TrimSuffix(term, "ies")+"y")
	case strings.HasSuffix(term, "s") && len(term) > 4:
		terms = append(terms, strings.TrimSuffix(term, "s"))
	default:
		terms = append(terms, term+"s")
	}
	return uniqueStrings(terms)
}

func hasConcept(concepts []QueryConcept, key string) bool {
	return firstConceptKey(concepts, key) != ""
}

func firstConceptKey(concepts []QueryConcept, key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	for _, concept := range concepts {
		if concept.Key == key {
			return concept.Key
		}
	}
	return ""
}
