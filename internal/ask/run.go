package ask

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"dbrain/internal/config"
	"dbrain/internal/entities"
	"dbrain/internal/model"
	"dbrain/internal/store"
	"dbrain/internal/summarizecli"
)

const defaultMaxCharsPerDoc = 1800

const answerPrompt = `Answer the user's question using only the provided evidence from a local second-brain.
Requirements:
- Be concise and factual.
- Cite claims inline using [source_key].
- If the evidence is insufficient, say so clearly.
- Prefer the strongest and most directly relevant evidence.
- End with a "Sources" section listing each cited source_key, title, and note path.`

type Options struct {
	Limit          int
	RetrieveOnly   bool
	Model          string
	CLI            string
	Length         string
	Timeout        time.Duration
	Binary         string
	MaxCharsPerDoc int
	SourceTypes    []string
	IncludeRelated bool
	RelatedLimit   int
}

type Evidence struct {
	SourceKey     string   `json:"source_key"`
	Kind          string   `json:"kind"`
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	NotePath      string   `json:"note_path"`
	Summary       string   `json:"summary"`
	Excerpt       string   `json:"excerpt"`
	Author        string   `json:"author,omitempty"`
	SourceType    string   `json:"source_type,omitempty"`
	PublishedAt   string   `json:"published_at,omitempty"`
	ExtractedAt   string   `json:"extracted_at,omitempty"`
	SummarizedAt  string   `json:"summarized_at,omitempty"`
	EntityMatches []string `json:"entity_matches,omitempty"`
	RelatedTo     string   `json:"related_to,omitempty"`
	Relationship  string   `json:"relationship,omitempty"`
}

type Response struct {
	Question string     `json:"question"`
	Answer   string     `json:"answer"`
	Evidence []Evidence `json:"evidence"`
}

func Run(ctx context.Context, cfg config.Config, st *store.Store, question string, opts Options) (Response, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return Response{}, fmt.Errorf("question cannot be empty")
	}
	if opts.Limit <= 0 {
		opts.Limit = 8
	}
	if opts.MaxCharsPerDoc <= 0 {
		opts.MaxCharsPerDoc = defaultMaxCharsPerDoc
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Minute
	}
	if strings.TrimSpace(opts.Length) == "" {
		opts.Length = "medium"
	}
	if opts.RelatedLimit < 0 {
		opts.RelatedLimit = 0
	}

	searchTerms := queryTerms(question)
	searchLimit := opts.Limit * 4
	if searchLimit < opts.Limit {
		searchLimit = opts.Limit
	}
	if searchLimit < 12 {
		searchLimit = 12
	}

	results, err := st.Search(ctx, strings.Join(searchTerms, " "), searchLimit)
	if err != nil {
		return Response{}, err
	}

	entityIndex, err := entities.BuildIndex(ctx, st)
	if err != nil {
		return Response{}, err
	}
	entityMatches := buildEntityMatchIndex(entityIndex, question, searchTerms, maxInt(opts.Limit*3, 12))

	candidates := make([]evidenceCandidate, 0, len(results))
	seen := map[string]struct{}{}
	for _, result := range results {
		candidate, ok, err := buildEvidence(ctx, cfg, st, result, opts.MaxCharsPerDoc)
		if err != nil {
			return Response{}, err
		}
		if !ok || !matchesSourceTypes(opts.SourceTypes, candidate.SourceType) {
			continue
		}
		if _, exists := seen[candidate.SourceKey]; exists {
			continue
		}
		seen[candidate.SourceKey] = struct{}{}
		candidate.Score = scoreEvidence(question, searchTerms, candidate.Evidence)
		applyEntityMatches(&candidate, entityMatches)
		candidates = append(candidates, candidate)
	}
	entityCandidates, err := collectEntityCandidates(ctx, cfg, st, opts, question, searchTerms, entityMatches, seen)
	if err != nil {
		return Response{}, err
	}
	candidates = append(candidates, entityCandidates...)
	rankCandidates(candidates)

	response := Response{
		Question: question,
		Evidence: make([]Evidence, 0, opts.Limit),
	}
	selected := make([]evidenceCandidate, 0, opts.Limit)

	relatedTarget := 0
	if opts.IncludeRelated {
		relatedTarget = opts.RelatedLimit
		if relatedTarget == 0 {
			relatedTarget = 2
		}
		if relatedTarget >= opts.Limit {
			relatedTarget = opts.Limit - 1
		}
	}
	primaryTarget := opts.Limit - relatedTarget
	if primaryTarget <= 0 {
		primaryTarget = opts.Limit
	}

	seen = map[string]struct{}{}
	for _, candidate := range candidates {
		if len(selected) >= primaryTarget {
			break
		}
		if _, exists := seen[candidate.SourceKey]; exists {
			continue
		}
		seen[candidate.SourceKey] = struct{}{}
		selected = append(selected, candidate)
	}

	if opts.IncludeRelated && len(selected) > 0 && len(selected) < opts.Limit {
		related, err := collectRelatedEvidence(ctx, cfg, st, selected, question, searchTerms, opts, seen, entityMatches)
		if err != nil {
			return Response{}, err
		}
		for _, candidate := range related {
			if len(selected) >= opts.Limit {
				break
			}
			if _, exists := seen[candidate.SourceKey]; exists {
				continue
			}
			seen[candidate.SourceKey] = struct{}{}
			selected = append(selected, candidate)
		}
	}

	for _, candidate := range selected {
		response.Evidence = append(response.Evidence, candidate.Evidence)
	}

	if opts.RetrieveOnly || len(response.Evidence) == 0 {
		return response, nil
	}

	inputPath, cleanup, err := writePromptInput(question, response.Evidence)
	if err != nil {
		return Response{}, err
	}
	defer cleanup()

	runResult, err := summarizecli.Run(ctx, summarizecli.Options{
		Binary:    opts.Binary,
		Input:     inputPath,
		Summarize: true,
		Model:     opts.Model,
		CLI:       askCLI(opts),
		Prompt:    answerPrompt,
		Length:    opts.Length,
		Timeout:   opts.Timeout,
	})
	if err != nil {
		return Response{}, err
	}
	if strings.TrimSpace(runResult.Summary.Text) == "" {
		return Response{}, fmt.Errorf("ask returned no answer text")
	}

	response.Answer = strings.TrimSpace(runResult.Summary.Text)
	return response, nil
}

