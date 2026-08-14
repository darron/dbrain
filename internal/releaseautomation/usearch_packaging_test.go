package releaseautomation

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const (
	usearchVersion          = "2.26.0"
	usearchCommit           = "cc23bbaf21ef52313c5a495adbc40cbd733cdcfb"
	usearchSourceURL        = "https://proxy.golang.org/github.com/unum-cloud/usearch/@v/v2.26.0+incompatible.zip"
	usearchSourceSHA256     = "54f280b044757a391a6905b0850ad96789b6b2ce7c20ec9511b3b4628e27b0bd"
	usearchDeploymentTarget = "12.0"
)

func TestUSearchPackagingTaskPolicy(t *testing.T) {
	taskfile := readRepoFile(t, "Taskfile.yml")
	goMod := readRepoFile(t, "go.mod")

	const bindingVersion = "github.com/unum-cloud/usearch/golang v0.0.0-20260710183501-cc23bbaf21ef"
	if !strings.Contains(goMod, bindingVersion) {
		t.Errorf("go.mod must pin the USearch Go binding to the native source commit with %q", bindingVersion)
	}

	for _, required := range []string{
		`USEARCH_VERSION: "` + usearchVersion + `"`,
		`USEARCH_COMMIT: "` + usearchCommit + `"`,
		`USEARCH_SOURCE_URL: "` + usearchSourceURL + `"`,
		`USEARCH_SOURCE_SHA256: "` + usearchSourceSHA256 + `"`,
		`USEARCH_MACOS_DEPLOYMENT_TARGET: "` + usearchDeploymentTarget + `"`,
	} {
		if !strings.Contains(taskfile, required) {
			t.Errorf("Taskfile missing immutable USearch packaging policy %q", required)
		}
	}

	staticTask := taskfileTaskSection(t, taskfile, "usearch-static-darwin-arm64")
	for _, required := range []string{
		`go env GOOS`,
		`go env GOARCH`,
		`shasum -a 256`,
		`{{.USEARCH_SOURCE_SHA256}}`,
		`unzip -q -o`,
		`MACOSX_DEPLOYMENT_TARGET={{.USEARCH_MACOS_DEPLOYMENT_TARGET}}`,
		`-DCMAKE_OSX_ARCHITECTURES=arm64`,
		`-DCMAKE_OSX_DEPLOYMENT_TARGET={{.USEARCH_MACOS_DEPLOYMENT_TARGET}}`,
		`--target usearch_static_c`,
		`libusearch_static_c.a`,
		`libusearch_c.a`,
		`usearch.h`,
	} {
		if !strings.Contains(staticTask, required) {
			t.Errorf("usearch-static-darwin-arm64 missing %q", required)
		}
	}
	checksumAt := strings.Index(staticTask, "shasum -a 256")
	extractAt := strings.Index(staticTask, "unzip -q -o")
	if checksumAt < 0 || extractAt < 0 || checksumAt >= extractAt {
		t.Error("USearch source archive must be checksum-verified before extraction")
	}
	if !strings.Contains(staticTask, `.tmp`) {
		t.Error("USearch download must use a temporary file until checksum verification succeeds")
	}

	testTask := taskfileTaskSection(t, taskfile, "test-usearch-darwin-arm64")
	for _, required := range []string{
		`CGO_ENABLED=1`,
		`GOOS=darwin`,
		`GOARCH=arm64`,
		`MACOSX_DEPLOYMENT_TARGET={{.USEARCH_MACOS_DEPLOYMENT_TARGET}}`,
		`go test`,
		`-tags usearch`,
		`./...`,
	} {
		if !strings.Contains(testTask, required) {
			t.Errorf("test-usearch-darwin-arm64 missing %q", required)
		}
	}
	if got := strings.Count(testTask, "go test"); got != 2 {
		t.Errorf("test-usearch-darwin-arm64 go test commands=%d want full tagged and focused race commands", got)
	}
	if !strings.Contains(testTask, `go test -count=1 -tags usearch -timeout=20m ./...`) {
		t.Error("test-usearch-darwin-arm64 must run the full tagged suite without race instrumentation")
	}
	if !strings.Contains(testTask, `go test -count=1 -race -tags usearch -timeout=20m {{.USEARCH_RACE_PACKAGES}}`) {
		t.Error("test-usearch-darwin-arm64 must race-test the usearch-tagged package set")
	}
	if got := strings.Count(testTask, "-race"); got != 1 {
		t.Errorf("test-usearch-darwin-arm64 race-enabled commands=%d want only the focused package command", got)
	}

	// The cache is build-tag-independent, but its operation/close safety tests
	// are part of the native semantic runtime contract and therefore run in the
	// tagged race task as a deliberate supplemental package.
	wantRacePackages := append(usearchTaggedPackagePaths(t), "./internal/semanticruntime")
	sort.Strings(wantRacePackages)
	gotRacePackages := strings.Fields(taskfileQuotedVariable(t, taskfile, "USEARCH_RACE_PACKAGES"))
	if !reflect.DeepEqual(gotRacePackages, wantRacePackages) {
		t.Errorf("USEARCH_RACE_PACKAGES=%v want every package selected by a usearch build constraint: %v", gotRacePackages, wantRacePackages)
	}

	buildTask := taskfileTaskSection(t, taskfile, "build-usearch-darwin-arm64")
	for _, required := range []string{
		`CGO_ENABLED=1`,
		`GOOS=darwin`,
		`GOARCH=arm64`,
		`MACOSX_DEPLOYMENT_TARGET={{.USEARCH_MACOS_DEPLOYMENT_TARGET}}`,
		`go build`,
		`-tags usearch`,
		`-buildvcs=true`,
		`./cmd/dbrain`,
	} {
		if !strings.Contains(buildTask, required) {
			t.Errorf("build-usearch-darwin-arm64 missing %q", required)
		}
	}

	verifyTask := taskfileTaskSection(t, taskfile, "verify-usearch-darwin-arm64")
	for _, required := range []string{
		`file`,
		`lipo -archs`,
		`go version -m`,
		`otool -l`,
		`minos {{.USEARCH_MACOS_DEPLOYMENT_TARGET}}`,
		`otool -L`,
		`libusearch`,
		`semantic status --json`,
		`"state":"supported_ready"`,
		`"backend":"usearch"`,
		`"version":"{{.USEARCH_VERSION}}"`,
	} {
		if !strings.Contains(verifyTask, required) {
			t.Errorf("verify-usearch-darwin-arm64 missing %q", required)
		}
	}
}

