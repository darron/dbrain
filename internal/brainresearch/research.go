package brainresearch

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/queryterms"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/topics"
)

const (
	SchemaVersion       = "research_pack.v1"
	maxExactTagEvidence = 3
	maxQueryVariants    = 8
)

type Builder struct {
	cfg config.Config
	st  *store.Store
}

type Options struct {
	Question        string
	Topic           string
	Limit           int
	SourceTypes     []string
	IncludeRelated  bool
	RelatedLimit    int
	SeedLimit       int
	IncludeTopic    *bool
	MaxCharsPerDoc  int
	PlannerModel    string
	PlannerTimeout  time.Duration
	PlannerBinary   string
	UseModelPlanner bool
	DisablePlanner  bool
}

type Pack struct {
	SchemaVersion    string            `json:"schema_version"`
	Question         string            `json:"question"`
	Mode             string            `json:"mode"`
	QueryPlan        QueryPlan         `json:"query_plan"`
	Coverage         Coverage          `json:"coverage"`
	Topic            string            `json:"topic,omitempty"`
	UsedTopicBrief   bool              `json:"used_topic_brief"`
	Evidence         []ask.Evidence    `json:"evidence"`
	ExactTagEvidence []ask.Evidence    `json:"exact_tag_evidence,omitempty"`
	TopicBrief       *TopicBrief       `json:"topic_brief,omitempty"`
	NextSteps        []SuggestedAction `json:"next_steps,omitempty"`
}

type QueryPlan struct {
	TextQuery         string         `json:"text_query"`
	QueryTerms        []string       `json:"query_terms"`
	TagQueries        []string       `json:"tag_queries"`
	QueryVariants     []QueryVariant `json:"query_variants,omitempty"`
	Concepts          []QueryConcept `json:"concepts,omitempty"`
	Planner           string         `json:"planner,omitempty"`
	PlannerModel      string         `json:"planner_model,omitempty"`
	PlannerError      string         `json:"planner_error,omitempty"`
	SourceTypes       []string       `json:"source_types,omitempty"`
	Limit             int            `json:"limit"`
	MaxCharsPerDoc    int            `json:"max_chars_per_doc"`
	IncludeRelated    bool           `json:"include_related"`
	RelatedLimit      int            `json:"related_limit,omitempty"`
	Topic             string         `json:"topic,omitempty"`
	TopicSource       string         `json:"topic_source,omitempty"`
	IncludeTopicBrief bool           `json:"include_topic_brief"`
}

type QueryVariant struct {
	Query  string `json:"query"`
	Reason string `json:"reason,omitempty"`
}

type QueryConcept struct {
	Key       string   `json:"key"`
	Preferred string   `json:"preferred,omitempty"`
	Terms     []string `json:"terms"`
	Required  bool     `json:"required"`
}

type Coverage struct {
	EvidenceCount     int      `json:"evidence_count"`
	ByKind            []Bucket `json:"by_kind"`
	BySourceType      []Bucket `json:"by_source_type"`
	TopUserTags       []Bucket `json:"top_user_tags,omitempty"`
	ExactTagMatches   []Bucket `json:"exact_tag_matches,omitempty"`
	ItemTextMatches   int      `json:"item_text_matches,omitempty"`
	SourceTextMatches int      `json:"source_text_matches,omitempty"`
	TopicNodeCount    int      `json:"topic_node_count,omitempty"`
	TopicEdgeCount    int      `json:"topic_edge_count,omitempty"`
	DisplayedLimit    int      `json:"displayed_limit"`
	RelatedLimit      int      `json:"related_limit,omitempty"`
	RecallNote        string   `json:"recall_note,omitempty"`
}

type Bucket struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type TopicBrief struct {
	Topic        string                `json:"topic"`
	SourceTypes  []string              `json:"source_types,omitempty"`
	SeedLimit    int                   `json:"seed_limit"`
	RelatedLimit int                   `json:"related_limit"`
	Summary      string                `json:"summary"`
	Pivots       topics.TopicPivots    `json:"pivots,omitempty"`
	Entities     []topics.TopicEntity  `json:"entities,omitempty"`
	Nodes        []topics.TopicMapNode `json:"nodes"`
	Edges        []topics.TopicMapEdge `json:"edges"`
}

