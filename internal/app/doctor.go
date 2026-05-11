package app

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const fullDiskAccessSettingsURL = "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"
const defaultAppleNotesDBRelPath = "Library/Group Containers/group.com.apple.notes/NoteStore.sqlite"

type doctorFullDiskAccessFlags struct {
	label        string
	binPath      string
	notesDBPath  string
	openSettings bool
	probe        bool
}

var openFullDiskAccessSettings = func(ctx context.Context) error {
	return exec.CommandContext(ctx, "open", fullDiskAccessSettingsURL).Run()
}

var runFullDiskAccessProbeBinary = func(ctx context.Context, binPath string, notesDBPath string) ([]byte, error) {
	args := []string{"--no-debug", "doctor", "full-disk-access-probe"}
	if strings.TrimSpace(notesDBPath) != "" {
		args = append(args, "--apple-notes-db", notesDBPath)
	}
	return exec.CommandContext(ctx, binPath, args...).CombinedOutput()
}

func newDoctorCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "doctor",
		Short:       "Diagnose local dbrain runtime issues",
		RunE:        helpCommand,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
	}
	cmd.AddCommand(newDoctorFullDiskAccessCommand(root), newDoctorFullDiskAccessProbeCommand())
	return cmd
}

func newDoctorFullDiskAccessCommand(root *rootOptions) *cobra.Command {
	flags := doctorFullDiskAccessFlags{
		label:        defaultLaunchdLabel,
		openSettings: true,
		probe:        true,
	}
	cmd := &cobra.Command{
		Use:         "full-disk-access",
		Short:       "Diagnose and open macOS Full Disk Access settings",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			notesPath, err := resolveDoctorAppleNotesDBPath(flags.notesDBPath)
			if err != nil {
				return err
			}
			target, source, err := resolveDoctorFullDiskAccessTarget(flags.binPath, flags.label)
			if err != nil {
				return err
			}
			resolvedTarget := resolveSymlinkOrOriginal(target)

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Full Disk Access\n")
			_, _ = fmt.Fprintf(out, "Target binary: %s\n", target)
			if resolvedTarget != target {
				_, _ = fmt.Fprintf(out, "Resolved target: %s\n", resolvedTarget)
			}
			_, _ = fmt.Fprintf(out, "Target source: %s\n", source)
			_, _ = fmt.Fprintf(out, "Launchd label: %s\n", flags.label)
			_, _ = fmt.Fprintf(out, "Launchd plist: %s\n", doctorLaunchdPlistPathOrDash(flags.label))
			_, _ = fmt.Fprintf(out, "Config path: %s\n", cfg.ConfigPath)
			_, _ = fmt.Fprintf(out, "Apple Notes probe: %s\n", notesPath)
			_, _ = fmt.Fprintln(out)
			_, _ = fmt.Fprintln(out, "What this can do:")
			_, _ = fmt.Fprintln(out, "- Open System Settings to Privacy & Security > Full Disk Access.")
			_, _ = fmt.Fprintln(out, "- Run a protected-file probe through the target dbrain binary so macOS can attribute the denied access to that binary.")
			_, _ = fmt.Fprintln(out, "- It cannot grant Full Disk Access automatically; enable the target binary in System Settings after the probe.")

			if flags.probe {
				_, _ = fmt.Fprintln(out)
				_, _ = fmt.Fprintln(out, "Probe output:")
				probeOut, probeErr := runFullDiskAccessProbeBinary(cmd.Context(), target, notesPath)
				if len(probeOut) > 0 {
					_, _ = out.Write(probeOut)
					if probeOut[len(probeOut)-1] != '\n' {
						_, _ = fmt.Fprintln(out)
					}
				}
				if probeErr != nil {
					_, _ = fmt.Fprintf(out, "Probe status: failed (%v)\n", probeErr)
					_, _ = fmt.Fprintln(out, "If the failure is an operation-not-permitted error, the binary should now be easier to find in the Full Disk Access list.")
				}
			}

			if flags.openSettings {
				_, _ = fmt.Fprintln(out)
				_, _ = fmt.Fprintln(out, "Opening Full Disk Access settings...")
				if err := openFullDiskAccessSettings(cmd.Context()); err != nil {
					return fmt.Errorf("open Full Disk Access settings: %w", err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&flags.label, "label", defaultLaunchdLabel, "launchd label to inspect when --bin is not set")
	cmd.Flags().StringVar(&flags.binPath, "bin", "", "dbrain binary to diagnose; defaults to the launchd plist binary, then current executable")
	cmd.Flags().StringVar(&flags.notesDBPath, "apple-notes-db", "", "Apple Notes NoteStore.sqlite path to probe")
	cmd.Flags().BoolVar(&flags.openSettings, "open", true, "Open macOS Full Disk Access settings")
	cmd.Flags().BoolVar(&flags.probe, "probe", true, "Run the target binary against a protected Apple Notes path")
	return cmd
}

func newDoctorFullDiskAccessProbeCommand() *cobra.Command {
	var notesDBPath string
	cmd := &cobra.Command{
		Use:         "full-disk-access-probe",
		Short:       "Probe protected macOS files for Full Disk Access diagnostics",
		Hidden:      true,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipKeepAwakeAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			notesPath, err := resolveDoctorAppleNotesDBPath(notesDBPath)
			if err != nil {
				return err
			}
			exe, _ := os.Executable()
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Probe binary: %s\n", exe)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Probe file: %s\n", notesPath)
			if err := probeProtectedFile(notesPath); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Probe status: readable")
			return nil
		},
	}
	cmd.Flags().StringVar(&notesDBPath, "apple-notes-db", "", "Apple Notes NoteStore.sqlite path to probe")
	return cmd
}

func resolveDoctorFullDiskAccessTarget(binPath string, label string) (string, string, error) {
	if strings.TrimSpace(binPath) != "" {
		abs, err := filepath.Abs(strings.TrimSpace(binPath))
		if err != nil {
			return "", "", fmt.Errorf("resolve --bin: %w", err)
		}
		return abs, "--bin", nil
	}
	if plistPath, err := launchdPlistPath(label); err == nil {
		if args, readErr := readLaunchdProgramArguments(plistPath); readErr == nil && len(args) > 0 {
			return args[0], "launchd plist", nil
		}
	}
	current, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("resolve current executable: %w", err)
	}
	abs, err := filepath.Abs(current)
	if err != nil {
		return current, "current executable", nil
	}
	return abs, "current executable", nil
}