type evidenceCandidate struct {
	Evidence
	ItemID   int64
	SourceID int64
	Score    int
}

type entityMatch struct {
	Labels []string
	Boost  int
}

type weightedQuery struct {
	Value  string
	Weight int
}

func queryTerms(question string) []string {
	stopwords := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "are": {}, "can": {}, "did": {}, "do": {}, "does": {},
		"about": {}, "find": {}, "for": {}, "github": {}, "how": {}, "in": {}, "is": {}, "me": {}, "of": {}, "on": {}, "or": {}, "the": {},
		"repo": {}, "repos": {}, "repository": {}, "repositories": {},
		"show": {}, "source": {}, "sources": {}, "tell": {}, "tweet": {}, "tweets": {},
		"to": {}, "what": {}, "when": {}, "where": {}, "which": {}, "who": {}, "why": {},
	}

	parts := strings.Fields(strings.TrimSpace(question))
	if len(parts) == 0 {
		return nil
	}

	terms := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimFunc(part, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		})
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		if _, skip := stopwords[part]; skip {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		terms = append(terms, part)
	}
	if len(terms) == 0 {
		return strings.Fields(strings.ToLower(strings.TrimSpace(question)))
	}
	return terms
}

func buildEvidence(ctx context.Context, cfg config.Config, st *store.Store, result model.SearchResult, maxChars int) (evidenceCandidate, bool, error) {
	if item, err := st.GetItem(ctx, result.SourceKey); err == nil {
		return evidenceFromItem(cfg, item, result, maxChars), true, nil
	}

	source, err := st.GetSource(ctx, result.SourceKey)
	if err != nil {
		return evidenceCandidate{}, false, nil
	}
	return evidenceFromSource(cfg, source, result, maxChars), true, nil
}

func evidenceFromItem(cfg config.Config, item model.Item, result model.SearchResult, maxChars int) evidenceCandidate {
	author := strings.TrimSpace(item.AuthorName)
	if strings.TrimSpace(item.AuthorHandle) != "" {
		handle := "@" + strings.TrimSpace(item.AuthorHandle)
		if author != "" {
			author += " "
		}
		author += handle
	}

	excerpt := firstNonEmpty(
		trimTo(item.XPostText, maxChars),
		trimTo(item.ArticleText, maxChars),
		trimTo(item.Text, maxChars),
		trimTo(result.Snippet, maxChars),
	)

	return evidenceCandidate{
		Evidence: Evidence{
			SourceKey:   item.SourceKey,
			Kind:        "item",
			Title:       item.Title,
			URL:         item.CanonicalURL,
			NotePath:    filepath.Join(cfg.VaultDir, filepath.FromSlash(item.NotePath)),
			Excerpt:     excerpt,
			Author:      author,
			SourceType:  item.SourceType,
			PublishedAt: item.PublishedAt,
		},
		ItemID: item.ID,
	}
}

