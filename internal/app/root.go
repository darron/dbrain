package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

const defaultCLIProvider = "codex"
const skipKeepAwakeAnnotation = "dbrain.skip_keep_awake"

type rootOptions struct {
	root         string
	caffeinate   bool
	noCaffeinate bool
	debug        bool
	noDebug      bool
}

func Run(ctx context.Context, args []string) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := NewRootCommand()
	cmd.SetArgs(args)
	cmd.SetIn(os.Stdin)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	return cmd.ExecuteContext(ctx)
}

func NewRootCommand() *cobra.Command {
	opts := &rootOptions{}

	rootCmd := &cobra.Command{
		Use:           "dbrain",
		Short:         "Local-first second-brain tooling",
		Long:          "Local-first second-brain tooling for importing, enriching, searching, and serving a personal research corpus.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			debug := commandDebugEnabled(cmd)
			if cmd.HasAvailableSubCommands() {
				return nil
			}
			if cmd.Annotations[skipKeepAwakeAnnotation] == "true" {
				return nil
			}
			if opts.noCaffeinate {
				logKeepAwakeStatus(cmd, debug, "disabled via --no-caffeinate")
				return nil
			}
			if !opts.caffeinate && !keepAwakeAvailable() {
				logKeepAwakeStatus(cmd, debug, "unavailable")
				return nil
			}
			if err := startKeepAwake(os.Getpid()); err != nil {
				return err
			}
			logKeepAwakeStatus(cmd, debug, fmt.Sprintf("started for pid %d", os.Getpid()))
			return nil
		},
		RunE: helpCommand,
	}
	rootCmd.PersistentFlags().StringVar(&opts.root, "root", "", "Brain root directory override (defaults to DBRAIN_ROOT, then ~/.config/dbrain and ~/.local/share/dbrain)")
	rootCmd.PersistentFlags().BoolVar(&opts.caffeinate, "caffeinate", false, "Force keep-awake behavior while the command is running")
	rootCmd.PersistentFlags().BoolVar(&opts.noCaffeinate, "no-caffeinate", false, "Disable automatic keep-awake behavior for this command")
	rootCmd.PersistentFlags().BoolVar(&opts.debug, "debug", true, "Enable structured debug logging to stderr")
	rootCmd.PersistentFlags().BoolVar(&opts.noDebug, "no-debug", false, "Disable structured debug logging to stderr")

	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Import source data into the brain",
		RunE:  helpCommand,
	}
	importCmd.AddCommand(newImportXBookmarksCommand(opts), newImportGitHubCommand(opts), newImportYouTubeCommand(opts), newImportAppleNotesCommand(opts))

	extractCmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract and summarize linked sources",
		RunE:  helpCommand,
	}
	extractCmd.AddCommand(newExtractLinksCommand(opts), newExtractSourcesCommand(opts))

	hydrateCmd := &cobra.Command{
		Use:   "hydrate",
		Short: "Hydrate canonical source data",
		RunE:  helpCommand,
	}
	hydrateCmd.AddCommand(newHydrateXCommand(opts))

	repairCmd := &cobra.Command{
		Use:   "repair",
		Short: "Repair derived local artifacts",
		RunE:  helpCommand,
	}
	repairCmd.AddCommand(newRepairNotesCommand(opts), newRepairFTSCommand(opts), newRepairSourcesCommand(opts))

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve local interfaces",
		RunE:  helpCommand,
	}
	serveCmd.AddCommand(newServeMCPCommand(opts), newServeRemoteCommand(opts), newServeWebCommand(opts))

	rootCmd.AddCommand(
		newArchiveCommand(opts),
		newConfigCommand(opts),
		newSQLiteCommand(opts),
		newTSNetCommand(opts),
		importCmd,
		newSyncCommand(opts),
		newEvalCommand(opts),
		newEntityCommand(opts),
		newTopicCommand(opts),
		newWorkerCommand(opts),
		newLinkCommand(opts),
		extractCmd,
		hydrateCmd,
		newTranscribeCommand(opts),
		repairCmd,
		serveCmd,
		newOCRCommand(opts),
		newStatsCommand(opts),
		newResearchCommand(opts),
		newSearchCommand(opts),
		newGetCommand(opts),
		newCategorizeCommand(opts),
		newVersionCommand(),
	)

	applyEnvHelpTemplate(rootCmd)

	return rootCmd
}

func applyEnvHelpTemplate(cmd *cobra.Command) {
	template := cmd.HelpTemplate()
	if !strings.Contains(template, "Environment:") {
		template += `
Environment:
  --root wins over DBRAIN_ROOT.
  Defaults: config in ~/.config/dbrain, state in ~/.local/share/dbrain.
  Runtime values resolve from shell env, then .envrc/.env, then config.yaml.
  Run "dbrain config env" for the full environment/config key table.
`
	}
	cmd.SetHelpTemplate(template)
	for _, child := range cmd.Commands() {
		applyEnvHelpTemplate(child)
	}
}

func helpCommand(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}

func commandDebugEnabled(cmd *cobra.Command) bool {
	disabled, err := cmd.Flags().GetBool("no-debug")
	if err == nil && disabled {
		return false
	}
	value, err := cmd.Flags().GetBool("debug")
	return err == nil && value
}

func logKeepAwakeStatus(cmd *cobra.Command, debug bool, status string) {
	if !debug {
		return
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "keep-awake: %s\n", status)
}
