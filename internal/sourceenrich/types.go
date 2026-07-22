package sourceenrich

import (
	"context"
	"log/slog"
	"time"

	"github.com/darron/dbrain/internal/llmprovider"
	"github.com/darron/dbrain/internal/metrics"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/safehttp"
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
	Metrics               metrics.RunContext
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
	httpPolicy                *safehttp.Policy
	prepareSourceInput        func(context.Context, string) (preparedSourceInput, error)
	configuredSourceOrigin    string
	Binary                    string
	YouTubeBrowser            string
	YouTubeProfile            string
	YouTubeCookiesArg         string
	YouTubeTranscriber        string
	YTDLPBinary               string
	WhisperBinary             string
	WhisperModelPath          string
	WhisperVADModelPath       string
	TranscriptionLanguage     string
	MacWhisperBinary          string
	// OnSourceResult is called when a source enrichment candidate completes.
	// Callers must treat it as concurrent when Concurrency > 1.
	OnSourceResult func(SourceResult)
}

type preparedSourceInput struct {
	Path        string
	FinalURL    string
	ContentType string
	Cleanup     func()
}

// WithConfiguredSourceOrigin authorizes one exact operator-configured HTTP
// origin for source fetching. Redirects and unrelated source origins remain
// subject to the public-destination policy.
func WithConfiguredSourceOrigin(opts Options, origin string) Options {
	opts.configuredSourceOrigin = origin
	return opts
}

type Stats struct {
	SourcesQueued     int `json:"sources_queued"`
	SourcesExtracted  int `json:"sources_extracted"`
	SourcesSummarized int `json:"sources_summarized"`
	SourcesRendered   int `json:"sources_rendered"`
	SourcesUnchanged  int `json:"sources_unchanged"`
	Errors            int `json:"errors"`
}

type SourceResult struct {
	SourceID           int64
	SourceKey          string
	Duration           time.Duration
	Error              string
	Extracted          bool
	SummaryCreated     bool
	SummaryStatus      string
	SummaryModel       string
	SummaryProvider    string
	SummaryAPIModel    string
	SummaryTransport   string
	SummaryTool        string
	SummaryToolVersion string
	SummaryChars       int
}
