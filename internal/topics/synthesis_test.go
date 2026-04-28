package topics

import (
	"strings"
	"testing"
)

func TestExtractSignalPhrasesSkipsGenericWords(t *testing.T) {
	t.Parallel()

	phrases := extractSignalPhrases(
		"agent memory",
		"What makes agent memory work without losing context windows?",
		"Long term retrieval keeps useful context available for assistants.",
	)

	for _, unwanted := range []string{"what", "makes", "without", "agent", "memory"} {
		if containsString(phrases, unwanted) {
			t.Fatalf("unexpected generic phrase %q in %#v", unwanted, phrases)
		}
	}
	if containsString(phrases, "agents") {
		t.Fatalf("unexpected pluralized topic phrase in %#v", phrases)
	}
	for _, wanted := range []string{"context windows", "long term retrieval"} {
		if !containsString(phrases, wanted) {
			t.Fatalf("expected phrase %q in %#v", wanted, phrases)
		}
	}
}

func TestBuildSignalClustersPrefersRepeatedPhrases(t *testing.T) {
	t.Parallel()

	graph := TopicMap{Topic: "agent memory"}
	evidence := []topicEvidence{
		{
			Node:    TopicMapNode{SourceKey: "x:1"},
			Title:   "Agent memory with context windows",
			Detail:  "Long term retrieval keeps agent state available.",
			Phrases: extractSignalPhrases("agent memory", "Agent memory with context windows", "Long term retrieval keeps agent state available."),
		},
		{
			Node:    TopicMapNode{SourceKey: "x:2"},
			Title:   "Why context windows break without retrieval",
			Detail:  "Long term retrieval gives the system persistent memory.",
			Phrases: extractSignalPhrases("agent memory", "Why context windows break without retrieval", "Long term retrieval gives the system persistent memory."),
		},
		{
			Node:    TopicMapNode{SourceKey: "x:3"},
			Title:   "Agent memory patterns",
			Detail:  "Context windows and long term retrieval work together here.",
			Phrases: extractSignalPhrases("agent memory", "Agent memory patterns", "Context windows and long term retrieval work together here."),
		},
	}

	clusters := buildSignalClusters(graph, evidence)
	if len(clusters) == 0 {
		t.Fatalf("expected repeated signal clusters")
	}

	signals := synthesizeSignals(evidence, clusters)
	if len(signals) == 0 {
		t.Fatalf("expected synthesized signals")
	}

	foundMeaningfulSignal := false
	for _, signal := range signals {
		lower := strings.ToLower(signal.Title)
		if strings.Contains(lower, "what") || strings.Contains(lower, "makes") || strings.Contains(lower, "without") {
			t.Fatalf("unexpected noisy signal title %q", signal.Title)
		}
		if strings.Contains(lower, "context windows") || strings.Contains(lower, "long term retrieval") {
			foundMeaningfulSignal = true
		}
	}
	if !foundMeaningfulSignal {
		t.Fatalf("expected meaningful signal title in %#v", signals)
	}
}

func TestExtractSignalPhrasesSkipsPossessiveFragmentsAndSingleBoilerplate(t *testing.T) {
	t.Parallel()

	phrases := extractSignalPhrases(
		"mark carney",
		"Terry Newman: Mark Carney's long, deceitful quest for the Liberal crown",
		"National Newswatch article about national security and climate finance",
		"Another article says national security policy and climate finance remain linked.",
	)

	for _, unwanted := range []string{"s long", "s", "article", "national", "news"} {
		if containsString(phrases, unwanted) {
			t.Fatalf("unexpected noisy phrase %q in %#v", unwanted, phrases)
		}
	}
	for _, wanted := range []string{"national security", "climate finance"} {
		if !containsString(phrases, wanted) {
			t.Fatalf("expected phrase %q in %#v", wanted, phrases)
		}
	}
}

func TestSynthesizeAnglesUsesTopicPivots(t *testing.T) {
	t.Parallel()

	graph := TopicMap{
		Topic: "vector database",
		Pivots: TopicPivots{
			Projects: []TopicEntity{{Name: "milvus"}, {Name: "qdrant"}},
			Sites:    []TopicEntity{{Name: "qdrant.tech"}},
			Orgs:     []TopicEntity{{Name: "Pinecone"}},
			People:   []TopicEntity{{Name: "Alice Example"}},
		},
	}

	angles := synthesizeAngles(graph)
	if len(angles) < 3 {
		t.Fatalf("expected several synthesized angles, got %#v", angles)
	}
	if !containsSubstring(angles, "Implementation-heavy material shows up through projects") {
		t.Fatalf("expected project angle in %#v", angles)
	}
	if !containsSubstring(angles, "Recurring explainers and landing pages come from sites") {
		t.Fatalf("expected site angle in %#v", angles)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsSubstring(values []string, target string) bool {
	for _, value := range values {
		if strings.Contains(value, target) {
			return true
		}
	}
	return false
}
