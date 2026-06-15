package okf

import (
	"time"

	"github.com/darron/dbrain/internal/entities"
	"github.com/darron/dbrain/internal/model"
	"github.com/darron/dbrain/internal/topics"
)

const (
	ProfilePrivate = "private"

	manifestFileName = ".dbrain-okf-manifest.json"
)

type ExportOptions struct {
	OutDir             string
	Profile            string
	IncludeItems       bool
	IncludeSources     bool
	IncludeEntities    bool
	IncludeTopics      bool
	SourceTypes        []string
	Limit              int
	IncludeRaw         bool
	MaxRawChars        int
	MediaPublicBaseURL string
	MediaProxyBaseURL  string
	Now                time.Time
	DbrainVersion      string
	Entities           []entities.Entity
	Topics             []topics.TopicMap
}

type ExportResult struct {
	Bundle               string   `json:"bundle"`
	Profile              string   `json:"profile"`
	ItemsWritten         int      `json:"items_written"`
	SourcesWritten       int      `json:"sources_written"`
	EntitiesWritten      int      `json:"entities_written"`
	TopicsWritten        int      `json:"topics_written"`
	IndexesWritten       int      `json:"indexes_written"`
	ConceptsWritten      int      `json:"concepts_written"`
	BrokenInternalLinks  int      `json:"broken_internal_links"`
	OmittedByFilterLinks int      `json:"omitted_by_filter_links"`
	Errors               []string `json:"errors"`
}

type ValidationResult struct {
	Bundle               string               `json:"bundle"`
	Conformant           bool                 `json:"conformant"`
	Concepts             int                  `json:"concepts"`
	Indexes              int                  `json:"indexes"`
	BrokenInternalLinks  int                  `json:"broken_internal_links"`
	OmittedByFilterLinks int                  `json:"omitted_by_filter_links"`
	Errors               []string             `json:"errors"`
	Documents            []ValidationDocument `json:"documents,omitempty"`
}

type ValidationDocument struct {
	Path  string `json:"path"`
	Type  string `json:"type,omitempty"`
	Title string `json:"title,omitempty"`
	Error string `json:"error,omitempty"`
}

type Field struct {
	Name  string
	Value any
}

type Document struct {
	Path        string
	Type        string
	Title       string
	Description string
	Resource    string
	Tags        []string
	Timestamp   string
	Fields      []Field
	Body        string
}

type Manifest struct {
	OKFVersion   string            `json:"okf_version"`
	Profile      string            `json:"profile"`
	ExportedAt   string            `json:"exported_at"`
	Concepts     []ManifestConcept `json:"concepts"`
	OmittedLinks []OmittedLink     `json:"omitted_links,omitempty"`
}

type ManifestConcept struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ConceptID   string `json:"dbrain_concept_id"`
	Kind        string `json:"dbrain_kind"`
	SourceKey   string `json:"dbrain_source_key,omitempty"`
	SourceType  string `json:"dbrain_source_type,omitempty"`
}

type OmittedLink struct {
	FromPath        string `json:"from_path"`
	TargetPath      string `json:"target_path"`
	TargetConceptID string `json:"target_concept_id,omitempty"`
	Reason          string `json:"reason"`
}

type Bundle struct {
	Documents []Document
	Manifest  Manifest
	Stats     ExportResult
}

type SearchResult struct {
	Path        string `json:"path"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ConceptID   string `json:"dbrain_concept_id"`
	SourceKey   string `json:"dbrain_source_key,omitempty"`
	SourceType  string `json:"dbrain_source_type,omitempty"`
	Snippet     string `json:"snippet,omitempty"`
}

type Concept struct {
	Path        string         `json:"path"`
	Type        string         `json:"type"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	ConceptID   string         `json:"dbrain_concept_id"`
	SourceKey   string         `json:"dbrain_source_key,omitempty"`
	SourceType  string         `json:"dbrain_source_type,omitempty"`
	Frontmatter map[string]any `json:"frontmatter"`
	Markdown    string         `json:"markdown"`
	Body        string         `json:"body"`
}

type itemDoc struct {
	Item      model.Item
	Path      string
	ConceptID string
}

type sourceDoc struct {
	Source    model.SourceDocument
	Path      string
	ConceptID string
}

type entityDoc struct {
	Entity    entities.Entity
	Path      string
	ConceptID string
}

type topicDoc struct {
	Topic     topics.TopicMap
	Path      string
	ConceptID string
}
