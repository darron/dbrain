package releaseautomation

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func readRepoFile(t *testing.T, relative string) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	data, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestHomebrewTestWorkflowPolicy(t *testing.T) {
	text := readRepoFile(t, ".github/workflows/homebrew-test.yaml")
	required := []string{
		"workflow_dispatch:", "sha:", "label:",
		"github.actor", "github.triggering_actor", "darron",
		"refs/heads/main", "persist-credentials: false",
		"permissions:\n  contents: read",
		"DBRAIN_RELEASE_VERSION", "--prerelease", "--target",
		"HOMEBREW_TAP_TOKEN", "Formula/dbrain-test.rb",
		"audit_exceptions/github_prerelease_allowlist.json",
		"git diff --exit-code -- Formula/dbrain.rb",
		"dbrain-homebrew-tap-update", "cancel-in-progress: false",
		"brew install darron/tap/dbrain-test", "brew upgrade dbrain-test",
		"brew unlink dbrain-test", "brew uninstall dbrain-test",
	}
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Errorf("workflow missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"push:\n", "pull_request:\n", "schedule:\n",
		"brew uninstall dbrain\n", "~/.config/dbrain", "~/.local/share/dbrain", "launchctl",
		"darvisf", "write-all",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("manual workflow contains forbidden text %q", forbidden)
		}
	}

	var root yaml.Node
	if err := yaml.Unmarshal([]byte(text), &root); err != nil {
		t.Fatalf("parse workflow: %v", err)
	}
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		t.Fatal("workflow root must be a mapping")
	}
	jobs := mappingValue(t, root.Content[0], "jobs")
	if jobs.Kind != yaml.MappingNode {
		t.Fatal("jobs must be a mapping")
	}
	var names []string
	for i := 0; i < len(jobs.Content); i += 2 {
		names = append(names, jobs.Content[i].Value)
	}
	sort.Strings(names)
	wantNames := []string{"build", "prepare", "publish", "update-homebrew-tap", "verify"}
	if strings.Join(names, ",") != strings.Join(wantNames, ",") {
		t.Fatalf("jobs=%v, want %v", names, wantNames)
	}
	triggers := mappingValue(t, root.Content[0], "on")
	if triggers.Kind != yaml.MappingNode || len(triggers.Content) != 2 || triggers.Content[0].Value != "workflow_dispatch" {
		t.Fatalf("workflow triggers must contain only workflow_dispatch: %s", marshalNode(t, triggers))
	}

	for i := 0; i < len(jobs.Content); i += 2 {
		name := jobs.Content[i].Value
		job := jobs.Content[i+1]
		jobText := marshalNode(t, job)
		if strings.Contains(jobText, "HOMEBREW_TAP_TOKEN") && name != "update-homebrew-tap" {
			t.Errorf("job %q references HOMEBREW_TAP_TOKEN", name)
		}
		permissions := optionalMappingValue(job, "permissions")
		contents := ""
		if permissions != nil {
			contentsNode := optionalMappingValue(permissions, "contents")
			if contentsNode != nil {
				contents = contentsNode.Value
			}
		}
		if contents == "write" && name != "publish" {
			t.Errorf("job %q receives contents: write", name)
		}
		if name == "publish" && contents != "write" {
			t.Errorf("publish contents permission=%q, want write", contents)
		}
	}

	assertJobContains(t, jobs, "verify", "needs: prepare", "ref: ${{ needs.prepare.outputs.sha }}", "persist-credentials: false", "cache: false", "npm ci", "task web-build", "task lint", "task test-ci", "task build")
	assertJobContains(t, jobs, "build", "needs:", "prepare", "verify", "ref: ${{ needs.prepare.outputs.sha }}", "persist-credentials: false", "cache: false", "DBRAIN_RELEASE_VERSION", "darwin", "linux", "windows")
	assertJobContains(t, jobs, "publish", "needs:", "build", "gh release create", "--target", "--prerelease", "gh release upload")
	assertJobContains(t, jobs, "update-homebrew-tap", "ref: ${{ github.sha }}", "persist-credentials: false", "HOMEBREW_TAP_TOKEN", "validate-paths", "Formula/dbrain.rb")
	assertJobContains(t, jobs, "prepare", `test "${ACTOR}" = "darron"`, `test "${TRIGGERING_ACTOR}" = "darron"`, `test "${WORKFLOW_REF}" = "refs/heads/main"`)

	for _, name := range []string{"verify", "build"} {
		jobText := marshalNode(t, mappingValue(t, jobs, name))
		for _, forbidden := range []string{"HOMEBREW_TAP_TOKEN", "github.token", "contents: write"} {
			if strings.Contains(jobText, forbidden) {
				t.Errorf("candidate job %q contains forbidden authority %q", name, forbidden)
			}
		}
	}
	publishText := marshalNode(t, mappingValue(t, jobs, "publish"))
	if strings.Contains(publishText, "actions/checkout@") {
		t.Error("publish job must not check out candidate source")
	}
	tapText := marshalNode(t, mappingValue(t, jobs, "update-homebrew-tap"))
	for _, forbidden := range []string{"task build", "npm ci", "bin/dbrain", "tar -", "unzip "} {
		if strings.Contains(tapText, forbidden) {
			t.Errorf("tap job may not execute or extract candidate content: %q", forbidden)
		}
	}

	for _, archive := range []string{
		"dbrain_${RELEASE_TAG}_darwin_amd64.tar.gz",
		"dbrain_${RELEASE_TAG}_darwin_arm64.tar.gz",
		"dbrain_${RELEASE_TAG}_linux_amd64.tar.gz",
		"dbrain_${RELEASE_TAG}_linux_arm64.tar.gz",
		"dbrain_${RELEASE_TAG}_windows_amd64.zip",
	} {
		if !strings.Contains(text, archive) {
			t.Errorf("workflow missing expected archive %q", archive)
		}
	}

	assertNoExpressionsInRunScripts(t, root.Content[0])

	pins := []string{
		"actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd",
		"actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c",
		"actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020",
		"go-task/setup-task@01a4adf9db2d14c1de7a560f09170b6e0df736aa",
		"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
		"actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c",
	}
	for _, pin := range pins {
		if !strings.Contains(text, pin) {
			t.Errorf("workflow missing immutable action %q", pin)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "uses:") && !containsAny(line, pins) {
			t.Errorf("unapproved or unpinned action: %s", line)
		}
	}
}

