package web

import (
	"github.com/darron/dbrain/internal/brainresearch"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/store"
)

type AppInfo struct {
	Name     string `json:"name"`
	RootDir  string `json:"root_dir"`
	VaultDir string `json:"vault_dir"`
	DBPath   string `json:"db_path"`
	HasFTS   bool   `json:"has_fts"`
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
	Item          *model.Item            `json:"item,omitempty"`
	Source        *model.SourceDocument  `json:"source,omitempty"`
	LinkedSources []model.ItemSourceRef  `json:"linked_sources,omitempty"`
	Backlinks     []model.SourceBacklink `json:"backlinks,omitempty"`
	QuotedPosts   []model.Item           `json:"quoted_posts,omitempty"`
	NoteContent   string                 `json:"note_content,omitempty"`
	NoteError     string                 `json:"note_error,omitempty"`
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
