package app

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/semanticgc"
	"github.com/darron/dbrain/internal/store"
)

const (
	defaultSemanticGCGracePeriod     = 10 * time.Minute
	defaultSemanticGCRetainPublished = 2
)

func newSemanticGCCommand(root *rootOptions, deps semanticDeps) *cobra.Command {
	var apply, vacuum, jsonOut bool
	var gracePeriod time.Duration
	var retainGenerations int
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Plan or apply safe semantic cache garbage collection",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if gracePeriod < 0 {
				return fmt.Errorf("grace-period must not be negative")
			}
			if retainGenerations < 0 {
				return fmt.Errorf("retain-generations must not be negative")
			}
			if vacuum && !apply {
				return fmt.Errorf("--vacuum requires --apply")
			}
			cfg, err := deps.loadWriteConfig(root.root, root.configFile)
			if err != nil {
				return err
			}
			var st *store.Store
			if apply {
				st, err = deps.openWritable(cfg.DBPath, cfg.CacheDir)
			} else {
				st, err = deps.openReadOnly(cfg.DBPath)
			}
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			databaseID, err := st.RetrievalDatabaseID(cmd.Context())
			if err != nil {
				return fmt.Errorf("read semantic database ID: %w", err)
			}
			result, err := deps.runGC(cmd.Context(), st, cfg.CacheDir, databaseID, semanticgc.Options{
				Now: time.Now().UTC(), GracePeriod: gracePeriod, RetainPublished: retainGenerations,
				Apply: apply, Vacuum: vacuum,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			return writeSemanticGCResult(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "Apply the recomputed plan; the default is a read-only dry-run")
	cmd.Flags().BoolVar(&vacuum, "vacuum", false, "After apply, checkpoint and VACUUM SQLite (stop the daemon and ensure disk headroom first)")
	cmd.Flags().DurationVar(&gracePeriod, "grace-period", defaultSemanticGCGracePeriod, "Retain recently superseded and orphaned artifacts for this duration")
	cmd.Flags().IntVar(&retainGenerations, "retain-generations", defaultSemanticGCRetainPublished, "Retain this many newest published roots per profile for rollback")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print structured JSON")
	return cmd
}

func writeSemanticGCResult(out io.Writer, result semanticgc.Result) error {
	mode := "dry-run"
	if result.Applied {
		mode = "applied"
	}
	if _, err := fmt.Fprintf(out, "semantic GC %s\n", mode); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "  generations: retain=%d prune=%d\n", len(result.Catalog.RetainedGenerations), len(result.Catalog.PrunableGenerations)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "  segments: retain=%d prune=%d\n", len(result.Catalog.RetainedSegments), len(result.Catalog.PrunableSegments)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "  segment member rows: prune=%d\n", result.Catalog.PrunableMemberRows); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "  filesystem directories: prune=%d bytes=%d\n", len(result.FilesystemArtifacts), result.PrunableBytes); err != nil {
		return err
	}
	if !result.Applied {
		_, err := fmt.Fprintln(out, "  no changes made; rerun with --apply after reviewing this plan")
		return err
	}
	if result.Vacuumed {
		_, err := fmt.Fprintln(out, "  SQLite VACUUM completed")
		return err
	}
	_, err := fmt.Fprintln(out, "  SQLite rows were pruned; the database file may not shrink until an explicit --vacuum run")
	return err
}