type SuggestedAction struct {
	Action string                 `json:"action"`
	Label  string                 `json:"label"`
	Reason string                 `json:"reason,omitempty"`
	Params map[string]interface{} `json:"params,omitempty"`
}

func New(cfg config.Config, st *store.Store) *Builder {
	return &Builder{cfg: cfg, st: st}
}

func Build(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Pack, error) {
	return New(cfg, st).Build(ctx, opts)
}

func (b *Builder) Build(ctx context.Context, opts Options) (Pack, error) {
	question := strings.TrimSpace(opts.Question)
	if question == "" {
		return Pack{}, fmt.Errorf("question is required")
	}

	hints := ask.Hints(question)
	limit := defaultInt(opts.Limit, 8)
	maxChars := defaultInt(opts.MaxCharsPerDoc, 700)
	topic, topicSource, hasTopic := resolveTopic(question, opts.Topic)
	includeTopic := hasTopic
	if opts.IncludeTopic != nil {
		includeTopic = *opts.IncludeTopic
	}
	if includeTopic && !hasTopic {
		topic = normalizeTopicPhrase(question)
		if topic != "" {
			topicSource = "normalized_question"
			hasTopic = true
		}
	}
	if !hasTopic {
		includeTopic = false
	}

	strategy := b.buildResearchStrategy(ctx, question, hints, opts)
	evidence, err := b.collectStrategyEvidence(ctx, strategy, opts, limit, maxChars)
	if err != nil {
		return Pack{}, err
	}

	corpusCoverage, err := b.buildCorpusCoverage(ctx, topic, hints, opts.SourceTypes, limit, opts.RelatedLimit)
	if err != nil {
		return Pack{}, err
	}
	exactTagEvidence, err := b.buildExactTagEvidence(ctx, topic, hints, opts.SourceTypes, maxChars)
	if err != nil {
		return Pack{}, err
	}

	pack := Pack{
		SchemaVersion: SchemaVersion,
		Question:      question,
		Mode:          "evidence_only",
		QueryPlan: QueryPlan{
			TextQuery:         hints.TextQuery,
			QueryTerms:        hints.Terms,
			TagQueries:        hints.TagQueries,
			QueryVariants:     strategy.Variants,
			Concepts:          strategy.Concepts,
			Planner:           strategy.Planner,
			PlannerModel:      strategy.PlannerModel,
			PlannerError:      strategy.PlannerError,
			SourceTypes:       opts.SourceTypes,
			Limit:             limit,
			MaxCharsPerDoc:    maxChars,
			IncludeRelated:    opts.IncludeRelated,
			RelatedLimit:      opts.RelatedLimit,
			Topic:             topic,
			TopicSource:       topicSource,
			IncludeTopicBrief: includeTopic,
		},
		Evidence:         evidence,
		ExactTagEvidence: exactTagEvidence,
		Coverage:         mergeCoverage(buildCoverage(evidence), corpusCoverage),
		NextSteps:        buildNextSteps(evidence, hints.TextQuery),
	}

	if includeTopic {
		graph, err := topics.Build(ctx, b.st, topic, topics.Options{
			SourceTypes:  opts.SourceTypes,
			SeedLimit:    opts.SeedLimit,
			RelatedLimit: defaultInt(opts.RelatedLimit, 2),
		})
		if err != nil {
			return Pack{}, err
		}
		pack.Mode = "topic_brief_and_evidence"
		pack.Topic = topic
		pack.UsedTopicBrief = true
		pack.TopicBrief = &TopicBrief{
			Topic:        graph.Topic,
			SourceTypes:  graph.SourceTypes,
			SeedLimit:    graph.SeedLimit,
			RelatedLimit: graph.RelatedLimit,
			Summary:      topics.SummaryText(graph),
			Pivots:       graph.Pivots,
			Entities:     graph.Entities,
			Nodes:        graph.Nodes,
			Edges:        graph.Edges,
		}
		pack.Coverage.TopicNodeCount = len(graph.Nodes)
		pack.Coverage.TopicEdgeCount = len(graph.Edges)
	}
	pack.Coverage.RecallNote = recallNote(pack.Coverage)

	return pack, nil
}

