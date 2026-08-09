package model

import "time"

const (
	MediaDownloadMaxConsecutiveErrors = 3
	MediaDownloadRetryCooldown        = 24 * time.Hour
)

// Media download/archive statuses are persisted in SQLite; keep these values stable.
const (
	MediaDownloadStatusPending    = "pending"
	MediaDownloadStatusDownloaded = "downloaded"
	MediaDownloadStatusError      = "error"
	MediaDownloadStatusGone       = "gone"
	MediaDownloadStatusBlocked    = "blocked"

	MediaArchiveStatusArchived = "archived"
	MediaArchiveStatusError    = "error"
)

// MediaCandidate is a source-neutral reference discovered while importing an
// item. The remote URL identifies the downloadable asset; ExpandedURL keeps
// the human-facing provenance shown with the item media link.
type MediaCandidate struct {
	RemoteURL   string `json:"remote_url"`
	ExpandedURL string `json:"expanded_url"`
	MediaType   string `json:"media_type"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

type MediaAsset struct {
	ID              int64     `json:"id"`
	RemoteURL       string    `json:"remote_url"`
	MediaType       string    `json:"media_type"`
	MIMEType        string    `json:"mime_type"`
	Width           int       `json:"width"`
	Height          int       `json:"height"`
	ByteSize        int64     `json:"byte_size"`
	ContentHash     string    `json:"content_hash"`
	DownloadStatus  string    `json:"download_status"`
	DownloadError   string    `json:"download_error"`
	DownloadErrors  int       `json:"download_errors"`
	LastDownloadAt  time.Time `json:"last_download_attempt_at"`
	LocalPath       string    `json:"local_path"`
	ArchiveProvider string    `json:"archive_provider"`
	ArchiveBucket   string    `json:"archive_bucket"`
	ArchiveKey      string    `json:"archive_key"`
	ArchiveURL      string    `json:"archive_url"`
	ArchiveETag     string    `json:"archive_etag"`
	ArchiveStatus   string    `json:"archive_status"`
	ArchiveError    string    `json:"archive_error"`
	DiscoveredAt    time.Time `json:"discovered_at"`
	DownloadedAt    time.Time `json:"downloaded_at"`
	ArchivedAt      time.Time `json:"archived_at"`
	LocalPrunedAt   time.Time `json:"local_pruned_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ItemMediaRef struct {
	ItemID          int64     `json:"item_id"`
	MediaAssetID    int64     `json:"media_asset_id"`
	Ordinal         int       `json:"ordinal"`
	ExpandedURL     string    `json:"expanded_url"`
	RemoteURL       string    `json:"remote_url"`
	MediaType       string    `json:"media_type"`
	DownloadStatus  string    `json:"download_status"`
	DownloadErrors  int       `json:"download_errors"`
	LastDownloadAt  time.Time `json:"last_download_attempt_at"`
	LocalPath       string    `json:"local_path"`
	ArchiveProvider string    `json:"archive_provider"`
	ArchiveBucket   string    `json:"archive_bucket"`
	ArchiveKey      string    `json:"archive_key"`
	ArchiveURL      string    `json:"archive_url"`
	ArchiveStatus   string    `json:"archive_status"`
	Width           int       `json:"width"`
	Height          int       `json:"height"`
	LocalPrunedAt   time.Time `json:"local_pruned_at"`
}

type MediaDownloadResult struct {
	MIMEType     string    `json:"mime_type"`
	ByteSize     int64     `json:"byte_size"`
	ContentHash  string    `json:"content_hash"`
	LocalPath    string    `json:"local_path"`
	Status       string    `json:"status"`
	Error        string    `json:"error"`
	AttemptedAt  time.Time `json:"attempted_at"`
	DownloadedAt time.Time `json:"downloaded_at"`
}

type MediaArchiveResult struct {
	Provider   string    `json:"provider"`
	Bucket     string    `json:"bucket"`
	Key        string    `json:"key"`
	URL        string    `json:"url"`
	ETag       string    `json:"etag"`
	Status     string    `json:"status"`
	Error      string    `json:"error"`
	ArchivedAt time.Time `json:"archived_at"`
}
