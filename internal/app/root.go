package app

import (
	"context"
	"os"

	"github.com/spf13/cobra"
)

type rootOptions struct {
	root string
}

func Run(ctx context.Context, args []string) error {
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
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          helpCommand,
	}
	rootCmd.PersistentFlags().StringVar(&opts.root, "root", ".", "Brain root directory")

	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Import source data into the brain",
		RunE:  helpCommand,
	}
	importCmd.AddCommand(newImportFTCommand(opts), newImportYouTubeCommand(opts))

	extractCmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract and summarize linked sources",
		RunE:  helpCommand,
	}
	extractCmd.AddCommand(newExtractLinksCommand(opts))

	hydrateCmd := &cobra.Command{
		Use:   "hydrate",
		Short: "Hydrate canonical source data",
		RunE:  helpCommand,
	}
	hydrateCmd.AddCommand(newHydrateXCommand(opts))

	rootCmd.AddCommand(
		importCmd,
		extractCmd,
		hydrateCmd,
		newSearchCommand(opts),
		newGetCommand(opts),
	)

	return rootCmd
}

func helpCommand(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}