type researchStrategy struct {
	Variants     []QueryVariant
	Concepts     []QueryConcept
	Planner      string
	PlannerModel string
	PlannerError string
}

func buildResearchStrategy(question string, hints ask.QueryHints) researchStrategy {
	return buildDeterministicResearchStrategy(question, hints)
}

func (b *Builder) buildResearchStrategy(ctx context.Context, question string, hints ask.QueryHints, opts Options) researchStrategy {
	strategy := buildDeterministicResearchStrategy(question, hints)
	strategy.Planner = "deterministic"
	if opts.DisablePlanner {
		return strategy
	}
	if !opts.UseModelPlanner && strings.TrimSpace(opts.PlannerModel) == "" {
		return strategy
	}

	planned, modelName, err := b.buildModelResearchPlan(ctx, question, hints, strategy, opts)
	if modelName != "" {
		strategy.PlannerModel = modelName
	}
	if err != nil {
		strategy.PlannerError = err.Error()
		return strategy
	}
	if planned.Empty() {
		return strategy
	}

	strategy.Planner = "model_assisted"
	strategy.Concepts = mergeQueryConcepts(strategy.Concepts, planned.Concepts)
	strategy.Variants = mergeQueryVariants(strategy.Variants, planned.QueryVariants)
	if preferred := preferredConceptQuery(strategy.Concepts); preferred != "" {
		strategy.Variants = mergeQueryVariants(strategy.Variants, []QueryVariant{{Query: preferred, Reason: "model_concept_terms"}})
	}
	strategy.Variants = limitQueryVariants(strategy.Variants)
	return strategy
}

func buildDeterministicResearchStrategy(question string, hints ask.QueryHints) researchStrategy {
	terms := uniqueStrings(hints.Terms)
	concepts := buildQueryConcepts(terms)
	variants := buildQueryVariants(question, hints.TextQuery, terms, concepts)
	return researchStrategy{Variants: variants, Concepts: concepts, Planner: "deterministic"}
}

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
	default:
		if len([]rune(term)) < 3 {
			return QueryConcept{}
		}
		return QueryConcept{Key: term, Preferred: term, Terms: conceptTermAliases(term), Required: true}
	}
}

func buildQueryVariants(question string, textQuery string, terms []string, concepts []QueryConcept) []QueryVariant {
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
	for _, variant := range focusedConceptVariants(concepts) {
		add(variant.Query, variant.Reason)
	}

	if len(variants) == 0 {
		add(question, "original_question")
	}
	return limitQueryVariants(variants)
}

