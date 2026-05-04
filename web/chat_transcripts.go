package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/brainresearch"
)

func (s *server) handleChatTranscriptSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var req ChatTranscriptSaveRequest
	limitedBody := http.MaxBytesReader(w, r.Body, defaultTranscriptBytes)
	if err := json.NewDecoder(limitedBody).Decode(&req); err != nil {
		writeMessage(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}
	if len(req.Turns) == 0 {
		writeMessage(w, http.StatusBadRequest, "turns are required")
		return
	}

	firstQuestion := "chat"
	validTurns := 0
	for _, turn := range req.Turns {
		if strings.TrimSpace(turn.Question) != "" || strings.TrimSpace(turn.Answer) != "" {
			validTurns++
			if strings.TrimSpace(turn.Question) != "" {
				firstQuestion = turn.Question
				break
			}
		}
	}
	if validTurns == 0 {
		writeMessage(w, http.StatusBadRequest, "at least one turn must include a question or answer")
		return
	}

	savedAt := time.Now().UTC()
	dir := filepath.Join(s.cfg.DataDir, "chat-transcripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("create transcript directory: %w", err))
		return
	}

	filename := fmt.Sprintf("%s-%s-%d.md", savedAt.Format("20060102-150405"), chatTranscriptSlug(firstQuestion), savedAt.UnixNano())
	path := filepath.Join(dir, filename)
	content := renderChatTranscriptMarkdown(req, savedAt)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("write transcript: %w", err))
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("stat transcript: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, ChatTranscriptSaveResponse{
		Path:  path,
		Turns: len(req.Turns),
		Bytes: info.Size(),
	})
}

func renderChatTranscriptMarkdown(req ChatTranscriptSaveRequest, savedAt time.Time) string {
	var b strings.Builder
	b.WriteString("# dbrain chat transcript\n\n")
	b.WriteString("Saved: ")
	b.WriteString(savedAt.Format(time.RFC3339))
	b.WriteString("\n\n")
	b.WriteString("Scope: diagnostic export only; this file is not indexed into dbrain retrieval unless imported separately.\n\n")

	if selected := strings.TrimSpace(req.SelectedLookup); selected != "" {
		b.WriteString("Selected lookup: `")
		b.WriteString(selected)
		b.WriteString("`\n\n")
	}
	if len(req.PinnedEvidenceKeys) > 0 {
		b.WriteString("Pinned evidence:\n")
		for _, key := range req.PinnedEvidenceKeys {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			b.WriteString("- `")
			b.WriteString(key)
			b.WriteString("`\n")
		}
		b.WriteByte('\n')
	}

	for i, turn := range req.Turns {
		b.WriteString("## Turn ")
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString("\n\n")
		if value := strings.TrimSpace(turn.ID); value != "" {
			b.WriteString("ID: `")
			b.WriteString(value)
			b.WriteString("`\n\n")
		}
		if value := strings.TrimSpace(turn.CreatedAt); value != "" {
			b.WriteString("Created: ")
			b.WriteString(value)
			b.WriteString("\n\n")
		}
		if value := strings.TrimSpace(turn.Status); value != "" {
			b.WriteString("Status: `")
			b.WriteString(value)
			b.WriteString("`\n\n")
		}
		if value := strings.TrimSpace(turn.Error); value != "" {
			b.WriteString("Error: ")
			b.WriteString(value)
			b.WriteString("\n\n")
		}

		b.WriteString("### Question\n\n")
		b.WriteString(truncateTranscriptText(turn.Question, 16000))
		b.WriteString("\n\n")

		if value := strings.TrimSpace(turn.RetrievalQuestion); value != "" {
			b.WriteString("### Retrieval Query\n\n```text\n")
			b.WriteString(truncateTranscriptText(value, 16000))
			b.WriteString("\n```\n\n")
		}

		if value := strings.TrimSpace(turn.Answer); value != "" {
			b.WriteString("### Answer\n\n")
			b.WriteString(truncateTranscriptText(value, 64000))
			b.WriteString("\n\n")
		}

		if len(turn.Citations) > 0 {
			b.WriteString("### Citations\n\n")
			for _, citation := range turn.Citations {
				writeTranscriptCitation(&b, citation)
			}
			b.WriteByte('\n')
		}

		writeTranscriptResearchPack(&b, turn.ResearchPack)
	}

	return b.String()
}

func writeTranscriptCitation(b *strings.Builder, citation brainresearch.Citation) {
	sourceKey := strings.TrimSpace(citation.SourceKey)
	if sourceKey == "" {
		return
	}
	b.WriteString("- `")
	b.WriteString(sourceKey)
	b.WriteString("`")
	if title := strings.TrimSpace(citation.Title); title != "" {
		b.WriteString(" - ")
		b.WriteString(title)
	}
	if citation.Kind != "" || citation.NotePath != "" || citation.URL != "" {
		b.WriteString(" (")
		parts := make([]string, 0, 3)
		if value := strings.TrimSpace(citation.Kind); value != "" {
			parts = append(parts, value)
		}
		if value := strings.TrimSpace(citation.NotePath); value != "" {
			parts = append(parts, value)
		}
		if value := strings.TrimSpace(citation.URL); value != "" {
			parts = append(parts, value)
		}
		b.WriteString(strings.Join(parts, "; "))
		b.WriteString(")")
	}
	b.WriteByte('\n')
}

