package app

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const defaultLaunchdLabel = "com.darron.dbrain"

type launchdInstallFlags struct {
	label   string
	binPath string
	noStart bool
}

var runLaunchctl = func(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "launchctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func newLaunchdCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "launchd",
		Short:       "Install or print a macOS launchd service for dbrain",
		RunE:        helpCommand,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
	}
	cmd.AddCommand(newLaunchdPlistCommand(root), newLaunchdInstallCommand(root), newLaunchdRestartCommand(), newLaunchdUninstallCommand())
	return cmd
}

func newLaunchdPlistCommand(root *rootOptions) *cobra.Command {
	flags := launchdInstallFlags{label: defaultLaunchdLabel}
	cmd := &cobra.Command{
		Use:         "plist",
		Short:       "Print the launchd plist for serve remote",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			binPath, err := resolveLaunchdBinPath(flags.binPath)
			if err != nil {
				return err
			}
			plist, err := renderLaunchdPlist(launchdPlistOptions{
				Label:   flags.label,
				BinPath: binPath,
				Args:    launchdConfigArgs(root),
				OutPath: filepath.Join(cfg.LogDir, "launchd.out.log"),
				ErrPath: filepath.Join(cfg.LogDir, "launchd.err.log"),
			})
			if err != nil {
				return err
			}
			_, _ = cmd.OutOrStdout().Write(plist)
			return nil
		},
	}
	addLaunchdCommonFlags(cmd, &flags)
	return cmd
}

func newLaunchdInstallCommand(root *rootOptions) *cobra.Command {
	flags := launchdInstallFlags{label: defaultLaunchdLabel}
	cmd := &cobra.Command{
		Use:         "install",
		Short:       "Write and load a launchd service for serve remote",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			binPath, err := resolveLaunchdBinPath(flags.binPath)
			if err != nil {
				return err
			}
			plistPath, err := launchdPlistPath(flags.label)
			if err != nil {
				return err
			}
			plist, err := renderLaunchdPlist(launchdPlistOptions{
				Label:   flags.label,
				BinPath: binPath,
				Args:    launchdConfigArgs(root),
				OutPath: filepath.Join(cfg.LogDir, "launchd.out.log"),
				ErrPath: filepath.Join(cfg.LogDir, "launchd.err.log"),
			})
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
				return fmt.Errorf("create launch agents dir: %w", err)
			}
			if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
				return fmt.Errorf("create log dir: %w", err)
			}
			if err := os.WriteFile(plistPath, plist, 0o644); err != nil {
				return fmt.Errorf("write plist %s: %w", plistPath, err)
			}
			domain := launchdDomain()
			target := domain + "/" + flags.label
			if !flags.noStart {
				_ = runLaunchctl(cmd.Context(), "bootout", domain, plistPath)
				if err := runLaunchctl(cmd.Context(), "bootstrap", domain, plistPath); err != nil {
					return err
				}
				if err := runLaunchctl(cmd.Context(), "enable", target); err != nil {
					return err
				}
				if err := runLaunchctl(cmd.Context(), "kickstart", "-k", target); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Wrote launchd plist: %s\n", plistPath)
			if flags.noStart {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Not loaded because --no-start was set.\n")
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Loaded launchd service: %s\n", target)
			}
			return nil
		},
	}
	addLaunchdCommonFlags(cmd, &flags)
	cmd.Flags().BoolVar(&flags.noStart, "no-start", false, "Write the plist without loading or starting it")
	return cmd
}

func newLaunchdUninstallCommand() *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:         "uninstall",
		Short:       "Unload and remove the dbrain launchd service",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			plistPath, err := launchdPlistPath(label)
			if err != nil {
				return err
			}
			domain := launchdDomain()
			_ = runLaunchctl(cmd.Context(), "bootout", domain, plistPath)
			if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove plist %s: %w", plistPath, err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed launchd plist: %s\n", plistPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&label, "label", defaultLaunchdLabel, "launchd label")
	return cmd
}