func preferredConceptQuery(concepts []QueryConcept) string {
	terms := make([]string, 0, len(concepts))
	for _, concept := range concepts {
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
		if concept.Preferred != "" {
			required = append(required, concept.Preferred)
			continue
		}
		required = append(required, concept.Key)
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

func (b *Builder) collectStrategyEvidence(ctx context.Context, strategy researchStrategy, opts Options, limit int, maxChars int) ([]ask.Evidence, error) {
	variants := strategy.Variants
	if len(variants) == 0 {
		variants = []QueryVariant{{Query: opts.Question, Reason: "original_question"}}
	}

	type scoredEvidence struct {
		doc   ask.Evidence
		score int
		order int
	}
	seen := map[string]scoredEvidence{}
	order := 0
	perVariantLimit := max(limit, 8)
	entityIndex, err := entities.BuildIndex(ctx, b.st)
	if err != nil {
		return nil, err
	}
	for _, variant := range variants {
		resp, err := ask.Run(ctx, b.cfg, b.st, variant.Query, ask.Options{
			Limit:               perVariantLimit,
			RetrieveOnly:        true,
			SourceTypes:         opts.SourceTypes,
			IncludeRelated:      opts.IncludeRelated,
			RelatedLimit:        opts.RelatedLimit,
			MaxCharsPerDoc:      maxChars,
			SearchLimit:         max(perVariantLimit*2, 12),
			EntityIndex:         entityIndex,
			DisableTagExpansion: true,
		})
		if err != nil {
			return nil, err
		}
		for rank, doc := range resp.Evidence {
			if strings.TrimSpace(doc.SourceKey) == "" {
				continue
			}
			scored := scoreEvidenceWithResearchStrategy(doc, strategy, variant, rank)
			current, exists := seen[doc.SourceKey]
			if !exists || scored.score > current.score {
				scored.order = order
				if exists {
					scored.order = current.order
				} else {
					order++
				}
				seen[doc.SourceKey] = scored
			}
		}
	}

	ordered := make([]scoredEvidence, 0, len(seen))
	for _, doc := range seen {
		ordered = append(ordered, doc)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].score != ordered[j].score {
			return ordered[i].score > ordered[j].score
		}
		return ordered[i].order < ordered[j].order
	})
	if len(ordered) > limit {
		ordered = ordered[:limit]
	}
	out := make([]ask.Evidence, 0, len(ordered))
	for _, scored := range ordered {
		out = append(out, scored.doc)
	}
	return out, nil
}

func scoreEvidenceWithResearchStrategy(doc ask.Evidence, strategy researchStrategy, variant QueryVariant, rank int) struct {
	doc   ask.Evidence
	score int
	order int
} {
	if doc.Retrieval == nil {
		doc.Retrieval = &ask.RetrievalInfo{}
	}
	score := doc.Retrieval.Score
	score += max(0, 8-rank)
	appendResearchSignal(doc.Retrieval, "research_query_variant", variant.Query, max(0, 2-rank))

	text := researchEvidenceText(doc)
	matched := make([]string, 0, len(strategy.Concepts))
	missing := make([]string, 0, len(strategy.Concepts))
	requiredTotal := 0
	requiredMatched := 0
	for _, concept := range strategy.Concepts {
		matches := conceptMatchesText(concept, text)
		if concept.Required {
			requiredTotal++
			if matches {
				requiredMatched++
				matched = append(matched, concept.Key)
				continue
			}
			missing = append(missing, concept.Key)
			continue
		}
		if matches {
			matched = append(matched, concept.Key)
		}
	}

	if len(matched) > 0 {
		weight := requiredMatched*14 + (len(matched)-requiredMatched)*4
		score += weight
		appendResearchSignal(doc.Retrieval, "research_concepts_matched", strings.Join(matched, ", "), weight)
	}
	if requiredTotal > 1 && len(missing) > 0 {
		weight := -12 * len(missing)
		score += weight
		appendResearchSignal(doc.Retrieval, "research_concepts_missing", strings.Join(missing, ", "), weight)
	}
	if requiredTotal > 1 && requiredMatched == requiredTotal {
		score += 24
		appendResearchSignal(doc.Retrieval, "all_required_research_concepts_matched", strings.Join(matched, ", "), 24)
	}
	doc.Retrieval.Score = score
	return struct {
		doc   ask.Evidence
		score int
		order int
	}{doc: doc, score: score}
}

func appendResearchSignal(info *ask.RetrievalInfo, name string, detail string, weight int) {
	if info == nil || strings.TrimSpace(name) == "" {
		return
	}
	info.Signals = append(info.Signals, ask.RetrievalSignal{
		Name:   name,
		Detail: strings.TrimSpace(detail),
		Weight: weight,
	})
}

func researchEvidenceText(doc ask.Evidence) string {
	return strings.ToLower(strings.Join([]string{
		doc.SourceKey,
		doc.Title,
		doc.URL,
		doc.NotePath,
		doc.Summary,
		doc.Excerpt,
		doc.Author,
		doc.SourceType,
		doc.UserTags,
		strings.Join(doc.EntityMatches, " "),
	}, "\n"))
}