func mappingValue(t *testing.T, mapping *yaml.Node, key string) *yaml.Node {
	t.Helper()
	value := optionalMappingValue(mapping, key)
	if value == nil {
		t.Fatalf("mapping missing %q", key)
	}
	return value
}

func optionalMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func marshalNode(t *testing.T, node *yaml.Node) string {
	t.Helper()
	data, err := yaml.Marshal(node)
	if err != nil {
		t.Fatalf("marshal YAML node: %v", err)
	}
	return string(data)
}

func assertJobContains(t *testing.T, jobs *yaml.Node, name string, required ...string) {
	t.Helper()
	job := mappingValue(t, jobs, name)
	text := marshalNode(t, job)
	for _, want := range required {
		if !strings.Contains(text, want) {
			t.Errorf("job %q missing %q", name, want)
		}
	}
}

func containsAny(text string, values []string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func assertNoExpressionsInRunScripts(t *testing.T, node *yaml.Node) {
	t.Helper()
	if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Value == "run" && strings.Contains(value.Value, "${{") {
				t.Errorf("run script contains a direct GitHub expression; pass it through env instead: %q", value.Value)
			}
			assertNoExpressionsInRunScripts(t, value)
		}
		return
	}
	for _, child := range node.Content {
		assertNoExpressionsInRunScripts(t, child)
	}
}
