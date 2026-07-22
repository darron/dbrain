package app

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/store"
)

func newStatsActivityCommand(root *rootOptions) *cobra.Command {
	var window time.Duration
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Show recent database write activity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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

			stats, err := st.Activity(cmd.Context(), time.Now().UTC(), window)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}
			return writeActivityStats(cmd.OutOrStdout(), stats)
		},
	}

	cmd.Flags().DurationVar(&window, "window", 15*time.Minute, "Recent activity window")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print activity as JSON")
	return cmd
}