func conceptMatchesText(concept QueryConcept, text string) bool {
	for _, term := range concept.Terms {
		if containsTerm(text, term) {
			return true
		}
	}
	return false
}

func containsTerm(text string, term string) bool {
	text = strings.ToLower(text)
	term = strings.ToLower(strings.TrimSpace(term))
	if text == "" || term == "" {
		return false
	}
	if strings.Contains(term, " ") {
		return strings.Contains(text, term)
	}
	start := 0
	for {
		idx := strings.Index(text[start:], term)
		if idx < 0 {
			return false
		}
		idx += start
		beforeOK := idx == 0 || !isAlphaNumeric(rune(text[idx-1]))
		after := idx + len(term)
		afterOK := after >= len(text) || !isAlphaNumeric(rune(text[after]))
		if beforeOK && afterOK {
			return true
		}
		start = idx + len(term)
		if start >= len(text) {
			return false
		}
	}
}

func isAlphaNumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

func resolveTopic(question string, explicitTopic string) (string, string, bool) {
	if topic := normalizeTopicPhrase(explicitTopic); topic != "" {
		return topic, "explicit", true
	}
	if topic, ok := inferTopic(question); ok {
		return topic, "inferred", true
	}
	return "", "", false
}

func inferTopic(question string) (string, bool) {
	q := strings.TrimSpace(question)
	if q == "" {
		return "", false
	}
	lower := strings.ToLower(q)
	switch {
	case hasAnyPrefix(lower,
		"what do i have in my brain about ",
		"what do we have in my brain about ",
		"what do i have saved about ",
		"what do we have saved about ",
		"what do i have about ",
		"what do we have about ",
		"what do i know about ",
		"what do we know about ",
		"what do you know about ",
		"what is in my brain about ",
		"what's in my brain about ",
		"what does my brain know about ",
		"what does dbrain know about ",
		"what does the brain know about ",
		"ask my brain about ",
		"use my brain to research ",
		"use my brain for ",
		"research "):
		return normalizeTopicPhrase(trimQuestionPrefix(lower,
			"what do i have in my brain about ",
			"what do we have in my brain about ",
			"what do i have saved about ",
			"what do we have saved about ",
			"what do i have about ",
			"what do we have about ",
			"what do i know about ",
			"what do we know about ",
			"what do you know about ",
			"what is in my brain about ",
			"what's in my brain about ",
			"what does my brain know about ",
			"what does dbrain know about ",
			"what does the brain know about ",
			"ask my brain about ",
			"use my brain to research ",
			"use my brain for ",
			"research ")), true
	case strings.HasPrefix(lower, "what is "),
		strings.HasPrefix(lower, "what are "),
		strings.HasPrefix(lower, "explain "),
		strings.HasPrefix(lower, "tell me about "),
		strings.HasPrefix(lower, "overview of "),
		strings.HasPrefix(lower, "give me an overview of "):
		return normalizeTopicPhrase(trimQuestionPrefix(lower,
			"what is ", "what are ", "explain ", "tell me about ", "overview of ", "give me an overview of ")), true
	case (strings.HasPrefix(lower, "show me ") || strings.HasPrefix(lower, "find me ")) &&
		(strings.Contains(lower, " about ") || strings.Contains(lower, " for ")):
		if topic := topicAfterPreposition(lower); topic != "" {
			return topic, true
		}
	case strings.HasSuffix(lower, "?") && (strings.HasPrefix(lower, "how ") || strings.HasPrefix(lower, "why ")):
		terms := queryterms.Terms(lower)
		if len(terms) >= 2 {
			return normalizeTopicPhrase(strings.Join(terms[:min(len(terms), 4)], " ")), true
		}
	}
	return "", false
}

func hasAnyPrefix(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func trimQuestionPrefix(value string, prefixes ...string) string {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimSpace(value[len(prefix):])
		}
	}
	return strings.TrimSpace(value)
}

func topicAfterPreposition(value string) string {
	for _, marker := range []string{" about ", " for "} {
		if idx := strings.Index(value, marker); idx >= 0 {
			return normalizeTopicPhrase(value[idx+len(marker):])
		}
	}
	return ""
}

