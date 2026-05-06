package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/topics"
)

func TestWriteTopicRoundTripsDefinitionAndIndex(t *testing.T) {
	t.Parallel()

	cfg := config.Config{VaultDir: t.TempDir()}
	graph := topics.TopicMap{
		Topic:        "Local First AI",
		SourceTypes:  []string{"x_bookmark", "github"},
		SeedLimit:    7,
		RelatedLimit: 3,
		Synthesis: topics.TopicSynthesis{
			Overview:     "Local notes point at a reusable workflow.",
			WhyItMatters: "The topic has enough linked material to revisit.",
		},
		Nodes: []topics.TopicMapNode{
			{SourceKey: "x:1", Title: "Seed note", NotePath: "items/seed.md", SourceType: "x_bookmark", Role: "seed"},
		},
		Pivots: topics.TopicPivots{
			SeedNodes: []topics.TopicMapNode{
				{SourceKey: "x:1", Title: "Seed note", NotePath: "items/seed.md", SourceType: "x_bookmark", Role: "seed"},
			},
		},
	}

	if err := WriteTopic(cfg, graph); err != nil {
		t.Fatalf("WriteTopic: %v", err)
	}

	def, err := ReadTopicDefinition(cfg, graph.Topic)
	if err != nil {
		t.Fatalf("ReadTopicDefinition: %v", err)
	}
	if def.Topic != graph.Topic || def.SeedLimit != graph.SeedLimit || def.RelatedLimit != graph.RelatedLimit {
		t.Fatalf("definition mismatch: %#v", def)
	}
	if strings.Join(def.SourceTypes, ",") != "x_bookmark,github" {
		t.Fatalf("source types mismatch: %#v", def.SourceTypes)
	}

	defs, err := ListTopicDefinitions(cfg)
	if err != nil {
		t.Fatalf("ListTopicDefinitions: %v", err)
	}
	if len(defs) != 1 || defs[0].NotePath != TopicNoteRelativePath(graph.Topic) {
		t.Fatalf("definitions mismatch: %#v", defs)
	}

	if err := WriteTopicIndex(cfg, defs); err != nil {
		t.Fatalf("WriteTopicIndex: %v", err)
	}
	indexBody, err := os.ReadFile(filepath.Join(cfg.VaultDir, filepath.FromSlash(TopicIndexRelativePath())))
	if err != nil {
		t.Fatalf("read topic index: %v", err)
	}
	if !strings.Contains(string(indexBody), "[[topics/local-first-ai|Local First AI]]") {
		t.Fatalf("index did not link generated topic note:\n%s", string(indexBody))
	}
}
