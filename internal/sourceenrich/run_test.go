package sourceenrich

import (
	"testing"

	"dbrain/internal/model"
)

func TestSkipSummaryReasonSkipsTranscriptUnavailableYouTubeMetadataOnly(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{SourceType: "youtube"}
	extract := model.ExtractResult{
		Content: "Why I will NEVER surrender my guns.\nChannel business contact - ladner_chevy@hotmail.com",
		RawJSON: `{"extracted":{"transcriptSource":"unavailable","transcriptionProvider":null,"transcriptCharacters":null}}`,
	}

	reason, ok := skipSummaryReason(source, extract)
	if !ok {
		t.Fatal("expected summary to be skipped")
	}
	if reason == "" {
		t.Fatal("expected skip reason")
	}
}

func TestSkipSummaryReasonAllowsTranscriptBackedYouTubeExtract(t *testing.T) {
	t.Parallel()

	source := model.SourceDocument{SourceType: "youtube"}
	extract := model.ExtractResult{
		Content: "Transcript:\nreal transcript content",
		RawJSON: `{"extracted":{"transcriptSource":"captionTracks","transcriptionProvider":null,"transcriptCharacters":2048}}`,
	}

	if reason, ok := skipSummaryReason(source, extract); ok {
		t.Fatalf("expected transcript-backed extract to summarize, got reason %q", reason)
	}
}