func normalizeTopicPhrase(value string) string {
	value = strings.TrimSpace(strings.TrimRight(value, "?.!"))
	terms := queryterms.Terms(value)
	if len(terms) == 0 {
		return ""
	}
	if len(terms) > 5 {
		terms = terms[:5]
	}
	return strings.Join(terms, " ")
}

func buildCoverage(evidence []ask.Evidence) Coverage {
	return Coverage{
		EvidenceCount: len(evidence),
		ByKind:        countValues(evidence, func(doc ask.Evidence) string { return doc.Kind }, 10),
		BySourceType:  countValues(evidence, func(doc ask.Evidence) string { return doc.SourceType }, 10),
		TopUserTags:   topTags(evidence, 12),
	}
}

func (b *Builder) buildCorpusCoverage(ctx context.Context, topic string, hints ask.QueryHints, sourceTypes []string, limit int, relatedLimit int) (Coverage, error) {
	coverage := Coverage{
		DisplayedLimit: limit,
		RelatedLimit:   relatedLimit,
	}
	tagQueries := append([]string(nil), hints.TagQueries...)
	if topicAlias := tagAlias(topic); topicAlias != "" {
		tagQueries = append(tagQueries, topicAlias)
	}
	tagQueries = uniqueStrings(tagQueries)
	for _, tagQuery := range tagQueries {
		count, err := b.st.CountExactUserTag(ctx, tagQuery, sourceTypes)
		if err != nil {
			return Coverage{}, err
		}
		if count > 0 {
			coverage.ExactTagMatches = append(coverage.ExactTagMatches, Bucket{Key: tagQuery, Count: count})
		}
	}
	sort.Slice(coverage.ExactTagMatches, func(i, j int) bool {
		if coverage.ExactTagMatches[i].Count != coverage.ExactTagMatches[j].Count {
			return coverage.ExactTagMatches[i].Count > coverage.ExactTagMatches[j].Count
		}
		return coverage.ExactTagMatches[i].Key < coverage.ExactTagMatches[j].Key
	})

	textQuery := strings.TrimSpace(topic)
	if textQuery == "" {
		textQuery = strings.TrimSpace(hints.TextQuery)
	}
	if textQuery != "" {
		itemCount, err := b.st.CountItemTextMatches(ctx, textQuery, sourceTypes)
		if err != nil {
			return Coverage{}, err
		}
		sourceCount, err := b.st.CountSourceTextMatches(ctx, textQuery, sourceTypes)
		if err != nil {
			return Coverage{}, err
		}
		coverage.ItemTextMatches = itemCount
		coverage.SourceTextMatches = sourceCount
	}
	return coverage, nil
}

func mergeCoverage(base Coverage, corpus Coverage) Coverage {
	base.ExactTagMatches = corpus.ExactTagMatches
	base.ItemTextMatches = corpus.ItemTextMatches
	base.SourceTextMatches = corpus.SourceTextMatches
	base.TopicNodeCount = corpus.TopicNodeCount
	base.TopicEdgeCount = corpus.TopicEdgeCount
	base.DisplayedLimit = corpus.DisplayedLimit
	base.RelatedLimit = corpus.RelatedLimit
	base.RecallNote = corpus.RecallNote
	return base
}

