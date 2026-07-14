package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/runtimeenv"
)

type auditConfigMeta struct {
	Layout string
	Source string
}

func resolveAuditFeatures(cfg config.Config, meta auditConfigMeta) (audit.Features, error) {
	timeouts, remoteRequestTimeout, err := resolveAuditTimeoutOverrides(cfg.RootDir)
	if err != nil {
		return audit.Features{}, err
	}
	scheduler, err := schedulerSyncConfigFromRuntime(cfg.RootDir)
	if err != nil {
		return audit.Features{}, err
	}
	flags, err := resolveSyncAllFlags(cfg.RootDir, scheduler.Flags)
	if err != nil {
		return audit.Features{}, err
	}
	configVerified := false
	if info, statErr := os.Stat(cfg.ConfigPath); statErr == nil && !info.IsDir() {
		configVerified = true
	}
	sources := map[audit.Source]bool{
		audit.SourceAppleNotes:        flags.appleNotes && !flags.skipAppleNotes,
		audit.SourceSafariTabs:        flags.safariTabs && !flags.skipSafariTabs,
		audit.SourceXBookmarks:        !flags.skipXBookmarks,
		audit.SourceGitHubStars:       !flags.skipGitHub,
		audit.SourceYouTubeWatchLater: !flags.skipYouTube && flags.watchLater,
		audit.SourceYouTubeLiked:      !flags.skipYouTube && flags.liked,
		audit.SourceFeeds:             !flags.skipFeeds,
	}
	stages := map[audit.PipelineStage]bool{
		audit.PipelineHydration:     !flags.skipX,
		audit.PipelineExtraction:    !flags.skipLinks || sources[audit.SourceAppleNotes] || sources[audit.SourceSafariTabs],
		audit.PipelineSummary:       flags.summarize && (!flags.skipSources || !flags.skipXMedia),
		audit.PipelineTranscription: !flags.skipXMedia,
		audit.PipelineOCR:           !flags.skipXPhotoOCR,
	}
	selected := make([]string, 0, 12)
	appendStage := func(enabled bool, name string) {
		if enabled {
			selected = append(selected, name)
		}
	}
	appendStage(sources[audit.SourceAppleNotes], "apple_notes")
	appendStage(sources[audit.SourceSafariTabs], "safari_tabs")
	appendStage(!flags.skipXBookmarks, "x_bookmarks")
	appendStage(!flags.skipX, "x")
	appendStage(!flags.skipLinks, "links")
	appendStage(!flags.skipXMedia, "x_media")
	appendStage(!flags.skipXPhotoOCR, "x_photo_ocr")
	appendStage(!flags.skipGitHub, "github")
	appendStage(!flags.skipYouTube, "youtube")
	appendStage(!flags.skipFeeds, "feeds")
	appendStage(!flags.skipSources, "sources")
	appendStage(flags.archiveMedia, "media_archive")
	appendStage(flags.okfExport && !flags.skipOKFExport, "okf_export")

	provider := strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_ENDPOINT", "DBRAIN_S3_ENDPOINT")) != "" &&
		strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_BUCKET", "DBRAIN_ARCHIVE_BUCKET", "DBRAIN_S3_BUCKET")) != ""
	credential := strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_ACCESS_KEY_ID", "DBRAIN_S3_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID")) != "" &&
		strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_R2_SECRET_ACCESS_KEY", "DBRAIN_S3_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY")) != ""
	mediaProvider := strings.TrimSpace(runtimeenv.FirstNonEmpty(cfg.RootDir, "DBRAIN_ARCHIVE_PROVIDER", "DBRAIN_R2_PROVIDER"))
	if mediaProvider == "" {
		mediaProvider = "cloudflare_r2"
	}
	return audit.Features{
		Layout: meta.Layout, ConfigSource: meta.Source, ConfigVerified: configVerified,
		SchedulerEnabled: scheduler.Enabled, SchedulerInterval: scheduler.Interval, SchedulerJitter: scheduler.Jitter,
		SelectedStages: selected, Sources: sources, Stages: stages,
		MediaLocalEnabled: flags.archiveMedia || !flags.skipXMedia || !flags.skipXPhotoOCR, MediaRemoteEnabled: flags.archiveMedia, MediaProvider: mediaProvider,
		SQLiteArchiveCapabilityConfigured: provider || credential,
		SQLiteBackupSchedulerEnabled:      runtimeenv.FirstBool(cfg.RootDir, "DBRAIN_SCHEDULER_SQLITE_ARCHIVE_ENABLED"),
		SQLiteBackupAuditRequired:         runtimeenv.FirstBool(cfg.RootDir, "DBRAIN_AUDIT_REQUIRE_SQLITE_BACKUP"),
		SQLiteProviderConfigured:          provider,
		SQLiteCredentialConfigured:        credential,
		OKFEnabled:                        flags.okfExport && !flags.skipOKFExport,
		Timeouts:                          timeouts,
		RemoteRequestTimeout:              remoteRequestTimeout,
	}, nil
}

