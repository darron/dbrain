package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/researcheval"
	"github.com/darron/dbrain/internal/store"
)

func newEvalResearchCommand(root *rootOptions) *cobra.Command {
	var casesPath string
	var writeExamplePath string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "research",
		Short: "Evaluate the full research and chat harness",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if writeExamplePath != "" {
				return writeResearchEvalExample(cmd, writeExamplePath)
			}
			if casesPath == "" {
				return fmt.Errorf("--file is required unless --write-example is set")
			}

			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := store.OpenReadOnly(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			cases, err := researcheval.LoadCases(casesPath)
			if err != nil {
				return err
			}
			report, err := researcheval.Run(cmd.Context(), cfg, st, researcheval.Options{Cases: cases})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), report)
			}
			printResearchEvalReport(cmd, report)
			if report.Failed > 0 {
				return fmt.Errorf("research eval failed: %d failed, %d passed", report.Failed, report.Passed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&casesPath, "file", "", "JSON research eval cases file")
	cmd.Flags().StringVar(&writeExamplePath, "write-example", "", "Write an example JSON research eval file to this path, or '-' for stdout")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print eval report as JSON")
	cmd.AddCommand(newEvalResearchProposeCommand(root))
	cmd.AddCommand(newEvalResearchDiffCommand(root))
	return cmd
}

func newEvalResearchProposeCommand(root *rootOptions) *cobra.Command {
	var fromTranscript string
	var fromTrace string
	var outputPath string
	var apply bool
	var includeAnswerText bool

	cmd := &cobra.Command{
		Use:   "propose",
		Short: "Propose research eval cases from a saved transcript or trace",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (fromTranscript == "") == (fromTrace == "") {
				return fmt.Errorf("set exactly one of --from-transcript or --from-trace")
			}
			if apply && outputPath == "" {
				return fmt.Errorf("--apply requires --output")
			}

			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			opts := researcheval.ProposalOptions{IncludeAnswerText: includeAnswerText}
			var proposal researcheval.Proposal
			if fromTrace != "" {
				input := resolveEvalDataPath(cfg.DataDir, fromTrace)
				proposal, err = researcheval.ProposeFromTrace(input, opts)
			} else {
				input := resolveEvalDataPath(cfg.DataDir, fromTranscript)
				proposal, err = researcheval.ProposeFromTranscript(input, opts)
			}
			if err != nil {
				return err
			}
			data, err := json.MarshalIndent(proposal, "", "  ")
			if err != nil {
				return err
			}
			data = append(data, '\n')
			if outputPath != "" && apply {
				if err := os.WriteFile(outputPath, data, 0o644); err != nil {
					return fmt.Errorf("write research eval proposal %s: %w", outputPath, err)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Wrote research eval proposal: %s\n", outputPath)
				return nil
			}
			if outputPath != "" {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Dry run only; add --apply to write %s.\n", outputPath)
			}
			_, _ = cmd.OutOrStdout().Write(data)
			return nil
		},
	}
	cmd.Flags().StringVar(&fromTranscript, "from-transcript", "", "Saved chat transcript path")
	cmd.Flags().StringVar(&fromTrace, "from-trace", "", "Saved research trace directory or run.json path")
	cmd.Flags().StringVar(&outputPath, "output", "", "Path to write the proposed research eval JSON when --apply is set")
	cmd.Flags().BoolVar(&apply, "apply", false, "Write the proposal to --output")
	cmd.Flags().BoolVar(&includeAnswerText, "include-answer-text", false, "Include opt-in answer text assertions in proposed cases")
	return cmd
}

func newEvalResearchDiffCommand(root *rootOptions) *cobra.Command {
	var tracePath string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Diff a saved research trace against a fresh pack build",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(tracePath) == "" {
				return fmt.Errorf("--trace is required")
			}
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			st, err := store.OpenReadOnly(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()
			diff, err := researcheval.DiffTrace(cmd.Context(), cfg, st, tracePath)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), diff)
			}
			printResearchTraceDiff(cmd, diff)
			return nil
		},
	}
	cmd.Flags().StringVar(&tracePath, "trace", "", "Saved research trace directory, run.json path, or data-relative trace path")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print trace diff as JSON")
	return cmd
}

func writeResearchEvalExample(cmd *cobra.Command, path string) error {
	data, err := json.MarshalIndent(researcheval.ExampleCases(), "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if path == "-" {
		_, _ = cmd.OutOrStdout().Write(data)
		return nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write example research eval file %s: %w", path, err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Wrote example research eval file: %s\n", path)
	return nil
}

func printResearchEvalReport(cmd *cobra.Command, report researcheval.Report) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Research eval: %d passed, %d failed (%dms)\n", report.Passed, report.Failed, report.DurationMS)
	for _, result := range report.Cases {
		status := "PASS"
		if !result.Passed {
			status = "FAIL"
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "- %s %s: evidence=%d signals=%d planner=%s family=%s duration=%dms\n", status, result.Name, result.EvidenceCount, result.RetrievalSignalCount, result.Planner, result.QueryFamily, result.DurationMS)
		if result.PlannerModel != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  planner_model: %s\n", result.PlannerModel)
		}
		if result.PlannerError != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  planner_error: %s\n", result.PlannerError)
		}
		if len(result.RetrievalLanes) > 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  retrieval_lanes: %v\n", result.RetrievalLanes)
		}
		if len(result.QueryTerms) > 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  query_terms: %v\n", result.QueryTerms)
		}
		if len(result.QueryVariants) > 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  query_variants: %v\n", result.QueryVariants)
		}
		if len(result.CitationSourceKeys) > 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  citation_source_keys (%s): %v\n", result.CitationSourceKeysMode, result.CitationSourceKeys)
		}
		for _, stage := range []struct {
			name string
			keys []string
		}{
			{name: "relevance_excluded", keys: result.EvidenceFlow.RelevanceExcludedSourceKeys},
			{name: "prompt_admitted", keys: result.EvidenceFlow.PromptAdmittedSourceKeys},
			{name: "budget_dropped", keys: result.EvidenceFlow.BudgetDroppedSourceKeys},
			{name: "answer_cited", keys: result.EvidenceFlow.AnswerCitedSourceKeys},
		} {
			if len(stage.keys) > 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s_source_keys: %v\n", stage.name, stage.keys)
			}
		}
		if len(result.TopEvidence) > 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  top_retrieval_signals:\n")
			for _, ev := range result.TopEvidence {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "    - %s score=%d signals=%v matched=%v missing=%v\n", ev.SourceKey, ev.Score, ev.Signals, ev.MatchedTerms, ev.MissingTerms)
			}
		}
		for _, failure := range result.Failures {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  failure: %s\n", failure)
		}
	}
}

func printResearchTraceDiff(cmd *cobra.Command, diff researcheval.TraceDiff) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Research trace diff: %s\n", diff.Question)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "old_source_keys: %v\n", diff.OldSourceKeys)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "new_source_keys: %v\n", diff.NewSourceKeys)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "added: %v\n", diff.Added)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "removed: %v\n", diff.Removed)
	if len(diff.Reordered) > 0 {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "reordered:\n")
		for _, row := range diff.Reordered {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "- %s: %d -> %d\n", row.SourceKey, row.OldIndex, row.NewIndex)
		}
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "propose: %s\n", diff.ProposalCommand)
}

func resolveEvalDataPath(dataDir string, value string) string {
	if filepath.IsAbs(value) {
		return value
	}
	if _, err := os.Stat(value); err == nil {
		return value
	}
	candidate := filepath.Join(dataDir, filepath.FromSlash(value))
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return value
}
