package topics

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"dbrain/internal/store"
)

type topicEvidence struct {
	Node    TopicMapNode
	Title   string
	Detail  string
	Phrases []string
}

type topicSignalCluster struct {
	Phrase     string
	SourceKeys []string
	Titles     []string
	Count      int
}

func synthesizeTopic(ctx context.Context, st *store.Store, graph TopicMap) TopicSynthesis {
	evidence := collectTopicEvidence(ctx, st, graph)
	clusters := buildSignalClusters(graph, evidence)

	return TopicSynthesis{
		Overview:      synthesizeOverview(graph, clusters),
		Angles:        synthesizeAngles(graph),
		Signals:       synthesizeSignals(evidence, clusters),
		OpenQuestions: synthesizeOpenQuestions(graph, evidence),
		WhyItMatters:  synthesizeWhyItMatters(graph, clusters),
	}
}

func collectTopicEvidence(ctx context.Context, st *store.Store, graph TopicMap) []topicEvidence {
	evidence := make([]topicEvidence, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		entry := topicEvidence{Node: node, Title: strings.TrimSpace(node.Title)}
		switch node.Kind {
		case "item":
			item, err := st.GetItem(ctx, node.SourceKey)
			if err != nil {
				continue
			}
			entry.Detail = firstNonEmpty(
				bestSentence(item.XPostText),
				bestSentence(item.ArticleText),
				bestSentence(item.Text),
				strings.TrimSpace(item.ArticleTitle),
			)
		case "source":
			source, err := st.GetSource(ctx, node.SourceKey)
			if err != nil {
				continue
			}
			entry.Detail = firstNonEmpty(
				bestSentence(source.SummaryText),
				bestSentence(source.ExtractedText),
				bestSentence(source.Description),
				strings.TrimSpace(source.SiteName),
			)
		}
		entry.Phrases = extractSignalPhrases(graph.Topic, entry.Title, entry.Detail)
		evidence = append(evidence, entry)
	}
	return evidence
}

func synthesizeOverview(graph TopicMap, clusters []topicSignalCluster) string {
	parts := []string{
		fmt.Sprintf("This topic currently maps %d notes and %d explicit relationships in the local brain.", len(graph.Nodes), len(graph.Edges)),
	}

	if pivotSummary := describeTopicPivots(graph.Pivots); pivotSummary != "" {
		parts = append(parts, "The strongest pivots are "+pivotSummary+".")
	}

	if signalSummary := summarizeSignalClusters(clusters, 3); signalSummary != "" {
		parts = append(parts, "Repeated signals in the saved material point to "+signalSummary+".")
	}

	if sourceMix := describeSourceMix(graph.Nodes); sourceMix != "" {
		parts = append(parts, "The corpus here is a mix of "+sourceMix+".")
	}

	return strings.Join(parts, " ")
}

func synthesizeAngles(graph TopicMap) []string {
	angles := make([]string, 0, 4)

	if len(graph.Pivots.Projects) > 0 {
		angles = append(angles, "Implementation-heavy material shows up through projects "+joinLabels(topicEntityNames(graph.Pivots.Projects))+".")
	}
	if len(graph.Pivots.Sites) > 0 {
		angles = append(angles, "Recurring explainers and landing pages come from sites "+joinLabels(topicEntityNames(graph.Pivots.Sites))+".")
	}
	if len(graph.Pivots.Orgs) > 0 {
		angles = append(angles, "Organizations shaping the topic in this corpus include "+joinLabels(topicEntityNames(graph.Pivots.Orgs))+".")
	}
	if len(graph.Pivots.People) > 0 {
		angles = append(angles, "Repeated voices in the saved material include "+joinLabels(topicEntityNames(graph.Pivots.People))+".")
	}
	if len(angles) == 0 {
		if sourceMix := describeSourceMix(graph.Nodes); sourceMix != "" {
			angles = append(angles, "The current corpus is mostly made up of "+sourceMix+".")
		}
	}
	if len(angles) > 4 {
		angles = angles[:4]
	}
	return angles
}

