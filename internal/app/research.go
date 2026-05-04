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

			cfg, err := loadConfig(root.root)
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

func writeResearchSynthesis(out interface {
	Write([]byte) (int, error)
}, synthesis brainresearch.SynthesisResult) {
	_, _ = fmt.Fprintf(out, "Answer status: %s\n", synthesis.AnswerStatus)
	if synthesis.Model != "" {
		_, _ = fmt.Fprintf(out, "Model: %s\n", synthesis.Model)
	}
	if synthesis.Tool != "" {
		_, _ = fmt.Fprintf(out, "Tool: %s", synthesis.Tool)
		if synthesis.ToolVersion != "" {
			_, _ = fmt.Fprintf(out, " %s", synthesis.ToolVersion)
		}
		_, _ = fmt.Fprintln(out)
	}
	if len(synthesis.Warnings) > 0 {
		_, _ = fmt.Fprintf(out, "Warnings: %s\n", strings.Join(synthesis.Warnings, ", "))
	}
	if synthesis.Answer != "" {
		_, _ = fmt.Fprintln(out, "\nAnswer:")
		_, _ = fmt.Fprintf(out, "%s\n", synthesis.Answer)
	}
	if len(synthesis.Citations) > 0 {
		_, _ = fmt.Fprintln(out, "\nCitations:")
		for _, citation := range synthesis.Citations {
			_, _ = fmt.Fprintf(out, "- %s", citation.SourceKey)
			if citation.NotePath != "" {
				_, _ = fmt.Fprintf(out, " (%s)", citation.NotePath)
			}
			_, _ = fmt.Fprintln(out)
		}
	}
	_, _ = fmt.Fprintln(out)
}

func writeResearchPack(out interface {
	Write([]byte) (int, error)
}, pack brainresearch.Pack) {
	_, _ = fmt.Fprintf(out, "Research pack: %s\n", pack.Question)
	_, _ = fmt.Fprintf(out, "Mode: %s\n", pack.Mode)
	_, _ = fmt.Fprintf(out, "Evidence: %d\n", len(pack.Evidence))
	if pack.Coverage.RecallNote != "" {
		_, _ = fmt.Fprintf(out, "Recall: %s\n", pack.Coverage.RecallNote)
	}
	if len(pack.Coverage.ExactTagMatches) > 0 {
		_, _ = fmt.Fprintln(out, "Exact tag matches:")
		for _, bucket := range pack.Coverage.ExactTagMatches {
			_, _ = fmt.Fprintf(out, "- %s (%d)\n", bucket.Key, bucket.Count)
		}
	}
	if pack.TopicBrief != nil {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintf(out, "Topic brief: %s\n", pack.TopicBrief.Topic)
		if pack.TopicBrief.Summary != "" {
			_, _ = fmt.Fprintf(out, "%s\n", pack.TopicBrief.Summary)
		}
	}

	if len(pack.Evidence) == 0 {
		_, _ = fmt.Fprintln(out, "\nNo matching evidence found.")
		return
	}

	_, _ = fmt.Fprintln(out, "\nRetrieved evidence:")
	for _, doc := range pack.Evidence {
		_, _ = fmt.Fprintf(out, "- [%s] %s\n", doc.SourceKey, doc.Title)
		_, _ = fmt.Fprintf(out, "  kind: %s\n", doc.Kind)
		_, _ = fmt.Fprintf(out, "  url: %s\n", doc.URL)
		_, _ = fmt.Fprintf(out, "  note: %s\n", doc.NotePath)
		if doc.SourceType != "" {
			_, _ = fmt.Fprintf(out, "  source_type: %s\n", doc.SourceType)
		}
		if len(doc.EntityMatches) > 0 {
			_, _ = fmt.Fprintf(out, "  entity_matches: %s\n", strings.Join(doc.EntityMatches, ", "))
		}
		if doc.Relationship != "" {
			_, _ = fmt.Fprintf(out, "  relationship: %s", doc.Relationship)
			if doc.RelatedTo != "" {
				_, _ = fmt.Fprintf(out, " (%s)", doc.RelatedTo)
			}
			_, _ = fmt.Fprintln(out)
		}
		if doc.Summary != "" {
			_, _ = fmt.Fprintf(out, "  summary: %s\n", singleLine(doc.Summary))
		} else if doc.Excerpt != "" {
			_, _ = fmt.Fprintf(out, "  excerpt: %s\n", singleLine(doc.Excerpt))
		}
	}
}

func singleLine(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if len(runes) <= 220 {
		return value
	}
	return string(runes[:220]) + "..."
}
