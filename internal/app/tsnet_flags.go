package app

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/remote"
)

type tsnetStateFlags struct {
	webEnabled bool
	mcpEnabled bool
	mcpPath    string
	hostname   string
	stateDir   string
	listen     string
	tlsEnabled bool
	funnel     bool
	controlURL string
}

func defaultTSNetStateFlags() tsnetStateFlags {
	return tsnetStateFlags{
		webEnabled: true,
		mcpEnabled: true,
		mcpPath:    remote.DefaultMCPPath,
		tlsEnabled: true,
	}
}

func addTSNetStateFlags(cmd *cobra.Command, flags *tsnetStateFlags) {
	cmd.Flags().BoolVar(&flags.webEnabled, "web", true, "Configured remote web surface")
	cmd.Flags().BoolVar(&flags.mcpEnabled, "mcp", true, "Configured remote MCP surface")
	cmd.Flags().StringVar(&flags.mcpPath, "mcp-path", remote.DefaultMCPPath, "Remote MCP endpoint path")
	cmd.Flags().StringVar(&flags.hostname, "tsnet-hostname", "", "Stable tailnet machine name used to derive the default state directory")
	cmd.Flags().StringVar(&flags.stateDir, "tsnet-state-dir", "", "Durable tsnet state directory")
	cmd.Flags().StringVar(&flags.listen, "tsnet-listen", "", "Tailnet listen address")
	cmd.Flags().BoolVar(&flags.tlsEnabled, "tsnet-tls", true, "Use Tailscale HTTPS via ListenTLS")
	cmd.Flags().BoolVar(&flags.funnel, "tsnet-funnel", false, "Configured Tailscale Funnel exposure")
	cmd.Flags().StringVar(&flags.controlURL, "tsnet-control-url", "", "Experimental alternate Tailscale control server URL")
}

func applyTSNetStateFlagOverrides(cmd *cobra.Command, dataDir string, opts *remote.Options, flags tsnetStateFlags) error {
	changed := cmd.Flags().Changed
	if changed("web") {
		opts.Web = flags.webEnabled
	}
	if changed("mcp") {
		opts.MCP = flags.mcpEnabled
	}
	if changed("mcp-path") {
		opts.MCPPath = flags.mcpPath
	}
	if changed("tsnet-hostname") {
		opts.Hostname = flags.hostname
		if !changed("tsnet-state-dir") {
			opts.StateDir = filepath.Join(dataDir, "tsnet", opts.Hostname)
		}
	}
	if changed("tsnet-state-dir") {
		opts.StateDir = flags.stateDir
	}
	if changed("tsnet-listen") {
		opts.Listen = flags.listen
	}
	if changed("tsnet-tls") {
		opts.TLS = flags.tlsEnabled
		if !changed("tsnet-listen") {
			opts.Listen = ""
		}
	}
	if changed("tsnet-funnel") {
		opts.Funnel = flags.funnel
		if !changed("tsnet-listen") {
			opts.Listen = ""
		}
	}
	if changed("tsnet-control-url") {
		opts.ControlURL = flags.controlURL
	}
	finalizeRemoteServeDefaults(dataDir, opts)
	cleaned, err := remote.ValidateMCPPath(opts.MCPPath)
	if err != nil {
		return err
	}
	opts.MCPPath = cleaned
	return remote.ValidateFunnelOptions(*opts)
}
