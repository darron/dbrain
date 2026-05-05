package topics

import (
	"context"

	"github.com/darron/dbrain/internal/store"
)

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