func evidenceFromSource(cfg config.Config, source model.SourceDocument, result model.SearchResult, maxChars int) evidenceCandidate {
	return evidenceCandidate{
		Evidence: Evidence{
			SourceKey:    source.SourceKey,
			Kind:         "source",
			Title:        firstNonEmpty(source.Title, source.CanonicalURL),
			URL:          source.CanonicalURL,
			NotePath:     filepath.Join(cfg.VaultDir, filepath.FromSlash(source.NotePath)),
			Summary:      trimTo(source.SummaryText, maxChars),
			Excerpt:      firstNonEmpty(trimTo(source.ExtractedText, maxChars), trimTo(result.Snippet, maxChars)),
			SourceType:   source.SourceType,
			ExtractedAt:  formatTime(source.ExtractedAt),
			SummarizedAt: formatTime(source.SummarizedAt),
		},
		SourceID: source.ID,
	}
}

func collectRelatedEvidence(ctx context.Context, cfg config.Config, st *store.Store, selected []evidenceCandidate, question string, terms []string, opts Options, seen map[string]struct{}, entityMatches map[string]entityMatch) ([]evidenceCandidate, error) {
	related := make([]evidenceCandidate, 0, opts.RelatedLimit)
	for _, candidate := range selected {
		if len(related) >= opts.RelatedLimit {
			break
		}

		if candidate.ItemID > 0 {
			refs, err := st.ListSourcesForItem(ctx, candidate.ItemID)
			if err != nil {
				return nil, err
			}
			for _, ref := range refs {
				if len(related) >= opts.RelatedLimit {
					break
				}
				if _, exists := seen[ref.SourceKey]; exists {
					continue
				}
				source, err := st.GetSource(ctx, ref.SourceKey)
				if err != nil {
					continue
				}
				rel := evidenceFromSource(cfg, source, model.SearchResult{}, opts.MaxCharsPerDoc)
				if !matchesSourceTypes(opts.SourceTypes, rel.SourceType) {
					continue
				}
				rel.RelatedTo = candidate.SourceKey
				rel.Relationship = "linked source"
				rel.Score = scoreEvidence(question, terms, rel.Evidence) + 1
				applyEntityMatches(&rel, entityMatches)
				related = append(related, rel)
			}
		}

		if candidate.SourceID > 0 {
			backlinks, err := st.ListBacklinksForSource(ctx, candidate.SourceID)
			if err != nil {
				return nil, err
			}
			for _, ref := range backlinks {
				if len(related) >= opts.RelatedLimit {
					break
				}
				if _, exists := seen[ref.SourceKey]; exists {
					continue
				}
				item, err := st.GetItem(ctx, ref.SourceKey)
				if err != nil {
					continue
				}
				rel := evidenceFromItem(cfg, item, model.SearchResult{}, opts.MaxCharsPerDoc)
				if !matchesSourceTypes(opts.SourceTypes, rel.SourceType) {
					continue
				}
				rel.RelatedTo = candidate.SourceKey
				rel.Relationship = "referenced by"
				rel.Score = scoreEvidence(question, terms, rel.Evidence) + 1
				applyEntityMatches(&rel, entityMatches)
				related = append(related, rel)
			}
		}
	}
	rankCandidates(related)
	return related, nil
}

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

