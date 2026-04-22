package app

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"dbrain/internal/mcpserver"
	"dbrain/web"
)

func newServeMCPCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve the local brain over MCP stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			return mcpserver.Serve(cmd.Context(), cfg, os.Stdin, os.Stdout)
		},
	}

	return cmd
}

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
