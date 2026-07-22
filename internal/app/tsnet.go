package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/remote"
	"github.com/darron/dbrain/internal/schedulerstate"
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
			return writeTSNetStatus(cmd.OutOrStdout(), status)
		},
	}

	addTSNetStateFlags(cmd, &flags)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print status as JSON")

	return cmd
}

func writeTSNetStatus(dst io.Writer, status tsnetStateInfo) error {
	if _, err := fmt.Fprintln(dst, "TSNet Node"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(dst, renderTSNetTable(dst, [][]string{
		{"Hostname", status.Hostname},
		{"State", status.State},
		{"Running", boolString(status.Running)},
		{"Reachable", boolString(status.Reachable)},
		{"State dir", status.StateDir},
		{"State exists", boolString(status.Exists)},
		{"State locked", boolString(status.Locked)},
		{"Lock path", status.LockPath},
		{"Needs login", boolString(status.NeedsLogin)},
		{"Warning", tsnetEmptyDash(status.Warning)},
	})); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(dst); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(dst, "TSNet Endpoints"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(dst, renderTSNetTable(dst, [][]string{
		{"TLS", boolString(status.TLS)},
		{"Funnel", boolString(status.Funnel)},
		{"Control URL", tsnetEmptyDash(status.ControlURL)},
		{"Cert health", tsnetEmptyDash(status.CertHealth)},
		{"Cert error", tsnetEmptyDash(status.CertError)},
		{"Tailnet IPs", tsnetEmptyDash(strings.Join(status.TailnetIPs, ", "))},
		{"Web reachable", boolString(status.WebReachable)},
		{"Web URL", tsnetEmptyDash(status.WebURL)},
		{"Web error", tsnetEmptyDash(status.WebError)},
		{"MCP reachable", boolString(status.MCPReachable)},
		{"MCP URL", tsnetEmptyDash(status.MCPURL)},
		{"MCP error", tsnetEmptyDash(status.MCPError)},
	})); err != nil {
		return err
	}
	if status.SyncAll != nil || status.SyncAllError != nil {
		if _, err := fmt.Fprintln(dst); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(dst, "Scheduled Sync All"); err != nil {
			return err
		}
		if status.SyncAll != nil {
			if _, err := fmt.Fprintln(dst, renderTSNetSchedulerTable(dst, *status.SyncAll)); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintln(dst, renderTSNetTable(dst, [][]string{
				{"Error", status.SyncAllError.Code},
				{"HTTP status", fmt.Sprintf("%d", status.SyncAllError.StatusCode)},
			})); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderTSNetTable(dst io.Writer, rows [][]string) string {
	width := tsnetStatusTableWidth(dst)
	valueWidth := width - 26
	if valueWidth < 32 {
		valueWidth = 32
	}
	return table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240"))).
		Headers("Field", "Value").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			base := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return base.Bold(true).Foreground(lipgloss.Color("39"))
			}
			if col == 0 {
				return base.Bold(true).Width(18)
			}
			return base.Width(valueWidth)
		}).
		Width(width).
		Wrap(true).
		String()
}

func tsnetStatusTableWidth(dst io.Writer) int {
	width := outputWidth(dst)
	if width > 132 {
		return 132
	}
	return width
}

func renderTSNetSchedulerTable(dst io.Writer, status schedulerstate.SyncAllStatus) string {
	now := time.Now()
	currentStarted := timeStringWithRelative(status.CurrentStartedAt, now)
	currentElapsed := "-"
	if status.Running && !status.CurrentStartedAt.IsZero() {
		currentElapsed = elapsedSinceAt(status.CurrentStartedAt, now)
	}
	return renderTSNetTable(dst, [][]string{
		{"Enabled", boolString(status.Enabled)},
		{"Running", boolString(status.Running)},
		{"Interval", tsnetEmptyDash(status.Interval)},
		{"Jitter", tsnetEmptyDash(status.Jitter)},
		{"Run on start", boolString(status.RunOnStart)},
		{"Current reason", tsnetEmptyDash(status.CurrentReason)},
		{"Current started", currentStarted},
		{"Current elapsed", currentElapsed},
		{"Last reason", tsnetEmptyDash(status.LastReason)},
		{"Last started", timeStringWithRelative(status.LastStartedAt, now)},
		{"Last finished", timeStringWithRelative(status.LastFinishedAt, now)},
		{"Last status", tsnetEmptyDash(status.LastStatus)},
		{"Last error", tsnetEmptyDash(status.LastError)},
		{"Next run", timeStringWithRelative(status.NextRunAt, now)},
	})
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func tsnetEmptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func timeString(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

func timeStringWithRelative(value time.Time, now time.Time) string {
	if value.IsZero() {
		return "-"
	}
	relative := relativeTimeString(value, now)
	if relative == "" {
		return timeString(value)
	}
	return fmt.Sprintf("%s (%s)", timeString(value), relative)
}

func relativeTimeString(value time.Time, now time.Time) string {
	if value.IsZero() || now.IsZero() {
		return ""
	}
	delta := value.Sub(now)
	suffix := "from now"
	if delta < 0 {
		suffix = "ago"
		delta = -delta
	}
	if delta < time.Minute {
		return "less than a minute " + suffix
	}

	unitValue := int((delta + 30*time.Second) / time.Minute)
	unit := "minute"
	switch {
	case unitValue < 60:
	case delta < 36*time.Hour:
		unitValue = int((delta + 30*time.Minute) / time.Hour)
		unit = "hour"
	default:
		unitValue = int((delta + 12*time.Hour) / (24 * time.Hour))
		unit = "day"
	}
	if unitValue != 1 {
		unit += "s"
	}
	return fmt.Sprintf("%d %s %s", unitValue, unit, suffix)
}

func elapsedSinceAt(value time.Time, now time.Time) string {
	elapsed := now.UTC().Sub(value.UTC()).Round(time.Second)
	if elapsed < 0 {
		return "0s"
	}
	return elapsed.String()
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
