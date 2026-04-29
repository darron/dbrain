package app

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/store"
)

func newArchiveCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "archive",
		Short: "Manage archived media and other durable storage tiers",
		RunE:  helpCommand,
	}
	cmd.AddCommand(newArchiveMediaCommand(root))
	return cmd
}

func newArchiveMediaCommand(root *rootOptions) *cobra.Command {
	var limit int
	var force bool
	var upload bool
	var pruneLocal bool
	var provider string
	var bucket string
	var publicBaseURL string
	var endpoint string
	var region string
	var accessKeyID string
	var secretAccessKey string
	var sessionToken string
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "media",
		Short: "Mark uploaded media as archived and optionally prune local copies",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(root.root)
			if err != nil {
				return err
			}

			if provider == "" {
				provider = firstNonEmptyEnv(cfg.RootDir, "DBRAIN_ARCHIVE_PROVIDER", "DBRAIN_R2_PROVIDER")
			}
			if bucket == "" {
				bucket = firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_BUCKET", "DBRAIN_ARCHIVE_BUCKET")
			}
			if publicBaseURL == "" {
				publicBaseURL = firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_PUBLIC_BASE_URL", "DBRAIN_MEDIA_PUBLIC_BASE_URL")
			}
			if endpoint == "" {
				endpoint = firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_ENDPOINT", "DBRAIN_S3_ENDPOINT")
			}
			if region == "" {
				region = firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_REGION", "DBRAIN_S3_REGION")
			}
			if accessKeyID == "" {
				accessKeyID = firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_ACCESS_KEY_ID", "DBRAIN_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID")
			}
			if secretAccessKey == "" {
				secretAccessKey = firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_SECRET_ACCESS_KEY", "DBRAIN_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY")
			}
			if sessionToken == "" {
				sessionToken = firstNonEmptyEnv(cfg.RootDir, "DBRAIN_R2_SESSION_TOKEN", "DBRAIN_S3_SESSION_TOKEN", "AWS_SESSION_TOKEN")
			}
			if !upload {
				upload = firstEnvBool(cfg.RootDir, "DBRAIN_ARCHIVE_UPLOAD", "DBRAIN_R2_UPLOAD")
			}

			st, err := store.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() {
				_ = st.Close()
			}()

			stats, err := mediaarchive.Run(cmd.Context(), cfg, st, mediaarchive.Options{
				Limit:         limit,
				Force:         force,
				Upload:        upload,
				PruneLocal:    pruneLocal,
				Provider:      provider,
				Bucket:        bucket,
				PublicBaseURL: publicBaseURL,
				Endpoint:      endpoint,
				Region:        region,
				AccessKeyID:   accessKeyID,
				SecretKey:     secretAccessKey,
				SessionToken:  sessionToken,
				PathStyle:     true,
				Logger:        newLogger(commandDebugEnabled(cmd), cmd.ErrOrStderr()),
			})
			if err != nil {
				return err
			}

			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), stats)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Candidates: %d\n", stats.Candidates)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Uploaded: %d\n", stats.Uploaded)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Archived: %d\n", stats.Archived)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Unchanged: %d\n", stats.Unchanged)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Prune skipped: %d\n", stats.PruneSkipped)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Local files pruned: %d\n", stats.LocalFilesPruned)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Local rows pruned: %d\n", stats.LocalRowsPruned)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Errors: %d\n", stats.Errors)
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 5000, "Maximum media assets to inspect")
	cmd.Flags().BoolVar(&force, "force", false, "Revisit already archived assets that have not been pruned locally")
	cmd.Flags().BoolVar(&upload, "upload", false, "Upload eligible media to S3-compatible archive storage before marking/pruning")
	cmd.Flags().BoolVar(&pruneLocal, "prune-local", false, "Delete local archived media files after all same-path refs are safely archived")
	cmd.Flags().StringVar(&provider, "provider", "", "Archive provider label override (defaults to DBRAIN_ARCHIVE_PROVIDER/DBRAIN_R2_PROVIDER or cloudflare_r2)")
	cmd.Flags().StringVar(&bucket, "bucket", "", "Archive bucket name (defaults to DBRAIN_R2_BUCKET/DBRAIN_ARCHIVE_BUCKET)")
	cmd.Flags().StringVar(&publicBaseURL, "public-base-url", "", "Optional public base URL used to render archived media links (defaults to DBRAIN_R2_PUBLIC_BASE_URL/DBRAIN_MEDIA_PUBLIC_BASE_URL)")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "S3-compatible archive endpoint (defaults to DBRAIN_R2_ENDPOINT/DBRAIN_S3_ENDPOINT)")
	cmd.Flags().StringVar(&region, "region", "", "S3-compatible archive region (defaults to DBRAIN_R2_REGION/DBRAIN_S3_REGION or auto)")
	cmd.Flags().StringVar(&accessKeyID, "access-key-id", "", "S3-compatible access key id (defaults to DBRAIN_R2_ACCESS_KEY_ID/DBRAIN_S3_ACCESS_KEY_ID/AWS_ACCESS_KEY_ID)")
	cmd.Flags().StringVar(&secretAccessKey, "secret-access-key", "", "S3-compatible secret access key (defaults to DBRAIN_R2_SECRET_ACCESS_KEY/DBRAIN_S3_SECRET_ACCESS_KEY/AWS_SECRET_ACCESS_KEY)")
	cmd.Flags().StringVar(&sessionToken, "session-token", "", "Optional S3-compatible session token (defaults to DBRAIN_R2_SESSION_TOKEN/DBRAIN_S3_SESSION_TOKEN/AWS_SESSION_TOKEN)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print archive stats as JSON")

	return cmd
}
