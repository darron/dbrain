package applenotes

type NoteDocument struct {
	SourceKey         string         `json:"source_key"`
	ExternalID        string         `json:"external_id"`
	CanonicalURL      string         `json:"canonical_url"`
	Title             string         `json:"title"`
	Text              string         `json:"text"`
	Snippet           string         `json:"snippet,omitempty"`
	AccountName       string         `json:"account_name,omitempty"`
	FolderPath        string         `json:"folder_path,omitempty"`
	CreatedAt         string         `json:"created_at,omitempty"`
	UpdatedAt         string         `json:"updated_at,omitempty"`
	PasswordProtected bool           `json:"password_protected"`
	Shared            bool           `json:"shared"`
	Deleted           bool           `json:"deleted"`
	BlockedReason     string         `json:"blocked_reason,omitempty"`
	Links             []string       `json:"links,omitempty"`
	AppleNoteTags     []string       `json:"apple_note_tags,omitempty"`
	Attachments       []Attachment   `json:"attachments,omitempty"`
	AttachmentTexts   []string       `json:"attachment_texts,omitempty"`
	Raw               map[string]any `json:"raw,omitempty"`
}

type Attachment struct {
	ID            string         `json:"id,omitempty"`
	Name          string         `json:"name,omitempty"`
	ContentID     string         `json:"content_id,omitempty"`
	URL           string         `json:"url,omitempty"`
	FileName      string         `json:"file_name,omitempty"`
	FilePath      string         `json:"file_path,omitempty"`
	MIMEType      string         `json:"mime_type,omitempty"`
	UTI           string         `json:"uti,omitempty"`
	ByteSize      int64          `json:"byte_size,omitempty"`
	CreatedAt     string         `json:"created_at,omitempty"`
	UpdatedAt     string         `json:"updated_at,omitempty"`
	Shared        bool           `json:"shared,omitempty"`
	Text          string         `json:"text,omitempty"`
	ExtractStatus string         `json:"extract_status,omitempty"`
	ExtractTool   string         `json:"extract_tool,omitempty"`
	ExtractedAt   string         `json:"extracted_at,omitempty"`
	ExtractError  string         `json:"extract_error,omitempty"`
	BlockedReason string         `json:"blocked_reason,omitempty"`
	Raw           map[string]any `json:"raw,omitempty"`
}
