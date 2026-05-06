package topics

import (
	"fmt"
	"strings"
)

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