func scoreEvidence(question string, terms []string, evidence Evidence) int {
	score := 0
	title := strings.ToLower(evidence.Title)
	summary := strings.ToLower(evidence.Summary)
	excerpt := strings.ToLower(evidence.Excerpt)
	url := strings.ToLower(evidence.URL)

	joined := strings.ToLower(strings.Join(terms, " "))
	if joined != "" && strings.Contains(title, joined) {
		score += 15
	}

	for _, term := range terms {
		switch {
		case strings.Contains(title, term):
			score += 12
		case strings.Contains(summary, term):
			score += 8
		case strings.Contains(excerpt, term):
			score += 5
		case strings.Contains(url, term):
			score += 2
		}
	}
	if strings.TrimSpace(evidence.Summary) != "" {
		score += 3
	}
	if evidence.Kind == "source" {
		score += 1
	}
	if matchesSourceTypes([]string{"github"}, evidence.SourceType) && strings.Contains(strings.ToLower(question), "repo") {
		score += 2
	}
	score += minInt(len(evidence.EntityMatches)*3, 9)
	return score
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

func writePromptInput(question string, evidence []Evidence) (string, func(), error) {
	var b strings.Builder
	b.WriteString("# Question\n\n")
	b.WriteString(question)
	b.WriteString("\n")

	b.WriteString("\n# Evidence\n")
	for _, doc := range evidence {
		b.WriteString("\n## ")
		b.WriteString(doc.SourceKey)
		b.WriteString("\n\n")
		b.WriteString("- Kind: ")
		b.WriteString(doc.Kind)
		b.WriteString("\n")
		if doc.SourceType != "" {
			b.WriteString("- Source type: ")
			b.WriteString(doc.SourceType)
			b.WriteString("\n")
		}
		if doc.Title != "" {
			b.WriteString("- Title: ")
			b.WriteString(doc.Title)
			b.WriteString("\n")
		}
		if doc.Author != "" {
			b.WriteString("- Author: ")
			b.WriteString(doc.Author)
			b.WriteString("\n")
		}
		if doc.PublishedAt != "" {
			b.WriteString("- Published at: ")
			b.WriteString(doc.PublishedAt)
			b.WriteString("\n")
		}
		if doc.ExtractedAt != "" {
			b.WriteString("- Extracted at: ")
			b.WriteString(doc.ExtractedAt)
			b.WriteString("\n")
		}
		if doc.SummarizedAt != "" {
			b.WriteString("- Summarized at: ")
			b.WriteString(doc.SummarizedAt)
			b.WriteString("\n")
		}
		if len(doc.EntityMatches) > 0 {
			b.WriteString("- Entity matches: ")
			b.WriteString(strings.Join(doc.EntityMatches, ", "))
			b.WriteString("\n")
		}
		if doc.Relationship != "" {
			b.WriteString("- Relationship: ")
			b.WriteString(doc.Relationship)
			if doc.RelatedTo != "" {
				b.WriteString(" (")
				b.WriteString(doc.RelatedTo)
				b.WriteString(")")
			}
			b.WriteString("\n")
		}
		b.WriteString("- URL: ")
		b.WriteString(doc.URL)
		b.WriteString("\n")
		b.WriteString("- Note: ")
		b.WriteString(doc.NotePath)
		b.WriteString("\n")
		if strings.TrimSpace(doc.Summary) != "" {
			b.WriteString("\n### Summary\n\n")
			b.WriteString(strings.TrimSpace(doc.Summary))
			b.WriteString("\n")
		}
		if strings.TrimSpace(doc.Excerpt) != "" {
			b.WriteString("\n### Excerpt\n\n")
			b.WriteString(strings.TrimSpace(doc.Excerpt))
			b.WriteString("\n")
		}
	}

	file, err := os.CreateTemp("", "dbrain-ask-*.md")
	if err != nil {
		return "", nil, err
	}
	if _, err := file.WriteString(b.String()); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", nil, err
	}
	return file.Name(), func() { _ = os.Remove(file.Name()) }, nil
}

func askCLI(opts Options) string {
	if value := strings.TrimSpace(opts.CLI); value != "" {
		return value
	}
	if strings.TrimSpace(opts.Model) != "" {
		return ""
	}
	return summarizecli.PreferredCLIProvider()
}

func buildEntityMatchIndex(index []entities.Entity, question string, terms []string, limit int) map[string]entityMatch {
	if len(index) == 0 {
		return nil
	}

	type scoredEntity struct {
		Entity entities.Entity
		Score  int
	}

	queries := make([]weightedQuery, 0, len(terms)+2)
	full := strings.ToLower(strings.TrimSpace(question))
	if full != "" {
		queries = append(queries, weightedQuery{Value: full, Weight: 8})
	}
	joined := strings.ToLower(strings.TrimSpace(strings.Join(terms, " ")))
	if joined != "" && joined != full {
		queries = append(queries, weightedQuery{Value: joined, Weight: 6})
	}
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			continue
		}
		weight := 3
		if len(term) >= 5 {
			weight = 4
		}
		queries = append(queries, weightedQuery{Value: term, Weight: weight})
	}

	scored := map[string]*scoredEntity{}
	for _, query := range queries {
		for _, entity := range index {
			matchScore := scoreEntityForQuery(entity, query.Value)
			if matchScore == 0 {
				continue
			}
			entry, ok := scored[entity.Key]
			if !ok {
				entry = &scoredEntity{Entity: entity}
				scored[entity.Key] = entry
			}
			entry.Score += query.Weight + matchScore
			entry.Score += minInt(entity.ReferenceCount, 3)
		}
	}

	ordered := make([]scoredEntity, 0, len(scored))
	for _, entry := range scored {
		ordered = append(ordered, *entry)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Score != ordered[j].Score {
			return ordered[i].Score > ordered[j].Score
		}
		if ordered[i].Entity.ReferenceCount != ordered[j].Entity.ReferenceCount {
			return ordered[i].Entity.ReferenceCount > ordered[j].Entity.ReferenceCount
		}
		return strings.ToLower(ordered[i].Entity.Name) < strings.ToLower(ordered[j].Entity.Name)
	})
	if limit > 0 && len(ordered) > limit {
		ordered = ordered[:limit]
	}

	matches := map[string]entityMatch{}
	for _, entry := range ordered {
		for _, ref := range entry.Entity.References {
			current := matches[ref.SourceKey]
			current.Labels = appendUniqueFold(current.Labels, entry.Entity.Name)
			current.Boost += minInt(entry.Score, 8)
			matches[ref.SourceKey] = current
		}
	}
	return matches
}

