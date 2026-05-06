package githubimport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/model"
)

func (c *client) repoExtract(ctx context.Context, repo repository) (model.ExtractResult, string, error) {
	readme, hasReadme, err := c.repoReadme(ctx, repo.FullName)
	if err != nil {
		return model.ExtractResult{}, "", err
	}

	readmeText := ""
	if hasReadme {
		readmeText, err = decodeReadme(readme)
		if err != nil {
			return model.ExtractResult{}, "", err
		}
	}

	content := buildRepoExtractContent(repo, readmeText)
	rawJSONBytes, err := json.Marshal(map[string]any{
		"repo":   repo,
		"readme": map[string]any{"present": hasReadme, "path": readme.Path, "html_url": readme.HTMLURL},
	})
	if err != nil {
		return model.ExtractResult{}, "", fmt.Errorf("marshal github extract: %w", err)
	}

	result := model.ExtractResult{
		CanonicalURL: strings.TrimSpace(repo.HTMLURL),
		FinalURL:     strings.TrimSpace(repo.HTMLURL),
		Title:        strings.TrimSpace(repo.FullName),
		Description:  strings.TrimSpace(repo.Description),
		SiteName:     githubSiteName,
		Content:      content,
		RawJSON:      string(rawJSONBytes),
		Status:       "ok",
		FetchedAt:    time.Now().UTC(),
		Tool:         "github-api",
		ToolVersion:  apiVersion,
	}
	return result, hashText(content), nil
}

func buildRepoExtractContent(repo repository, readme string) string {
	lines := []string{
		"Repository: " + strings.TrimSpace(repo.FullName),
	}
	if value := strings.TrimSpace(repo.Description); value != "" {
		lines = append(lines, "Description: "+value)
	}
	if value := strings.TrimSpace(repo.HTMLURL); value != "" {
		lines = append(lines, "GitHub URL: "+value)
	}
	if value := strings.TrimSpace(repo.Homepage); value != "" {
		lines = append(lines, "Homepage: "+value)
	}
	if value := strings.TrimSpace(repo.Language); value != "" {
		lines = append(lines, "Primary language: "+value)
	}
	if len(repo.Topics) > 0 {
		topics := append([]string(nil), repo.Topics...)
		sort.Strings(topics)
		lines = append(lines, "Topics: "+strings.Join(topics, ", "))
	}
	if value := strings.TrimSpace(repo.DefaultBranch); value != "" {
		lines = append(lines, "Default branch: "+value)
	}
	if repo.License != nil && strings.TrimSpace(repo.License.Name) != "" {
		lines = append(lines, "License: "+strings.TrimSpace(repo.License.Name))
	}
	lines = append(lines, "Private: "+boolLabel(repo.Private))
	lines = append(lines, "Archived: "+boolLabel(repo.Archived))
	lines = append(lines, "Fork: "+boolLabel(repo.Fork))

	parts := []string{strings.Join(lines, "\n")}
	if strings.TrimSpace(readme) != "" {
		parts = append(parts, "README:\n"+strings.TrimSpace(readme))
	}
	return strings.Join(parts, "\n\n")
}

func decodeReadme(readme readmePayload) (string, error) {
	if strings.TrimSpace(readme.Content) == "" {
		return "", nil
	}
	if strings.TrimSpace(strings.ToLower(readme.Encoding)) != "base64" {
		return "", fmt.Errorf("unsupported github readme encoding %q", readme.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(readme.Content, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("decode github readme: %w", err)
	}
	return strings.TrimSpace(string(decoded)), nil
}
