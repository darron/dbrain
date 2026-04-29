package app

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/version"
)

func newVersionCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print build and version information",
		Args:  cobra.NoArgs,
		Annotations: map[string]string{
			skipKeepAwakeAnnotation: "true",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			current := version.Current()
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), current)
			}
			return printVersionDetails(cmd.OutOrStdout(), current)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Write JSON to stdout")
	return cmd
}

func printVersionDetails(writer io.Writer, current version.Details) error {
	lines := []string{
		fmt.Sprintf("commit: %s", current.Commit),
		fmt.Sprintf("short: %s", current.Short),
		fmt.Sprintf("build_time: %s", current.BuildTime),
		fmt.Sprintf("git_status: %s", current.GitStatus),
		fmt.Sprintf("go_version: %s", current.GoVersion),
		fmt.Sprintf("git_version: %s", current.GitVersion),
		fmt.Sprintf("build_platform: %s", current.BuildPlatform),
	}
	if current.ModulePath != "" {
		lines = append(lines, fmt.Sprintf("module_path: %s", current.ModulePath))
	}
	if current.ModuleVersion != "" {
		lines = append(lines, fmt.Sprintf("module_version: %s", current.ModuleVersion))
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(writer, line); err != nil {
			return err
		}
	}
	return nil
}
