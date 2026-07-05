package brainresearch

import "strings"

const (
	conceptRoleAnchor  = "anchor"
	conceptRoleContent = "content"
	conceptRoleIntent  = "intent"
	conceptRoleFrame   = "frame"
)

func buildQueryConcepts(terms []string) []QueryConcept {
	return buildQueryConceptsWithAnchors(terms, nil)
}

func buildQueryConceptsWithAnchors(terms []string, anchors []ProtectedAnchor) []QueryConcept {
	concepts := make([]QueryConcept, 0, len(terms))
	seen := map[string]struct{}{}
	for _, concept := range conceptsForAnchors(anchors) {
		if concept.Key == "" {
			continue
		}
		if _, ok := seen[concept.Key]; ok {
			continue
		}
		seen[concept.Key] = struct{}{}
		concepts = append(concepts, concept)
	}
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
	return applyConceptRolePolicy(concepts)
}

func conceptsForAnchors(anchors []ProtectedAnchor) []QueryConcept {
	concepts := make([]QueryConcept, 0, len(anchors))
	for _, anchor := range anchors {
		anchor = normalizeProtectedAnchor(anchor)
		key := firstNonEmpty(anchor.Canonical, anchor.ResolvedID, strings.ToLower(strings.TrimPrefix(anchor.Raw, "@")))
		key = strings.TrimPrefix(key, "#")
		if key == "" {
			continue
		}
		preferred := firstNonEmpty(firstString(anchor.ExactTerms), anchor.Raw, anchor.Canonical)
		terms := uniqueStrings(append(append(append([]string{key, anchor.Raw, anchor.ResolvedID, preferred}, anchor.ExactTerms...), anchor.PhraseTerms...), anchor.ExpansionTerms...))
		concepts = append(concepts, QueryConcept{
			Key:       key,
			Preferred: preferred,
			Terms:     sanitizeConceptTerms(terms),
			Required:  true,
			Role:      conceptRoleAnchor,
		})
	}
	return concepts
}

func conceptForTerm(term string) QueryConcept {
	term = strings.TrimSpace(strings.ToLower(term))
	role := classifyConceptRole(term)
	if role == conceptRoleFrame {
		return QueryConcept{Key: term, Preferred: term, Terms: []string{term}, Required: false, Role: conceptRoleFrame}
	}
	required := role != conceptRoleIntent
	switch term {
	case "father", "dad", "parent", "man":
		return QueryConcept{Key: "father", Preferred: "father", Terms: []string{"father", "dad", "parent", "man"}, Required: true, Role: conceptRoleContent}
	case "mother", "mom", "woman":
		return QueryConcept{Key: "mother", Preferred: "mother", Terms: []string{"mother", "mom", "parent", "woman"}, Required: true, Role: conceptRoleContent}
	case "children", "child", "kid", "kids", "son", "daughter":
		return QueryConcept{Key: "children", Preferred: "children", Terms: []string{"children", "child", "kid", "kids", "son", "daughter"}, Required: true, Role: conceptRoleContent}
	case "kill", "killed", "killing", "murder", "murdered", "murdering", "death", "deaths":
		return QueryConcept{Key: "kill", Preferred: "killing", Terms: []string{"kill", "killed", "killing", "kills", "murder", "murdered", "murdering", "death", "deaths", "dead"}, Required: true, Role: conceptRoleContent}
	case "charge", "charged", "charges", "charging":
		return QueryConcept{Key: "charge", Preferred: "charged", Terms: []string{"charge", "charged", "charges", "charging"}, Required: true, Role: conceptRoleContent}
	case "vehicle", "suv", "car":
		return QueryConcept{Key: "vehicle", Preferred: "vehicle", Terms: []string{"vehicle", "suv", "car", "automobile"}, Required: true, Role: conceptRoleContent}
	case "two", "2":
		return QueryConcept{Key: "two", Preferred: "two", Terms: []string{"two", "2"}, Required: false, Role: conceptRoleContent}
	case "k8s", "kubernetes":
		return QueryConcept{Key: "kubernetes", Preferred: "kubernetes", Terms: []string{"kubernetes", "k8s"}, Required: true, Role: conceptRoleContent}
	case "alternative", "alternatives", "replacement", "replacements":
		return QueryConcept{Key: "alternative", Preferred: "alternative", Terms: []string{"alternative", "alternatives", "replacement", "replacements", "instead of"}, Required: true, Role: conceptRoleContent}
	case "model", "models", "llm", "llms":
		return QueryConcept{Key: "model", Preferred: "model", Terms: []string{"model", "models", "llm", "llms", "qwen", "gpt", "claude", "gemini", "minimax", "deepseek", "ollama", "openrouter"}, Required: true, Role: conceptRoleContent}
	case "software", "app", "apps", "application", "applications", "program", "programs":
		return QueryConcept{Key: "software", Preferred: "software", Terms: []string{"software", "app", "apps", "application", "applications", "tool", "tools", "utility", "utilities", "program", "programs"}, Required: true, Role: conceptRoleContent}
	case "tool", "tools":
		return QueryConcept{Key: "tool", Preferred: "tool", Terms: []string{"tool", "tools", "utility", "utilities"}, Required: true, Role: conceptRoleContent}
	case "project", "projects", "package", "packages", "library", "libraries":
		return QueryConcept{Key: "project", Preferred: "project", Terms: []string{"project", "projects", "package", "packages", "library", "libraries", "repo", "repository"}, Required: true, Role: conceptRoleContent}
	case "compare", "comparison", "versus", "vs":
		return QueryConcept{Key: "comparison", Preferred: "comparison", Terms: []string{"compare", "comparison", "versus", "vs"}, Required: false, Role: conceptRoleContent}
	case "timeline", "history", "historical", "evolution":
		return QueryConcept{Key: "timeline", Preferred: "timeline", Terms: []string{"timeline", "history", "historical", "evolution"}, Required: false, Role: conceptRoleContent}
	case "transcript", "transcripts":
		return QueryConcept{Key: "transcript", Preferred: "transcript", Terms: []string{"transcript", "transcripts", "transcription"}, Required: true, Role: conceptRoleContent}
	case "ocr", "screenshot", "screenshots", "image", "images", "photo", "photos":
		return QueryConcept{Key: "ocr", Preferred: "ocr", Terms: []string{"ocr", "screenshot", "screenshots", "image", "images", "photo", "photos", "vision"}, Required: true, Role: conceptRoleContent}
	case "video", "videos", "audio", "recording", "recordings", "recorder", "recorders":
		return QueryConcept{Key: "video", Preferred: "video", Terms: []string{"video", "videos", "audio", "record", "records", "recording", "recordings", "recorder", "recorders", "clip"}, Required: true, Role: conceptRoleContent}
	case "mac", "macs", "macos", "macbook", "macbooks", "osx":
		return QueryConcept{Key: "mac", Preferred: "mac", Terms: []string{"mac", "macs", "macos", "mac os", "macbook", "macbooks", "osx", "os x"}, Required: true, Role: conceptRoleContent}
	case "not", "wrong", "exclude", "without", "instead", "correct", "correction":
		return QueryConcept{Key: term, Preferred: term, Terms: []string{term}, Required: false, Role: conceptRoleContent}
	default:
		if len([]rune(term)) < 3 {
			return QueryConcept{}
		}
		return QueryConcept{Key: term, Preferred: term, Terms: conceptTermAliases(term), Required: required, Role: role}
	}
}

