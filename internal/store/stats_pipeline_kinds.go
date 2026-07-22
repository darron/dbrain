package store

const (
	// PipelineKindAll identifies the aggregate row returned before grouped
	// pipeline rows. Consumers must not present it as an ordinary kind.
	PipelineKindAll              = "ALL"
	pipelineKindAll              = PipelineKindAll
	pipelineKindAppleNote        = "apple_note"
	pipelineKindSafariTab        = "safari_tab"
	pipelineKindXMediaTranscript = "x_media_transcript"
	pipelineKindXMediaSummary    = "x_media_summary"
	pipelineKindXPhotoOCR        = "x_photo_ocr"
)
