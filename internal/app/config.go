package app

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

func newConfigCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show active configuration and storage paths",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newConfigPathsCommand(root), newConfigEnvCommand())
	return cmd
}

func newConfigPathsCommand(root *rootOptions) *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:         "paths",
		Short:       "Print active config, data, cache, log, and vault paths",
		Long:        "Print the active config, categories, data, database, vault, media, temp, cache, and log paths after resolving --config-file, --root, DBRAIN_CONFIG_FILE, DBRAIN_ROOT, and XDG defaults.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}

			paths := map[string]string{
				"root_dir":        cfg.RootDir,
				"config_dir":      cfg.ConfigDir,
				"config_file":     cfg.ConfigPath,
				"categories_file": cfg.CategoriesPath,
				"data_dir":        cfg.DataDir,
				"database":        cfg.DBPath,
				"vault_dir":       cfg.VaultDir,
				"okf_dir":         cfg.OKFDir,
				"media_dir":       cfg.MediaDir,
				"temp_dir":        cfg.TempDir,
				"cache_dir":       cfg.CacheDir,
				"log_dir":         cfg.LogDir,
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), paths)
			}

			for _, key := range []string{"root_dir", "config_dir", "config_file", "categories_file", "data_dir", "database", "vault_dir", "okf_dir", "media_dir", "temp_dir", "cache_dir", "log_dir"} {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", key, paths[key])
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print paths as JSON")

	return cmd
}

func newConfigEnvCommand() *cobra.Command {
	var jsonOut bool
	var markdownOut bool

	cmd := &cobra.Command{
		Use:         "env",
		Short:       "Print supported environment variables and config.yaml keys",
		Long:        "Print the supported environment variables, matching config.yaml keys, defaults, and purpose. Runtime values resolve from shell environment, then .envrc/.env, then config.yaml.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			specs := configEnvSpecs()
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), specs)
			}
			if markdownOut {
				writeEnvMarkdownTable(cmd.OutOrStdout(), specs)
				return nil
			}

			writeEnvPrettyTable(cmd.OutOrStdout(), specs)
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print supported env/config keys as JSON")
	cmd.Flags().BoolVar(&markdownOut, "markdown", false, "Print supported env/config keys as a Markdown table")

	return cmd
}

func writeEnvPrettyTable(w io.Writer, specs []envSpec) {
	t := table.New().
		Headers("Environment", "config.yaml", "Default", "Purpose").
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle()).
		StyleFunc(func(row, col int) lipgloss.Style {
			style := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				style = style.Bold(true)
			}
			return style
		}).
		Width(outputWidth(w)).
		Wrap(true)

	for _, spec := range specs {
		t.Row(spec.Key, spec.ConfigPath, spec.Default, spec.Description)
	}

	_, _ = fmt.Fprintln(w, t.Render())
}

func writeEnvMarkdownTable(w io.Writer, specs []envSpec) {
	_, _ = fmt.Fprintln(w, "| Environment variable(s) | config.yaml key | Default | Purpose |")
	_, _ = fmt.Fprintln(w, "| --- | --- | --- | --- |")
	for _, spec := range specs {
		_, _ = fmt.Fprintf(w, "| `%s` | `%s` | `%s` | %s |\n",
			escapeTablePipes(spec.Key),
			escapeTablePipes(spec.ConfigPath),
			escapeTablePipes(spec.Default),
			escapeTablePipes(spec.Description),
		)
	}
}

func outputWidth(w io.Writer) int {
	if width := parseTerminalWidth(os.Getenv("COLUMNS")); width > 0 {
		return width
	}

	file, ok := w.(*os.File)
	if !ok || file == nil {
		return 160
	}
	width, _, err := term.GetSize(file.Fd())
	if err != nil || width < 80 {
		return 160
	}
	return clampTerminalWidth(width)
}

func parseTerminalWidth(value string) int {
	width, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || width < 80 {
		return 0
	}
	return clampTerminalWidth(width)
}

func clampTerminalWidth(width int) int {
	if width > 240 {
		return 240
	}
	return width
}

func escapeTablePipes(value string) string {
	return strings.ReplaceAll(value, "|", `\|`)
}
