package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRootCommandHelpIncludesCoreCommands(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(nil)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"import", "extract", "hydrate", "search", "get"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected help output to contain %q, got %q", value, output)
		}
	}
}

func TestImportCommandHelpIncludesYouTubeImporter(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"import"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext: %v", err)
	}

	output := stdout.String()
	for _, value := range []string{"ft", "youtube"} {
		if !strings.Contains(output, value) {
			t.Fatalf("expected import help output to contain %q, got %q", value, output)
		}
	}
}
