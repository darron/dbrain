package topics

import (
	"sort"
	"strings"
)

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
