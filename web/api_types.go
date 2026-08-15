package web

import (
	"time"

	"github.com/darron/dbrain/internal/audit"
	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/researchtrace"
	"github.com/darron/dbrain/internal/schedulerstate"
	"github.com/darron/dbrain/internal/store"
)

type AuditHistoryResponse struct {
	Profile audit.Profile       `json:"profile"`
	History []AuditHistoryEntry `json:"history"`
}

type AuditHistoryEntry struct {
	AuditID     string           `json:"audit_id"`
	Profile     audit.Profile    `json:"profile"`
	Status      audit.Status     `json:"status"`
	Confidence  audit.Confidence `json:"confidence"`
	StartedAt   time.Time        `json:"started_at"`
	CompletedAt time.Time        `json:"completed_at"`
	Summary     audit.Summary    `json:"summary"`
	Freshness   audit.Freshness  `json:"freshness"`
}

type AuditRunState string

const (
	AuditRunRunning   AuditRunState = "running"
	AuditRunCompleted AuditRunState = "completed"
	AuditRunFailed    AuditRunState = "failed"
)

const (
	AuditRunErrorFailed  = "audit_run_failed"
	AuditRunErrorPersist = "audit_report_persist_failed"
)

type AuditRunStatusResponse struct {
	AuditID     string           `json:"audit_id"`
	Profile     audit.Profile    `json:"profile"`
	State       AuditRunState    `json:"state"`
	StatusPath  string           `json:"status_path"`
	StartedAt   time.Time        `json:"started_at,omitempty"`
	CompletedAt time.Time        `json:"completed_at,omitempty"`
	ErrorCode   string           `json:"error_code,omitempty"`
	Report      *audit.Report    `json:"report,omitempty"`
	Freshness   *audit.Freshness `json:"freshness,omitempty"`
}

type AuditRunConflictResponse struct {
	Error         string        `json:"error"`
	ActiveAuditID string        `json:"active_audit_id"`
	ActiveProfile audit.Profile `json:"active_profile"`
}

type AuditRunRateLimitResponse struct {
	Error             string `json:"error"`
	RetryAfterSeconds int64  `json:"retry_after_seconds"`
}

type AppInfo struct {
	Name    string         `json:"name"`
	HasFTS  bool           `json:"has_fts"`
	Version WebVersionInfo `json:"version"`
}

type WebVersionInfo struct {
	Commit         string `json:"commit"`
	Short          string `json:"short"`
	GitStatus      string `json:"git_status"`
	ReleaseVersion string `json:"release_version"`
	ModuleVersion  string `json:"module_version,omitempty"`
}

type BootstrapResponse struct {
	App            AppInfo                  `json:"app"`
	Auth           AuthInfo                 `json:"auth"`
	Backlog        BacklogResponse          `json:"backlog"`
	Activity       store.ActivityStats      `json:"activity"`
	SourceActivity store.SourceActivityFeed `json:"source_activity"`
}

const SourceBacklogScopeDescription = "X hydration, link discovery, source extraction, and source summary only; this is not whole-system health."

type BacklogResponse struct {
	store.BacklogStats
	ScopeDescription string `json:"scope_description"`
}

func newBacklogResponse(backlog store.BacklogStats) BacklogResponse {
	return BacklogResponse{BacklogStats: backlog, ScopeDescription: SourceBacklogScopeDescription}
}

type AuthInfo struct {
	Enabled  bool          `json:"enabled"`
	Provider string        `json:"provider,omitempty"`
	User     *AuthUserInfo `json:"user,omitempty"`
}

type AuthUserInfo struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
}

type SearchResponse struct {
	Query   string                 `json:"query"`
	Limit   int                    `json:"limit"`
	Results []SearchResultResponse `json:"results"`
}

type SearchResultResponse struct {
	SourceKey     string             `json:"source_key"`
	SourceType    string             `json:"source_type"`
	ExternalID    string             `json:"external_id"`
	Title         string             `json:"title"`
	AuthorHandle  string             `json:"author_handle"`
	AuthorName    string             `json:"author_name"`
	CanonicalURL  string             `json:"canonical_url"`
	PrimaryDomain string             `json:"primary_domain"`
	NotePath      string             `json:"note_path"`
	UserTags      string             `json:"user_tags"`
	Snippet       string             `json:"snippet"`
	Media         []MediaRefResponse `json:"media,omitempty"`
}

type SchedulerSyncAllResponse struct {
	SyncAll schedulerstate.SyncAllStatus `json:"sync_all"`
}

type FullDiskAccessResponse struct {
	OK         bool   `json:"ok"`
	Readable   bool   `json:"readable"`
	Path       string `json:"path"`
	Executable string `json:"executable"`
	PID        int    `json:"pid"`
	Error      string `json:"error,omitempty"`
}