func (b *Builder) buildExactTagEvidence(ctx context.Context, topic string, hints ask.QueryHints, sourceTypes []string, maxChars int) ([]ask.Evidence, error) {
	tagQueries := append([]string(nil), hints.TagQueries...)
	if topicAlias := tagAlias(topic); topicAlias != "" {
		tagQueries = append(tagQueries, topicAlias)
	}
	tagQueries = uniqueStrings(tagQueries)
	if len(tagQueries) == 0 {
		return nil, nil
	}

	examples := make([]ask.Evidence, 0, maxExactTagEvidence)
	seen := map[string]struct{}{}
	for _, tagQuery := range tagQueries {
		results, err := b.st.SearchExactUserTag(ctx, tagQuery, maxExactTagEvidence*3)
		if err != nil {
			return nil, err
		}
		results = filterSearchResults(ctx, b.st, results, sourceTypes)
		for _, result := range results {
			if len(examples) >= maxExactTagEvidence {
				return examples, nil
			}
			if _, exists := seen[result.SourceKey]; exists {
				continue
			}
			if item, err := b.st.GetItem(ctx, result.SourceKey); err == nil {
				seen[result.SourceKey] = struct{}{}
				examples = append(examples, exactTagEvidenceFromItem(b.cfg.VaultDir, item, result, tagQuery, maxChars, hints.Terms))
				continue
			}
			source, err := b.st.GetSource(ctx, result.SourceKey)
			if err != nil {
				continue
			}
			seen[result.SourceKey] = struct{}{}
			examples = append(examples, exactTagEvidenceFromSource(b.cfg.VaultDir, source, result, tagQuery, maxChars, hints.Terms))
		}
	}
	return examples, nil
}

func exactTagEvidenceFromItem(vaultDir string, item model.Item, result model.SearchResult, tag string, maxChars int, terms []string) ask.Evidence {
	author := strings.TrimSpace(item.AuthorName)
	if handle := strings.TrimSpace(item.AuthorHandle); handle != "" {
		if author != "" {
			author += " "
		}
		author += "@" + strings.TrimPrefix(handle, "@")
	}
	excerpt := trimText(firstNonEmpty(
		item.SummaryText,
		item.XPostText,
		item.ArticleText,
		item.Text,
		item.OCRText,
		result.Snippet,
	), maxChars)
	matchText := strings.ToLower(strings.Join([]string{
		item.Title,
		item.CanonicalURL,
		item.AuthorName,
		item.AuthorHandle,
		item.UserTags,
		item.SummaryText,
		item.XPostText,
		item.ArticleText,
		item.Text,
		item.OCRText,
	}, "\n"))
	retrieval := exactTagExampleRetrieval(tag, terms, matchText)
	return ask.Evidence{
		SourceKey:   item.SourceKey,
		Kind:        "item",
		Title:       item.Title,
		URL:         item.CanonicalURL,
		NotePath:    filepath.Join(vaultDir, filepath.FromSlash(item.NotePath)),
		Summary:     trimText(item.SummaryText, maxChars),
		Excerpt:     excerpt,
		Author:      author,
		SourceType:  item.SourceType,
		PublishedAt: item.PublishedAt,
		UserTags:    item.UserTags,
		Retrieval:   &retrieval,
	}
}

func exactTagEvidenceFromSource(vaultDir string, source model.SourceDocument, result model.SearchResult, tag string, maxChars int, terms []string) ask.Evidence {
	excerpt := trimText(firstNonEmpty(
		source.SummaryText,
		source.ExtractedText,
		source.Description,
		result.Snippet,
	), maxChars)
	matchText := strings.ToLower(strings.Join([]string{
		source.Title,
		source.CanonicalURL,
		source.Description,
		source.SiteName,
		source.UserTags,
		source.SummaryText,
		source.ExtractedText,
	}, "\n"))
	retrieval := exactTagExampleRetrieval(tag, terms, matchText)
	return ask.Evidence{
		SourceKey:    source.SourceKey,
		Kind:         "source",
		Title:        firstNonEmpty(source.Title, source.CanonicalURL),
		URL:          source.CanonicalURL,
		NotePath:     filepath.Join(vaultDir, filepath.FromSlash(source.NotePath)),
		Summary:      trimText(source.SummaryText, maxChars),
		Excerpt:      excerpt,
		SourceType:   source.SourceType,
		ExtractedAt:  formatTime(source.ExtractedAt),
		SummarizedAt: formatTime(source.SummarizedAt),
		UserTags:     source.UserTags,
		Retrieval:    &retrieval,
	}
}

