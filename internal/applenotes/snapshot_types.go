package applenotes

import "time"

type Options struct {
	DBPath             string
	SnapshotDir        string
	KeepSnapshot       bool
	Limit              int
	DryRun             bool
	ShowTitles         bool
	Force              bool
	SkipAttachments    bool
	SkipAttachmentOCR  bool
	AttachmentMaxBytes int64
	TesseractBinary    string
	ExcludeFolders     []string
	ExcludeAccounts    []string
	ExcludeShared      bool
	IncludeLocked      bool
	ForgetExcluded     bool
	Summarize          bool
	SummaryModel       string
	SummaryCLI         string
	SummaryLength      string
	Timeout            time.Duration
	Progress           ProgressFunc
}

type ProgressFunc func(ProgressEvent)

type ProgressEvent struct {
	Phase           string `json:"phase"`
	Index           int    `json:"index,omitempty"`
	Total           int    `json:"total,omitempty"`
	SourceKey       string `json:"source_key,omitempty"`
	Title           string `json:"title,omitempty"`
	Status          string `json:"status,omitempty"`
	Reason          string `json:"reason,omitempty"`
	Links           int    `json:"links,omitempty"`
	Attachments     int    `json:"attachments,omitempty"`
	TextChars       int    `json:"text_chars,omitempty"`
	AttachmentChars int    `json:"attachment_chars,omitempty"`
	Rendered        bool   `json:"rendered,omitempty"`
	SummaryStatus   string `json:"summary_status,omitempty"`
	SummaryChanged  bool   `json:"summary_changed,omitempty"`
}

type SnapshotInfo struct {
	SourceDBPath string   `json:"source_db_path"`
	Dir          string   `json:"dir"`
	DBPath       string   `json:"db_path"`
	CopiedFiles  []string `json:"copied_files"`
	Kept         bool     `json:"kept"`
}

type ProbeStats struct {
	SourceDBPath string                `json:"source_db_path"`
	Snapshot     SnapshotInfo          `json:"snapshot"`
	Tables       map[string]TableProbe `json:"tables"`
	NoteCount    int                   `json:"note_count"`
	AccountCount int                   `json:"account_count"`
	FolderCount  int                   `json:"folder_count"`
	Warnings     []string              `json:"warnings,omitempty"`
	Duration     time.Duration         `json:"duration"`
}

type TableProbe struct {
	Exists  bool     `json:"exists"`
	Columns []string `json:"columns,omitempty"`
	Rows    int      `json:"rows,omitempty"`
}

type snapshotCleanup func() error
