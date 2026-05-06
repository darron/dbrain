package app

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/applenotes"
)

func newImportAppleNotesProbeCommand(root *rootOptions) *cobra.Command {
	var dbPath string
	var snapshotDir string
	var keepSnapshot bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "probe",
		Short: "Probe Apple Notes database access and schema without decoding note bodies",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}

			stats, err := applenotes.Probe(cmd.Context(), cfg, applenotes.Options{
				DBPath:       dbPath,
				SnapshotDir:  snapshotDir,
				KeepSnapshot: keepSnapshot,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Source DB: %s\n", stats.SourceDBPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Snapshot:  %s\n", stats.Snapshot.DBPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Notes:     %d\n", stats.NoteCount)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Folders:   %d\n", stats.FolderCount)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Accounts:  %d\n", stats.AccountCount)
			for name, table := range stats.Tables {
				status := "missing"
				if table.Exists {
					status = fmt.Sprintf("rows=%d columns=%d", table.Rows, len(table.Columns))
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Table %-24s %s\n", name+":", status)
			}
			if len(stats.Warnings) > 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Warnings:  %s\n", strings.Join(stats.Warnings, "; "))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "Apple Notes NoteStore.sqlite path override")
	cmd.Flags().StringVar(&snapshotDir, "snapshot-dir", "", "Keep snapshot in this directory instead of a temporary path")
	cmd.Flags().BoolVar(&keepSnapshot, "keep-snapshot", false, "Keep the temporary snapshot after probing")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print probe stats as JSON")
	return cmd
}

func newImportAppleNotesSnapshotCommand(root *rootOptions) *cobra.Command {
	var dbPath string
	var dir string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "snapshot --dir <path>",
		Short: "Create a read-only Apple Notes DB/WAL/SHM snapshot for debugging",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(dir) == "" {
				return fmt.Errorf("--dir is required")
			}
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}
			info, cleanup, err := applenotes.CreateSnapshot(cfg, applenotes.Options{
				DBPath:      dbPath,
				SnapshotDir: dir,
			})
			if err != nil {
				return err
			}
			if cleanup != nil {
				defer func() {
					_ = cleanup()
				}()
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), info)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Source DB: %s\n", info.SourceDBPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Snapshot:  %s\n", info.DBPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Files:\n")
			for _, path := range info.CopiedFiles {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "- %s\n", path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "Apple Notes NoteStore.sqlite path override")
	cmd.Flags().StringVar(&dir, "dir", "", "Directory where the snapshot should be copied")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print snapshot metadata as JSON")
	return cmd
}

func newImportAppleNotesDecodeCommand(root *rootOptions) *cobra.Command {
	var dbPath string
	var noteID string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:    "decode --note <id>",
		Short:  "Decode one Apple Note body from a snapshot for local debugging",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(noteID) == "" {
				return fmt.Errorf("--note is required")
			}
			cfg, err := loadConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			if err := cfg.EnsureDirs(); err != nil {
				return err
			}
			doc, _, err := applenotes.DecodeNote(cmd.Context(), cfg, applenotes.Options{
				DBPath: dbPath,
			}, noteID)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), doc)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "# %s\n\n%s\n", doc.Title, doc.Text)
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "Apple Notes NoteStore.sqlite path override")
	cmd.Flags().StringVar(&noteID, "note", "", "Apple Notes identifier/source key to decode")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print decoded note as JSON")
	return cmd
}
