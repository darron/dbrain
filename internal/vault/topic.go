package vault

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/topics"
)

func WriteTopic(cfg config.Config, graph topics.TopicMap) error {
	fullPath := filepath.Join(cfg.VaultDir, filepath.FromSlash(TopicNoteRelativePath(graph.Topic)))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return fmt.Errorf("create topic note dir: %w", err)
	}

	body := RenderTopic(graph)
	existing, err := os.ReadFile(fullPath)
	if err == nil && string(existing) == body {
		return nil
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write topic note: %w", err)
	}
	return nil
}
