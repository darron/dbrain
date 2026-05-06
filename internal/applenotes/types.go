package applenotes

const sourceType = "apple_note"
const appleNoteSummaryPromptVersion = "apple-notes-summary-v2"
const appleNoteSummaryPrompt = "Summarize this Apple Note for personal second-brain retrieval. Start with a note shape label: authored_note, research_link_list, shopping_or_checklist, meeting_or_log, scratchpad, or mixed. Preserve personal framing, distinguish tasks/ideas/meeting notes/copied excerpts/topical link collections when clear, treat rough lists as collected context rather than polished claims, do not invent certainty, and write a concise plain-text summary without markdown headings."

type Stats struct {
	SourceDBPath         string       `json:"source_db_path"`
	Snapshot             SnapshotInfo `json:"snapshot"`
	NotesSeen            int          `json:"notes_seen"`
	NotesMatched         int          `json:"notes_matched"`
	NotesImported        int          `json:"notes_imported"`
	NotesCreated         int          `json:"notes_created"`
	NotesUpdated         int          `json:"notes_updated"`
	NotesUnchanged       int          `json:"notes_unchanged"`
	NotesRendered        int          `json:"notes_rendered"`
	NotesSkipped         int          `json:"notes_skipped"`
	NotesBlocked         int          `json:"notes_blocked"`
	NotesPurged          int          `json:"notes_purged"`
	ExcludedAccounts     int          `json:"excluded_accounts"`
	ExcludedFolders      int          `json:"excluded_folders"`
	ExcludedShared       int          `json:"excluded_shared"`
	BlockedLocked        int          `json:"blocked_locked"`
	BlockedEmpty         int          `json:"blocked_empty"`
	AttachmentsSeen      int          `json:"attachments_seen"`
	AttachmentsIndexed   int          `json:"attachments_indexed"`
	AttachmentsExtracted int          `json:"attachments_extracted"`
	AttachmentsOCRed     int          `json:"attachments_ocred"`
	AttachmentsBlocked   int          `json:"attachments_blocked"`
	LinksDiscovered      int          `json:"links_discovered"`
	SummariesCreated     int          `json:"summaries_created"`
	SummaryErrors        int          `json:"summary_errors"`
	SampleTitles         []string     `json:"sample_titles,omitempty"`
	DryRun               bool         `json:"dry_run"`
	Applied              bool         `json:"applied"`
	Errors               int          `json:"errors"`
}
