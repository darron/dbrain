package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/store"
)

func newResearchCommand(root *rootOptions) *cobra.Command {
	var limit int
	var maxCharsPerDoc int
	var sourceTypes []string
	var includeRelated bool
	var relatedLimit int
	var topic string
	var seedLimit int
	var topicBrief bool
	var noTopicBrief bool
	var jsonOut bool
	var retrievalOnly bool
	var synthesisModel string
	var synthesisMaxEvidenceChars int
	var synthesisTimeout time.Duration
	var plannerModel string
	var plannerTimeout time.Duration
	usePlanner := true
	var noPlanner bool

	cmd := &cobra.Command{
		Use:   "research <question>",
		Short: "Research the local brain with evidence and local synthesis",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if topicBrief && noTopicBrief {
				return fmt.Errorf("--topic-brief and --no-topic-brief cannot both be set")
			}

			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}

			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			var includeTopic *bool
			if topicBrief || noTopicBrief {
				value := topicBrief && !noTopicBrief
				includeTopic = &value
			}

			pack, err := brainresearch.Build(cmd.Context(), cfg, st, brainresearch.Options{
				Question:        strings.Join(args, " "),
				Topic:           topic,
				Limit:           limit,
				SourceTypes:     sourceTypes,
				IncludeRelated:  includeRelated,
				RelatedLimit:    relatedLimit,
				SeedLimit:       seedLimit,
				IncludeTopic:    includeTopic,
				MaxCharsPerDoc:  maxCharsPerDoc,
				PlannerModel:    firstNonEmpty(plannerModel, synthesisModel),
				PlannerTimeout:  plannerTimeout,
				UseModelPlanner: usePlanner,
				DisablePlanner:  noPlanner,
			})
			if err != nil {
				return err
			}

			if retrievalOnly {
				if jsonOut {
					return writeJSON(cmd.OutOrStdout(), pack)
				}
				writeResearchPack(cmd.OutOrStdout(), pack)
				return nil
			}

			synthesis, err := brainresearch.Synthesize(cmd.Context(), cfg, brainresearch.SynthesisOptions{
				Question:         pack.Question,
				Pack:             pack,
				Model:            synthesisModel,
				CLI:              defaultCLIProvider,
				Timeout:          synthesisTimeout,
				MaxEvidenceChars: synthesisMaxEvidenceChars,
			})
			if err != nil {
				if jsonOut {
					return writeJSON(cmd.OutOrStdout(), researchCommandOutput{
						ResearchPack: pack,
						Synthesis:    nil,
						Error:        err.Error(),
					})
				}
				writeResearchPack(cmd.OutOrStdout(), pack)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nSynthesis error: %s\n", err)
				return nil
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), researchCommandOutput{
					ResearchPack: pack,
					Synthesis:    &synthesis,
				})
			}

			writeResearchSynthesis(cmd.OutOrStdout(), synthesis)
			writeResearchPack(cmd.OutOrStdout(), pack)
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 8, "Maximum pieces of evidence to retrieve")
	cmd.Flags().IntVar(&maxCharsPerDoc, "max-chars-per-doc", 700, "Maximum summary/excerpt characters per retrieved document")
	cmd.Flags().StringSliceVar(&sourceTypes, "source-type", nil, "Optional source type filter; repeat or comma-separate values like github, web, x_bookmark, apple_note")
	cmd.Flags().BoolVar(&includeRelated, "include-related", false, "Include related evidence from linked items or sources")
	cmd.Flags().IntVar(&relatedLimit, "related-limit", 2, "Maximum number of related evidence documents to append when --include-related is set")
	cmd.Flags().StringVar(&topic, "topic", "", "Optional explicit topic for the topic brief")
	cmd.Flags().IntVar(&seedLimit, "seed-limit", 6, "Maximum primary topic nodes when a topic brief is included")
	cmd.Flags().BoolVar(&topicBrief, "topic-brief", false, "Force topic brief generation")
	cmd.Flags().BoolVar(&noTopicBrief, "no-topic-brief", false, "Disable inferred topic brief generation")
	cmd.Flags().BoolVar(&retrievalOnly, "retrieval-only", false, "Only print the research pack without local synthesis")
	cmd.Flags().StringVar(&synthesisModel, "model", "", "Optional synthesis model; empty uses the configured default")
	cmd.Flags().IntVar(&synthesisMaxEvidenceChars, "max-evidence-chars", brainresearch.DefaultMaxEvidenceChars, "Maximum total evidence characters sent to synthesis")
	cmd.Flags().DurationVar(&synthesisTimeout, "synthesis-timeout", 2*time.Minute, "Maximum time to wait for local synthesis")
	cmd.Flags().StringVar(&plannerModel, "planner-model", "", "Optional planner model; empty uses --model or the configured default")
	cmd.Flags().DurationVar(&plannerTimeout, "planner-timeout", 20*time.Second, "Maximum time to wait for model-assisted query planning")
	cmd.Flags().BoolVar(&usePlanner, "planner", true, "Use the configured model for query planning before retrieval")
	cmd.Flags().BoolVar(&noPlanner, "no-planner", false, "Disable model-assisted query planning")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print JSON output; includes synthesis unless --retrieval-only is set")

	return cmd
}

type researchCommandOutput struct {
	ResearchPack brainresearch.Pack             `json:"research_pack"`
	Synthesis    *brainresearch.SynthesisResult `json:"synthesis,omitempty"`
	Error        string                         `json:"error,omitempty"`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
