package topics

import (
	"context"
	"strings"

	"github.com/darron/dbrain/internal/store"
)

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
