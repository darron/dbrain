package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
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
		if strings.TrimSpace(turn.Status) == "verification_failed" {
			writeMessage(w, http.StatusBadRequest, "verification-failed turns cannot be exported as chat transcripts")
			return
		}
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
	responsePath := filepath.ToSlash(filepath.Join("chat-transcripts", filename))
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
		Path:  responsePath,
		Turns: len(req.Turns),
		Bytes: info.Size(),
	})
}
