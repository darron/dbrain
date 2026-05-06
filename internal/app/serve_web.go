package app

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/web"
)

func newServeWebCommand(root *rootOptions) *cobra.Command {
	var addr string

	cmd := &cobra.Command{
		Use:   "web",
		Short: "Serve the local brain over HTTP",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Web UI: http://%s\n", addr)
			return web.Serve(cmd.Context(), cfg, addr)
		},
	}

	cmd.Flags().StringVar(&addr, "addr", web.DefaultAddr(), "HTTP listen address")

	return cmd
}
