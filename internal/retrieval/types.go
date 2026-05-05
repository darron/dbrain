package retrieval

type ContentSection struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Status    string `json:"status,omitempty"`
	Model     string `json:"model,omitempty"`
	Tool      string `json:"tool,omitempty"`
	At        string `json:"at,omitempty"`
	Chars     int    `json:"chars"`
	Text      string `json:"text,omitempty"`
	Truncated bool   `json:"truncated"`
}

type EvidenceDocument struct {
	SourceKey     string         `json:"source_key"`
	Kind          string         `json:"kind"`
	Title         string         `json:"title"`
	URL           string         `json:"url"`
	NotePath      string         `json:"note_path"`
	Summary       string         `json:"summary"`
	Excerpt       string         `json:"excerpt"`
	Author        string         `json:"author,omitempty"`
	SourceType    string         `json:"source_type,omitempty"`
	PublishedAt   string         `json:"published_at,omitempty"`
	ExtractedAt   string         `json:"extracted_at,omitempty"`
	SummarizedAt  string         `json:"summarized_at,omitempty"`
	UserTags      string         `json:"user_tags,omitempty"`
	EntityMatches []string       `json:"entity_matches,omitempty"`
	RelatedTo     string         `json:"related_to,omitempty"`
	Relationship  string         `json:"relationship,omitempty"`
	Retrieval     *RetrievalInfo `json:"retrieval,omitempty"`
}

type RetrievalInfo struct {
	Score        int               `json:"score"`
	Signals      []RetrievalSignal `json:"signals,omitempty"`
	MatchedTerms []string          `json:"matched_terms,omitempty"`
	MissingTerms []string          `json:"missing_terms,omitempty"`
}

type RetrievalSignal struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	Weight int    `json:"weight"`
}
