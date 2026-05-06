package app

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/remote"
)

func newTSNetCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tsnet",
		Short: "Inspect or reset built-in Tailscale state",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newTSNetStatusCommand(root), newTSNetResetCommand(root))
	return cmd
}

func newTSNetStatusCommand(root *rootOptions) *cobra.Command {
	flags := defaultTSNetStateFlags()
	var jsonOut bool

	cmd := &cobra.Command{
		Use:         "status",
		Short:       "Print resolved tsnet state status",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			opts, err := remote.OptionsFromRuntime(cfg)
			if err != nil {
				return err
			}
			if err := applyTSNetStateFlagOverrides(cmd, cfg.DataDir, &opts, flags); err != nil {
				return err
			}

			status, err := tsnetStateStatus(cmd.Context(), opts)
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), status)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "hostname: %s\n", status.Hostname)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "state_dir: %s\n", status.StateDir)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "exists: %t\n", status.Exists)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "locked: %t\n", status.Locked)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "running: %t\n", status.Running)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "reachable: %t\n", status.Reachable)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "web_reachable: %t\n", status.WebReachable)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "mcp_reachable: %t\n", status.MCPReachable)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "tls: %t\n", status.TLS)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "control_url: %s\n", status.ControlURL)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "state: %s\n", status.State)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "cert_health: %s\n", status.CertHealth)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "needs_login: %t\n", status.NeedsLogin)
			if len(status.TailnetIPs) > 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "tailnet_ips: %s\n", strings.Join(status.TailnetIPs, ", "))
			}
			if status.WebURL != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "web_url: %s\n", status.WebURL)
			}
			if status.MCPURL != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "mcp_url: %s\n", status.MCPURL)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "lock_path: %s\n", status.LockPath)
			if status.WebError != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "web_error: %s\n", status.WebError)
			}
			if status.MCPError != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "mcp_error: %s\n", status.MCPError)
			}
			if status.CertError != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "cert_error: %s\n", status.CertError)
			}
			if status.Warning != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "warning: %s\n", status.Warning)
			}
			return nil
		},
	}

	addTSNetStateFlags(cmd, &flags)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print status as JSON")

	return cmd
}

func newTSNetResetCommand(root *rootOptions) *cobra.Command {
	flags := defaultTSNetStateFlags()
	var yes bool

	cmd := &cobra.Command{
		Use:         "reset",
		Short:       "Remove built-in Tailscale tsnet state",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			opts, err := remote.OptionsFromRuntime(cfg)
			if err != nil {
				return err
			}
			if err := applyTSNetStateFlagOverrides(cmd, cfg.DataDir, &opts, flags); err != nil {
				return err
			}

			resolved, err := remote.ResolveStateDir(opts.StateDir)
			if err != nil {
				return err
			}
			info, err := os.Stat(resolved)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "State directory does not exist: %s\n", resolved)
					return nil
				}
				return err
			}
			if !info.IsDir() {
				return fmt.Errorf("state path is not a directory: %s", resolved)
			}

			prepared, err := remote.PrepareStateDir(resolved)
			if err != nil {
				return err
			}
			lock, err := remote.AcquireStateLock(prepared)
			if err != nil {
				return err
			}
			defer func() {
				_ = lock.Close()
			}()

			if !yes {
				ok, err := confirmTSNetReset(cmd.InOrStdin(), cmd.OutOrStdout(), opts.Hostname, prepared)
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
					return nil
				}
			}

			// The advisory lock proves no running dbrain owns the current state
			// directory. On Unix, RemoveAll unlinks the locked file while this FD
			// stays open; a later process may recreate a fresh state dir/lock.
			if err := os.RemoveAll(prepared); err != nil {
				return fmt.Errorf("remove tsnet state dir %s: %w", prepared, err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed tsnet state directory: %s\n", prepared)
			return nil
		},
	}

	addTSNetStateFlags(cmd, &flags)
	cmd.Flags().BoolVar(&yes, "yes", false, "Reset without interactive confirmation")

	return cmd
}