func taskfileQuotedVariable(t *testing.T, taskfile, name string) string {
	t.Helper()
	prefix := "  " + name + ": "
	for _, line := range strings.Split(taskfile, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
			t.Fatalf("Taskfile variable %s must be a quoted string", name)
		}
		return value[1 : len(value)-1]
	}
	t.Fatalf("Taskfile missing variable %s", name)
	return ""
}

func usearchTaggedPackagePaths(t *testing.T) []string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	packages := map[string]struct{}{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".gomodcache", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "package ") {
				break
			}
			if !strings.HasPrefix(line, "//go:build ") || !strings.Contains(line, "usearch") {
				continue
			}
			relative, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			packages["./"+filepath.ToSlash(relative)] = struct{}{}
			break
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result := make([]string, 0, len(packages))
	for pkg := range packages {
		result = append(result, pkg)
	}
	sort.Strings(result)
	return result
}

func TestUSearchDarwinLinkPolicy(t *testing.T) {
	linkShim := readRepoFile(t, "internal/semanticindex/usearch_link_darwin.go")
	for _, required := range []string{
		"//go:build usearch && cgo && darwin",
		"#cgo LDFLAGS: -lc++",
	} {
		if !strings.Contains(linkShim, required) {
			t.Errorf("Darwin USearch link shim missing %q", required)
		}
	}
}

func TestUSearchNativeLicenseNoticePolicy(t *testing.T) {
	license := readRepoFile(t, "third_party/usearch/LICENSE")
	for _, required := range []string{
		"Apache License",
		"Version 2.0, January 2004",
		"http://www.apache.org/licenses/",
	} {
		if !strings.Contains(license, required) {
			t.Errorf("vendored USearch license missing %q", required)
		}
	}

	notices := readRepoFile(t, "THIRD_PARTY_NOTICES.md")
	for _, required := range []string{
		"USearch",
		"`v" + usearchVersion + "`",
		"`" + usearchCommit + "`",
		"Apache-2.0",
		"Darwin arm64",
		"statically linked",
		"`LICENSE-USearch`",
		"`third_party/usearch/LICENSE`",
	} {
		if !strings.Contains(notices, required) {
			t.Errorf("third-party notices missing USearch policy %q", required)
		}
	}
}

func taskfileTaskSection(t *testing.T, taskfile, name string) string {
	t.Helper()
	startMarker := "\n  " + name + ":\n"
	start := strings.Index(taskfile, startMarker)
	if start < 0 {
		t.Fatalf("Taskfile missing task %q", name)
	}
	rest := taskfile[start+len(startMarker):]
	var lines []string
	for _, line := range strings.Split(rest, "\n") {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") {
			break
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
