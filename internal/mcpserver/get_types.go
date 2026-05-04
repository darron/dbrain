package mcpserver

const (
	getModeBrief    = "brief"
	getModeEvidence = "evidence"
	getModeRaw      = "raw"
	getModeRendered = "rendered"

	defaultGetSectionChars = 4000
	maxGetSectionCharsHard = 50000
	maxGetRelatedSections  = 5
	maxGetManyLookups      = 20
)

type getSection struct {
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

type getManyError struct {
	Lookup string `json:"lookup"`
	Error  string `json:"error"`
}
