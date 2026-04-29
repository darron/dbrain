package app

import "github.com/darron/dbrain/internal/runtimeenv"

func firstNonEmptyEnv(rootDir string, keys ...string) string {
	return runtimeenv.FirstNonEmpty(rootDir, keys...)
}

func firstEnvBool(rootDir string, keys ...string) bool {
	return runtimeenv.FirstBool(rootDir, keys...)
}
