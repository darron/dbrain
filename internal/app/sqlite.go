package app

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dbrain/internal/sqlitearchive"
)

func newSQLiteCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sqlite",
		Short: "Manage the local SQLite database",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newSQLiteArchiveCommand(root), newSQLiteRestoreCommand(root))
	return cmd
}

func newSQLiteArchiveCommand(root *rootOptions) *cobra.Command {
	var opts sqliteArchiveFlags
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "archive",
		Short: "Snapshot, compress, and upload the local SQLite database",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			store, prefix, err := buildSQLiteArchiveStore(cfg.RootDir, opts)
			if err != nil {
				return err
			}
			var progressFn func(sqlitearchive.Event)
			if !jsonOut {
				ui := newCLIProgressUI(cmd.ErrOrStderr())
				defer ui.stopActive(false, "")
				progressFn = ui.Handle
			}
			result, err := sqlitearchive.Archive(cmd.Context(), cfg, sqlitearchive.Options{
				Prefix:   prefix,
				Store:    store,
				Progress: progressFn,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Archived SQLite database: %s\n", result.Key)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Local DB: %s\n", result.LocalDBPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Snapshot bytes: %d\n", result.SnapshotSize)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Compressed bytes: %d\n", result.ArchiveSize)
			return nil
		},
	}
	addSQLiteArchiveFlags(cmd, &opts)
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print archive result as JSON")
	return cmd
}

func newSQLiteRestoreCommand(root *rootOptions) *cobra.Command {
	var opts sqliteArchiveFlags
	var yes bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Download and restore the newest archived SQLite database",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}
			store, prefix, err := buildSQLiteArchiveStore(cfg.RootDir, opts)
			if err != nil {
				return err
			}
			archiveOpts := sqlitearchive.Options{
				Prefix: prefix,
				Store:  store,
			}
			var ui *cliProgressUI
			if !jsonOut {
				ui = newCLIProgressUI(cmd.ErrOrStderr())
				defer ui.stopActive(false, "")
				archiveOpts.Progress = ui.Handle
				ui.Handle(sqlitearchive.Event{Kind: sqlitearchive.EventStageStart, Stage: "list", Message: "Finding newest SQLite archive"})
			}
			plan, err := sqlitearchive.Latest(cmd.Context(), archiveOpts)
			if err != nil {
				return err
			}
			if ui != nil {
				ui.Handle(sqlitearchive.Event{Kind: sqlitearchive.EventStageDone, Stage: "list", Message: fmt.Sprintf("Found %s", plan.Object.Key)})
			}
			if !yes {
				ok, err := confirmRestore(cmd.InOrStdin(), cmd.OutOrStdout(), cfg.DBPath, plan.Object)
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Restore cancelled.")
					return nil
				}
			}
			result, err := sqlitearchive.Restore(cmd.Context(), cfg, plan, archiveOpts)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Restored SQLite database: %s\n", result.RestoredPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Archive key: %s\n", result.Key)
			for _, backupPath := range result.BackupPaths {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Moved existing file: %s\n", backupPath)
			}
			return nil
		},
	}
	addSQLiteArchiveFlags(cmd, &opts)
	cmd.Flags().BoolVar(&yes, "yes", false, "Restore without interactive confirmation")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print restore result as JSON")
	return cmd
}

type sqliteArchiveFlags struct {
	bucket          string
	prefix          string
	endpoint        string
	region          string
	accessKeyID     string
	secretAccessKey string
	sessionToken    string
}

