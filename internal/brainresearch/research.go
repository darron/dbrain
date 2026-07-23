package brainresearch

import (
	"context"
	"fmt"
	"strings"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/researchhybrid"
	"github.com/darron/dbrain/internal/retrieval"
	"github.com/darron/dbrain/internal/semanticconfig"
	"github.com/darron/dbrain/internal/semanticindex"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/internal/topics"
)

func New(cfg config.Config, st *store.Store) *Builder {
	return &Builder{cfg: cfg, st: st, semanticMode: semanticconfig.ModeOff}
}

func Build(ctx context.Context, cfg config.Config, st *store.Store, opts Options) (Pack, error) {
	b, err := NewRuntimeBuilderContext(ctx, cfg, st, opts.EffectiveSemanticMode, opts.UseSemantic, opts.DisableSemantic)
	if err != nil {
		return Pack{}, err
	}
	defer func() { _ = b.Close() }()
	return b.Build(ctx, opts)
}

func (b *Builder) Build(ctx context.Context, opts Options) (Pack, error) {
	configuredMode := b.semanticMode
	if opts.EffectiveSemanticMode != "" {
		configuredMode = opts.EffectiveSemanticMode
	}
	mode, err := semanticconfig.EffectiveMode(configuredMode, opts.UseSemantic, opts.DisableSemantic)
	if err != nil {
		return Pack{}, err
	}
	resolved := *b
	resolved.semanticMode = mode
	resolved.semanticStatus = nil
	resolved.shadowComparison = nil
	return resolved.buildResolved(ctx, opts)
}

func (b *Builder) buildResolved(ctx context.Context, opts Options) (Pack, error) {
	question := strings.TrimSpace(opts.Question)
	if question == "" {
		return Pack{}, fmt.Errorf("question is required")
	}
	rawQuestion := strings.TrimSpace(firstNonEmpty(opts.RawQuestion, opts.Question))
	anchors := extractProtectedAnchors(rawQuestion)
	if len(anchors) == 0 && len(opts.ContinuityAnchors) > 0 {
		anchors = append([]ProtectedAnchor(nil), opts.ContinuityAnchors...)
	}
	anchors = b.resolveProtectedAnchors(ctx, anchors, opts)
	searchQuestion := ask.SearchText(question)
	if searchQuestion == "" {
		searchQuestion = question
	}

	hints := ask.Hints(question)
	emitEvent(opts.Observer, "question_normalized", map[string]interface{}{
		"question":          question,
		"search_question":   searchQuestion,
		"text_query":        hints.TextQuery,
		"terms":             hints.Terms,
		"tag_queries":       hints.TagQueries,
		"raw_question":      rawQuestion,
		"protected_anchors": anchors,
	})
	limit := defaultInt(opts.Limit, 8)
	maxChars := defaultInt(opts.MaxCharsPerDoc, 700)
	topic, topicSource, hasTopic := resolveTopic(searchQuestion, opts.Topic)
	includeTopic := hasTopic
	if opts.IncludeTopic != nil {
		includeTopic = *opts.IncludeTopic
	}
	if includeTopic && !hasTopic {
		topic = normalizeTopicPhrase(searchQuestion)
		if topic != "" {
			topicSource = "normalized_question"
			hasTopic = true
		}
	}
	if !hasTopic {
		includeTopic = false
	}

	strategyOpts := opts
	strategyOpts.ContinuityAnchors = anchors
	strategy := b.buildResearchStrategy(ctx, searchQuestion, hints, strategyOpts)
	retrievalLanes := researchhybrid.LaneStatuses(researchhybrid.Options{
		UseSemantic:     b.semanticMode == semanticconfig.ModeOn,
		DisableSemantic: opts.DisableSemantic,
	})
	emitEvent(opts.Observer, "query_plan_built", map[string]interface{}{
		"query_family":      strategy.Family,
		"planner":           strategy.Planner,
		"planner_model":     strategy.PlannerModel,
		"planner_error":     strategy.PlannerError,
		"variants":          strategy.Variants,
		"concepts":          strategy.Concepts,
		"protected_anchors": anchors,
		"variant_count":     len(strategy.Variants),
		"concept_count":     len(strategy.Concepts),
		"planner_failed":    strategy.PlannerError != "",
		"retrieval_lanes":   retrievalLanes,
	})
	evidence, err := b.collectStrategyEvidence(ctx, strategy, hints, opts, limit, maxChars, anchors)
	if err != nil {
		return Pack{}, err
	}
	if evidenceUsesLane(evidence, researchhybrid.LaneExactTag) {
		retrievalLanes = append(retrievalLanes, retrieval.RetrievalLane{Name: researchhybrid.LaneExactTag, Status: researchhybrid.StatusUsed})
	}
	if b.semanticMode != semanticconfig.ModeOff && b.semanticStatus != nil {
		for i := range retrievalLanes {
			if retrievalLanes[i].Name != researchhybrid.LaneSemantic {
				continue
			}
			retrievalLanes[i].Backend = b.semanticStatus.Backend
			retrievalLanes[i].Profile = b.semanticStatus.ProfileID
			retrievalLanes[i].Generation = b.semanticStatus.GenerationID
			retrievalLanes[i].Reason = string(b.semanticStatus.Reason)
			if b.semanticStatus.State == semanticindex.StateSearched {
				retrievalLanes[i].Status = researchhybrid.StatusUsed
			} else {
				retrievalLanes[i].Status = researchhybrid.StatusDisabled
			}
		}
	}
	emitEvent(opts.Observer, "evidence_selected", map[string]interface{}{
		"count":       len(evidence),
		"source_keys": evidenceSourceKeys(evidence),
	})

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
			TextQuery:                    hints.TextQuery,
			QueryFamily:                  strategy.Family,
			QueryTerms:                   hints.Terms,
			TagQueries:                   hints.TagQueries,
			QueryVariants:                strategy.Variants,
			Concepts:                     strategy.Concepts,
			ProtectedAnchors:             anchors,
			Planner:                      strategy.Planner,
			PlannerModel:                 strategy.PlannerModel,
			PlannerError:                 strategy.PlannerError,
			SourceTypes:                  opts.SourceTypes,
			RetrievalLanes:               retrievalLanes,
			Limit:                        limit,
			MaxCharsPerDoc:               maxChars,
			IncludeRelated:               opts.IncludeRelated,
			RelatedLimit:                 opts.RelatedLimit,
			Topic:                        topic,
			TopicSource:                  topicSource,
			IncludeTopicBrief:            includeTopic,
			SemanticMode:                 b.semanticMode,
			SemanticReadiness:            b.semanticReadiness.State,
			SemanticReadinessDiagnostics: b.semanticReadinessDiagnostics,
			ShadowComparison:             b.shadowComparison,
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
	emitEvent(opts.Observer, "pack_built", map[string]interface{}{
		"mode":           pack.Mode,
		"evidence_count": pack.Coverage.EvidenceCount,
		"recall_note":    pack.Coverage.RecallNote,
		"used_topic":     pack.UsedTopicBrief,
	})

	return pack, nil
}

func evidenceSourceKeys(rows []ask.Evidence) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.SourceKey) == "" {
			continue
		}
		out = append(out, row.SourceKey)
	}
	return out
}

func evidenceUsesLane(rows []ask.Evidence, laneName string) bool {
	laneName = strings.TrimSpace(laneName)
	if laneName == "" {
		return false
	}
	for _, row := range rows {
		if row.Retrieval == nil {
			continue
		}
		for _, lane := range row.Retrieval.Lanes {
			if strings.EqualFold(lane.Name, laneName) {
				return true
			}
		}
	}
	return false
}
