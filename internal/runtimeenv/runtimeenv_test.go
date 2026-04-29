package runtimeenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstNonEmptyReadsGroupedConfigValue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeConfig(t, root, `
test:
  service:
    api_key: from-config
`)

	got := FirstNonEmpty(root, "DBRAIN_TEST_SERVICE_API_KEY")
	if got != "from-config" {
		t.Fatalf("FirstNonEmpty = %q, want %q", got, "from-config")
	}
}

func TestFirstNonEmptyReadsExactEnvConfigValue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeConfig(t, root, `
env:
  DBRAIN_RUNTIMEENV_TEST_VALUE: from-env-map
`)

	got := FirstNonEmpty(root, "DBRAIN_RUNTIMEENV_TEST_VALUE")
	if got != "from-env-map" {
		t.Fatalf("FirstNonEmpty = %q, want %q", got, "from-env-map")
	}
}

func TestFirstNonEmptyReadsNestedConfigValue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeConfig(t, root, `
test:
  reader:
    base_url: https://r.jina.ai/
`)

	got := FirstNonEmpty(root, "DBRAIN_TEST_READER_BASE_URL")
	if got != "https://r.jina.ai/" {
		t.Fatalf("FirstNonEmpty = %q, want %q", got, "https://r.jina.ai/")
	}
}

func TestFirstBoolReadsConfigValue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeConfig(t, root, `
test:
  archive:
    upload: true
`)

	if !FirstBool(root, "DBRAIN_TEST_ARCHIVE_UPLOAD") {
		t.Fatalf("FirstBool = false, want true")
	}
}

func TestEnvironmentOverridesConfig(t *testing.T) {
	root := t.TempDir()
	writeConfig(t, root, `
test:
  service:
    api_key: from-config
`)
	t.Setenv("DBRAIN_TEST_SERVICE_API_KEY", "from-env")

	got := FirstNonEmpty(root, "DBRAIN_TEST_SERVICE_API_KEY")
	if got != "from-env" {
		t.Fatalf("FirstNonEmpty = %q, want %q", got, "from-env")
	}
}

func TestDotEnvOverridesConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeConfig(t, root, `
test:
  service:
    api_key: from-config
`)
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DBRAIN_TEST_SERVICE_API_KEY=from-dotenv\n"), 0o644); err != nil {
		t.Fatalf("WriteFile .env: %v", err)
	}

	got := FirstNonEmpty(root, "DBRAIN_TEST_SERVICE_API_KEY")
	if got != "from-dotenv" {
		t.Fatalf("FirstNonEmpty = %q, want %q", got, "from-dotenv")
	}
}

func writeConfig(t *testing.T, root string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile config.yaml: %v", err)
	}
}