func addSQLiteArchiveFlags(cmd *cobra.Command, opts *sqliteArchiveFlags) {
	cmd.Flags().StringVar(&opts.bucket, "bucket", "", "Archive bucket name (defaults to DBRAIN_R2_BUCKET/DBRAIN_ARCHIVE_BUCKET/DBRAIN_S3_BUCKET)")
	cmd.Flags().StringVar(&opts.prefix, "prefix", sqlitearchive.DefaultPrefix, "Object key prefix for SQLite archives")
	cmd.Flags().StringVar(&opts.endpoint, "endpoint", "", "S3-compatible archive endpoint (defaults to DBRAIN_R2_ENDPOINT/DBRAIN_S3_ENDPOINT)")
	cmd.Flags().StringVar(&opts.region, "region", "", "S3-compatible archive region (defaults to DBRAIN_R2_REGION/DBRAIN_S3_REGION/AWS_REGION/AWS_DEFAULT_REGION or auto)")
	cmd.Flags().StringVar(&opts.accessKeyID, "access-key-id", "", "S3-compatible access key id (defaults to DBRAIN_R2_ACCESS_KEY_ID/DBRAIN_S3_ACCESS_KEY_ID/AWS_ACCESS_KEY_ID)")
	cmd.Flags().StringVar(&opts.secretAccessKey, "secret-access-key", "", "S3-compatible secret access key (defaults to DBRAIN_R2_SECRET_ACCESS_KEY/DBRAIN_S3_SECRET_ACCESS_KEY/AWS_SECRET_ACCESS_KEY)")
	cmd.Flags().StringVar(&opts.sessionToken, "session-token", "", "Optional S3-compatible session token (defaults to DBRAIN_R2_SESSION_TOKEN/DBRAIN_S3_SESSION_TOKEN/AWS_SESSION_TOKEN)")
}

func buildSQLiteArchiveStore(rootDir string, opts sqliteArchiveFlags) (*sqlitearchive.S3Store, string, error) {
	bucket := strings.TrimSpace(opts.bucket)
	if bucket == "" {
		bucket = firstNonEmptyEnv(rootDir, "DBRAIN_R2_BUCKET", "DBRAIN_ARCHIVE_BUCKET", "DBRAIN_S3_BUCKET")
	}
	endpoint := strings.TrimSpace(opts.endpoint)
	if endpoint == "" {
		endpoint = firstNonEmptyEnv(rootDir, "DBRAIN_R2_ENDPOINT", "DBRAIN_S3_ENDPOINT")
	}
	region := strings.TrimSpace(opts.region)
	if region == "" {
		region = firstNonEmptyEnv(rootDir, "DBRAIN_R2_REGION", "DBRAIN_S3_REGION", "AWS_REGION", "AWS_DEFAULT_REGION")
	}
	accessKeyID := strings.TrimSpace(opts.accessKeyID)
	if accessKeyID == "" {
		accessKeyID = firstNonEmptyEnv(rootDir, "DBRAIN_R2_ACCESS_KEY_ID", "DBRAIN_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID")
	}
	secretAccessKey := strings.TrimSpace(opts.secretAccessKey)
	if secretAccessKey == "" {
		secretAccessKey = firstNonEmptyEnv(rootDir, "DBRAIN_R2_SECRET_ACCESS_KEY", "DBRAIN_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY")
	}
	sessionToken := strings.TrimSpace(opts.sessionToken)
	if sessionToken == "" {
		sessionToken = firstNonEmptyEnv(rootDir, "DBRAIN_R2_SESSION_TOKEN", "DBRAIN_S3_SESSION_TOKEN", "AWS_SESSION_TOKEN")
	}

	store, err := sqlitearchive.NewS3Store(sqlitearchive.S3Options{
		Bucket:       bucket,
		Endpoint:     endpoint,
		Region:       region,
		AccessKeyID:  accessKeyID,
		SecretKey:    secretAccessKey,
		SessionToken: sessionToken,
		PathStyle:    true,
	})
	if err != nil {
		return nil, "", err
	}
	return store, strings.TrimSpace(opts.prefix), nil
}

func confirmRestore(in io.Reader, out io.Writer, dbPath string, obj sqlitearchive.Object) (bool, error) {
	modified := "unknown"
	if !obj.LastModified.IsZero() {
		modified = obj.LastModified.UTC().Format(time.RFC3339)
	}
	_, _ = fmt.Fprintf(out, "Restore newest SQLite archive?\n")
	_, _ = fmt.Fprintf(out, "Archive: %s\n", obj.Key)
	_, _ = fmt.Fprintf(out, "Modified: %s\n", modified)
	_, _ = fmt.Fprintf(out, "Target: %s\n", dbPath)
	_, _ = fmt.Fprintf(out, "Existing brain.db, brain.db-wal, and brain.db-shm files will be moved aside first.\n")
	_, _ = fmt.Fprintf(out, "Type 'restore' to continue: ")

	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	return strings.TrimSpace(scanner.Text()) == "restore", nil
}
