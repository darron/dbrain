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
	"github.com/darron/dbrain/internal/startuplog"
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
	var tsnetFunnel bool
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
read-only MCP directly from the built-in tailnet node; add --tsnet-funnel only
when you intentionally want public Tailscale Funnel exposure.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			switch strings.ToLower(strings.TrimSpace(transport)) {
			case "", "stdio":
				auditDependencies, err := resolveMCPAuditServerDependencies(cmd.Context(), cfg, auditMetaForInvocation(root))
				if err != nil {
					return fmt.Errorf("configure MCP audit: %w", err)
				}
				return mcpserver.Serve(cmd.Context(), cfg, os.Stdin, os.Stdout, auditDependencies)
			case "http", "streamable-http", "streamable_http":
				var auditDependencies mcpserver.ServerDependencies
				if mcpserver.AuthEnabled(cfg) {
					auditDependencies, err = resolveMCPAuditServerDependencies(cmd.Context(), cfg, auditMetaForInvocation(root))
					if err != nil {
						return fmt.Errorf("configure MCP audit: %w", err)
					}
				}
				resolvedAddr := addr
				if strings.TrimSpace(resolvedAddr) == "" {
					resolvedAddr = mcpserver.DefaultHTTPAddr
				}
				resolvedPath := path
				if strings.TrimSpace(resolvedPath) == "" {
					resolvedPath = mcpserver.DefaultHTTPPath
				}
				startuplog.WriteVersion(cmd.ErrOrStderr())
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "MCP HTTP: http://%s%s\n", resolvedAddr, resolvedPath)
				return mcpserver.ServeHTTP(cmd.Context(), cfg, mcpserver.HTTPOptions{
					Addr:             resolvedAddr,
					Path:             resolvedPath,
					AllowedOrigins:   allowOrigins,
					StoreOpenOptions: interactiveStoreOpenOptions(),
				}, auditDependencies)
			case "tsnet":
				var auditDependencies mcpserver.ServerDependencies
				if mcpserver.AuthEnabled(cfg) {
					auditDependencies, err = resolveMCPAuditServerDependencies(cmd.Context(), cfg, auditMetaForInvocation(root))
					if err != nil {
						return fmt.Errorf("configure MCP audit: %w", err)
					}
				}
				opts, err := remote.OptionsFromRuntime(cfg)
				if err != nil {
					return err
				}
				opts.Web = false
				opts.MCP = true
				remote.SetMCPDependencies(&opts, auditDependencies)
				applyServeMCPRemoteFlagOverrides(cmd, cfg.DataDir, &opts, serveMCPRemoteFlags{
					path:               path,
					hostname:           tsnetHostname,
					stateDir:           tsnetStateDir,
					listen:             tsnetListen,
					tlsEnabled:         tsnetTLS,
					funnelEnabled:      tsnetFunnel,
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
	cmd.Flags().StringVar(&path, "mcp-path", mcpserver.DefaultHTTPPath, "Alias for --path; if both are set, the last flag wins")
	cmd.Flags().StringArrayVar(&allowOrigins, "allow-origin", nil, "additional allowed HTTP Origin for --transport http; repeatable, exact match, use only with trusted origins")
	cmd.Flags().StringVar(&tsnetHostname, "tsnet-hostname", remote.DefaultHostname, "Stable tailnet machine name for --transport tsnet")
	cmd.Flags().StringVar(&tsnetStateDir, "tsnet-state-dir", "", "Durable tsnet state directory for --transport tsnet")
	cmd.Flags().StringVar(&tsnetListen, "tsnet-listen", "", "Tailnet listen address for --transport tsnet")
	cmd.Flags().BoolVar(&tsnetTLS, "tsnet-tls", true, "Use Tailscale HTTPS via ListenTLS for --transport tsnet")
	cmd.Flags().BoolVar(&tsnetFunnel, "tsnet-funnel", false, "Expose --transport tsnet through Tailscale Funnel")
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
	funnelEnabled      bool
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
	if changed("path") || changed("mcp-path") {
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
	if changed("tsnet-funnel") {
		opts.Funnel = flags.funnelEnabled
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
