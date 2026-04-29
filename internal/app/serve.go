package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/mcpserver"
	"github.com/darron/dbrain/internal/remote"
	"github.com/darron/dbrain/web"
)

func newServeMCPCommand(root *rootOptions) *cobra.Command {
	var transport string
	var addr string
	var path string
	var allowOrigins []string
	var tsnetHostname string
	var tsnetStateDir string
	var tsnetListen string
	var tsnetTLS bool
	var tsnetStartupTimeout time.Duration
	var tsnetAuthKey string
	var tsnetAuthKeyRef string
	var tsnetAllowSecretCommand bool
	var tsnetAdvertiseTags []string
	var tsnetControlURL string
	var tsnetVerbose bool

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve the local brain over MCP",
		Long: `Serve the local brain over MCP.

The default transport is stdio for local MCP clients that launch dbrain as a
subprocess. Use --transport http to run a stateless localhost Streamable HTTP
endpoint, usually behind Tailscale Serve. Use --transport tsnet to expose
read-only MCP directly from the built-in tailnet node.`,
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
			case "tsnet":
				opts, err := remote.OptionsFromRuntime(cfg)
				if err != nil {
					return err
				}
				opts.Web = false
				opts.MCP = true
				applyServeMCPRemoteFlagOverrides(cmd, cfg.DataDir, &opts, serveMCPRemoteFlags{
					path:               path,
					hostname:           tsnetHostname,
					stateDir:           tsnetStateDir,
					listen:             tsnetListen,
					tlsEnabled:         tsnetTLS,
					startupTimeout:     tsnetStartupTimeout,
					authKey:            tsnetAuthKey,
					authKeyRef:         tsnetAuthKeyRef,
					allowSecretCommand: tsnetAllowSecretCommand,
					advertiseTags:      tsnetAdvertiseTags,
					controlURL:         tsnetControlURL,
					verbose:            tsnetVerbose,
				})
				return remote.Serve(cmd.Context(), cfg, opts, cmd.ErrOrStderr())
			default:
				return fmt.Errorf("unsupported MCP transport %q (supported: stdio, http, tsnet)", transport)
			}
		},
	}

	cmd.Flags().StringVar(&transport, "transport", "stdio", "MCP transport: stdio, http, or tsnet")
	cmd.Flags().StringVar(&addr, "addr", mcpserver.DefaultHTTPAddr, "HTTP listen address for --transport http")
	cmd.Flags().StringVar(&path, "path", mcpserver.DefaultHTTPPath, "HTTP MCP endpoint path for --transport http or tsnet")
	cmd.Flags().StringArrayVar(&allowOrigins, "allow-origin", nil, "additional allowed HTTP Origin for --transport http; repeatable, exact match, use only with trusted origins")
	cmd.Flags().StringVar(&tsnetHostname, "tsnet-hostname", remote.DefaultHostname, "Stable tailnet machine name for --transport tsnet")
	cmd.Flags().StringVar(&tsnetStateDir, "tsnet-state-dir", "", "Durable tsnet state directory for --transport tsnet")
	cmd.Flags().StringVar(&tsnetListen, "tsnet-listen", "", "Tailnet listen address for --transport tsnet")
	cmd.Flags().BoolVar(&tsnetTLS, "tsnet-tls", true, "Use Tailscale HTTPS via ListenTLS for --transport tsnet")
	cmd.Flags().DurationVar(&tsnetStartupTimeout, "tsnet-startup-timeout", remote.DefaultStartupTimeout, "Maximum time to wait for tsnet Up(ctx)")
	cmd.Flags().StringVar(&tsnetAuthKey, "tsnet-auth-key", "", "Optional direct Tailscale auth key; prefer --tsnet-auth-key-ref")
	cmd.Flags().StringVar(&tsnetAuthKeyRef, "tsnet-auth-key-ref", "", "Typed auth key secret ref: env:, op://, or keychain://")
	cmd.Flags().BoolVar(&tsnetAllowSecretCommand, "tsnet-allow-secret-command", false, "Permit YAML-only tsnet.auth_key_command execution")
	cmd.Flags().StringSliceVar(&tsnetAdvertiseTags, "tsnet-advertise-tags", nil, "Comma-separated Tailscale tags to request")
	cmd.Flags().StringVar(&tsnetControlURL, "tsnet-control-url", "", "Experimental alternate Tailscale control server URL")
	cmd.Flags().BoolVar(&tsnetVerbose, "tsnet-verbose", false, "Enable verbose tsnet backend logs")

	return cmd
}

type serveMCPRemoteFlags struct {
	path               string
	hostname           string
	stateDir           string
	listen             string
	tlsEnabled         bool
	startupTimeout     time.Duration
	authKey            string
	authKeyRef         string
	allowSecretCommand bool
	advertiseTags      []string
	controlURL         string
	verbose            bool
}