func exactTagExampleRetrieval(tag string, terms []string, matchText string) ask.RetrievalInfo {
	info := ask.RetrievalInfo{
		Score: 12,
		Signals: []ask.RetrievalSignal{{
			Name:   "exact_user_tag_example",
			Detail: tag,
			Weight: 12,
		}},
	}
	for _, term := range uniqueStrings(terms) {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		if strings.Contains(matchText, term) {
			info.MatchedTerms = append(info.MatchedTerms, term)
		} else {
			info.MissingTerms = append(info.MissingTerms, term)
		}
	}
	if len(info.MatchedTerms) > 0 {
		info.Score += 2 * len(info.MatchedTerms)
		info.Signals = append(info.Signals, ask.RetrievalSignal{
			Name:   "exact_tag_example_matched_terms",
			Detail: strings.Join(info.MatchedTerms, ", "),
			Weight: 2 * len(info.MatchedTerms),
		})
	}
	return info
}

func recallNote(coverage Coverage) string {
	var parts []string
	if len(coverage.ExactTagMatches) > 0 {
		total := 0
		labels := make([]string, 0, len(coverage.ExactTagMatches))
		for _, bucket := range coverage.ExactTagMatches {
			total += bucket.Count
			labels = append(labels, fmt.Sprintf("%s=%d", bucket.Key, bucket.Count))
		}
		parts = append(parts, fmt.Sprintf("exact user-tag matches: %s (sum=%d)", strings.Join(labels, ", "), total))
	}
	if coverage.ItemTextMatches > 0 || coverage.SourceTextMatches > 0 {
		parts = append(parts, fmt.Sprintf("phrase/text matches: items=%d sources=%d", coverage.ItemTextMatches, coverage.SourceTextMatches))
	}
	if coverage.TopicNodeCount > 0 {
		parts = append(parts, fmt.Sprintf("topic brief shows %d nodes and %d edges", coverage.TopicNodeCount, coverage.TopicEdgeCount))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ") + ". Returned evidence is a capped working set, not the full corpus."
}

func countValues(evidence []ask.Evidence, keyFn func(ask.Evidence) string, limit int) []Bucket {
	counts := map[string]int{}
	for _, doc := range evidence {
		key := strings.TrimSpace(keyFn(doc))
		if key == "" {
			key = "unknown"
		}
		counts[key]++
	}
	return orderedBuckets(counts, limit)
}

func topTags(evidence []ask.Evidence, limit int) []Bucket {
	counts := map[string]int{}
	for _, doc := range evidence {
		for _, tag := range strings.Split(doc.UserTags, ",") {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			counts[tag]++
		}
	}
	return orderedBuckets(counts, limit)
}

func orderedBuckets(counts map[string]int, limit int) []Bucket {
	buckets := make([]Bucket, 0, len(counts))
	for key, count := range counts {
		buckets = append(buckets, Bucket{Key: key, Count: count})
	}
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].Count != buckets[j].Count {
			return buckets[i].Count > buckets[j].Count
		}
		return strings.ToLower(buckets[i].Key) < strings.ToLower(buckets[j].Key)
	})
	if limit > 0 && len(buckets) > limit {
		buckets = buckets[:limit]
	}
	return buckets
}

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

func filterSearchResults(ctx context.Context, st *store.Store, results []model.SearchResult, sourceTypes []string) []model.SearchResult {
	if len(sourceTypes) == 0 {
		return results
	}
	filtered := make([]model.SearchResult, 0, len(results))
	for _, result := range results {
		if item, err := st.GetItem(ctx, result.SourceKey); err == nil {
			if matchesSourceTypes(sourceTypes, item.SourceType) {
				filtered = append(filtered, result)
			}
			continue
		}
		if source, err := st.GetSource(ctx, result.SourceKey); err == nil {
			if matchesSourceTypes(sourceTypes, source.SourceType) {
				filtered = append(filtered, result)
			}
		}
	}
	return filtered
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

func tagAlias(value string) string {
	terms := queryterms.Terms(value)
	if len(terms) < 2 {
		return ""
	}
	return strings.Join(terms, "-")
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func defaultInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func trimText(value string, maxChars int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" || maxChars <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return strings.TrimSpace(string(runes[:maxChars])) + "..."
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