func synthesizeSignals(evidence []topicEvidence, clusters []topicSignalCluster) []TopicSignal {
	if len(clusters) == 0 {
		return nil
	}

	signals := make([]TopicSignal, 0, min(4, len(clusters)))
	for _, cluster := range clusters {
		title := formatSignalTitle(cluster.Phrase)
		if title == "" {
			continue
		}

		detail := fmt.Sprintf("Recurring across %d notes", cluster.Count)
		if titles := joinLabels(limitStrings(cluster.Titles, 3)); titles != "" {
			detail += ", including " + titles
		}
		detail += "."

		signals = append(signals, TopicSignal{
			Title:      title,
			Detail:     detail,
			SourceKeys: append([]string(nil), cluster.SourceKeys...),
		})
		if len(signals) >= 4 {
			break
		}
	}
	if len(signals) > 0 {
		return signals
	}
	return nil
}

func synthesizeOpenQuestions(graph TopicMap, evidence []topicEvidence) []string {
	questions := make([]string, 0, 3)
	seen := map[string]struct{}{}
	for _, entry := range evidence {
		for _, candidate := range []string{entry.Title, entry.Detail} {
			candidate = strings.TrimSpace(candidate)
			if !looksQuestionLike(candidate) {
				continue
			}
			key := strings.ToLower(candidate)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			questions = append(questions, candidate)
			if len(questions) >= 3 {
				return questions
			}
		}
	}

	if len(questions) > 0 {
		return questions
	}

	if len(graph.Pivots.Projects)+len(graph.Pivots.Orgs)+len(graph.Pivots.Sites) >= 2 {
		questions = append(questions, "Which of the linked projects or sources looks concrete enough to investigate next?")
	}
	if len(graph.Edges) > 0 {
		questions = append(questions, "Which linked notes add implementation detail versus high-level positioning?")
	}
	if len(graph.Pivots.People) > 0 {
		questions = append(questions, "Which voices here are reporting firsthand work versus commenting on it from the outside?")
	}
	if len(questions) > 3 {
		questions = questions[:3]
	}
	return questions
}

func synthesizeWhyItMatters(graph TopicMap, clusters []topicSignalCluster) string {
	parts := make([]string, 0, 3)
	if len(graph.Pivots.SeedNodes) > 0 {
		parts = append(parts, "The saved notes are concentrated enough that this looks like an emerging area rather than a one-off bookmark.")
	}
	if len(graph.Pivots.RelatedNodes) > 0 {
		parts = append(parts, "There are already linked deep dives attached to the seed posts, which makes this topic worth keeping as a reusable entry point.")
	}
	if signalSummary := summarizeSignalClusters(clusters, 2); signalSummary != "" {
		parts = append(parts, "The material clusters around "+signalSummary+", which is a good sign that the topic is coherent enough to revisit later.")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func buildSignalClusters(graph TopicMap, evidence []topicEvidence) []topicSignalCluster {
	type accumulator struct {
		sourceKeys map[string]struct{}
		titles     map[string]struct{}
	}

	accumulators := map[string]*accumulator{}
	for _, entry := range evidence {
		if len(entry.Phrases) == 0 {
			continue
		}
		seen := map[string]struct{}{}
		for _, phrase := range entry.Phrases {
			if _, exists := seen[phrase]; exists {
				continue
			}
			seen[phrase] = struct{}{}

			acc := accumulators[phrase]
			if acc == nil {
				acc = &accumulator{
					sourceKeys: map[string]struct{}{},
					titles:     map[string]struct{}{},
				}
				accumulators[phrase] = acc
			}
			acc.sourceKeys[entry.Node.SourceKey] = struct{}{}
			if title := strings.TrimSpace(entry.Title); title != "" {
				acc.titles[title] = struct{}{}
			}
		}
	}

	clusters := make([]topicSignalCluster, 0, len(accumulators))
	for phrase, acc := range accumulators {
		if len(acc.sourceKeys) <= 1 {
			continue
		}
		clusters = append(clusters, topicSignalCluster{
			Phrase:     phrase,
			SourceKeys: sortedKeys(acc.sourceKeys),
			Titles:     sortedKeys(acc.titles),
			Count:      len(acc.sourceKeys),
		})
	}

	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].Count != clusters[j].Count {
			return clusters[i].Count > clusters[j].Count
		}
		if phraseWordCount(clusters[i].Phrase) != phraseWordCount(clusters[j].Phrase) {
			return phraseWordCount(clusters[i].Phrase) > phraseWordCount(clusters[j].Phrase)
		}
		return clusters[i].Phrase < clusters[j].Phrase
	})

	selected := make([]topicSignalCluster, 0, min(6, len(clusters)))
	for _, cluster := range clusters {
		if overlapsSelectedPhrase(selected, cluster.Phrase, cluster.Count) {
			continue
		}
		selected = append(selected, cluster)
		if len(selected) >= 6 {
			break
		}
	}
	return selected
}

