package sourceenrich

import "testing"

func TestFirstMarkdownTitleReadsReaderTitleLine(t *testing.T) {
	t.Parallel()

	content := "Title: Government of Canada announces renewed funding\n\nURL Source: https://canada.ca/example\n\nMarkdown Content:\n# Government of Canada announces renewed funding"
	if got := firstMarkdownTitle(content); got != "Government of Canada announces renewed funding" {
		t.Fatalf("expected reader title line, got %q", got)
	}
}