func checkLaunchdFullDiskAccess(ctx context.Context, label string, notesDBPath string, openSettings bool, out io.Writer) error {
	target, source, err := resolveDoctorFullDiskAccessTarget("", label)
	if err != nil {
		return err
	}
	notesPath, err := resolveDoctorAppleNotesDBPath(notesDBPath)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintln(out, "Full Disk Access check")
	_, _ = fmt.Fprintf(out, "Target binary: %s\n", target)
	resolvedTarget := resolveSymlinkOrOriginal(target)
	if resolvedTarget != target {
		_, _ = fmt.Fprintf(out, "Resolved target: %s\n", resolvedTarget)
	}
	_, _ = fmt.Fprintf(out, "Target source: %s\n", source)
	_, _ = fmt.Fprintf(out, "Apple Notes probe: %s\n", notesPath)
	probeOut, probeErr := runFullDiskAccessProbeBinary(ctx, target, notesPath)
	if probeErr == nil {
		_, _ = fmt.Fprintln(out, "Full Disk Access check: ok")
		return nil
	}
	if len(probeOut) > 0 {
		_, _ = out.Write(probeOut)
		if probeOut[len(probeOut)-1] != '\n' {
			_, _ = fmt.Fprintln(out)
		}
	}
	_, _ = fmt.Fprintf(out, "Full Disk Access check: failed (%v)\n", probeErr)
	_, _ = fmt.Fprintln(out, "Enable the target binary in System Settings > Privacy & Security > Full Disk Access, then restart the service again.")
	if openSettings {
		_, _ = fmt.Fprintln(out, "Opening Full Disk Access settings...")
		if err := openFullDiskAccessSettings(ctx); err != nil {
			return fmt.Errorf("open Full Disk Access settings: %w", err)
		}
	}
	return nil
}

func readLaunchdProgramArguments(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	var sawProgramArguments bool
	var inProgramArguments bool
	var inString bool
	var args []string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "array":
				if sawProgramArguments {
					inProgramArguments = true
					sawProgramArguments = false
				}
			case "string":
				if inProgramArguments {
					inString = true
				}
			}
		case xml.EndElement:
			switch value.Name.Local {
			case "array":
				if inProgramArguments {
					return args, nil
				}
			case "string":
				inString = false
			}
		case xml.CharData:
			text := strings.TrimSpace(string(value))
			if text == "" {
				continue
			}
			if inProgramArguments && inString {
				args = append(args, text)
				continue
			}
			if text == "ProgramArguments" {
				sawProgramArguments = true
			}
		}
	}
	return nil, fmt.Errorf("ProgramArguments not found in %s", path)
}

func resolveDoctorAppleNotesDBPath(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return filepath.Abs(strings.TrimSpace(override))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("resolve home directory: empty HOME")
	}
	return filepath.Join(home, defaultAppleNotesDBRelPath), nil
}

func probeProtectedFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("probe protected file %s: %w", path, err)
	}
	return file.Close()
}

func resolveSymlinkOrOriginal(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

func doctorLaunchdPlistPathOrDash(label string) string {
	path, err := launchdPlistPath(label)
	if err != nil {
		return "-"
	}
	if _, err := os.Stat(path); err != nil {
		return path + " (not found)"
	}
	return path
}
