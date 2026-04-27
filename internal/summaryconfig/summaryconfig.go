package summaryconfig

import (
	"strings"

	"dbrain/internal/runtimeenv"
)

const (
	modelEnv          = "DBRAIN_SUMMARY_MODEL"
	summarizeModelEnv = "SUMMARIZE_MODEL"
)

type ModelResolution struct {
	Model       string
	FromDefault bool
}

func Model(rootDir string, explicit string) string {
	return ResolveModel(rootDir, explicit).Model
}

func ResolveModel(rootDir string, explicit string) ModelResolution {
	if value := strings.TrimSpace(explicit); value != "" {
		return ModelResolution{Model: value}
	}
	return ModelResolution{
		Model:       runtimeenv.FirstNonEmpty(rootDir, modelEnv, summarizeModelEnv),
		FromDefault: true,
	}
}
