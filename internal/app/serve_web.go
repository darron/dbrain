package app

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/startuplog"
	"github.com/darron/dbrain/internal/store"
	"github.com/darron/dbrain/web"
)

func newServeWebCommand(root *rootOptions) *cobra.Command {
	var addr string

	cmd := &cobra.Command{
		Use:   "web",
		Short: "Serve the local brain over HTTP",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			startuplog.WriteVersion(cmd.ErrOrStderr())
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Web UI: http://%s\n", addr)
			return web.ServeWithOptions(cmd.Context(), cfg, addr, web.ServeOptions{
				StoreOpenOptions: store.OpenOptions{
					MigrationReporter: startuplog.MigrationReporter(cmd.ErrOrStderr()),
				},
			})
		},
	}

	cmd.Flags().StringVar(&addr, "addr", web.DefaultAddr(), "HTTP listen address")

	return cmd
}
