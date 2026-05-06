package app

import (
	"context"

	"github.com/darron/dbrain/internal/runtimeenv"
)

func firstNonEmptyEnv(rootDir string, keys ...string) string {
	return runtimeenv.FirstNonEmpty(rootDir, keys...)
}

func firstNonEmptySecret(ctx context.Context, rootDir string, keys ...string) (string, error) {
	return runtimeenv.FirstNonEmptySecret(ctx, rootDir, keys...)
}

func firstEnvBool(rootDir string, keys ...string) bool {
	return runtimeenv.FirstBool(rootDir, keys...)
}

func firstEnvList(rootDir string, keys ...string) []string {
	return runtimeenv.FirstList(rootDir, keys...)
}
