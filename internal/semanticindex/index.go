package semanticindex

import "context"

const BackendExact = "exact"

type StatusState string

const (
	StateSearched    StatusState = "searched"
	StateUnavailable StatusState = "unavailable"
)

type StatusReason string

const (
	ReasonNone                 StatusReason = ""
	ReasonProfileMismatch      StatusReason = "profile_mismatch"
	ReasonDimensionMismatch    StatusReason = "dimension_mismatch"
	ReasonTooLarge             StatusReason = "too_large"
	ReasonIndexCorrupt         StatusReason = "index_corrupt"
	ReasonCanceled             StatusReason = "canceled"
	ReasonSearchError          StatusReason = "search_error"
	ReasonProviderUnavailable  StatusReason = "provider_unavailable"
	ReasonQueryEmbeddingFailed StatusReason = "query_embedding_failed"
)

type Status struct {
	State        StatusState  `json:"state"`
	Reason       StatusReason `json:"reason,omitempty"`
	Backend      string       `json:"backend,omitempty"`
	ProfileID    string       `json:"profile_id,omitempty"`
	GenerationID string       `json:"generation_id,omitempty"`
	Scanned      int          `json:"scanned"`
}

type Hit struct {
	ChunkID  string  `json:"chunk_id"`
	Rank     int     `json:"rank"`
	Distance float64 `json:"distance"`
}

type Filters struct {
	AllowedParentKeys    []string `json:"allowed_parent_keys,omitempty"`
	AllowedParentKinds   []string `json:"allowed_parent_kinds,omitempty"`
	AllowedEvidenceRoles []string `json:"allowed_evidence_roles,omitempty"`
}

type SearchOptions struct {
	ProfileID  string
	Dimensions int
	Limit      int
	MaxChunks  int
	Filters    Filters
}

type Searcher interface {
	Search(context.Context, []float32, SearchOptions) ([]Hit, Status, error)
}
