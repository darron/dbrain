package cipolicy

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var immutableActionRef = regexp.MustCompile(`^[^\s@]+@[0-9a-f]{40}$`)
var mutableGoInstall = regexp.MustCompile(`(?m)\bgo\s+install\b[^\n]*@latest(?:[\s;|&<>()]|$)`)

type workflowViolation struct {
	line    int
	message string
}

func TestWorkflowsPinExternalActionsAndGoInstalls(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	workflowRoot := filepath.Join(repoRoot, ".github", "workflows")
	var workflowPaths []string
	for _, pattern := range []string{"*.yml", "*.yaml"} {
		matches, err := filepath.Glob(filepath.Join(workflowRoot, pattern))
		if err != nil {
			t.Fatalf("list workflows matching %s: %v", pattern, err)
		}
		workflowPaths = append(workflowPaths, matches...)
	}
	sort.Strings(workflowPaths)
	if len(workflowPaths) == 0 {
		t.Fatal("no GitHub Actions workflows found")
	}

	for _, path := range workflowPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read workflow %s: %v", path, err)
		}
		violations, err := inspectWorkflowPolicy(data)
		if err != nil {
			t.Fatalf("parse workflow %s: %v", path, err)
		}
		for _, violation := range violations {
			t.Errorf("%s:%d %s", filepath.Base(path), violation.line, violation.message)
		}
	}
}

func TestWorkflowPolicyParsesYAMLFormsWithoutCommentFalsePositives(t *testing.T) {
	workflow := []byte(`
jobs:
  test:
    steps:
      - uses: "actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5" # quoted and pinned
      - uses: actions/setup-node@v4
      - name: explanatory shell comment
        run: |
          # go install example.invalid/comment-only@latest
          go install example.invalid/pinned@v1.2.3
      - name: mutable install
        run: go install example.invalid/tool@latest;
      - name: mutable continued install
        run: |
          go install \
            example.invalid/continued@latest
      - name: mutable redirected install
        run: go install example.invalid/redirected@latest>/dev/null
      - name: mutable subshell install
        run: (go install example.invalid/subshell@latest)
`)
	violations, err := inspectWorkflowPolicy(workflow)
	if err != nil {
		t.Fatalf("inspect fixture: %v", err)
	}
	if len(violations) != 5 {
		t.Fatalf("violations = %+v, want mutable action and four mutable installs only", violations)
	}
	if !strings.Contains(violations[0].message, "not pinned") {
		t.Fatalf("unexpected violations: %+v", violations)
	}
	for _, violation := range violations[1:] {
		if !strings.Contains(violation.message, "@latest") {
			t.Fatalf("unexpected violations: %+v", violations)
		}
	}
}

func inspectWorkflowPolicy(data []byte) ([]workflowViolation, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	var violations []workflowViolation
	var walk func(*yaml.Node)
	walk = func(node *yaml.Node) {
		if node.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(node.Content); i += 2 {
				key := node.Content[i]
				value := node.Content[i+1]
				switch key.Value {
				case "uses":
					if value.Kind == yaml.ScalarNode {
						ref := strings.TrimSpace(value.Value)
						if !strings.HasPrefix(ref, "./") && !immutableActionRef.MatchString(ref) {
							violations = append(violations, workflowViolation{line: value.Line, message: fmt.Sprintf("external action is not pinned to a 40-hex commit: %s", ref)})
						}
					}
				case "run":
					if value.Kind == yaml.ScalarNode && mutableGoInstall.MatchString(normalizeShellPolicyInput(value.Value)) {
						violations = append(violations, workflowViolation{line: value.Line, message: "go install uses mutable @latest"})
					}
				}
				walk(value)
			}
			return
		}
		for _, child := range node.Content {
			walk(child)
		}
	}
	walk(&document)
	return violations, nil
}

// normalizeShellPolicyInput removes unquoted shell comments and joins escaped
// line continuations. It is intentionally small: the policy only needs to
// avoid treating explanatory comments as executable text and cover ordinary
// workflow command syntax, not interpret an entire shell program.
func normalizeShellPolicyInput(script string) string {
	lines := strings.Split(script, "\n")
	for i, line := range lines {
		var quote rune
		escaped := false
		atTokenStart := true
		for pos, r := range line {
			if escaped {
				escaped = false
				atTokenStart = false
				continue
			}
			if quote != '\'' && r == '\\' {
				escaped = true
				continue
			}
			if quote != 0 {
				if r == quote {
					quote = 0
				}
				atTokenStart = false
				continue
			}
			if r == '\'' || r == '"' {
				quote = r
				atTokenStart = false
				continue
			}
			if r == '#' && atTokenStart {
				lines[i] = line[:pos]
				break
			}
			atTokenStart = r == ' ' || r == '\t' || r == ';' || r == '|' || r == '&'
		}
	}
	return strings.ReplaceAll(strings.Join(lines, "\n"), "\\\n", " ")
}
