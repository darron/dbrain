package mediaarchive

import (
	"context"
	"log/slog"
	"time"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/model"
)

const (
	defaultProvider = "cloudflare_r2"
	// DefaultPrefix is the fixed remote namespace produced by media archival.
	DefaultPrefix = "media/"
)

type Options struct {
	Limit                 int
	Force                 bool
	Upload                bool
	PruneLocal            bool
	Provider              string
	Bucket                string
	PublicBaseURL         string
	Endpoint              string
	Region                string
	AccessKeyID           string
	SecretKey             string
	SessionToken          string
	PathStyle             bool
	ConnectTimeout        time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	Uploader              Uploader
	Logger                *slog.Logger
}

type Stats struct {
	Candidates       int   `json:"candidates"`
	Uploaded         int   `json:"uploaded"`
	Archived         int   `json:"archived"`
	Unchanged        int   `json:"unchanged"`
	PruneSkipped     int   `json:"prune_skipped"`
	LocalFilesPruned int   `json:"local_files_pruned"`
	LocalRowsPruned  int64 `json:"local_rows_pruned"`
	Errors           int   `json:"errors"`
}

type Uploader interface {
	Upload(ctx context.Context, cfg config.Config, asset model.MediaAsset, opts Options) (model.MediaArchiveResult, bool, error)
}
