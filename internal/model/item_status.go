package model

// Item enrichment status values are persisted in SQLite. Keep these string
// values stable unless a schema migration intentionally changes them.
const XMediaTranscriptArticleTitle = "X Media Transcript"

const (
	ItemSummaryStatusOK      = "ok"
	ItemSummaryStatusError   = "error"
	ItemSummaryStatusBlocked = "blocked"
	ItemSummaryStatusSkipped = "skipped"
)

const (
	ItemOCRStatusOK      = "ok"
	ItemOCRStatusError   = "error"
	ItemOCRStatusBlocked = "blocked"
	ItemOCRStatusSkipped = "skipped"
)

const (
	XMediaTranscriptStatusOK       = "ok"
	XMediaTranscriptStatusError    = "error"
	XMediaTranscriptStatusEmpty    = "empty"
	XMediaTranscriptStatusNoise    = "noise"
	XMediaTranscriptStatusTooShort = "too_short"
	XMediaTranscriptStatusNoAudio  = "no_audio"
)