func writeTranscriptResearchPack(b *strings.Builder, pack brainresearch.Pack) {
	hasPack := strings.TrimSpace(pack.Question) != "" ||
		len(pack.Evidence) > 0 ||
		len(pack.ExactTagEvidence) > 0 ||
		pack.TopicBrief != nil ||
		strings.TrimSpace(pack.Coverage.RecallNote) != ""
	if !hasPack {
		return
	}

	b.WriteString("### Research Pack\n\n")
	if value := strings.TrimSpace(pack.Question); value != "" {
		b.WriteString("Question: ")
		b.WriteString(value)
		b.WriteString("\n\n")
	}
	if value := strings.TrimSpace(pack.Coverage.RecallNote); value != "" {
		b.WriteString("Recall: ")
		b.WriteString(value)
		b.WriteString("\n\n")
	}
	if plan := pack.QueryPlan; plan.TextQuery != "" || len(plan.QueryTerms) > 0 || len(plan.TagQueries) > 0 {
		b.WriteString("Query plan:\n")
		if value := strings.TrimSpace(plan.TextQuery); value != "" {
			b.WriteString("- text: `")
			b.WriteString(value)
			b.WriteString("`\n")
		}
		if len(plan.QueryTerms) > 0 {
			b.WriteString("- terms: ")
			b.WriteString(strings.Join(plan.QueryTerms, ", "))
			b.WriteByte('\n')
		}
		if len(plan.TagQueries) > 0 {
			b.WriteString("- tags: ")
			b.WriteString(strings.Join(plan.TagQueries, ", "))
			b.WriteByte('\n')
		}
		if strings.TrimSpace(plan.Planner) != "" {
			b.WriteString("- planner: ")
			b.WriteString(plan.Planner)
			if strings.TrimSpace(plan.PlannerModel) != "" {
				b.WriteString(" (")
				b.WriteString(plan.PlannerModel)
				b.WriteString(")")
			}
			b.WriteByte('\n')
		}
		if strings.TrimSpace(plan.PlannerError) != "" {
			b.WriteString("- planner_error: ")
			b.WriteString(plan.PlannerError)
			b.WriteByte('\n')
		}
		if len(plan.QueryVariants) > 0 {
			b.WriteString("- variants:\n")
			for _, variant := range plan.QueryVariants {
				b.WriteString("  - `")
				b.WriteString(variant.Query)
				b.WriteString("`")
				if strings.TrimSpace(variant.Reason) != "" {
					b.WriteString(" (")
					b.WriteString(variant.Reason)
					b.WriteString(")")
				}
				b.WriteByte('\n')
			}
		}
		if len(plan.Concepts) > 0 {
			b.WriteString("- concepts:\n")
			for _, concept := range plan.Concepts {
				b.WriteString("  - ")
				b.WriteString(concept.Key)
				if !concept.Required {
					b.WriteString(" (optional)")
				}
				b.WriteString(": ")
				b.WriteString(strings.Join(concept.Terms, ", "))
				b.WriteByte('\n')
			}
		}
		b.WriteByte('\n')
	}
	if pack.TopicBrief != nil {
		b.WriteString("Topic brief: ")
		b.WriteString(truncateTranscriptText(pack.TopicBrief.Summary, 8000))
		b.WriteString("\n\n")
	}

	writeTranscriptEvidenceSection(b, "Evidence", pack.Evidence)
	writeTranscriptEvidenceSection(b, "Exact Tag Evidence", pack.ExactTagEvidence)
}

func writeTranscriptEvidenceSection(b *strings.Builder, heading string, evidence []ask.Evidence) {
	if len(evidence) == 0 {
		return
	}
	b.WriteString("### ")
	b.WriteString(heading)
	b.WriteString("\n\n")
	for _, row := range evidence {
		sourceKey := strings.TrimSpace(row.SourceKey)
		if sourceKey == "" {
			continue
		}
		b.WriteString("- `")
		b.WriteString(sourceKey)
		b.WriteString("`")
		if title := strings.TrimSpace(row.Title); title != "" {
			b.WriteString(" - ")
			b.WriteString(title)
		}
		b.WriteByte('\n')
		if meta := evidenceMeta(row); meta != "" {
			b.WriteString("  - ")
			b.WriteString(meta)
			b.WriteByte('\n')
		}
		if summary := strings.TrimSpace(row.Summary); summary != "" {
			b.WriteString("  - summary: ")
			b.WriteString(truncateTranscriptText(summary, 2000))
			b.WriteByte('\n')
		}
		if excerpt := strings.TrimSpace(row.Excerpt); excerpt != "" {
			b.WriteString("  - excerpt: ")
			b.WriteString(truncateTranscriptText(excerpt, 2000))
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')
}

func evidenceMeta(row ask.Evidence) string {
	parts := make([]string, 0, 6)
	if value := strings.TrimSpace(row.Kind); value != "" {
		parts = append(parts, "kind="+value)
	}
	if value := strings.TrimSpace(row.SourceType); value != "" {
		parts = append(parts, "type="+value)
	}
	if value := strings.TrimSpace(row.RelatedTo); value != "" {
		parts = append(parts, "related_to="+value)
	}
	if value := strings.TrimSpace(row.Relationship); value != "" {
		parts = append(parts, "relationship="+value)
	}
	if value := strings.TrimSpace(row.NotePath); value != "" {
		parts = append(parts, "note="+value)
	}
	if value := strings.TrimSpace(row.URL); value != "" {
		parts = append(parts, "url="+value)
	}
	return strings.Join(parts, "; ")
}

func chatTranscriptSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if b.Len() >= 48 {
				break
			}
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "chat"
	}
	return slug
}

func truncateTranscriptText(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	if maxChars <= 0 || len([]rune(value)) <= maxChars {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxChars]) + "\n\n[truncated]"
}
