package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/darron/dbrain/internal/brainresearch"
)

type ResearchPack = brainresearch.Pack
type ResearchPackOptions = brainresearch.Options
type researchBucket = brainresearch.Bucket

func (s *Server) toolResearchPack(ctx context.Context, raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Question          string   `json:"question"`
		Topic             string   `json:"topic"`
		Limit             int      `json:"limit"`
		SourceTypes       []string `json:"source_types"`
		IncludeRelated    bool     `json:"include_related"`
		RelatedLimit      int      `json:"related_limit"`
		SeedLimit         int      `json:"seed_limit"`
		IncludeTopicBrief *bool    `json:"include_topic_brief"`
		MaxCharsPerDoc    int      `json:"max_chars_per_doc"`
		PlannerModel      string   `json:"planner_model"`
		UseModelPlanner   bool     `json:"use_model_planner"`
		DisablePlanner    bool     `json:"disable_planner"`
		UseSemantic       bool     `json:"use_semantic"`
		DisableSemantic   bool     `json:"disable_semantic"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode research pack args: %w", err)
	}

	pack, err := s.BuildResearchPack(ctx, ResearchPackOptions{
		Question:        args.Question,
		Topic:           args.Topic,
		Limit:           args.Limit,
		SourceTypes:     args.SourceTypes,
		IncludeRelated:  args.IncludeRelated,
		RelatedLimit:    args.RelatedLimit,
		SeedLimit:       args.SeedLimit,
		IncludeTopic:    args.IncludeTopicBrief,
		MaxCharsPerDoc:  args.MaxCharsPerDoc,
		PlannerModel:    args.PlannerModel,
		UseModelPlanner: args.UseModelPlanner || !args.DisablePlanner,
		DisablePlanner:  args.DisablePlanner,
		UseSemantic:     args.UseSemantic,
		DisableSemantic: args.DisableSemantic,
	})
	if err != nil {
		return nil, err
	}

	return toolOKResult(formatResearchPack(pack), pack), nil
}

func (s *Server) BuildResearchPack(ctx context.Context, opts ResearchPackOptions) (ResearchPack, error) {
	return brainresearch.Build(ctx, s.cfg, s.st, opts)
}
