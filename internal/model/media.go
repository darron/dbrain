package model

import "time"

type MediaAsset struct {
	ID             int64     `json:"id"`
	RemoteURL      string    `json:"remote_url"`
	MediaType      string    `json:"media_type"`
	MIMEType       string    `json:"mime_type"`
	Width          int       `json:"width"`
	Height         int       `json:"height"`
	ByteSize       int64     `json:"byte_size"`
	ContentHash    string    `json:"content_hash"`
	DownloadStatus string    `json:"download_status"`
	DownloadError  string    `json:"download_error"`
	LocalPath      string    `json:"local_path"`
	DiscoveredAt   time.Time `json:"discovered_at"`
	DownloadedAt   time.Time `json:"downloaded_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ItemMediaRef struct {
	ItemID         int64  `json:"item_id"`
	MediaAssetID   int64  `json:"media_asset_id"`
	Ordinal        int    `json:"ordinal"`
	ExpandedURL    string `json:"expanded_url"`
	RemoteURL      string `json:"remote_url"`
	MediaType      string `json:"media_type"`
	DownloadStatus string `json:"download_status"`
	LocalPath      string `json:"local_path"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
}

type MediaDownloadResult struct {
	MIMEType     string    `json:"mime_type"`
	ByteSize     int64     `json:"byte_size"`
	ContentHash  string    `json:"content_hash"`
	LocalPath    string    `json:"local_path"`
	Status       string    `json:"status"`
	Error        string    `json:"error"`
	DownloadedAt time.Time `json:"downloaded_at"`
}
