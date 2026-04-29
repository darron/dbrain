package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/mcpserver"
	"github.com/darron/dbrain/web"
)

func newServeMCPCommand(root *rootOptions) *cobra.Command {
	var transport string
	var addr string
	var path string
	var allowOrigins []string

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve the local brain over MCP",
		Long: `Serve the local brain over MCP.

The default transport is stdio for local MCP clients that launch dbrain as a
subprocess. Use --transport http to run a stateless Streamable HTTP endpoint
for remote MCP clients, usually behind Tailscale Serve.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			switch strings.ToLower(strings.TrimSpace(transport)) {
			case "", "stdio":
				return mcpserver.Serve(cmd.Context(), cfg, os.Stdin, os.Stdout)
			case "http", "streamable-http", "streamable_http":
				resolvedAddr := addr
				if strings.TrimSpace(resolvedAddr) == "" {
					resolvedAddr = mcpserver.DefaultHTTPAddr
				}
				resolvedPath := path
				if strings.TrimSpace(resolvedPath) == "" {
					resolvedPath = mcpserver.DefaultHTTPPath
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "MCP HTTP: http://%s%s\n", resolvedAddr, resolvedPath)
				return mcpserver.ServeHTTP(cmd.Context(), cfg, mcpserver.HTTPOptions{
					Addr:           resolvedAddr,
					Path:           resolvedPath,
					AllowedOrigins: allowOrigins,
				})
			default:
				return fmt.Errorf("unsupported MCP transport %q (supported: stdio, http)", transport)
			}
		},
	}

	cmd.Flags().StringVar(&transport, "transport", "stdio", "MCP transport: stdio or http")
	cmd.Flags().StringVar(&addr, "addr", mcpserver.DefaultHTTPAddr, "HTTP listen address for --transport http")
	cmd.Flags().StringVar(&path, "path", mcpserver.DefaultHTTPPath, "HTTP MCP endpoint path for --transport http")
	cmd.Flags().StringArrayVar(&allowOrigins, "allow-origin", nil, "additional allowed HTTP Origin for --transport http; repeatable, exact match, use only with trusted origins")

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
