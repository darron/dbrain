package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/sqlitearchive"
)

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

func buildSQLiteArchiveStore(ctx context.Context, rootDir string, opts sqliteArchiveFlags) (*sqlitearchive.S3Store, string, error) {
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
		value, err := firstNonEmptySecret(ctx, rootDir, "DBRAIN_R2_ACCESS_KEY_ID", "DBRAIN_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID")
		if err != nil {
			return nil, "", err
		}
		accessKeyID = value
	}
	secretAccessKey := strings.TrimSpace(opts.secretAccessKey)
	if secretAccessKey == "" {
		value, err := firstNonEmptySecret(ctx, rootDir, "DBRAIN_R2_SECRET_ACCESS_KEY", "DBRAIN_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY")
		if err != nil {
			return nil, "", err
		}
		secretAccessKey = value
	}
	sessionToken := strings.TrimSpace(opts.sessionToken)
	if sessionToken == "" {
		value, err := firstNonEmptySecret(ctx, rootDir, "DBRAIN_R2_SESSION_TOKEN", "DBRAIN_S3_SESSION_TOKEN", "AWS_SESSION_TOKEN")
		if err != nil {
			return nil, "", err
		}
		sessionToken = value
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