type GetResponse struct {
	Lookup        string                 `json:"lookup"`
	Kind          string                 `json:"kind"`
	Item          *ItemResponse          `json:"item,omitempty"`
	Source        *SourceResponse        `json:"source,omitempty"`
	LinkedSources []model.ItemSourceRef  `json:"linked_sources,omitempty"`
	Backlinks     []model.SourceBacklink `json:"backlinks,omitempty"`
	QuotedPosts   []ItemResponse         `json:"quoted_posts,omitempty"`
	NoteContent   string                 `json:"note_content,omitempty"`
	NoteError     string                 `json:"note_error,omitempty"`
}

type ItemResponse struct {
	ID                     int64              `json:"id"`
	SourceKey              string             `json:"source_key"`
	SourceType             string             `json:"source_type"`
	ExternalID             string             `json:"external_id"`
	CanonicalURL           string             `json:"canonical_url"`
	Title                  string             `json:"title"`
	AuthorHandle           string             `json:"author_handle"`
	AuthorName             string             `json:"author_name"`
	PublishedAt            string             `json:"published_at"`
	SavedAt                string             `json:"saved_at"`
	SyncedAt               string             `json:"synced_at"`
	Language               string             `json:"language"`
	Text                   string             `json:"text"`
	ArticleTitle           string             `json:"article_title"`
	ArticleText            string             `json:"article_text"`
	PrimaryCategory        string             `json:"primary_category"`
	PrimaryDomain          string             `json:"primary_domain"`
	LinksJSON              string             `json:"links_json"`
	Categories             string             `json:"categories"`
	Domains                string             `json:"domains"`
	GitHubURLs             string             `json:"github_urls"`
	FolderNames            string             `json:"folder_names"`
	LikeCount              int                `json:"like_count"`
	RepostCount            int                `json:"repost_count"`
	ReplyCount             int                `json:"reply_count"`
	QuoteCount             int                `json:"quote_count"`
	BookmarkCount          int                `json:"bookmark_count"`
	NotePath               string             `json:"note_path"`
	ImportedAt             time.Time          `json:"imported_at"`
	UpdatedAt              time.Time          `json:"updated_at"`
	LastSeenAt             time.Time          `json:"last_seen_at"`
	XPostText              string             `json:"x_post_text"`
	XPostLang              string             `json:"x_post_lang"`
	XPostFetchedAt         time.Time          `json:"x_post_fetched_at"`
	XPostStatus            string             `json:"x_post_status"`
	LinkExtractSyncedAt    time.Time          `json:"link_extract_synced_at"`
	SummaryText            string             `json:"summary_text"`
	SummaryStatus          string             `json:"summary_status"`
	SummaryModel           string             `json:"summary_model"`
	SummaryTool            string             `json:"summary_tool"`
	SummarizedAt           time.Time          `json:"summarized_at"`
	OCRText                string             `json:"ocr_text"`
	OCRStatus              string             `json:"ocr_status"`
	OCRModel               string             `json:"ocr_model"`
	OCRTool                string             `json:"ocr_tool"`
	OCRAt                  time.Time          `json:"ocr_at"`
	XMediaTranscriptStatus string             `json:"x_media_transcript_status"`
	XMediaTranscriptAt     time.Time          `json:"x_media_transcript_at"`
	UserTags               string             `json:"user_tags"`
	Media                  []MediaRefResponse `json:"media,omitempty"`
}

