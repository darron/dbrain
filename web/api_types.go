package web

import (
	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

type AppInfo struct {
	Name   string `json:"name"`
	HasFTS bool   `json:"has_fts"`
}

type BootstrapResponse struct {
	App            AppInfo                  `json:"app"`
	Backlog        store.BacklogStats       `json:"backlog"`
	Activity       store.ActivityStats      `json:"activity"`
	SourceActivity store.SourceActivityFeed `json:"source_activity"`
}

type SearchResponse struct {
	Query   string               `json:"query"`
	Limit   int                  `json:"limit"`
	Results []model.SearchResult `json:"results"`
}

type GetResponse struct {
	Lookup        string                 `json:"lookup"`
	Kind          string                 `json:"kind"`
	Item          *ItemResponse          `json:"item,omitempty"`
	Source        *model.SourceDocument  `json:"source,omitempty"`
	LinkedSources []model.ItemSourceRef  `json:"linked_sources,omitempty"`
	Backlinks     []model.SourceBacklink `json:"backlinks,omitempty"`
	QuotedPosts   []ItemResponse         `json:"quoted_posts,omitempty"`
	NoteContent   string                 `json:"note_content,omitempty"`
	NoteError     string                 `json:"note_error,omitempty"`
}

type ItemResponse struct {
	model.Item
	Media []MediaRefResponse `json:"media,omitempty"`
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
}

type ResearchSynthesisRequest struct {
	Question         string             `json:"question"`
	ResearchPack     brainresearch.Pack `json:"research_pack"`
	Model            string             `json:"model"`
	MaxEvidenceChars int                `json:"max_evidence_chars"`
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

type LinkAddRequest struct {
	URL    string   `json:"url"`
	URLs   []string `json:"urls"`
	Enrich bool     `json:"enrich"`
}

type TagRequest struct {
	Lookup string `json:"lookup"`
	Tags   string `json:"tags"`
}
