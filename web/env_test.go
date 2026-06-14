package web

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	scrubAmbientWebTestEnv()
	os.Exit(m.Run())
}

func scrubAmbientWebTestEnv() {
	for _, key := range []string{
		"DBRAIN_AUTH_BASE_URL",
		"DBRAIN_AUTH_ENABLED",
		"DBRAIN_AUTH_GITHUB_CLIENT_ID",
		"DBRAIN_AUTH_GITHUB_CLIENT_SECRET",
		"DBRAIN_AUTH_PROVIDERS",
		"DBRAIN_AUTH_SESSION_KEY",
	} {
		_ = os.Unsetenv(key)
	}
}
