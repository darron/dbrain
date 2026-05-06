package version

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestDetailsFromBuildInfo(t *testing.T) {
	t.Parallel()

	buildInfo := &debug.BuildInfo{
		GoVersion: "go1.26.2",
		Main: debug.Module{
			Path:    "dbrain",
			Version: "(devel)",
		},
		Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: "cbf18e1c92e0a2efd7b6212b127d18cdb3c6f906"},
			{Key: "vcs.time", Value: "2026-04-20T03:29:13Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}

	details := detailsFromBuildInfo(buildInfo, true, "2.54.0", "darwin/arm64")
	if details.Commit != "cbf18e1c92e0a2efd7b6212b127d18cdb3c6f906" {
		t.Fatalf("Commit = %q", details.Commit)
	}
	if details.Short != "cbf18e1" {
		t.Fatalf("Short = %q", details.Short)
	}
	if details.BuildTime != "2026-04-20T03:29:13Z" {
		t.Fatalf("BuildTime = %q", details.BuildTime)
	}
	if details.GitStatus != "modified" {
		t.Fatalf("GitStatus = %q", details.GitStatus)
	}
	if details.GoVersion != "go1.26.2" {
		t.Fatalf("GoVersion = %q", details.GoVersion)
	}
	if details.ReleaseVersion != "2.54.0" {
		t.Fatalf("ReleaseVersion = %q", details.ReleaseVersion)
	}
	if details.BuildPlatform != "darwin/arm64" {
		t.Fatalf("BuildPlatform = %q", details.BuildPlatform)
	}
	if details.ModulePath != "dbrain" {
		t.Fatalf("ModulePath = %q", details.ModulePath)
	}
	if details.ModuleVersion != "(devel)" {
		t.Fatalf("ModuleVersion = %q", details.ModuleVersion)
	}
	if got := details.BuildSettings["vcs.modified"]; got != "true" {
		t.Fatalf("BuildSettings[vcs.modified] = %q", got)
	}
}

func TestInfoIncludesRequiredFields(t *testing.T) {
	originalGitVersion := GitVersion
	originalReleaseVersion := ReleaseVersion
	originalBuildPlatform := BuildPlatform
	defer func() {
		GitVersion = originalGitVersion
		ReleaseVersion = originalReleaseVersion
		BuildPlatform = originalBuildPlatform
	}()

	GitVersion = "unknown"
	ReleaseVersion = "v1.2.3"
	BuildPlatform = "darwin/arm64"

	info := Info()
	for _, field := range []string{"commit", "short", "build_time", "git_status", "go_version", "release_version", "build_platform"} {
		if _, ok := info[field]; !ok {
			t.Fatalf("Info() missing %q: %#v", field, info)
		}
	}
	if info["release_version"] != ReleaseVersion {
		t.Fatalf("release_version = %v, want %q", info["release_version"], ReleaseVersion)
	}
	if info["build_platform"] != BuildPlatform {
		t.Fatalf("build_platform = %v, want %q", info["build_platform"], BuildPlatform)
	}
}

func TestReleaseVersionFallsBackToDeprecatedGitVersion(t *testing.T) {
	originalGitVersion := GitVersion
	originalReleaseVersion := ReleaseVersion
	defer func() {
		GitVersion = originalGitVersion
		ReleaseVersion = originalReleaseVersion
	}()

	ReleaseVersion = "unknown"
	GitVersion = "v1.2.3"

	if got := Current().ReleaseVersion; got != "v1.2.3" {
		t.Fatalf("ReleaseVersion fallback = %q", got)
	}
}

func TestStartupLineUsesShortCommitAsPrimaryIdentifier(t *testing.T) {
	t.Parallel()

	line := startupLineFromDetails(Details{
		Short:          "bb5fcc8",
		ReleaseVersion: "v1.2.3",
		GitStatus:      "modified",
		BuildPlatform:  "darwin/arm64",
	})
	for _, want := range []string{
		"dbrain version: bb5fcc8",
		"release=v1.2.3",
		"status=modified",
		"platform=darwin/arm64",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("StartupLine() = %q, want substring %q", line, want)
		}
	}
}

func TestStartupLineFallsBackWhenShortCommitMissing(t *testing.T) {
	t.Parallel()

	line := startupLineFromDetails(Details{
		Commit:        "bb5fcc8c57ffe6fcb7a51f76bbe4e00a8443074c",
		GitStatus:     "clean",
		BuildPlatform: "darwin/arm64",
	})
	if !strings.Contains(line, "dbrain version: bb5fcc8c57ffe6fcb7a51f76bbe4e00a8443074c") {
		t.Fatalf("StartupLine() = %q", line)
	}
}

func TestStartupLineOmitsUnknownRelease(t *testing.T) {
	t.Parallel()

	line := startupLineFromDetails(Details{
		Short:          "bb5fcc8",
		ReleaseVersion: "unknown",
		GitStatus:      "modified",
		BuildPlatform:  "darwin/arm64",
	})
	if strings.Contains(line, "release=") {
		t.Fatalf("StartupLine() should omit unknown release version, got %q", line)
	}
}

func TestShortCommit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		commit string
		want   string
	}{
		{commit: "cbf18e1c92e0a2efd7b6212b127d18cdb3c6f906", want: "cbf18e1"},
		{commit: "abc1234", want: "abc1234"},
		{commit: "abc", want: "abc"},
		{commit: "unknown", want: "unknown"},
	}
	for _, tt := range tests {
		if got := shortCommit(tt.commit); got != tt.want {
			t.Fatalf("shortCommit(%q) = %q, want %q", tt.commit, got, tt.want)
		}
	}
}

func TestUserAgentFromDetails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		details Details
		want    string
	}{
		{
			name: "clean short sha",
			details: Details{
				Short:     "cbf18e1",
				GitStatus: "clean",
			},
			want: "dbrain/cbf18e1",
		},
		{
			name: "dirty short sha",
			details: Details{
				Short:     "cbf18e1",
				GitStatus: "modified",
			},
			want: "dbrain/cbf18e1+dirty",
		},
		{
			name: "unknown fallback",
			details: Details{
				Short:     "",
				GitStatus: "unknown",
			},
			want: "dbrain/unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := userAgentFromDetails(tt.details); got != tt.want {
				t.Fatalf("userAgentFromDetails() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUserAgentOverride(t *testing.T) {
	t.Parallel()

	if got := UserAgent("custom-agent/1.0"); got != "custom-agent/1.0" {
		t.Fatalf("UserAgent override = %q", got)
	}
}