func resolveAuditTimeoutOverrides(root string) (map[audit.TimeoutClass]time.Duration, time.Duration, error) {
	overrides := map[audit.TimeoutClass]time.Duration{}
	classes := []struct {
		class audit.TimeoutClass
		key   string
	}{
		{audit.TimeoutBootstrap, "DBRAIN_AUDIT_TIMEOUT_BOOTSTRAP"},
		{audit.TimeoutLocalQuery, "DBRAIN_AUDIT_TIMEOUT_LOCAL_QUERY"},
		{audit.TimeoutMetricsOrManifest, "DBRAIN_AUDIT_TIMEOUT_METRICS_OR_MANIFEST"},
		{audit.TimeoutSQLiteOrOKFIntegrity, "DBRAIN_AUDIT_TIMEOUT_SQLITE_OR_OKF_INTEGRITY"},
		{audit.TimeoutRemoteMetadata, "DBRAIN_AUDIT_TIMEOUT_REMOTE_METADATA"},
	}
	for _, item := range classes {
		value := strings.TrimSpace(runtimeenv.FirstNonEmpty(root, item.key))
		if value == "" {
			continue
		}
		duration, err := audit.ParseDuration(value)
		if err != nil || duration <= 0 {
			if err == nil {
				err = fmt.Errorf("duration must be positive")
			}
			return nil, 0, fmt.Errorf("invalid audit timeout %s: %w", item.class, err)
		}
		overrides[item.class] = duration
	}
	remoteRequest := time.Duration(0)
	if value := strings.TrimSpace(runtimeenv.FirstNonEmpty(root, "DBRAIN_AUDIT_TIMEOUT_REMOTE_REQUEST")); value != "" {
		duration, err := audit.ParseDuration(value)
		if err != nil || duration <= 0 {
			if err == nil {
				err = fmt.Errorf("duration must be positive")
			}
			return nil, 0, fmt.Errorf("invalid audit remote request timeout: %w", err)
		}
		remoteRequest = duration
	}
	return overrides, remoteRequest, nil
}

// loadAuditConfig resolves the same target precedence as ordinary commands,
// but deliberately omits directory creation, cleanup, preflight, and secret
// resolution. An audit must not repair or otherwise alter the target it reads.
func loadAuditConfig(root, configFile string) (config.Config, auditConfigMeta, error) {
	return loadAuditConfigContext(context.Background(), root, configFile)
}

func loadAuditConfigContext(ctx context.Context, root, configFile string) (config.Config, auditConfigMeta, error) {
	if err := ctx.Err(); err != nil {
		return config.Config{}, auditConfigMeta{}, err
	}
	root = strings.TrimSpace(root)
	configFile = strings.TrimSpace(configFile)
	envConfig := strings.TrimSpace(os.Getenv(configFileEnvVar))
	envRoot := strings.TrimSpace(os.Getenv(rootEnvVar))

	var (
		cfg  config.Config
		meta auditConfigMeta
		err  error
	)
	switch {
	case configFile != "":
		cfg, err = config.LoadConfigFile(configFile)
		meta = auditConfigMeta{Layout: "explicit_config", Source: "flag"}
	case root != "":
		cfg, err = config.Load(root)
		meta = auditConfigMeta{Layout: "explicit_root", Source: "flag"}
	case envConfig != "":
		cfg, err = config.LoadConfigFile(envConfig)
		meta = auditConfigMeta{Layout: "explicit_config", Source: "environment"}
	case envRoot != "":
		cfg, err = config.Load(envRoot)
		meta = auditConfigMeta{Layout: "explicit_root", Source: "environment"}
	default:
		cfg, err = config.Load("")
		meta = auditConfigMeta{Layout: "xdg", Source: "default"}
	}
	if err != nil {
		return config.Config{}, auditConfigMeta{}, err
	}
	if err := ctx.Err(); err != nil {
		return config.Config{}, auditConfigMeta{}, err
	}
	return cfg, meta, nil
}
