package retrieval

import (
	"strings"
	"testing"
)

func TestQueryWindowReturnsFullTextWhenUnbounded(t *testing.T) {
	text := "alpha beta gamma"
	result := QueryWindow(text, []string{"beta"}, []string{"beta"}, QueryWindowOptions{})
	if !result.Matched {
		t.Fatal("expected a match")
	}
	if result.Truncated {
		t.Fatal("did not expect truncation")
	}
	if result.Text != text {
		t.Fatalf("expected full text %q, got %q", text, result.Text)
	}
}

func TestQueryWindowUsesCallerTermCountsForRarityScoring(t *testing.T) {
	text := "alpha target alpha alpha. rare target omega"
	result := QueryWindow(text, []string{"target"}, []string{"alpha", "rare"}, QueryWindowOptions{
		MaxChars:    24,
		LeadDivisor: 4,
		TermCounts: map[string]int{
			"alpha": 3,
			"rare":  1,
		},
	})
	if !result.Matched {
		t.Fatal("expected a match")
	}
	if !strings.Contains(result.Text, "rare target") {
		t.Fatalf("expected rarer term window, got %q", result.Text)
	}
}
