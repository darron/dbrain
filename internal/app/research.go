package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/researchrun"
	"github.com/darron/dbrain/internal/researchtrace"
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
	var useSemantic bool
	var noSemantic bool
	var noTrace bool
	var keepAllTraces bool
	var useRunner bool
	var answerReview bool
	var answerReviewModel string
	var answerReviewTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "research <question>",
		Short: "Research the local brain with evidence and local synthesis",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if topicBrief && noTopicBrief {
				return fmt.Errorf("--topic-brief and --no-topic-brief cannot both be set")
			}
			if cmd.Flags().Changed("planner") && usePlanner && noPlanner {
				return fmt.Errorf("--planner and --no-planner cannot both be set")
			}
			if useSemantic && noSemantic {
				return fmt.Errorf("--semantic and --no-semantic cannot both be set")
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

			question := strings.Join(args, " ")
			if useRunner {
				traceEnabled := !noTrace
				result, err := researchrun.Run(cmd.Context(), cfg, st, researchrun.Options{
					Question:            question,
					Topic:               topic,
					Limit:               limit,
					SourceTypes:         sourceTypes,
					RelatedLimit:        relatedLimit,
					SeedLimit:           seedLimit,
					IncludeTopic:        includeTopic,
					MaxCharsPerDoc:      maxCharsPerDoc,
					PlannerModel:        firstNonEmpty(plannerModel, synthesisModel),
					PlannerTimeout:      plannerTimeout,
					UseModelPlanner:     usePlanner,
					DisablePlanner:      noPlanner,
					UseSemantic:         useSemantic,
					DisableSemantic:     noSemantic,
					Model:               synthesisModel,
					CLI:                 defaultCLIProvider,
					MaxEvidenceChars:    synthesisMaxEvidenceChars,
					SynthesisTimeout:    synthesisTimeout,
					EnableAnswerReview:  answerReview,
					AnswerReviewModel:   answerReviewModel,
					AnswerReviewTimeout: answerReviewTimeout,
					TraceEnabled:        &traceEnabled,
					Surface:             "cli",
					KeepAllTraces:       keepAllTraces,
				})
				if err != nil {
					return err
				}
				if jsonOut {
					return writeJSON(cmd.OutOrStdout(), researchCommandOutput{
						ResearchPack: result.Pack,
						Synthesis:    result.Synthesis,
						TracePath:    result.TracePath,
						StopReason:   result.StopReason,
						Warnings:     result.Warnings,
						Judge:        result.Judge,
						Verification: result.Verification,
						AnswerReview: result.AnswerReview,
					})
				}
				if result.Synthesis != nil && result.Synthesis.Answer != "" {
					writeResearchSynthesis(cmd.OutOrStdout(), *result.Synthesis)
				} else if len(result.Warnings) > 0 {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Research runner stopped: %s\n", result.StopReason)
					for _, warning := range result.Warnings {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "warning: %s\n", warning)
					}
				}
				writeResearchPack(cmd.OutOrStdout(), result.Pack)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nStop reason: %s\n", result.StopReason)
				if result.TracePath != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Trace: %s\n", result.TracePath)
				}
				return nil
			}
			var recorder *researchtrace.Recorder
			if !noTrace {
				recorder = researchtrace.NewRecorder("cli", question)
			}
			pack, err := brainresearch.Build(cmd.Context(), cfg, st, brainresearch.Options{
				Question:        question,
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
				UseSemantic:     useSemantic,
				DisableSemantic: noSemantic,
				Observer:        recorder,
			})
			if err != nil {
				if recorder != nil {
					recorder.SetFailure("retrieve", "retrieval_failed", err)
					recorder.SetStopReason("retrieval_failed")
					if _, traceErr := writeResearchTrace(cfg, recorder, keepAllTraces); traceErr != nil {
						return traceErr
					}
				}
				return err
			}
			if recorder != nil {
				recorder.SetPack(pack)
			}

			if retrievalOnly {
				tracePath := ""
				if recorder != nil {
					recorder.SetStopReason("retrieval_only")
					tracePath, err = writeResearchTrace(cfg, recorder, keepAllTraces)
					if err != nil {
						return err
					}
				}
				if jsonOut {
					return writeJSON(cmd.OutOrStdout(), pack)
				}
				writeResearchPack(cmd.OutOrStdout(), pack)
				if tracePath != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nTrace: %s\n", tracePath)
				}
				return nil
			}

			prepared, err := brainresearch.PrepareSynthesis(cfg, brainresearch.SynthesisOptions{
				Question:         pack.Question,
				Pack:             pack,
				Model:            synthesisModel,
				CLI:              defaultCLIProvider,
				MaxEvidenceChars: synthesisMaxEvidenceChars,
			})
			if err == nil && recorder != nil {
				recorder.SetPreparedSynthesis(prepared)
			}
			if err == nil {
				var synthesis brainresearch.SynthesisResult
				synthesis, err = brainresearch.RunPreparedSynthesis(cmd.Context(), cfg, prepared, brainresearch.SynthesisOptions{
					Question:         pack.Question,
					Pack:             pack,
					Model:            synthesisModel,
					CLI:              defaultCLIProvider,
					Timeout:          synthesisTimeout,
					MaxEvidenceChars: synthesisMaxEvidenceChars,
				})
				if err == nil && recorder != nil {
					recorder.SetSynthesis(synthesis)
					recorder.SetStopReason(researchStopReason(synthesis))
				}
				if err == nil {
					tracePath := ""
					if recorder != nil {
						tracePath, err = writeResearchTrace(cfg, recorder, keepAllTraces)
						if err != nil {
							return err
						}
					}
					if jsonOut {
						return writeJSON(cmd.OutOrStdout(), researchCommandOutput{
							ResearchPack: pack,
							Synthesis:    &synthesis,
							TracePath:    tracePath,
						})
					}

					writeResearchSynthesis(cmd.OutOrStdout(), synthesis)
					writeResearchPack(cmd.OutOrStdout(), pack)
					if tracePath != "" {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nTrace: %s\n", tracePath)
					}
					return nil
				}
			}
			if err != nil {
				tracePath := ""
				if recorder != nil {
					recorder.SetFailure("synthesis", "synthesis_failed", err)
					recorder.SetStopReason("synthesis_failed")
					tracePath, err = writeResearchTrace(cfg, recorder, keepAllTraces)
					if err != nil {
						return err
					}
				}
				if jsonOut {
					return writeJSON(cmd.OutOrStdout(), researchCommandOutput{
						ResearchPack: pack,
						Synthesis:    nil,
						Error:        err.Error(),
						TracePath:    tracePath,
					})
				}
				writeResearchPack(cmd.OutOrStdout(), pack)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nSynthesis error: %s\n", err)
				if tracePath != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Trace: %s\n", tracePath)
				}
				return nil
			}
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
	cmd.Flags().BoolVar(&useSemantic, "semantic", false, "Request optional semantic retrieval when a local lane is configured")
	cmd.Flags().BoolVar(&noSemantic, "no-semantic", false, "Disable optional semantic retrieval for lexical debugging")
	cmd.Flags().BoolVar(&noTrace, "no-trace", false, "Do not save a research trace for this run")
	cmd.Flags().BoolVar(&keepAllTraces, "keep-all-traces", false, "Skip default research trace pruning for this run")
	cmd.Flags().BoolVar(&useRunner, "runner", false, "Use the bounded research runner")
	cmd.Flags().BoolVar(&answerReview, "answer-review", false, "Run optional advisory answer review in bounded runner mode")
	cmd.Flags().StringVar(&answerReviewModel, "answer-review-model", "", "Optional answer review model; empty uses the configured default when --answer-review is set")
	cmd.Flags().DurationVar(&answerReviewTimeout, "answer-review-timeout", 30*time.Second, "Maximum time to wait for advisory answer review")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print JSON output; includes synthesis unless --retrieval-only is set")

	return cmd
}

type researchCommandOutput struct {
	ResearchPack brainresearch.Pack             `json:"research_pack"`
	Synthesis    *brainresearch.SynthesisResult `json:"synthesis,omitempty"`
	Error        string                         `json:"error,omitempty"`
	TracePath    string                         `json:"trace_path,omitempty"`
	StopReason   string                         `json:"stop_reason,omitempty"`
	Warnings     []string                       `json:"warnings,omitempty"`
	Judge        researchrun.JudgeResult        `json:"judge,omitempty"`
	Verification researchrun.VerificationResult `json:"verification,omitempty"`
	AnswerReview researchrun.AnswerReviewResult `json:"answer_review,omitempty"`
}

func researchStopReason(result brainresearch.SynthesisResult) string {
	switch result.AnswerStatus {
	case "no_evidence":
		return "no_evidence"
	case "error":
		return "synthesis_failed"
	default:
		return "enough_evidence"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