type SourceResponse struct {
	ID            int64     `json:"id"`
	SourceKey     string    `json:"source_key"`
	CanonicalURL  string    `json:"canonical_url"`
	NormalizedURL string    `json:"normalized_url"`
	SourceType    string    `json:"source_type"`
	Domain        string    `json:"domain"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	SiteName      string    `json:"site_name"`
	ExtractedText string    `json:"extracted_text"`
	ExtractStatus string    `json:"extract_status"`
	ExtractedAt   time.Time `json:"extracted_at"`
	ExtractTool   string    `json:"extract_tool"`
	SummaryText   string    `json:"summary_text"`
	SummaryStatus string    `json:"summary_status"`
	SummaryModel  string    `json:"summary_model"`
	SummaryTool   string    `json:"summary_tool"`
	SummarizedAt  time.Time `json:"summarized_at"`
	NotePath      string    `json:"note_path"`
	UserTags      string    `json:"user_tags"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type MediaRefResponse struct {
	MediaAssetID   int64  `json:"media_asset_id"`
	Ordinal        int    `json:"ordinal"`
	ExpandedURL    string `json:"expanded_url,omitempty"`
	RemoteURL      string `json:"remote_url,omitempty"`
	MediaType      string `json:"media_type"`
	DownloadStatus string `json:"download_status,omitempty"`
	ArchiveURL     string `json:"archive_url,omitempty"`
	ArchiveStatus  string `json:"archive_status,omitempty"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
}

type ResearchRequest struct {
	Question          string   `json:"question"`
	Topic             string   `json:"topic"`
	Limit             int      `json:"limit"`
	SourceTypes       []string `json:"source_types"`
	IncludeRelated    bool     `json:"include_related"`
	RelatedLimit      int      `json:"related_limit"`
	SeedLimit         int      `json:"seed_limit"`
	IncludeTopicBrief *bool    `json:"include_topic_brief"`
	MaxCharsPerDoc    int      `json:"max_chars_per_doc"`
	PlannerModel      string   `json:"planner_model"`
	UseModelPlanner   bool     `json:"use_model_planner"`
	DisablePlanner    bool     `json:"disable_planner"`
	UseSemantic       bool     `json:"use_semantic"`
	DisableSemantic   bool     `json:"disable_semantic"`
}

type ResearchSynthesisRequest struct {
	Question         string                        `json:"question"`
	ResearchPack     brainresearch.Pack            `json:"research_pack"`
	Model            string                        `json:"model"`
	MaxEvidenceChars int                           `json:"max_evidence_chars"`
	TraceEnabled     *bool                         `json:"trace_enabled,omitempty"`
	TraceSurface     string                        `json:"trace_surface,omitempty"`
	TraceContinuity  *researchtrace.ChatContinuity `json:"trace_continuity,omitempty"`
}

type ResearchRunRequest struct {
	Question          string                        `json:"question"`
	Topic             string                        `json:"topic"`
	Limit             int                           `json:"limit"`
	SourceTypes       []string                      `json:"source_types"`
	RelatedLimit      int                           `json:"related_limit"`
	SeedLimit         int                           `json:"seed_limit"`
	IncludeTopicBrief *bool                         `json:"include_topic_brief"`
	MaxCharsPerDoc    int                           `json:"max_chars_per_doc"`
	PlannerModel      string                        `json:"planner_model"`
	UseModelPlanner   bool                          `json:"use_model_planner"`
	DisablePlanner    bool                          `json:"disable_planner"`
	UseSemantic       bool                          `json:"use_semantic"`
	DisableSemantic   bool                          `json:"disable_semantic"`
	Model             string                        `json:"model"`
	MaxEvidenceChars  int                           `json:"max_evidence_chars"`
	AnswerReview      bool                          `json:"answer_review"`
	AnswerReviewModel string                        `json:"answer_review_model"`
	TraceEnabled      *bool                         `json:"trace_enabled,omitempty"`
	TraceSurface      string                        `json:"trace_surface,omitempty"`
	TraceContinuity   *researchtrace.ChatContinuity `json:"trace_continuity,omitempty"`
}

type ResearchTraceCompareRequest struct {
	TracePath  string `json:"trace_path"`
	RunCurrent bool   `json:"run_current"`
}

type ChatTranscriptSaveRequest struct {
	Turns              []ChatTranscriptTurn `json:"turns"`
	PinnedEvidenceKeys []string             `json:"pinned_evidence_keys"`
	SelectedLookup     string               `json:"selected_lookup"`
}

type ChatTranscriptTurn struct {
	ID                string                   `json:"id"`
	Question          string                   `json:"question"`
	RetrievalQuestion string                   `json:"retrieval_question"`
	Status            string                   `json:"status"`
	Answer            string                   `json:"answer"`
	ResearchPack      brainresearch.Pack       `json:"research_pack"`
	Citations         []brainresearch.Citation `json:"citations"`
	Error             string                   `json:"error"`
	CreatedAt         string                   `json:"created_at"`
}

type ChatTranscriptSaveResponse struct {
	Path  string `json:"path"`
	Turns int    `json:"turns"`
	Bytes int64  `json:"bytes"`
}

type ChatShareCreateRequest struct {
	Turn ChatTranscriptTurn `json:"turn"`
}

type ChatShareResponse struct {
	Slug         string   `json:"slug"`
	URL          string   `json:"url"`
	Title        string   `json:"title"`
	Summary      string   `json:"summary"`
	Categories   []string `json:"categories"`
	OriginalURLs []string `json:"original_urls"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

type ChatShareListResponse struct {
	Shares []ChatShareResponse `json:"shares"`
}

type LinkAddRequest struct {
	URL    string   `json:"url"`
	URLs   []string `json:"urls"`
	Enrich bool     `json:"enrich"`
	Defer  bool     `json:"defer"`
}

type TagRequest struct {
	Lookup string `json:"lookup"`
	Tags   string `json:"tags"`
}
