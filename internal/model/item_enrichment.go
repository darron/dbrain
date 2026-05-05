package model

import "time"

const (
	ItemEnrichmentRoleSummary          = "summary"
	ItemEnrichmentRoleOCR              = "ocr"
	ItemEnrichmentRoleXMediaTranscript = "x_media_transcript"
)

type ItemEnrichment struct {
	ID            int64     `json:"id"`
	ItemID        int64     `json:"item_id"`
	Role          string    `json:"role"`
	Status        string    `json:"status"`
	Text          string    `json:"text"`
	RawJSON       string    `json:"raw_json"`
	Error         string    `json:"error"`
	Model         string    `json:"model"`
	PromptVersion string    `json:"prompt_version"`
	Tool          string    `json:"tool"`
	ToolVersion   string    `json:"tool_version"`
	InputHash     string    `json:"input_hash"`
	CompletedAt   time.Time `json:"completed_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