func classifyConceptRole(term string) string {
	switch strings.ToLower(strings.TrimSpace(term)) {
	case "synthesize", "synthesis", "summarize", "summary", "overview", "analysis", "analyze", "explain", "answer", "brief", "themes", "theme", "recap":
		return conceptRoleIntent
	case "dbrain", "brain", "current", "question", "please", "can", "you", "come", "up", "they", "their", "theyre", "re", "there", "in", "prior", "evidence", "titles", "recent", "user", "users", "focus", "research", "favored", "should", "what", "about", "with", "from", "into", "my", "the", "are", "is", "do", "does", "did", "have", "has", "this", "that", "these", "those", "find", "notes":
		return conceptRoleFrame
	default:
		return conceptRoleContent
	}
}

func applyConceptRolePolicy(concepts []QueryConcept) []QueryConcept {
	concepts = dedupeConceptsPreservingOrder(concepts)
	hasStrong := hasStrongConcept(concepts)
	out := make([]QueryConcept, 0, len(concepts))
	for _, concept := range concepts {
		if concept.Role == "" {
			concept.Role = classifyConceptRole(concept.Key)
		}
		if concept.Role == conceptRoleFrame {
			concept.Required = false
		}
		if concept.Role == conceptRoleIntent && hasStrong {
			concept.Required = false
		}
		if concept.Role == conceptRoleAnchor {
			concept.Required = true
		}
		out = append(out, concept)
	}
	return out
}

func hasStrongConcept(concepts []QueryConcept) bool {
	for _, concept := range concepts {
		role := concept.Role
		if role == "" {
			role = classifyConceptRole(concept.Key)
		}
		if role == conceptRoleAnchor || role == conceptRoleContent {
			return true
		}
	}
	return false
}

func dedupeConceptsPreservingOrder(concepts []QueryConcept) []QueryConcept {
	out := make([]QueryConcept, 0, len(concepts))
	seen := map[string]int{}
	for _, concept := range concepts {
		if concept.Key == "" {
			continue
		}
		if pos, exists := seen[concept.Key]; exists {
			out[pos] = mergeQueryConcept(out[pos], concept)
			continue
		}
		seen[concept.Key] = len(out)
		out = append(out, concept)
	}
	return out
}

func conceptPreferredTerm(concept QueryConcept) string {
	if concept.Preferred != "" {
		return concept.Preferred
	}
	return concept.Key
}

func firstString(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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
