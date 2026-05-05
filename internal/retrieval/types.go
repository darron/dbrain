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