func summarizeSignalClusters(clusters []topicSignalCluster, limit int) string {
	if len(clusters) == 0 {
		return ""
	}
	labels := make([]string, 0, min(limit, len(clusters)))
	for _, cluster := range clusters {
		labels = append(labels, cluster.Phrase)
		if limit > 0 && len(labels) >= limit {
			break
		}
	}
	return joinLabels(labels)
}

func extractSignalPhrases(topic string, values ...string) []string {
	stopwords := topicSignalStopwords(topic)
	seen := map[string]struct{}{}
	out := make([]string, 0, 12)
	for _, value := range values {
		tokens := signalTokens(value)
		maxN := min(3, len(tokens))
		for size := maxN; size >= 1; size-- {
			for i := 0; i+size <= len(tokens); i++ {
				candidate := tokens[i : i+size]
				if !validSignalPhrase(candidate, stopwords) {
					continue
				}
				phrase := strings.Join(candidate, " ")
				if _, exists := seen[phrase]; exists {
					continue
				}
				seen[phrase] = struct{}{}
				out = append(out, phrase)
			}
		}
	}
	return out
}

func signalTokens(value string) []string {
	return strings.Fields(strings.ToLower(strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r), r == ' ':
			return r
		default:
			return ' '
		}
	}, value)))
}

func validSignalPhrase(tokens []string, stopwords map[string]struct{}) bool {
	if len(tokens) == 0 {
		return false
	}
	for _, token := range tokens {
		if token == "" {
			return false
		}
		if _, skip := stopwords[token]; skip {
			return false
		}
	}
	if len(tokens) == 1 {
		token := tokens[0]
		if len(token) >= 5 || shortTechToken(token) {
			return true
		}
		return false
	}
	hasMeaningfulToken := false
	for _, token := range tokens {
		if len(token) >= 4 || shortTechToken(token) {
			hasMeaningfulToken = true
			break
		}
	}
	return hasMeaningfulToken
}

func topicSignalStopwords(topic string) map[string]struct{} {
	stopwords := make(map[string]struct{}, len(baseSignalStopwords)+8)
	for word := range baseSignalStopwords {
		stopwords[word] = struct{}{}
	}
	for _, token := range signalTokens(topic) {
		if token == "" {
			continue
		}
		for _, variant := range topicTokenVariants(token) {
			stopwords[variant] = struct{}{}
		}
	}
	return stopwords
}

func topicTokenVariants(token string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 4)
	add := func(value string) {
		value = strings.TrimSpace(strings.ToLower(value))
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}

	add(token)
	if strings.HasSuffix(token, "ies") && len(token) > 3 {
		add(strings.TrimSuffix(token, "ies") + "y")
	}
	if strings.HasSuffix(token, "y") && len(token) > 2 {
		add(strings.TrimSuffix(token, "y") + "ies")
	}
	if strings.HasSuffix(token, "s") && len(token) > 3 {
		add(strings.TrimSuffix(token, "s"))
	} else if len(token) > 2 {
		add(token + "s")
	}
	return out
}

