package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/darron/dbrain/internal/releaseautomation"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: homebrew_test_release <metadata|formula|allowlist|validate-paths>")
		return 2
	}
	switch args[0] {
	case "metadata":
		return runMetadata(args[1:], stdout, stderr)
	case "formula":
		return runFormula(args[1:], stdout, stderr)
	case "allowlist":
		return runAllowlist(args[1:], stdout, stderr)
	case "validate-paths":
		return runValidatePaths(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
}

func runMetadata(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("metadata", stderr)
	sha := flags.String("sha", "", "full candidate commit SHA")
	label := flags.String("label", "", "candidate display label")
	runNumber := flags.Int64("run-number", 0, "GitHub run number")
	runAttempt := flags.Int64("run-attempt", 0, "GitHub run attempt")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "metadata does not accept positional arguments")
		return 2
	}
	if !hasAllFlags(flags, "sha", "label", "run-number", "run-attempt") {
		fmt.Fprintln(stderr, "metadata requires --sha, --label, --run-number, and --run-attempt")
		return 2
	}

	candidate, err := releaseautomation.NewCandidate(*sha, *label, *runNumber, *runAttempt)
	if err != nil {
		fmt.Fprintf(stderr, "metadata: %v\n", err)
		return 1
	}
	if _, err := io.WriteString(stdout, candidate.GitHubOutput()); err != nil {
		fmt.Fprintf(stderr, "write metadata output: %v\n", err)
		return 1
	}
	return 0
}

func runFormula(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("formula", stderr)
	output := flags.String("output", "", "generated formula path")
	sha := flags.String("sha", "", "full candidate commit SHA")
	label := flags.String("label", "", "candidate display label")
	runNumber := flags.Int64("run-number", 0, "GitHub run number")
	runAttempt := flags.Int64("run-attempt", 0, "GitHub run attempt")
	releaseBase := flags.String("release-base", "", "GitHub release asset base URL")
	darwinAMD64SHA := flags.String("darwin-amd64-sha", "", "darwin amd64 archive checksum")
	darwinARM64SHA := flags.String("darwin-arm64-sha", "", "darwin arm64 archive checksum")
	linuxAMD64SHA := flags.String("linux-amd64-sha", "", "linux amd64 archive checksum")
	linuxARM64SHA := flags.String("linux-arm64-sha", "", "linux arm64 archive checksum")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "formula does not accept positional arguments")
		return 2
	}
	required := []string{
		"output", "sha", "label", "run-number", "run-attempt", "release-base",
		"darwin-amd64-sha", "darwin-arm64-sha", "linux-amd64-sha", "linux-arm64-sha",
	}
	if !hasAllFlags(flags, required...) {
		fmt.Fprintln(stderr, "formula requires --output, --sha, --label, --run-number, --run-attempt, --release-base, and all four checksum flags")
		return 2
	}
	if err := rejectStableFormulaPath(*output); err != nil {
		fmt.Fprintf(stderr, "formula: %v\n", err)
		return 1
	}

	candidate, err := releaseautomation.NewCandidate(*sha, *label, *runNumber, *runAttempt)
	if err != nil {
		fmt.Fprintf(stderr, "formula: %v\n", err)
		return 1
	}
	formula, err := releaseautomation.RenderTestFormula(releaseautomation.FormulaInput{
		Candidate:   candidate,
		ReleaseBase: *releaseBase,
		Checksums: map[string]string{
			"darwin_amd64": *darwinAMD64SHA,
			"darwin_arm64": *darwinARM64SHA,
			"linux_amd64":  *linuxAMD64SHA,
			"linux_arm64":  *linuxARM64SHA,
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "formula: %v\n", err)
		return 1
	}
	if err := writeGeneratedFile(*output, formula); err != nil {
		fmt.Fprintf(stderr, "write formula: %v\n", err)
		return 1
	}
	return 0
}

func runAllowlist(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("allowlist", stderr)
	input := flags.String("input", "", "existing allowlist path")
	output := flags.String("output", "", "generated allowlist path")
	version := flags.String("version", "", "candidate formula version")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "allowlist does not accept positional arguments")
		return 2
	}
	if !hasAllFlags(flags, "input", "output", "version") {
		fmt.Fprintln(stderr, "allowlist requires --input, --output, and --version")
		return 2
	}
	if err := rejectStableFormulaPath(*output); err != nil {
		fmt.Fprintf(stderr, "allowlist: %v\n", err)
		return 1
	}

	existing, err := os.ReadFile(*input)
	if err != nil {
		if !os.IsNotExist(err) || filepath.Clean(*input) != filepath.Clean(*output) {
			fmt.Fprintf(stderr, "read allowlist input: %v\n", err)
			return 1
		}
		existing = nil
	}
	updated, err := releaseautomation.UpdatePrereleaseAllowlist(existing, *version)
	if err != nil {
		fmt.Fprintf(stderr, "allowlist: %v\n", err)
		return 1
	}
	if err := writeGeneratedFile(*output, updated); err != nil {
		fmt.Fprintf(stderr, "write allowlist: %v\n", err)
		return 1
	}
	return 0
}

func runValidatePaths(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("validate-paths", stderr)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() == 0 {
		fmt.Fprintln(stderr, "validate-paths requires at least one path")
		return 2
	}
	if err := releaseautomation.ValidateTapPaths(flags.Args()); err != nil {
		fmt.Fprintf(stderr, "validate-paths: %v\n", err)
		return 1
	}
	return 0
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

func hasAllFlags(flags *flag.FlagSet, names ...string) bool {
	seen := make(map[string]bool, len(names))
	flags.Visit(func(value *flag.Flag) {
		seen[value.Name] = true
	})
	for _, name := range names {
		if !seen[name] {
			return false
		}
	}
	return true
}

func rejectStableFormulaPath(path string) error {
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "Formula/dbrain.rb" || strings.HasSuffix(clean, "/Formula/dbrain.rb") {
		return fmt.Errorf("must not write stable Formula/dbrain.rb")
	}
	return nil
}

func writeGeneratedFile(path string, data []byte) error {
	parent := filepath.Dir(path)
	if err := os.Mkdir(parent, 0o755); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("create output parent: %w", err)
		}
		info, statErr := os.Stat(parent)
		if statErr != nil {
			return fmt.Errorf("inspect output parent: %w", statErr)
		}
		if !info.IsDir() {
			return fmt.Errorf("create output parent: %q is not a directory", parent)
		}
	} else if err := os.Chmod(parent, 0o755); err != nil {
		return fmt.Errorf("set output parent mode: %w", err)
	}
	temporary, err := os.CreateTemp(parent, ".homebrew-test-release-*")
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("set temporary output mode: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary output: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary output: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace output: %w", err)
	}
	return nil
}
