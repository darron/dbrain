package sourceenrich

import (
	"context"
	"log/slog"
	"time"

	"github.com/darron/dbrain/internal/llmprovider"
	"github.com/darron/dbrain/internal/model"
)

type Options struct {
	Limit                 int
	Concurrency           int
	Force                 bool
	AcceptCurrentSummary  bool
	Summarize             bool
	Model                 string
	CLI                   string
	Length                string
	Language              string
	Timeout               time.Duration
	ProgressInterval      time.Duration
	Logger                *slog.Logger
	ExactSummaryFreshness bool
	// InferenceParams carries optional provider-specific sampler parameters
	// for parity-aware bakeoff runs. Normal source-summary callers leave
	// this empty so provider/runtime defaults apply.
	InferenceParams           llmprovider.ParityParams
	EnvFor                    func(source model.SourceDocument) map[string]string
	ArgsFor                   func(source model.SourceDocument) []string
	ResolveHost               func(ctx context.Context, host string) error
	ResolveRedirectURL        func(ctx context.Context, rawURL string) (string, error)
	FallbackExtractFor        func(ctx context.Context, source model.SourceDocument, extract model.ExtractResult) (model.ExtractResult, bool, error)
	HTTPReaderFallbackDomains []string
	HTTPReaderBaseURL         string
	WaybackFallbackEnabled    bool
	WaybackAvailabilityURL    string
	Binary                    string
	YouTubeBrowser            string
	YouTubeProfile            string
	YouTubeCookiesArg         string
	YouTubeTranscriber        string
	YTDLPBinary               string
	WhisperBinary             string
	WhisperModelPath          string
	MacWhisperBinary          string
}

type Stats struct {
	SourcesQueued     int `json:"sources_queued"`
	SourcesExtracted  int `json:"sources_extracted"`
	SourcesSummarized int `json:"sources_summarized"`
	SourcesRendered   int `json:"sources_rendered"`
	SourcesUnchanged  int `json:"sources_unchanged"`
	Errors            int `json:"errors"`
}