func applyServeMCPRemoteFlagOverrides(cmd *cobra.Command, dataDir string, opts *remote.Options, flags serveMCPRemoteFlags) {
	changed := cmd.Flags().Changed
	if changed("path") {
		opts.MCPPath = flags.path
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
	if changed("tsnet-startup-timeout") {
		opts.StartupTimeout = flags.startupTimeout
	}
	if changed("tsnet-auth-key") {
		opts.AuthKey = flags.authKey
	}
	if changed("tsnet-auth-key-ref") {
		opts.AuthKeyRef = flags.authKeyRef
	}
	if changed("tsnet-allow-secret-command") {
		opts.AllowSecretCommand = flags.allowSecretCommand
	}
	if changed("tsnet-advertise-tags") {
		opts.AdvertiseTags = flags.advertiseTags
	}
	if changed("tsnet-control-url") {
		opts.ControlURL = flags.controlURL
	}
	if changed("tsnet-verbose") {
		opts.Verbose = flags.verbose
	}
	finalizeRemoteServeDefaults(dataDir, opts)
}

func newServeRemoteCommand(root *rootOptions) *cobra.Command {
	var webEnabled bool
	var mcpEnabled bool
	var mcpPath string
	var hostname string
	var stateDir string
	var listen string
	var tlsEnabled bool
	var startupTimeout time.Duration
	var authKey string
	var authKeyRef string
	var allowSecretCommand bool
	var advertiseTags []string
	var controlURL string
	var verbose bool

	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Serve web and MCP on a built-in Tailscale tsnet node",
		Long: `Serve web and/or MCP on a built-in Tailscale tsnet node.

Remote web is the full read/write web UI. MCP remains read-only. Tailscale ACLs
govern who can reach the node.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			opts, err := remote.OptionsFromRuntime(cfg)
			if err != nil {
				return err
			}
			applyServeRemoteFlagOverrides(cmd, cfg.DataDir, &opts, serveRemoteFlags{
				webEnabled:         webEnabled,
				mcpEnabled:         mcpEnabled,
				mcpPath:            mcpPath,
				hostname:           hostname,
				stateDir:           stateDir,
				listen:             listen,
				tlsEnabled:         tlsEnabled,
				startupTimeout:     startupTimeout,
				authKey:            authKey,
				authKeyRef:         authKeyRef,
				allowSecretCommand: allowSecretCommand,
				advertiseTags:      advertiseTags,
				controlURL:         controlURL,
				verbose:            verbose,
			})
			return remote.Serve(cmd.Context(), cfg, opts, cmd.ErrOrStderr())
		},
	}

	cmd.Flags().BoolVar(&webEnabled, "web", true, "Mount the read/write web UI at /")
	cmd.Flags().BoolVar(&mcpEnabled, "mcp", true, "Mount read-only MCP Streamable HTTP at /mcp")
	cmd.Flags().StringVar(&mcpPath, "mcp-path", remote.DefaultMCPPath, "Remote MCP endpoint path")
	cmd.Flags().StringVar(&hostname, "tsnet-hostname", remote.DefaultHostname, "Stable tailnet machine name")
	cmd.Flags().StringVar(&stateDir, "tsnet-state-dir", "", "Durable tsnet state directory (default: <data_dir>/tsnet/<hostname>)")
	cmd.Flags().StringVar(&listen, "tsnet-listen", "", "Tailnet listen address (default: :443 with TLS, :80 without TLS)")
	cmd.Flags().BoolVar(&tlsEnabled, "tsnet-tls", true, "Use Tailscale HTTPS via ListenTLS")
	cmd.Flags().DurationVar(&startupTimeout, "tsnet-startup-timeout", remote.DefaultStartupTimeout, "Maximum time to wait for tsnet Up(ctx)")
	cmd.Flags().StringVar(&authKey, "tsnet-auth-key", "", "Optional direct Tailscale auth key; prefer --tsnet-auth-key-ref")
	cmd.Flags().StringVar(&authKeyRef, "tsnet-auth-key-ref", "", "Typed auth key secret ref: env:, op://, or keychain://")
	cmd.Flags().BoolVar(&allowSecretCommand, "tsnet-allow-secret-command", false, "Permit YAML-only tsnet.auth_key_command execution")
	cmd.Flags().StringSliceVar(&advertiseTags, "tsnet-advertise-tags", nil, "Comma-separated Tailscale tags to request")
	cmd.Flags().StringVar(&controlURL, "tsnet-control-url", "", "Experimental alternate Tailscale control server URL")
	cmd.Flags().BoolVar(&verbose, "tsnet-verbose", false, "Enable verbose tsnet backend logs")

	return cmd
}

type serveRemoteFlags struct {
	webEnabled         bool
	mcpEnabled         bool
	mcpPath            string
	hostname           string
	stateDir           string
	listen             string
	tlsEnabled         bool
	startupTimeout     time.Duration
	authKey            string
	authKeyRef         string
	allowSecretCommand bool
	advertiseTags      []string
	controlURL         string
	verbose            bool
}

func applyServeRemoteFlagOverrides(cmd *cobra.Command, dataDir string, opts *remote.Options, flags serveRemoteFlags) {
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
	if changed("tsnet-startup-timeout") {
		opts.StartupTimeout = flags.startupTimeout
	}
	if changed("tsnet-auth-key") {
		opts.AuthKey = flags.authKey
	}
	if changed("tsnet-auth-key-ref") {
		opts.AuthKeyRef = flags.authKeyRef
	}
	if changed("tsnet-allow-secret-command") {
		opts.AllowSecretCommand = flags.allowSecretCommand
	}
	if changed("tsnet-advertise-tags") {
		opts.AdvertiseTags = flags.advertiseTags
	}
	if changed("tsnet-control-url") {
		opts.ControlURL = flags.controlURL
	}
	if changed("tsnet-verbose") {
		opts.Verbose = flags.verbose
	}
	finalizeRemoteServeDefaults(dataDir, opts)
}

func finalizeRemoteServeDefaults(dataDir string, opts *remote.Options) {
	if opts.StateDir == "" {
		opts.StateDir = filepath.Join(dataDir, "tsnet", opts.Hostname)
	}
	if opts.Listen == "" {
		if opts.TLS {
			opts.Listen = ":443"
		} else {
			opts.Listen = ":80"
		}
	}
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
