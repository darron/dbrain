package mcpeval

type Case struct {
	Name                                string   `json:"name"`
	Question                            string   `json:"question"`
	Limit                               int      `json:"limit,omitempty"`
	MaxCharsPerDoc                      int      `json:"max_chars_per_doc,omitempty"`
	SourceTypes                         []string `json:"source_types,omitempty"`
	IncludeRelated                      bool     `json:"include_related,omitempty"`
	RelatedLimit                        int      `json:"related_limit,omitempty"`
	MinEvidence                         int      `json:"min_evidence,omitempty"`
	ExpectSourceKeys                    []string `json:"expect_source_keys,omitempty"`
	ExpectTopSourceKeys                 []string `json:"expect_top_source_keys,omitempty"`
	ExpectAnySourceKeys                 []string `json:"expect_any_source_keys,omitempty"`
	ForbidSourceKeys                    []string `json:"forbid_source_keys,omitempty"`
	ExpectText                          []string `json:"expect_text,omitempty"`
	ExpectTopText                       []string `json:"expect_top_text,omitempty"`
	ForbidText                          []string `json:"forbid_text,omitempty"`
	MinExactTagEvidence                 int      `json:"min_exact_tag_evidence,omitempty"`
	ExpectExactTagEvidenceSourceKeys    []string `json:"expect_exact_tag_evidence_source_keys,omitempty"`
	ExpectAnyExactTagEvidenceSourceKeys []string `json:"expect_any_exact_tag_evidence_source_keys,omitempty"`
	ExpectExactTagEvidenceText          []string `json:"expect_exact_tag_evidence_text,omitempty"`
	ForbidExactTagEvidenceText          []string `json:"forbid_exact_tag_evidence_text,omitempty"`
	RequireTopMatchedTerms              []string `json:"require_top_matched_terms,omitempty"`
	ForbidTopMissingTerms               []string `json:"forbid_top_missing_terms,omitempty"`
	MaxLatencyMS                        int64    `json:"max_latency_ms,omitempty"`
}

type Options struct {
	Cases []Case
}

type Report struct {
	StartedAt  string       `json:"started_at"`
	DurationMS int64        `json:"duration_ms"`
	Passed     int          `json:"passed"`
	Failed     int          `json:"failed"`
	Cases      []CaseResult `json:"cases"`
}

type CaseResult struct {
	Name                  string            `json:"name"`
	Question              string            `json:"question"`
	Passed                bool              `json:"passed"`
	DurationMS            int64             `json:"duration_ms"`
	EvidenceCount         int               `json:"evidence_count"`
	ExactTagEvidenceCount int               `json:"exact_tag_evidence_count,omitempty"`
	SourceKeys            []string          `json:"source_keys"`
	ExactTagSourceKeys    []string          `json:"exact_tag_source_keys,omitempty"`
	TopEvidence           []EvidenceSummary `json:"top_evidence,omitempty"`
	ExactTagEvidence      []EvidenceSummary `json:"exact_tag_evidence,omitempty"`
	Failures              []string          `json:"failures,omitempty"`
}

type EvidenceSummary struct {
	SourceKey    string   `json:"source_key"`
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Score        int      `json:"score,omitempty"`
	Signals      int      `json:"signals,omitempty"`
	MatchedTerms []string `json:"matched_terms,omitempty"`
	MissingTerms []string `json:"missing_terms,omitempty"`
}
