package xmediatranscribe

import (
	"strings"
	"testing"

	"dbrain/internal/model"
)

func TestBuildTranscriptSummaryInputIncludesQuotedPostContext(t *testing.T) {
	t.Parallel()

	input := buildTranscriptSummaryInput(model.Item{
		XPostText: "Parent fallback text",
		XPostJSON: `{
			"snapshot":{
				"id":"2040000000000000000",
				"text":"Parent post text",
				"author_handle":"parent",
				"url":"https://x.com/parent/status/2040000000000000000",
				"quoted_post":{
					"id":"2030838203549184127",
					"text":"Quoted post context that changes the meaning.",
					"author_handle":"quoted",
					"url":"https://x.com/quoted/status/2030838203549184127"
				}
			}
		}`,
		ArticleText: "Transcript line one.\nTranscript line two.",
	})

	for _, want := range []string{
		"Primary post:",
		"Parent post text",
		"Quoted post:",
		"Quoted post context that changes the meaning.",
		"Transcript line one.",
	} {
		if !strings.Contains(input, want) {
			t.Fatalf("expected summary input to contain %q\n%s", want, input)
		}
	}
}
