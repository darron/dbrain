// Package version exposes build metadata for the running dbrain binary.
package version

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// ReleaseVersion and BuildPlatform are injected by release/build commands
// because Go build info does not include them.
var (
	ReleaseVersion = "unknown"
	BuildPlatform  = "unknown"

	// GitVersion is kept as a deprecated fallback for older build scripts that
	// used this misleading name for release/tag metadata.
	GitVersion = "unknown"
)

// Details describes build and version metadata embedded in the binary.
type Details struct {
	Commit         string            `json:"commit"`
	Short          string            `json:"short"`
	BuildTime      string            `json:"build_time"`
	GitStatus      string            `json:"git_status"`
	GoVersion      string            `json:"go_version"`
	ReleaseVersion string            `json:"release_version"`
	BuildPlatform  string            `json:"build_platform"`
	ModulePath     string            `json:"module_path,omitempty"`
	ModuleVersion  string            `json:"module_version,omitempty"`
	BuildSettings  map[string]string `json:"build_settings,omitempty"`
}

// Current returns build metadata for the running binary.
func Current() Details {
	buildInfo, ok := debug.ReadBuildInfo()
	return detailsFromBuildInfo(buildInfo, ok, releaseVersion(), BuildPlatform)
}

// GetCommit returns the full git commit SHA when build info includes it.
func GetCommit() string {
	return Current().Commit
}

// ShortCommit returns the first seven characters of the current commit.
func ShortCommit() string {
	return Current().Short
}

// UserAgent returns the HTTP user agent used for outbound API calls.
func UserAgent(override string) string {
	if value := strings.TrimSpace(override); value != "" {
		return value
	}
	return userAgentFromDetails(Current())
}

// StartupLine returns a concise human-readable build identifier for startup logs.
func StartupLine() string {
	return startupLineFromDetails(Current())
}

func startupLineFromDetails(details Details) string {
	displayVersion := strings.TrimSpace(details.Short)
	if displayVersion == "" || strings.EqualFold(displayVersion, "unknown") {
		displayVersion = strings.TrimSpace(details.Commit)
	}
	if displayVersion == "" || strings.EqualFold(displayVersion, "unknown") {
		displayVersion = strings.TrimSpace(details.ModuleVersion)
	}
	displayVersion = defaultValue(displayVersion)
	parts := []string{fmt.Sprintf("dbrain version: %s", displayVersion)}
	if release := strings.TrimSpace(details.ReleaseVersion); release != "" && !strings.EqualFold(release, "unknown") {
		parts = append(parts, fmt.Sprintf("release=%s", release))
	}
	parts = append(
		parts,
		fmt.Sprintf("status=%s", defaultValue(details.GitStatus)),
		fmt.Sprintf("platform=%s", defaultValue(details.BuildPlatform)),
	)
	return strings.Join(parts, " ")
}

// Info returns version information as a map for API-style payloads.
func Info() map[string]interface{} {
	current := Current()
	info := map[string]interface{}{
		"commit":          current.Commit,
		"short":           current.Short,
		"build_time":      current.BuildTime,
		"git_status":      current.GitStatus,
		"go_version":      current.GoVersion,
		"release_version": current.ReleaseVersion,
		"build_platform":  current.BuildPlatform,
	}
	if strings.TrimSpace(current.ModulePath) != "" {
		info["module_path"] = current.ModulePath
	}
	if strings.TrimSpace(current.ModuleVersion) != "" {
		info["module_version"] = current.ModuleVersion
	}
	if len(current.BuildSettings) > 0 {
		info["build_settings"] = current.BuildSettings
	}
	return info
}

func detailsFromBuildInfo(buildInfo *debug.BuildInfo, ok bool, releaseVersion string, buildPlatform string) Details {
	details := Details{
		Commit:         "unknown",
		Short:          "unknown",
		BuildTime:      "unknown",
		GitStatus:      "unknown",
		GoVersion:      "unknown",
		ReleaseVersion: defaultValue(releaseVersion),
		BuildPlatform:  defaultValue(buildPlatform),
	}
	if !ok || buildInfo == nil {
		return details
	}

	if value := strings.TrimSpace(buildInfo.GoVersion); value != "" {
		details.GoVersion = value
	}
	if value := strings.TrimSpace(buildInfo.Main.Path); value != "" {
		details.ModulePath = value
	}
	if value := strings.TrimSpace(buildInfo.Main.Version); value != "" {
		details.ModuleVersion = value
	}
	if len(buildInfo.Settings) == 0 {
		return details
	}

	settings := make(map[string]string, len(buildInfo.Settings))
	for _, setting := range buildInfo.Settings {
		settings[setting.Key] = setting.Value
	}
	details.BuildSettings = settings
	if commit := strings.TrimSpace(settings["vcs.revision"]); commit != "" {
		details.Commit = commit
		details.Short = shortCommit(commit)
	}
	if buildTime := strings.TrimSpace(settings["vcs.time"]); buildTime != "" {
		details.BuildTime = buildTime
	}
	details.GitStatus = gitStatus(settings["vcs.modified"])
	return details
}

func releaseVersion() string {
	if value := strings.TrimSpace(ReleaseVersion); value != "" && !strings.EqualFold(value, "unknown") {
		return value
	}
	return GitVersion
}

func shortCommit(commit string) string {
	if len(commit) >= 7 {
		return commit[:7]
	}
	return commit
}

func gitStatus(modified string) string {
	switch strings.TrimSpace(strings.ToLower(modified)) {
	case "true":
		return "modified"
	case "false":
		return "clean"
	default:
		return "unknown"
	}
}

func userAgentFromDetails(details Details) string {
	version := strings.TrimSpace(details.Short)
	if version == "" {
		version = "unknown"
	}
	if strings.TrimSpace(strings.ToLower(details.GitStatus)) == "modified" && !strings.Contains(version, "+dirty") {
		version += "+dirty"
	}
	return "dbrain/" + version
}

func defaultValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}