var baseSignalStopwords = map[string]struct{}{
	"a": {}, "about": {}, "after": {}, "all": {}, "also": {}, "an": {}, "and": {}, "any": {},
	"are": {}, "around": {}, "because": {}, "been": {}, "between": {}, "bookmark": {}, "bookmarks": {},
	"build": {}, "building": {}, "built": {}, "can": {}, "could": {}, "data": {}, "does": {}, "enough": {},
	"even": {}, "for": {}, "from": {}, "gets": {}, "getting": {}, "github": {}, "have": {}, "here": {},
	"how": {}, "into": {}, "just": {}, "like": {}, "links": {}, "linked": {}, "look": {}, "looking": {},
	"looks": {}, "made": {}, "make": {}, "makes": {}, "many": {}, "more": {}, "most": {}, "much": {},
	"note": {}, "notes": {}, "onto": {}, "other": {}, "over": {}, "post": {}, "posts": {}, "repo": {},
	"repos": {}, "saved": {}, "save": {}, "saving": {}, "show": {}, "showing": {}, "shows": {}, "source": {},
	"sources": {}, "such": {}, "than": {}, "that": {}, "their": {}, "them": {}, "then": {}, "there": {},
	"these": {}, "they": {}, "this": {}, "those": {}, "through": {}, "tweet": {}, "tweets": {}, "using": {},
	"used": {}, "user": {}, "users": {}, "video": {}, "videos": {}, "what": {}, "when": {}, "where": {},
	"which": {}, "while": {}, "who": {}, "why": {}, "will": {}, "with": {}, "without": {}, "work": {},
	"worked": {}, "working": {}, "works": {}, "your": {},
}

func shortTechToken(token string) bool {
	switch token {
	case "ai", "api", "cli", "cpu", "gpu", "json", "llm", "mcp", "orm", "pdf", "rag", "sdk", "sql", "ui", "ux":
		return true
	default:
		return false
	}
}

func overlapsSelectedPhrase(selected []topicSignalCluster, phrase string, count int) bool {
	for _, current := range selected {
		if current.Count < count {
			continue
		}
		if phraseContains(current.Phrase, phrase) {
			return true
		}
	}
	return false
}

func phraseContains(container string, candidate string) bool {
	if container == candidate {
		return true
	}
	return strings.Contains(" "+container+" ", " "+candidate+" ")
}

func phraseWordCount(value string) int {
	return len(strings.Fields(strings.TrimSpace(value)))
}

func formatSignalTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := []rune(value)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func bestSentence(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")
	value = strings.Join(strings.Fields(replacer.Replace(value)), " ")
	if value == "" {
		return ""
	}
	for _, delimiter := range []string{". ", "? ", "! "} {
		if idx := strings.Index(value, delimiter); idx > 0 {
			return strings.TrimSpace(value[:idx+1])
		}
	}
	return trimRunes(value, 220)
}

func looksQuestionLike(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return false
	}
	if strings.Contains(value, "?") {
		return true
	}
	for _, prefix := range []string{"how ", "what ", "why ", "which ", "who ", "when ", "where ", "should ", "can "} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func describeSourceMix(nodes []TopicMapNode) string {
	counts := map[string]int{}
	for _, node := range nodes {
		if node.SourceType == "" {
			continue
		}
		counts[sourceTypeFamily(strings.ToLower(strings.TrimSpace(node.SourceType)))]++
	}
	type bucket struct {
		Key   string
		Count int
	}
	ordered := make([]bucket, 0, len(counts))
	for key, count := range counts {
		ordered = append(ordered, bucket{Key: key, Count: count})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Count != ordered[j].Count {
			return ordered[i].Count > ordered[j].Count
		}
		return ordered[i].Key < ordered[j].Key
	})
	labels := make([]string, 0, len(ordered))
	for _, entry := range ordered {
		switch entry.Key {
		case "x":
			labels = append(labels, "saved X posts")
		case "web":
			labels = append(labels, "linked web sources")
		case "github":
			labels = append(labels, "GitHub repos")
		case "youtube":
			labels = append(labels, "YouTube sources")
		default:
			labels = append(labels, entry.Key+" sources")
		}
	}
	return joinLabels(labels)
}

func trimRunes(value string, max int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= max {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(string(runes[:max])) + "..."
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}
