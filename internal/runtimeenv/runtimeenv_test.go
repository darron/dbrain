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

func TestAppleNotesConfigNamespaceMapping(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeConfig(t, root, `
apple_notes:
  enabled: true
  db_path: /tmp/NoteStore.sqlite
  exclude_folders:
    - Private
    - Archive
`)

	if !FirstBool(root, "DBRAIN_APPLE_NOTES_ENABLED") {
		t.Fatalf("FirstBool = false, want true")
	}
	if got := FirstNonEmpty(root, "DBRAIN_APPLE_NOTES_DB_PATH"); got != "/tmp/NoteStore.sqlite" {
		t.Fatalf("FirstNonEmpty DB path = %q", got)
	}
	values := FirstList(root, "DBRAIN_APPLE_NOTES_EXCLUDE_FOLDERS")
	if len(values) != 2 || values[0] != "Private" || values[1] != "Archive" {
		t.Fatalf("FirstList exclude folders = %#v", values)
	}
}

func TestSafariTabsConfigNamespaceMapping(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeConfig(t, root, `
safari_tabs:
  enabled: true
  db_path: /tmp/CloudTabs.db
  device: dfone
  limit: 500
  older_than: 168h
`)

	if !FirstBool(root, "DBRAIN_SAFARI_TABS_ENABLED") {
		t.Fatalf("FirstBool = false, want true")
	}
	if got := FirstNonEmpty(root, "DBRAIN_SAFARI_TABS_DB_PATH"); got != "/tmp/CloudTabs.db" {
		t.Fatalf("FirstNonEmpty DB path = %q", got)
	}
	if got := FirstNonEmpty(root, "DBRAIN_SAFARI_TABS_DEVICE"); got != "dfone" {
		t.Fatalf("FirstNonEmpty device = %q", got)
	}
	if got := FirstNonEmpty(root, "DBRAIN_SAFARI_TABS_LIMIT"); got != "500" {
		t.Fatalf("FirstNonEmpty limit = %q", got)
	}
	if got := FirstNonEmpty(root, "DBRAIN_SAFARI_TABS_OLDER_THAN"); got != "168h" {
		t.Fatalf("FirstNonEmpty older_than = %q", got)
	}
}

func TestFirstNonEmptyReadsHTTPUserAgentConfigValue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeConfig(t, root, `
http:
  user_agent: dbrain/test-agent
`)

	got := FirstNonEmpty(root, "DBRAIN_USER_AGENT")
	if got != "dbrain/test-agent" {
		t.Fatalf("FirstNonEmpty = %q, want %q", got, "dbrain/test-agent")
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