func newLaunchdRestartCommand() *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:         "restart",
		Short:       "Restart the loaded dbrain launchd service",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(label) == "" {
				return fmt.Errorf("launchd label is required")
			}
			target := launchdDomain() + "/" + label
			if err := runLaunchctl(cmd.Context(), "kickstart", "-k", target); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Restarted launchd service: %s\n", target)
			return nil
		},
	}
	cmd.Flags().StringVar(&label, "label", defaultLaunchdLabel, "launchd label")
	return cmd
}

func addLaunchdCommonFlags(cmd *cobra.Command, flags *launchdInstallFlags) {
	cmd.Flags().StringVar(&flags.label, "label", defaultLaunchdLabel, "launchd label")
	cmd.Flags().StringVar(&flags.binPath, "bin", "", "dbrain binary path; defaults to dbrain from PATH")
}

type launchdPlistOptions struct {
	Label   string
	BinPath string
	Args    []string
	OutPath string
	ErrPath string
}

func renderLaunchdPlist(opts launchdPlistOptions) ([]byte, error) {
	if strings.TrimSpace(opts.Label) == "" {
		return nil, fmt.Errorf("launchd label is required")
	}
	if strings.TrimSpace(opts.BinPath) == "" {
		return nil, fmt.Errorf("dbrain binary path is required")
	}
	args := []string{opts.BinPath}
	args = append(args, opts.Args...)
	args = append(args, "serve", "remote")

	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n")
	b.WriteString("<dict>\n")
	writePlistString(&b, "Label", opts.Label)
	b.WriteString("  <key>ProgramArguments</key>\n")
	b.WriteString("  <array>\n")
	for _, arg := range args {
		b.WriteString("    <string>")
		_ = xml.EscapeText(&b, []byte(arg))
		b.WriteString("</string>\n")
	}
	b.WriteString("  </array>\n")
	writePlistBool(&b, "RunAtLoad", true)
	writePlistBool(&b, "KeepAlive", true)
	writePlistString(&b, "StandardOutPath", opts.OutPath)
	writePlistString(&b, "StandardErrorPath", opts.ErrPath)
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes(), nil
}

func writePlistString(b *bytes.Buffer, key string, value string) {
	b.WriteString("  <key>")
	_ = xml.EscapeText(b, []byte(key))
	b.WriteString("</key>\n  <string>")
	_ = xml.EscapeText(b, []byte(value))
	b.WriteString("</string>\n")
}

func writePlistBool(b *bytes.Buffer, key string, value bool) {
	b.WriteString("  <key>")
	_ = xml.EscapeText(b, []byte(key))
	if value {
		b.WriteString("</key>\n  <true/>\n")
		return
	}
	b.WriteString("</key>\n  <false/>\n")
}

func resolveLaunchdBinPath(value string) (string, error) {
	if strings.TrimSpace(value) != "" {
		return filepath.Abs(value)
	}
	path, err := exec.LookPath("dbrain")
	if err == nil {
		return filepath.Abs(path)
	}
	current, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve dbrain binary path: %w", err)
	}
	return filepath.Abs(current)
}

func launchdConfigArgs(root *rootOptions) []string {
	if root == nil {
		return nil
	}
	if value := strings.TrimSpace(root.configFile); value != "" {
		return []string{"--config-file", absOrOriginal(value)}
	}
	if value := strings.TrimSpace(root.root); value != "" {
		return []string{"--root", absOrOriginal(value)}
	}
	if value := strings.TrimSpace(os.Getenv(configFileEnvVar)); value != "" {
		return []string{"--config-file", absOrOriginal(value)}
	}
	if value := strings.TrimSpace(os.Getenv(rootEnvVar)); value != "" {
		return []string{"--root", absOrOriginal(value)}
	}
	return nil
}

func absOrOriginal(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func launchdPlistPath(label string) (string, error) {
	if strings.TrimSpace(label) == "" {
		return "", fmt.Errorf("launchd label is required")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

func launchdDomain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}
