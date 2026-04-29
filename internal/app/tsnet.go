package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	var hostname string
	var stateDir string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:         "status",
		Short:       "Print resolved tsnet state status",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			opts, err := remote.OptionsFromRuntime(cfg)
			if err != nil {
				return err
			}
			applyTSNetStateFlagOverrides(cmd, cfg.DataDir, &opts, hostname, stateDir)

			status, err := tsnetStateStatus(opts.StateDir)
			if err != nil {
				return err
			}
			status.Hostname = opts.Hostname

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), status)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "hostname: %s\n", status.Hostname)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "state_dir: %s\n", status.StateDir)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "exists: %t\n", status.Exists)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "locked: %t\n", status.Locked)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "lock_path: %s\n", status.LockPath)
			if status.Warning != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "warning: %s\n", status.Warning)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&hostname, "tsnet-hostname", "", "Stable tailnet machine name used to derive the default state directory")
	cmd.Flags().StringVar(&stateDir, "tsnet-state-dir", "", "Durable tsnet state directory")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print status as JSON")

	return cmd
}

func newTSNetResetCommand(root *rootOptions) *cobra.Command {
	var hostname string
	var stateDir string
	var yes bool

	cmd := &cobra.Command{
		Use:         "reset",
		Short:       "Remove built-in Tailscale tsnet state",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			opts, err := remote.OptionsFromRuntime(cfg)
			if err != nil {
				return err
			}
			applyTSNetStateFlagOverrides(cmd, cfg.DataDir, &opts, hostname, stateDir)

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
				ok, err := confirmTSNetReset(cmd.InOrStdin(), cmd.OutOrStdout(), prepared)
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
					return nil
				}
			}

			if err := os.RemoveAll(prepared); err != nil {
				return fmt.Errorf("remove tsnet state dir %s: %w", prepared, err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed tsnet state directory: %s\n", prepared)
			return nil
		},
	}

	cmd.Flags().StringVar(&hostname, "tsnet-hostname", "", "Stable tailnet machine name used to derive the default state directory")
	cmd.Flags().StringVar(&stateDir, "tsnet-state-dir", "", "Durable tsnet state directory")
	cmd.Flags().BoolVar(&yes, "yes", false, "Reset without interactive confirmation")

	return cmd
}

type tsnetStateInfo struct {
	Hostname string `json:"hostname"`
	StateDir string `json:"state_dir"`
	Exists   bool   `json:"exists"`
	Locked   bool   `json:"locked"`
	LockPath string `json:"lock_path"`
	Warning  string `json:"warning,omitempty"`
}

func tsnetStateStatus(stateDir string) (tsnetStateInfo, error) {
	resolved, err := remote.ResolveStateDir(stateDir)
	if err != nil {
		return tsnetStateInfo{}, err
	}
	info := tsnetStateInfo{
		StateDir: resolved,
		LockPath: filepath.Join(resolved, remote.StateLockName),
	}
	if remote.LooksLikeSyncedPath(resolved) {
		info.Warning = "state directory appears to be under a sync folder"
	}

	stat, err := os.Stat(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return info, nil
		}
		return tsnetStateInfo{}, err
	}
	if !stat.IsDir() {
		return tsnetStateInfo{}, fmt.Errorf("state path is not a directory: %s", resolved)
	}
	info.Exists = true

	lock, err := remote.AcquireStateLock(resolved)
	if err != nil {
		if strings.Contains(err.Error(), "already locked") {
			info.Locked = true
			return info, nil
		}
		return tsnetStateInfo{}, err
	}
	_ = lock.Close()
	return info, nil
}

func applyTSNetStateFlagOverrides(cmd *cobra.Command, dataDir string, opts *remote.Options, hostname string, stateDir string) {
	changed := cmd.Flags().Changed
	if changed("tsnet-hostname") {
		opts.Hostname = hostname
		if !changed("tsnet-state-dir") {
			opts.StateDir = filepath.Join(dataDir, "tsnet", opts.Hostname)
		}
	}
	if changed("tsnet-state-dir") {
		opts.StateDir = stateDir
	}
}

func confirmTSNetReset(in io.Reader, out io.Writer, stateDir string) (bool, error) {
	_, _ = fmt.Fprintf(out, "Remove tsnet state directory?\n")
	_, _ = fmt.Fprintf(out, "State dir: %s\n", stateDir)
	_, _ = fmt.Fprintf(out, "This will require dbrain to authenticate with Tailscale again.\n")
	_, _ = fmt.Fprintf(out, "Type 'reset' to continue: ")

	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	return strings.TrimSpace(scanner.Text()) == "reset", nil
}
