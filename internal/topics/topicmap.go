package topics

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

type Options struct {
	SourceTypes  []string
	SeedLimit    int
	RelatedLimit int
}

type TopicMap struct {
	Topic        string         `json:"topic"`
	SourceTypes  []string       `json:"source_types,omitempty"`
	SeedLimit    int            `json:"seed_limit"`
	RelatedLimit int            `json:"related_limit"`
	Entities     []TopicEntity  `json:"entities,omitempty"`
	Pivots       TopicPivots    `json:"pivots,omitempty"`
	Synthesis    TopicSynthesis `json:"synthesis,omitempty"`
	Nodes        []TopicMapNode `json:"nodes"`
	Edges        []TopicMapEdge `json:"edges"`
}

type Definition struct {
	Topic        string   `json:"topic"`
	SourceTypes  []string `json:"source_types,omitempty"`
	SeedLimit    int      `json:"seed_limit"`
	RelatedLimit int      `json:"related_limit"`
	NotePath     string   `json:"note_path,omitempty"`
}

type TopicMapNode struct {
	SourceKey  string `json:"source_key"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	NotePath   string `json:"note_path"`
	SourceType string `json:"source_type"`
	Role       string `json:"role"`
}

type TopicMapEdge struct {
	From         string `json:"from"`
	To           string `json:"to"`
	Relationship string `json:"relationship"`
}

type TopicEntity struct {
	Key               string   `json:"key"`
	Name              string   `json:"name"`
	Kind              string   `json:"kind"`
	NotePath          string   `json:"note_path"`
	CanonicalURL      string   `json:"canonical_url,omitempty"`
	ReferenceCount    int      `json:"reference_count"`
	MatchedReferences int      `json:"matched_references"`
	MatchedSourceKeys []string `json:"matched_source_keys,omitempty"`
}

type TopicPivots struct {
	Projects     []TopicEntity  `json:"projects,omitempty"`
	Orgs         []TopicEntity  `json:"orgs,omitempty"`
	Sites        []TopicEntity  `json:"sites,omitempty"`
	People       []TopicEntity  `json:"people,omitempty"`
	SeedNodes    []TopicMapNode `json:"seed_nodes,omitempty"`
	RelatedNodes []TopicMapNode `json:"related_nodes,omitempty"`
}

type TopicSynthesis struct {
	Overview      string        `json:"overview,omitempty"`
	Angles        []string      `json:"angles,omitempty"`
	Signals       []TopicSignal `json:"signals,omitempty"`
	OpenQuestions []string      `json:"open_questions,omitempty"`
	WhyItMatters  string        `json:"why_it_matters,omitempty"`
}

type TopicSignal struct {
	Title      string   `json:"title"`
	Detail     string   `json:"detail"`
	SourceKeys []string `json:"source_keys,omitempty"`
}

type graphNode struct {
	TopicMapNode
	ItemID   int64
	SourceID int64
}

func Build(ctx context.Context, st *store.Store, topic string, opts Options) (TopicMap, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return TopicMap{}, fmt.Errorf("topic cannot be empty")
	}
	if opts.SeedLimit <= 0 {
		opts.SeedLimit = 6
	}
	if opts.RelatedLimit <= 0 {
		opts.RelatedLimit = 2
	}

	results, err := st.Search(ctx, topic, max(opts.SeedLimit*4, opts.SeedLimit))
	if err != nil {
		return TopicMap{}, err
	}
	sourceResults, err := st.SearchSources(ctx, topic, max(opts.SeedLimit*4, opts.SeedLimit))
	if err != nil {
		return TopicMap{}, err
	}
	results = append(results, sourceResults...)
	results = filterSearchResults(ctx, st, results, opts.SourceTypes)

	graph := TopicMap{
		Topic:        topic,
		SourceTypes:  normalizeSourceTypes(opts.SourceTypes),
		SeedLimit:    opts.SeedLimit,
		RelatedLimit: opts.RelatedLimit,
		Nodes:        make([]TopicMapNode, 0, opts.SeedLimit*(opts.RelatedLimit+1)),
		Edges:        make([]TopicMapEdge, 0, opts.SeedLimit*opts.RelatedLimit),
	}

	nodeIndex := map[string]graphNode{}
	edgeIndex := map[string]struct{}{}

	for _, result := range results {
		if seedCount(nodeIndex) >= opts.SeedLimit {
			break
		}
		node, ok, err := resolveGraphNode(ctx, st, result.SourceKey, "seed")
		if err != nil {
			return TopicMap{}, err
		}
		if !ok {
			continue
		}
		if _, exists := nodeIndex[node.SourceKey]; exists {
			continue
		}
		nodeIndex[node.SourceKey] = node
		graph.Nodes = append(graph.Nodes, node.TopicMapNode)
	}

	for _, node := range graph.Nodes {
		resolved := nodeIndex[node.SourceKey]
		remaining := opts.RelatedLimit
		if remaining <= 0 {
			continue
		}
		if resolved.ItemID > 0 {
			refs, err := st.ListSourcesForItem(ctx, resolved.ItemID)
			if err != nil {
				return TopicMap{}, err
			}
			for _, ref := range refs {
				if remaining == 0 {
					break
				}
				relatedNode, err := graphNodeFromItemSourceRef(ctx, st, ref)
				if err != nil {
					return TopicMap{}, err
				}
				if _, exists := nodeIndex[relatedNode.SourceKey]; !exists {
					nodeIndex[relatedNode.SourceKey] = relatedNode
					graph.Nodes = append(graph.Nodes, relatedNode.TopicMapNode)
				}
				addTopicEdge(edgeIndex, &graph, TopicMapEdge{
					From:         resolved.SourceKey,
					To:           relatedNode.SourceKey,
					Relationship: "links_to",
				})
				remaining--
			}
			continue
		}
		if resolved.SourceID > 0 {
			refs, err := st.ListBacklinksForSource(ctx, resolved.SourceID)
			if err != nil {
				return TopicMap{}, err
			}
			for _, ref := range refs {
				if remaining == 0 {
					break
				}
				relatedNode, err := graphNodeFromSourceBacklink(ctx, st, ref)
				if err != nil {
					return TopicMap{}, err
				}
				if _, exists := nodeIndex[relatedNode.SourceKey]; !exists {
					nodeIndex[relatedNode.SourceKey] = relatedNode
					graph.Nodes = append(graph.Nodes, relatedNode.TopicMapNode)
				}
				addTopicEdge(edgeIndex, &graph, TopicMapEdge{
					From:         relatedNode.SourceKey,
					To:           resolved.SourceKey,
					Relationship: "links_to",
				})
				remaining--
			}
		}
	}

	entityIndex, err := entities.BuildIndex(ctx, st)
	if err != nil {
		return TopicMap{}, err
	}
	graph.Entities = collectTopicEntities(graph, entityIndex, topic, max(opts.SeedLimit, 6))
	graph.Pivots = buildTopicPivots(graph)
	graph.Synthesis = synthesizeTopic(ctx, st, graph)

	return graph, nil
}

func FromDefinition(ctx context.Context, st *store.Store, def Definition) (TopicMap, error) {
	return Build(ctx, st, def.Topic, Options{
		SourceTypes:  def.SourceTypes,
		SeedLimit:    def.SeedLimit,
		RelatedLimit: def.RelatedLimit,
	})
}

func FormatText(graph TopicMap) string {
	var b strings.Builder
	b.WriteString("Topic map: ")
	b.WriteString(graph.Topic)
	b.WriteString("\n")
	b.WriteString("Nodes:\n")
	for _, node := range graph.Nodes {
		b.WriteString("- [")
		b.WriteString(node.SourceKey)
		b.WriteString("] ")
		b.WriteString(node.Title)
		if node.Role != "" {
			b.WriteString(" (")
			b.WriteString(node.Role)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
	if len(graph.Edges) > 0 {
		b.WriteString("Edges:\n")
		for _, edge := range graph.Edges {
			b.WriteString("- ")
			b.WriteString(edge.From)
			b.WriteString(" --")
			b.WriteString(edge.Relationship)
			b.WriteString("--> ")
			b.WriteString(edge.To)
			b.WriteString("\n")
		}
	}
	if len(graph.Entities) > 0 {
		b.WriteString("Entities:\n")
		appendTopicEntityLines(&b, "projects", graph.Pivots.Projects)
		appendTopicEntityLines(&b, "orgs", graph.Pivots.Orgs)
		appendTopicEntityLines(&b, "sites", graph.Pivots.Sites)
		appendTopicEntityLines(&b, "people", graph.Pivots.People)
	}
	if len(graph.Pivots.SeedNodes) > 0 {
		b.WriteString("Starting notes:\n")
		appendTopicNodeLines(&b, graph.Pivots.SeedNodes)
	}
	if len(graph.Pivots.RelatedNodes) > 0 {
		b.WriteString("Related notes:\n")
		appendTopicNodeLines(&b, graph.Pivots.RelatedNodes)
	}
	return strings.TrimSpace(b.String())
}

func resolveGraphNode(ctx context.Context, st *store.Store, lookup string, role string) (graphNode, bool, error) {
	if item, err := st.GetItem(ctx, lookup); err == nil {
		return graphNode{
			TopicMapNode: TopicMapNode{
				SourceKey:  item.SourceKey,
				Kind:       "item",
				Title:      firstNonEmpty(item.Title, item.SourceKey),
				URL:        item.CanonicalURL,
				NotePath:   item.NotePath,
				SourceType: item.SourceType,
				Role:       role,
			},
			ItemID: item.ID,
		}, true, nil
	}
	source, err := st.GetSource(ctx, lookup)
	if err != nil {
		return graphNode{}, false, nil
	}
	return graphNode{
		TopicMapNode: TopicMapNode{
			SourceKey:  source.SourceKey,
			Kind:       "source",
			Title:      firstNonEmpty(source.Title, source.SourceKey, source.CanonicalURL),
			URL:        source.CanonicalURL,
			NotePath:   source.NotePath,
			SourceType: source.SourceType,
			Role:       role,
		},
		SourceID: source.ID,
	}, true, nil
}

func graphNodeFromItemSourceRef(ctx context.Context, st *store.Store, ref model.ItemSourceRef) (graphNode, error) {
	source, err := st.GetSourceByID(ctx, ref.SourceID)
	if err != nil {
		return graphNode{}, err
	}
	return graphNode{
		TopicMapNode: TopicMapNode{
			SourceKey:  ref.SourceKey,
			Kind:       "source",
			Title:      firstNonEmpty(ref.Title, source.Title, ref.CanonicalURL),
			URL:        ref.CanonicalURL,
			NotePath:   ref.NotePath,
			SourceType: ref.SourceType,
			Role:       "related",
		},
		SourceID: ref.SourceID,
	}, nil
}

func graphNodeFromSourceBacklink(ctx context.Context, st *store.Store, ref model.SourceBacklink) (graphNode, error) {
	item, err := st.GetItem(ctx, ref.SourceKey)
	sourceType := ""
	if err == nil {
		sourceType = item.SourceType
	}
	return graphNode{
		TopicMapNode: TopicMapNode{
			SourceKey:  ref.SourceKey,
			Kind:       "item",
			Title:      firstNonEmpty(ref.Title, ref.SourceKey, ref.CanonicalURL),
			URL:        ref.CanonicalURL,
			NotePath:   ref.NotePath,
			SourceType: sourceType,
			Role:       "related",
		},
		ItemID: ref.ItemID,
	}, nil
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

func addTopicEdge(index map[string]struct{}, graph *TopicMap, edge TopicMapEdge) {
	key := edge.From + "|" + edge.To + "|" + edge.Relationship
	if _, exists := index[key]; exists {
		return
	}
	index[key] = struct{}{}
	graph.Edges = append(graph.Edges, edge)
}

func seedCount(nodes map[string]graphNode) int {
	count := 0
	for _, node := range nodes {
		if node.Role == "seed" {
			count++
		}
	}
	return count
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeSourceTypes(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func collectTopicEntities(graph TopicMap, index []entities.Entity, topic string, limit int) []TopicEntity {
	if len(graph.Nodes) == 0 || len(index) == 0 {
		return nil
	}

	nodeKeys := map[string]struct{}{}
	for _, node := range graph.Nodes {
		nodeKeys[node.SourceKey] = struct{}{}
	}
	topicTerms := topicQueryTerms(topic)

	type scoredTopicEntity struct {
		Entity       TopicEntity
		SortScore    int
		KindPriority int
	}

	matched := make([]scoredTopicEntity, 0, len(index))
	for _, entity := range index {
		sourceKeys := make([]string, 0, 4)
		seen := map[string]struct{}{}
		for _, ref := range entity.References {
			if _, ok := nodeKeys[ref.SourceKey]; !ok {
				continue
			}
			if _, exists := seen[ref.SourceKey]; exists {
				continue
			}
			seen[ref.SourceKey] = struct{}{}
			sourceKeys = append(sourceKeys, ref.SourceKey)
		}
		if len(sourceKeys) == 0 {
			continue
		}
		score := scoreTopicEntity(entity, topic, topicTerms, len(sourceKeys))
		if score <= 0 {
			continue
		}
		matched = append(matched, scoredTopicEntity{
			Entity: TopicEntity{
				Key:               entity.Key,
				Name:              entity.Name,
				Kind:              string(entity.Kind),
				NotePath:          entity.NotePath,
				CanonicalURL:      entity.CanonicalURL,
				ReferenceCount:    entity.ReferenceCount,
				MatchedReferences: len(sourceKeys),
				MatchedSourceKeys: sourceKeys,
			},
			SortScore:    score,
			KindPriority: topicEntityKindPriority(entity.Kind),
		})
	}

	sort.Slice(matched, func(i, j int) bool {
		if matched[i].SortScore != matched[j].SortScore {
			return matched[i].SortScore > matched[j].SortScore
		}
		if matched[i].Entity.MatchedReferences != matched[j].Entity.MatchedReferences {
			return matched[i].Entity.MatchedReferences > matched[j].Entity.MatchedReferences
		}
		if matched[i].KindPriority != matched[j].KindPriority {
			return matched[i].KindPriority > matched[j].KindPriority
		}
		if matched[i].Entity.ReferenceCount != matched[j].Entity.ReferenceCount {
			return matched[i].Entity.ReferenceCount > matched[j].Entity.ReferenceCount
		}
		if matched[i].Entity.Kind != matched[j].Entity.Kind {
			return matched[i].Entity.Kind < matched[j].Entity.Kind
		}
		return strings.ToLower(matched[i].Entity.Name) < strings.ToLower(matched[j].Entity.Name)
	})

	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	result := make([]TopicEntity, 0, len(matched))
	for _, entry := range matched {
		result = append(result, entry.Entity)
	}
	return result
}

func buildTopicPivots(graph TopicMap) TopicPivots {
	pivots := TopicPivots{
		Projects:     make([]TopicEntity, 0, 3),
		Orgs:         make([]TopicEntity, 0, 3),
		Sites:        make([]TopicEntity, 0, 3),
		People:       make([]TopicEntity, 0, 3),
		SeedNodes:    make([]TopicMapNode, 0, len(graph.Nodes)),
		RelatedNodes: make([]TopicMapNode, 0, len(graph.Nodes)),
	}

	for _, entity := range graph.Entities {
		switch entity.Kind {
		case "project":
			if len(pivots.Projects) < 3 {
				pivots.Projects = append(pivots.Projects, entity)
			}
		case "org":
			if len(pivots.Orgs) < 3 {
				pivots.Orgs = append(pivots.Orgs, entity)
			}
		case "site":
			if len(pivots.Sites) < 3 {
				pivots.Sites = append(pivots.Sites, entity)
			}
		case "person":
			if len(pivots.People) < 3 {
				pivots.People = append(pivots.People, entity)
			}
		}
	}

	for _, node := range graph.Nodes {
		switch node.Role {
		case "seed":
			if len(pivots.SeedNodes) < 5 {
				pivots.SeedNodes = append(pivots.SeedNodes, node)
			}
		case "related":
			if len(pivots.RelatedNodes) < 5 {
				pivots.RelatedNodes = append(pivots.RelatedNodes, node)
			}
		}
	}

	return pivots
}

func SummaryText(graph TopicMap) string {
	parts := []string{
		fmt.Sprintf("Mapped %d notes and %d relationships for %q.", len(graph.Nodes), len(graph.Edges), graph.Topic),
	}
	if strings.TrimSpace(graph.Synthesis.Overview) != "" {
		parts = append(parts, graph.Synthesis.Overview)
	}
	if totalTopicPivotEntities(graph.Pivots) > 0 {
		parts = append(parts, fmt.Sprintf("Key entity pivots: %s.", describeTopicPivots(graph.Pivots)))
	}
	if len(graph.Pivots.SeedNodes) > 0 {
		parts = append(parts, fmt.Sprintf("Start with %s.", joinLabels(topicNodeTitles(graph.Pivots.SeedNodes))))
	}
	return strings.Join(parts, " ")
}

func totalTopicPivotEntities(pivots TopicPivots) int {
	return len(pivots.Projects) + len(pivots.Orgs) + len(pivots.Sites) + len(pivots.People)
}

func describeTopicPivots(pivots TopicPivots) string {
	parts := make([]string, 0, 4)
	if len(pivots.Projects) > 0 {
		parts = append(parts, "projects "+joinLabels(topicEntityNames(pivots.Projects)))
	}
	if len(pivots.Orgs) > 0 {
		parts = append(parts, "orgs "+joinLabels(topicEntityNames(pivots.Orgs)))
	}
	if len(pivots.Sites) > 0 {
		parts = append(parts, "sites "+joinLabels(topicEntityNames(pivots.Sites)))
	}
	if len(pivots.People) > 0 {
		parts = append(parts, "people "+joinLabels(topicEntityNames(pivots.People)))
	}
	return strings.Join(parts, "; ")
}

func topicEntityNames(values []TopicEntity) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value.Name)
	}
	return out
}

func topicNodeTitles(values []TopicMapNode) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value.Title)
	}
	return out
}

func joinLabels(values []string) string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			filtered = append(filtered, value)
		}
	}
	switch len(filtered) {
	case 0:
		return ""
	case 1:
		return filtered[0]
	case 2:
		return filtered[0] + " and " + filtered[1]
	default:
		return strings.Join(filtered[:len(filtered)-1], ", ") + ", and " + filtered[len(filtered)-1]
	}
}

func appendTopicEntityLines(b *strings.Builder, label string, values []TopicEntity) {
	if len(values) == 0 {
		return
	}
	b.WriteString("- ")
	b.WriteString(label)
	b.WriteString(": ")
	for i, entity := range values {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(entity.Name)
		b.WriteString(" [")
		b.WriteString(entity.Kind)
		b.WriteString("] refs=")
		_, _ = fmt.Fprintf(b, "%d", entity.MatchedReferences)
		if len(entity.MatchedSourceKeys) > 0 {
			b.WriteString(" nodes=")
			b.WriteString(strings.Join(entity.MatchedSourceKeys, ", "))
		}
	}
	b.WriteString("\n")
}

func appendTopicNodeLines(b *strings.Builder, values []TopicMapNode) {
	for _, node := range values {
		b.WriteString("- [")
		b.WriteString(node.SourceKey)
		b.WriteString("] ")
		b.WriteString(node.Title)
		if node.SourceType != "" {
			b.WriteString(" (")
			b.WriteString(node.SourceType)
			b.WriteString(")")
		}
		b.WriteString("\n")
	}
}

func scoreTopicEntity(entity entities.Entity, topic string, topicTerms []string, matchedReferences int) int {
	score := matchedReferences * 20
	score += min(entity.ReferenceCount, 10)
	score += topicEntityKindPriority(entity.Kind)

	textScore := scoreTopicEntityText(entity, topic, topicTerms)
	score += textScore

	if entity.Kind == entities.KindPerson && matchedReferences == 1 && textScore == 0 {
		score -= 8
	}
	if entity.Kind == entities.KindSite && textScore == 0 {
		score -= 2
	}
	return score
}

func scoreTopicEntityText(entity entities.Entity, topic string, topicTerms []string) int {
	candidates := []string{
		strings.ToLower(strings.TrimSpace(entity.Name)),
		strings.ToLower(strings.TrimSpace(entity.Domain)),
		strings.ToLower(strings.TrimSpace(topicEntityKeyValue(entity.Key))),
	}
	for _, alias := range entity.Aliases {
		candidates = append(candidates, strings.ToLower(strings.TrimSpace(alias)))
	}

	topic = strings.ToLower(strings.TrimSpace(topic))
	score := 0
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if topic != "" {
			switch {
			case candidate == topic:
				score = max(score, 16)
			case strings.Contains(candidate, topic):
				score = max(score, 12)
			}
		}
		for _, term := range topicTerms {
			if term == "" {
				continue
			}
			switch {
			case candidate == term:
				score = max(score, 8)
			case len([]rune(term)) >= 5 && strings.Contains(candidate, term):
				score = max(score, 4)
			}
		}
	}
	return score
}

func topicEntityKindPriority(kind entities.Kind) int {
	switch kind {
	case entities.KindProject:
		return 6
	case entities.KindOrg:
		return 5
	case entities.KindSite:
		return 4
	case entities.KindPerson:
		return 2
	default:
		return 1
	}
}

func topicQueryTerms(topic string) []string {
	parts := strings.Fields(strings.ToLower(strings.TrimSpace(topic)))
	seen := map[string]struct{}{}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || len([]rune(part)) < 3 {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}

func topicEntityKeyValue(key string) string {
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