func collectEntityCandidates(ctx context.Context, cfg config.Config, st *store.Store, opts Options, question string, terms []string, entityMatches map[string]entityMatch, seen map[string]struct{}) ([]evidenceCandidate, error) {
	if len(entityMatches) == 0 {
		return nil, nil
	}

	keys := make([]string, 0, len(entityMatches))
	for sourceKey := range entityMatches {
		if _, exists := seen[sourceKey]; exists {
			continue
		}
		keys = append(keys, sourceKey)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := entityMatches[keys[i]]
		right := entityMatches[keys[j]]
		if left.Boost != right.Boost {
			return left.Boost > right.Boost
		}
		return keys[i] < keys[j]
	})
	if len(keys) > maxInt(opts.Limit*2, 8) {
		keys = keys[:maxInt(opts.Limit*2, 8)]
	}

	candidates := make([]evidenceCandidate, 0, len(keys))
	for _, sourceKey := range keys {
		candidate, ok, err := buildEvidence(ctx, cfg, st, model.SearchResult{SourceKey: sourceKey}, opts.MaxCharsPerDoc)
		if err != nil {
			return nil, err
		}
		if !ok || !matchesSourceTypes(opts.SourceTypes, candidate.SourceType) {
			continue
		}
		candidate.Relationship = "entity match"
		candidate.Score = scoreEvidence(question, terms, candidate.Evidence)
		applyEntityMatches(&candidate, entityMatches)
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func applyEntityMatches(candidate *evidenceCandidate, matches map[string]entityMatch) {
	match, ok := matches[candidate.SourceKey]
	if !ok {
		return
	}
	candidate.EntityMatches = append([]string(nil), match.Labels...)
	candidate.Score += match.Boost
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func trimTo(value string, maxChars int) string {
	value = strings.TrimSpace(value)
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

func appendUniqueFold(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}

func scoreEntityForQuery(entity entities.Entity, query string) int {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" || len([]rune(query)) < 3 {
		return 0
	}

	candidates := []string{
		strings.ToLower(strings.TrimSpace(entity.Name)),
		strings.ToLower(strings.TrimSpace(entity.Domain)),
		strings.ToLower(strings.TrimSpace(entityKeySearchValue(entity.Key))),
	}
	for _, alias := range entity.Aliases {
		candidates = append(candidates, strings.ToLower(strings.TrimSpace(alias)))
	}

	score := 0
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		switch {
		case candidate == query:
			score = maxInt(score, 10)
		case strings.Contains(candidate, query) && len([]rune(query)) >= 5:
			score = maxInt(score, 6)
		case strings.Contains(candidate, query):
			score = maxInt(score, 4)
		}
	}
	return score
}

func entityKeySearchValue(key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	switch {
	case strings.HasPrefix(key, "github-repo:"):
		return strings.TrimPrefix(key, "github-repo:")
	case strings.HasPrefix(key, "github-owner:"):
		return strings.TrimPrefix(key, "github-owner:")
	case strings.HasPrefix(key, "x-author:name:"):
		return strings.TrimPrefix(key, "x-author:name:")
	case strings.HasPrefix(key, "x-author:"):
		return strings.TrimPrefix(key, "x-author:")
	case strings.HasPrefix(key, "site:"):
		return strings.TrimPrefix(key, "site:")
	default:
		if idx := strings.IndexByte(key, ':'); idx >= 0 && idx+1 < len(key) {
			return key[idx+1:]
		}
		return key
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
