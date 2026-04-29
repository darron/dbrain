package app

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/mcpeval"
	"github.com/darron/dbrain/internal/store"
)

func newEvalCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Run local retrieval quality checks",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newEvalMCPCommand(root))
	return cmd
}

func newEvalMCPCommand(root *rootOptions) *cobra.Command {
	var casesPath string
	var writeExamplePath string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Evaluate the retrieval path used by MCP research tools",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if writeExamplePath != "" {
				return writeMCPEvalExample(cmd, writeExamplePath)
			}
			if casesPath == "" {
				return fmt.Errorf("--file is required unless --write-example is set")
			}

			cfg, err := loadConfig(root.root)
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

			cases, err := mcpeval.LoadCases(casesPath)
			if err != nil {
				return err
			}
			report, err := mcpeval.Run(cmd.Context(), cfg, st, mcpeval.Options{Cases: cases})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), report)
			}
			printMCPEvalReport(cmd, report)
			if report.Failed > 0 {
				return fmt.Errorf("mcp eval failed: %d failed, %d passed", report.Failed, report.Passed)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&casesPath, "file", "", "JSON eval cases file")
	cmd.Flags().StringVar(&writeExamplePath, "write-example", "", "Write an example JSON eval file to this path, or '-' for stdout")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print eval report as JSON")
	return cmd
}

func writeMCPEvalExample(cmd *cobra.Command, path string) error {
	data, err := json.MarshalIndent(mcpeval.ExampleCases(), "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if path == "-" {
		_, _ = cmd.OutOrStdout().Write(data)
		return nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write example eval file %s: %w", path, err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Wrote example eval file: %s\n", path)
	return nil
}

func printMCPEvalReport(cmd *cobra.Command, report mcpeval.Report) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "MCP eval: %d passed, %d failed (%dms)\n", report.Passed, report.Failed, report.DurationMS)
	for _, result := range report.Cases {
		status := "PASS"
		if !result.Passed {
			status = "FAIL"
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "- %s %s: evidence=%d duration=%dms\n", status, result.Name, result.EvidenceCount, result.DurationMS)
		if len(result.SourceKeys) > 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  source_keys: %v\n", result.SourceKeys)
		}
		for _, failure := range result.Failures {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  failure: %s\n", failure)
		}
	}
}
