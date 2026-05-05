package mcpserver

import "github.com/darron/dbrain/internal/retrieval"

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

type getSection = retrieval.ContentSection

type getManyError struct {
	Lookup string `json:"lookup"`
	Error  string `json:"error"`
}
