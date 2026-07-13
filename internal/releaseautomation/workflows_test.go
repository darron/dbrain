package releaseautomation

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

const finalReleaseTagPattern = `^v[0-9]+\.[0-9]+\.[0-9]+$`

var finalReleaseTagRegexp = regexp.MustCompile(finalReleaseTagPattern)

const (
	checkoutAction     = "actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd"
	setupGoAction      = "actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c"
	setupNodeAction    = "actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020"
	setupTaskAction    = "go-task/setup-task@01a4adf9db2d14c1de7a560f09170b6e0df736aa"
	uploadAction       = "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a"
	downloadAction     = "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c"
	tapTokenExpression = "${{ secrets.HOMEBREW_TAP_TOKEN }}"
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

func TestFinalReleaseTagPolicy(t *testing.T) {
	tests := map[string]bool{
		"v0.6.0":      true,
		"v10.20.300":  true,
		"v0.6.0-rc.1": false,
		"v0.6.0-test": false,
		"v0.6":        false,
		"version-1":   false,
		"v1.2.3.4":    false,
	}
	for tag, want := range tests {
		if got := finalReleaseTagRegexp.MatchString(tag); got != want {
			t.Errorf("tag %q match=%v, want %v", tag, got, want)
		}
	}
}

func TestStableReleaseWorkflowPolicy(t *testing.T) {
	text := readRepoFile(t, ".github/workflows/release.yaml")
	if err := validateStableReleaseWorkflow(text); err != nil {
		t.Fatal(err)
	}
}

func TestStableReleaseWorkflowPolicyRejectsSecurityMutations(t *testing.T) {
	text := readRepoFile(t, ".github/workflows/release.yaml")
	if err := validateStableReleaseWorkflow(text); err != nil {
		t.Fatalf("base workflow invalid: %v", err)
	}
	mutations := []struct {
		name string
		old  string
		new  string
	}{
		{
			name: "accept RC tag",
			old:  `^v[0-9]+\.[0-9]+\.[0-9]+$`,
			new:  `^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$`,
		},
		{
			name: "accept test tag",
			old:  `^v[0-9]+\.[0-9]+\.[0-9]+$`,
			new:  `^v[0-9]+\.[0-9]+\.[0-9]+(-test)?$`,
		},
		{
			name: "guard moved after checkout",
			old: `    steps:
      - name: Validate final release tag
        shell: bash
        env:
          RELEASE_TAG: ${{ github.ref_name }}
        run: |
          set -euo pipefail
          if [[ ! "${RELEASE_TAG}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            echo "stable releases require an exact vX.Y.Z tag; got ${RELEASE_TAG}" >&2
            exit 1
          fi

      - name: Check out repository`,
			new: `    steps:
      - name: Check out repository
        uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd
        with:
          fetch-depth: 0

      - name: Validate final release tag
        shell: bash
        env:
          RELEASE_TAG: ${{ github.ref_name }}
        run: |
          set -euo pipefail
          if [[ ! "${RELEASE_TAG}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            echo "stable releases require an exact vX.Y.Z tag; got ${RELEASE_TAG}" >&2
            exit 1
          fi

      - name: Check out repository`,
		},
		{
			name: "expression spliced into run",
			old:  `if [[ ! "${RELEASE_TAG}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then`,
			new:  `if [[ ! "${{ github.ref_name }}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then`,
		},
		{
			name: "guard continues after failure",
			old:  "      - name: Validate final release tag\n        shell: bash\n",
			new:  "      - name: Validate final release tag\n        shell: bash\n        continue-on-error: true\n",
		},
		{
			name: "guard condition skips validation",
			old:  "      - name: Validate final release tag\n        shell: bash\n",
			new:  "      - name: Validate final release tag\n        shell: bash\n        if: false\n",
		},
		{
			name: "verify condition skips validation",
			old:  "  verify:\n    name: Verify\n",
			new:  "  verify:\n    name: Verify\n    if: false\n",
		},
		{
			name: "verify continues after failure",
			old:  "  verify:\n    name: Verify\n",
			new:  "  verify:\n    name: Verify\n    continue-on-error: true\n",
		},
		{
			name: "build runs after failed verify",
			old:  "  build:\n    name: Build ${{ matrix.goos }}/${{ matrix.goarch }}\n",
			new:  "  build:\n    name: Build ${{ matrix.goos }}/${{ matrix.goarch }}\n    if: ${{ always() }}\n",
		},
		{
			name: "build continues after failure",
			old:  "  build:\n    name: Build ${{ matrix.goos }}/${{ matrix.goarch }}\n",
			new:  "  build:\n    name: Build ${{ matrix.goos }}/${{ matrix.goarch }}\n    continue-on-error: true\n",
		},
		{
			name: "publish runs after failed build",
			old:  "  publish:\n    name: Publish release assets\n",
			new:  "  publish:\n    name: Publish release assets\n    if: ${{ always() }}\n",
		},
		{
			name: "publish continues after failure",
			old:  "  publish:\n    name: Publish release assets\n",
			new:  "  publish:\n    name: Publish release assets\n    continue-on-error: true\n",
		},
		{
			name: "tap update runs after failed publish",
			old:  "  update-homebrew-tap:\n    name: Update Homebrew tap\n",
			new:  "  update-homebrew-tap:\n    name: Update Homebrew tap\n    if: ${{ always() }}\n",
		},
		{
			name: "tap update continues after failure",
			old:  "  update-homebrew-tap:\n    name: Update Homebrew tap\n",
			new:  "  update-homebrew-tap:\n    name: Update Homebrew tap\n    continue-on-error: true\n",
		},
		{
			name: "wrong concurrency group",
			old:  "group: dbrain-homebrew-tap-update",
			new:  "group: dbrain-stable-homebrew-tap-update",
		},
		{
			name: "missing concurrency queue",
			old:  "      queue: max\n",
			new:  "",
		},
		{
			name: "cancelling concurrency",
			old:  "cancel-in-progress: false",
			new:  "cancel-in-progress: true",
		},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			if !strings.Contains(text, mutation.old) {
				t.Fatalf("mutation anchor missing: %q", mutation.old)
			}
			mutated := strings.Replace(text, mutation.old, mutation.new, 1)
			if err := validateStableReleaseWorkflow(mutated); err == nil {
				t.Fatal("mutated workflow unexpectedly passed policy")
			}
		})
	}
}

func validateStableReleaseWorkflow(text string) error {
	p := &workflowPolicy{}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(text), &document); err != nil {
		return fmt.Errorf("parse workflow: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("workflow root must be a mapping")
	}
	root := document.Content[0]
	jobs := mappingNode(root, "jobs")
	p.require(exactMappingKeys(jobs, "build", "publish", "update-homebrew-tap", "verify"), "stable job set must be exact")
	job := func(name string) *yaml.Node { return mappingNode(jobs, name) }

	verifySteps := stepNodes(job("verify"))
	p.require(len(verifySteps) > 0, "verify must contain steps")
	if len(verifySteps) > 0 {
		guard := verifySteps[0]
		p.require(exactMappingKeys(guard, "env", "name", "run", "shell"), "final tag guard keys must be exactly name, shell, env, and run")
		p.require(scalarValue(mappingNode(guard, "name")) == "Validate final release tag", "final tag guard must be the first verify step")
		p.require(scalarValue(mappingNode(guard, "shell")) == "bash", "final tag guard must use bash")
		p.require(exactScalarMap(mappingNode(guard, "env"), map[string]string{
			"RELEASE_TAG": "${{ github.ref_name }}",
		}), "final tag guard env must derive exactly from github.ref_name")
		p.require(normalizeRun(scalarValue(mappingNode(guard, "run"))) == normalizeRun(exactStableTagGuardRun()), "final tag guard script must exactly match reviewed policy")
	}
	p.require(stepIndex(job("verify"), "Validate final release tag") < stepIndex(job("verify"), "Check out repository"), "final tag guard must precede checkout")

	p.require(exactNeeds(mappingNode(job("build"), "needs"), "verify"), "build must depend exactly on verify")
	p.require(exactNeeds(mappingNode(job("publish"), "needs"), "build"), "publish must depend exactly on build")
	p.require(exactNeeds(mappingNode(job("update-homebrew-tap"), "needs"), "publish"), "tap update must depend exactly on publish")
	for _, name := range []string{"verify", "build", "publish", "update-homebrew-tap"} {
		p.require(mappingNode(job(name), "if") == nil, "%s must not override default success gating with job-level if", name)
		p.require(mappingNode(job(name), "continue-on-error") == nil, "%s must not override failure propagation with job-level continue-on-error", name)
	}
	for _, name := range []string{"build", "publish", "update-homebrew-tap"} {
		p.require(jobTransitivelyNeeds(jobs, name, "verify", nil), "%s must transitively depend on verify", name)
	}

	p.require(exactScalarMap(mappingNode(job("update-homebrew-tap"), "concurrency"), map[string]string{
		"group": "dbrain-homebrew-tap-update", "cancel-in-progress": "false", "queue": "max",
	}), "stable tap concurrency must use the exact shared non-cancelling queue")
	if len(p.errors) == 0 {
		return nil
	}
	return fmt.Errorf("stable release workflow policy failed:\n- %s", strings.Join(p.errors, "\n- "))
}

func exactStableTagGuardRun() string {
	return `set -euo pipefail
if [[ ! "${RELEASE_TAG}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "stable releases require an exact vX.Y.Z tag; got ${RELEASE_TAG}" >&2
  exit 1
fi`
}

func jobTransitivelyNeeds(jobs *yaml.Node, from, target string, seen map[string]bool) bool {
	if seen == nil {
		seen = map[string]bool{}
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	needs := mappingNode(mappingNode(jobs, from), "needs")
	if scalarValue(needs) == target {
		return true
	}
	if needs != nil && needs.Kind == yaml.SequenceNode {
		for _, need := range needs.Content {
			if scalarValue(need) == target {
				return true
			}
		}
	}
	for _, dependency := range needNames(needs) {
		if jobTransitivelyNeeds(jobs, dependency, target, seen) {
			return true
		}
	}
	return false
}

func needNames(needs *yaml.Node) []string {
	if name := scalarValue(needs); name != "" {
		return []string{name}
	}
	if needs == nil || needs.Kind != yaml.SequenceNode {
		return nil
	}
	names := make([]string, 0, len(needs.Content))
	for _, need := range needs.Content {
		names = append(names, scalarValue(need))
	}
	return names
}

func TestHomebrewTestWorkflowPolicy(t *testing.T) {
	text := readRepoFile(t, ".github/workflows/homebrew-test.yaml")
	if err := validateHomebrewTestWorkflow(text); err != nil {
		t.Fatal(err)
	}
}

func TestHomebrewTestWorkflowPolicyRejectsSecurityMutations(t *testing.T) {
	text := readRepoFile(t, ".github/workflows/homebrew-test.yaml")
	if err := validateHomebrewTestWorkflow(text); err != nil {
		t.Fatalf("base workflow invalid: %v", err)
	}
	mutations := []struct {
		name string
		old  string
		new  string
	}{
		{
			name: "evil action prefix",
			old:  "uses: " + checkoutAction,
			new:  "uses: evil/" + checkoutAction,
		},
		{
			name: "gate moved after another step",
			old:  "    steps:\n      - name: Enforce owner and default branch",
			new:  "    steps:\n      - name: Unexpected pre-gate step\n        run: echo unsafe\n      - name: Enforce owner and default branch",
		},
		{
			name: "extra job permission",
			old:  "  verify:\n    name: Verify candidate",
			new:  "  verify:\n    name: Verify candidate\n    permissions:\n      issues: write",
		},
		{
			name: "persisted tap credential",
			old:  "          repository: darron/homebrew-tap\n          token: ${{ secrets.HOMEBREW_TAP_TOKEN }}\n          path: homebrew-tap\n          persist-credentials: false",
			new:  "          repository: darron/homebrew-tap\n          token: ${{ secrets.HOMEBREW_TAP_TOKEN }}\n          path: homebrew-tap\n          persist-credentials: true",
		},
		{
			name: "wildcard build artifact",
			old:  "path: dist/dbrain_${{ needs.prepare.outputs.release_tag }}_${{ matrix.goos }}_${{ matrix.goarch }}.${{ matrix.archive_ext }}",
			new:  "path: dist/dbrain_*_${{ matrix.goos }}_${{ matrix.goarch }}.${{ matrix.archive_ext }}",
		},
		{
			name: "extra accepted downloaded archive",
			old:  `"${expected[0]}"|"${expected[1]}"|"${expected[2]}"|"${expected[3]}"|"${expected[4]}") ;;`,
			new:  `"${expected[0]}"|"${expected[1]}"|"${expected[2]}"|"${expected[3]}"|"${expected[4]}"|"dbrain_evil_linux_amd64.tar.gz") ;;`,
		},
		{
			name: "privileged artifact extraction",
			old:  "      - name: Verify exact archive inventory and generate checksums\n        shell: bash\n        env:\n          RELEASE_TAG: ${{ needs.prepare.outputs.release_tag }}\n        run: |\n          set -euo pipefail",
			new:  "      - name: Verify exact archive inventory and generate checksums\n        shell: bash\n        env:\n          RELEASE_TAG: ${{ needs.prepare.outputs.release_tag }}\n        run: |\n          set -euo pipefail\n          tar -xf dist/candidate.tar.gz",
		},
		{
			name: "token disclosure in require step",
			old:  "          test -n \"${HOMEBREW_TAP_TOKEN}\"",
			new:  "          test -n \"${HOMEBREW_TAP_TOKEN}\"\n          printf '%s\\n' \"${HOMEBREW_TAP_TOKEN}\"",
		},
		{
			name: "token disclosure in push step",
			old:  "            push origin HEAD",
			new:  "            push origin HEAD\n          printf '%s\\n' \"${HOMEBREW_TAP_TOKEN}\"",
		},
		{
			name: "extra GitHub API write in publish",
			old:  "          gh release upload \"${RELEASE_TAG}\" \"${files[@]}\" \\\n            --repo \"${GITHUB_REPOSITORY}\"",
			new:  "          gh release upload \"${RELEASE_TAG}\" \"${files[@]}\" \\\n            --repo \"${GITHUB_REPOSITORY}\"\n          gh api --method DELETE \"repos/${GITHUB_REPOSITORY}/releases/tags/${RELEASE_TAG}\"",
		},
		{
			name: "variable-indirected candidate execution",
			old:  "          test \"${entry_count}\" -eq \"${#expected[@]}\"\n          (",
			new:  "          test \"${entry_count}\" -eq \"${#expected[@]}\"\n          candidate_dir=dist\n          python3 -c 'import os,sys; os.execv(sys.argv[1], [sys.argv[1]])' \"${candidate_dir}/some-file\"\n          (",
		},
		{
			name: "command after required push",
			old:  "            push origin HEAD",
			new:  "            push origin HEAD\n          echo after-push",
		},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			if !strings.Contains(text, mutation.old) {
				t.Fatalf("mutation anchor missing: %q", mutation.old)
			}
			mutated := strings.Replace(text, mutation.old, mutation.new, 1)
			if err := validateHomebrewTestWorkflow(mutated); err == nil {
				t.Fatal("mutated workflow unexpectedly passed policy")
			}
		})
	}
}

type workflowPolicy struct {
	errors []string
}

func (p *workflowPolicy) require(ok bool, format string, args ...any) {
	if !ok {
		p.errors = append(p.errors, fmt.Sprintf(format, args...))
	}
}

func (p *workflowPolicy) result() error {
	if len(p.errors) == 0 {
		return nil
	}
	return fmt.Errorf("Homebrew test workflow policy failed:\n- %s", strings.Join(p.errors, "\n- "))
}

func validateHomebrewTestWorkflow(text string) error {
	p := &workflowPolicy{}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(text), &document); err != nil {
		return fmt.Errorf("parse workflow: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("workflow root must be a mapping")
	}
	root := document.Content[0]
	p.require(exactScalarMap(mappingNode(root, "permissions"), map[string]string{"contents": "read"}), "root permissions must be exactly contents: read")

	triggers := mappingNode(root, "on")
	p.require(exactMappingKeys(triggers, "workflow_dispatch"), "workflow must have only workflow_dispatch trigger")
	dispatch := mappingNode(triggers, "workflow_dispatch")
	inputs := mappingNode(dispatch, "inputs")
	p.require(exactMappingKeys(inputs, "label", "sha"), "workflow_dispatch inputs must be exactly sha and label")
	for _, input := range []string{"sha", "label"} {
		definition := mappingNode(inputs, input)
		p.require(scalarValue(mappingNode(definition, "required")) == "true", "%s input must be required", input)
		p.require(scalarValue(mappingNode(definition, "type")) == "string", "%s input must be a string", input)
	}

	jobs := mappingNode(root, "jobs")
	p.require(exactMappingKeys(jobs, "build", "prepare", "publish", "update-homebrew-tap", "verify"), "job set must be exact")
	job := func(name string) *yaml.Node { return mappingNode(jobs, name) }
	for _, name := range []string{"prepare", "verify", "build", "update-homebrew-tap"} {
		p.require(mappingNode(job(name), "permissions") == nil, "%s must inherit exact root read permission", name)
	}
	p.require(exactScalarMap(mappingNode(job("publish"), "permissions"), map[string]string{"contents": "write"}), "publish permissions must be exactly contents: write")

	p.require(exactNeeds(mappingNode(job("verify"), "needs"), "prepare"), "verify needs must be exactly prepare")
	p.require(exactNeeds(mappingNode(job("build"), "needs"), "prepare", "verify"), "build needs must be exactly prepare and verify")
	p.require(exactNeeds(mappingNode(job("publish"), "needs"), "prepare", "build"), "publish needs must be exactly prepare and build")
	p.require(exactNeeds(mappingNode(job("update-homebrew-tap"), "needs"), "prepare", "publish"), "tap needs must be exactly prepare and publish")

	allowedActions := map[string]bool{
		checkoutAction: true, setupGoAction: true, setupNodeAction: true,
		setupTaskAction: true, uploadAction: true, downloadAction: true,
	}
	for name, node := range jobNodes(jobs) {
		for _, step := range stepNodes(node) {
			uses := scalarValue(mappingNode(step, "uses"))
			if uses != "" {
				p.require(allowedActions[uses], "job %s uses unapproved action %q", name, uses)
			}
			if uses == setupNodeAction {
				p.require(mappingNode(mappingNode(step, "with"), "cache") == nil, "setup-node cache input must stay unset because the pinned action caches only when configured")
			}
		}
	}

	prepareSteps := stepNodes(job("prepare"))
	p.require(len(prepareSteps) > 0 && scalarValue(mappingNode(prepareSteps[0], "name")) == "Enforce owner and default branch", "owner/ref gate must be first prepare step")
	if len(prepareSteps) > 0 {
		gate := prepareSteps[0]
		p.require(exactScalarMap(mappingNode(gate, "env"), map[string]string{
			"ACTOR": "${{ github.actor }}", "TRIGGERING_ACTOR": "${{ github.triggering_actor }}", "WORKFLOW_REF": "${{ github.ref }}",
		}), "gate env must derive exactly from actor, triggering_actor, and ref")
		wantGate := "set -euo pipefail\ntest \"${ACTOR}\" = \"darron\"\ntest \"${TRIGGERING_ACTOR}\" = \"darron\"\ntest \"${WORKFLOW_REF}\" = \"refs/heads/main\""
		p.require(strings.TrimSpace(scalarValue(mappingNode(gate, "run"))) == wantGate, "gate script must exactly enforce darron/darron/main")
	}

	assertCheckout := func(jobName, stepName string, expected map[string]string) {
		step := namedStep(job(jobName), stepName)
		p.require(step != nil, "%s missing step %q", jobName, stepName)
		if step == nil {
			return
		}
		p.require(scalarValue(mappingNode(step, "uses")) == checkoutAction, "%s/%s must use exact checkout action", jobName, stepName)
		p.require(exactScalarMap(mappingNode(step, "with"), expected), "%s/%s checkout inputs are not exact", jobName, stepName)
	}
	assertCheckout("prepare", "Check out trusted workflow source", map[string]string{"ref": "${{ github.sha }}", "persist-credentials": "false"})
	assertCheckout("verify", "Check out candidate source", map[string]string{"ref": "${{ needs.prepare.outputs.sha }}", "persist-credentials": "false"})
	assertCheckout("build", "Check out candidate source", map[string]string{"ref": "${{ needs.prepare.outputs.sha }}", "persist-credentials": "false"})
	assertCheckout("update-homebrew-tap", "Check out trusted workflow source", map[string]string{"ref": "${{ github.sha }}", "path": "trusted-source", "persist-credentials": "false"})
	assertCheckout("update-homebrew-tap", "Check out Homebrew tap", map[string]string{
		"repository": "darron/homebrew-tap", "token": tapTokenExpression, "path": "homebrew-tap", "persist-credentials": "false",
	})

	tap := job("update-homebrew-tap")
	p.require(exactScalarMap(mappingNode(tap, "concurrency"), map[string]string{
		"group": "dbrain-homebrew-tap-update", "cancel-in-progress": "false", "queue": "max",
	}), "tap concurrency must be non-cancelling queue: max")
	p.require(stepIndex(tap, "Set up Go for trusted helper") < stepIndex(tap, "Check out Homebrew tap"), "setup-go must run before secret-bearing tap checkout")

	assertMatrix(p, job("build"))
	upload := namedStep(job("build"), "Upload archive")
	p.require(upload != nil && scalarValue(mappingNode(upload, "uses")) == uploadAction, "build upload action must be exact")
	if upload != nil {
		with := mappingNode(upload, "with")
		p.require(exactScalarMap(with, map[string]string{
			"name":              "candidate-${{ matrix.goos }}-${{ matrix.goarch }}",
			"path":              "dist/dbrain_${{ needs.prepare.outputs.release_tag }}_${{ matrix.goos }}_${{ matrix.goarch }}.${{ matrix.archive_ext }}",
			"if-no-files-found": "error", "retention-days": "7",
		}), "build upload inputs must be exact")
		p.require(!strings.Contains(scalarValue(mappingNode(with, "path")), "*"), "build archive path must not contain a glob")
	}

	publish := job("publish")
	p.require(exactStepNames(publish, "Download candidate archives", "Verify exact archive inventory and generate checksums", "Publish GitHub prerelease"), "publish step sequence must be exact")
	p.require(!jobUsesAction(publish, checkoutAction), "publish must not check out source")
	assertDownloadStep(p, publish)
	assertExactRun(p, publish, "Verify exact archive inventory and generate checksums", exactArchiveInventoryRun(true))
	publishStep := namedStep(publish, "Publish GitHub prerelease")
	p.require(exactScalarMap(mappingNode(publishStep, "env"), map[string]string{
		"GH_TOKEN": "${{ github.token }}", "RELEASE_TAG": "${{ needs.prepare.outputs.release_tag }}",
		"CANDIDATE_SHA": "${{ needs.prepare.outputs.sha }}", "DISPLAY_LABEL": "${{ needs.prepare.outputs.label }}",
		"SHORT_SHA": "${{ needs.prepare.outputs.short_sha }}",
	}), "publish environment must contain only exact trusted metadata and repository token")
	assertExactRun(p, publish, "Publish GitHub prerelease", exactPublishRun())

	assertExactRun(p, tap, "Verify exact archive inventory", exactArchiveInventoryRun(false))
	p.require(exactStepNames(tap,
		"Require Homebrew tap token", "Download candidate archives", "Check out trusted workflow source",
		"Set up Go for trusted helper", "Check out Homebrew tap", "Verify exact archive inventory",
		"Generate and validate tap changes", "Commit tap update", "Push tap update", "Write candidate instructions",
	), "tap step sequence must be exact")
	assertDownloadStep(p, tap)
	assertExactRun(p, tap, "Generate and validate tap changes", exactGenerateTapRun())

	requireToken := namedStep(tap, "Require Homebrew tap token")
	p.require(exactScalarMap(mappingNode(requireToken, "env"), map[string]string{"HOMEBREW_TAP_TOKEN": tapTokenExpression}), "token check secret scope must be exact")
	assertExactRun(p, tap, "Require Homebrew tap token", exactRequireTokenRun())
	commit := namedStep(tap, "Commit tap update")
	p.require(exactScalarMap(mappingNode(commit, "env"), map[string]string{
		"DISPLAY_LABEL": "${{ needs.prepare.outputs.label }}", "SHORT_SHA": "${{ needs.prepare.outputs.short_sha }}",
	}), "commit env must contain only non-secret candidate metadata")
	assertExactRun(p, tap, "Commit tap update", exactCommitTapRun())
	push := namedStep(tap, "Push tap update")
	p.require(exactScalarMap(mappingNode(push, "env"), map[string]string{
		"HOMEBREW_TAP_TOKEN": tapTokenExpression, "GIT_TERMINAL_PROMPT": "0",
	}), "push env must contain only terminal guard and tap token")
	assertExactRun(p, tap, "Push tap update", exactPushTapRun())
	p.require(countScalarValue(root, tapTokenExpression) == 3, "tap token expression must appear only in check, checkout input, and final push env")

	for _, name := range []string{"verify", "build"} {
		text := marshalNode(job(name))
		p.require(!strings.Contains(text, "HOMEBREW_TAP_TOKEN") && !strings.Contains(text, "github.token") && !strings.Contains(text, "contents: write"), "%s received write authority", name)
	}
	p.require(!strings.Contains(text, "darvisf"), "darvisf must not be an allowed actor")
	p.require(!strings.Contains(text, "brew uninstall dbrain\n") && !strings.Contains(text, "~/.config/dbrain") && !strings.Contains(text, "~/.local/share/dbrain") && !strings.Contains(text, "launchctl"), "workflow must not remove runtime state")
	p.require(strings.Contains(text, "brew uninstall dbrain-test"), "summary must remove only dbrain-test")
	assertNoExpressionsInRunScripts(p, root)
	return p.result()
}

func assertDownloadStep(p *workflowPolicy, job *yaml.Node) {
	step := namedStep(job, "Download candidate archives")
	p.require(step != nil && scalarValue(mappingNode(step, "uses")) == downloadAction, "candidate download action must be exact")
	if step != nil {
		p.require(exactScalarMap(mappingNode(step, "with"), map[string]string{
			"path": "dist", "pattern": "candidate-*", "merge-multiple": "true",
		}), "candidate download inputs must be exact")
	}
}

func assertMatrix(p *workflowPolicy, build *yaml.Node) {
	include := mappingNode(mappingNode(mappingNode(build, "strategy"), "matrix"), "include")
	want := []map[string]string{
		{"goos": "darwin", "goarch": "amd64", "archive_ext": "tar.gz"},
		{"goos": "darwin", "goarch": "arm64", "archive_ext": "tar.gz"},
		{"goos": "linux", "goarch": "amd64", "archive_ext": "tar.gz"},
		{"goos": "linux", "goarch": "arm64", "archive_ext": "tar.gz"},
		{"goos": "windows", "goarch": "amd64", "archive_ext": "zip"},
	}
	p.require(include != nil && include.Kind == yaml.SequenceNode && len(include.Content) == len(want), "build matrix must have exact five tuples")
	if include == nil || include.Kind != yaml.SequenceNode || len(include.Content) != len(want) {
		return
	}
	for i := range want {
		p.require(exactScalarMap(include.Content[i], want[i]), "build matrix tuple %d is not exact", i)
	}
}

func assertExactRun(p *workflowPolicy, job *yaml.Node, stepName, want string) {
	step := namedStep(job, stepName)
	p.require(step != nil, "missing security-sensitive step %q", stepName)
	if step == nil {
		return
	}
	got := normalizeRun(scalarValue(mappingNode(step, "run")))
	p.require(got == normalizeRun(want), "%s run script must exactly match its reviewed fixture", stepName)
}

func normalizeRun(run string) string {
	return strings.TrimSpace(strings.ReplaceAll(run, "\r\n", "\n"))
}

func exactRequireTokenRun() string {
	return `set -euo pipefail
test -n "${HOMEBREW_TAP_TOKEN}"`
}

func exactArchiveInventoryRun(withChecksums bool) string {
	run := `set -euo pipefail
expected=(
  "dbrain_${RELEASE_TAG}_darwin_amd64.tar.gz"
  "dbrain_${RELEASE_TAG}_darwin_arm64.tar.gz"
  "dbrain_${RELEASE_TAG}_linux_amd64.tar.gz"
  "dbrain_${RELEASE_TAG}_linux_arm64.tar.gz"
  "dbrain_${RELEASE_TAG}_windows_amd64.zip"
)
for archive in "${expected[@]}"; do
  test -f "dist/${archive}"
  test ! -L "dist/${archive}"
done
entry_count=0
while IFS= read -r -d '' entry; do
  entry_count=$((entry_count + 1))
  case "${entry#dist/}" in
    "${expected[0]}"|"${expected[1]}"|"${expected[2]}"|"${expected[3]}"|"${expected[4]}") ;;
    *)
      echo "unexpected downloaded entry: ${entry}" >&2
      exit 1
      ;;
  esac
done < <(find dist -mindepth 1 -maxdepth 1 -print0)
test "${entry_count}" -eq "${#expected[@]}"`
	if withChecksums {
		run += `
(
  cd dist
  sha256sum "${expected[@]}" > dbrain_checksums.txt
)`
	}
	return run
}

func exactPublishRun() string {
	return `set -euo pipefail
if gh api "repos/${GITHUB_REPOSITORY}/git/ref/tags/${RELEASE_TAG}" >/dev/null 2>tag-error.txt; then
  echo "candidate tag already exists: ${RELEASE_TAG}" >&2
  exit 1
fi
if ! grep -q "HTTP 404" tag-error.txt; then
  cat tag-error.txt >&2
  exit 1
fi

files=(
  "dist/dbrain_${RELEASE_TAG}_darwin_amd64.tar.gz"
  "dist/dbrain_${RELEASE_TAG}_darwin_arm64.tar.gz"
  "dist/dbrain_${RELEASE_TAG}_linux_amd64.tar.gz"
  "dist/dbrain_${RELEASE_TAG}_linux_arm64.tar.gz"
  "dist/dbrain_${RELEASE_TAG}_windows_amd64.zip"
  "dist/dbrain_checksums.txt"
)
gh release create "${RELEASE_TAG}" \
  --repo "${GITHUB_REPOSITORY}" \
  --target "${CANDIDATE_SHA}" \
  --title "dbrain test: ${DISPLAY_LABEL} (${SHORT_SHA})" \
  --notes "Homebrew test candidate built from ${CANDIDATE_SHA}. Not a stable release." \
  --prerelease
gh release upload "${RELEASE_TAG}" "${files[@]}" \
  --repo "${GITHUB_REPOSITORY}"`
}

func exactGenerateTapRun() string {
	return `set -euo pipefail
tap="../homebrew-tap"
formula="${tap}/Formula/dbrain-test.rb"
allowlist="${tap}/audit_exceptions/github_prerelease_allowlist.json"
stable="${tap}/Formula/dbrain.rb"
release_base="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/releases/download/${RELEASE_TAG}"
stable_before="$(sha256sum "${stable}" | awk '{print $1}')"

darwin_amd64_sha="$(sha256sum "../dist/dbrain_${RELEASE_TAG}_darwin_amd64.tar.gz" | awk '{print $1}')"
darwin_arm64_sha="$(sha256sum "../dist/dbrain_${RELEASE_TAG}_darwin_arm64.tar.gz" | awk '{print $1}')"
linux_amd64_sha="$(sha256sum "../dist/dbrain_${RELEASE_TAG}_linux_amd64.tar.gz" | awk '{print $1}')"
linux_arm64_sha="$(sha256sum "../dist/dbrain_${RELEASE_TAG}_linux_arm64.tar.gz" | awk '{print $1}')"

go run ./cmd/devtools/homebrew_test_release formula \
  --output "${formula}" \
  --existing "${formula}" \
  --sha "${CANDIDATE_SHA}" --label "${DISPLAY_LABEL}" \
  --run-number "${RUN_NUMBER}" --run-attempt "${RUN_ATTEMPT}" \
  --release-base "${release_base}" \
  --darwin-amd64-sha "${darwin_amd64_sha}" \
  --darwin-arm64-sha "${darwin_arm64_sha}" \
  --linux-amd64-sha "${linux_amd64_sha}" \
  --linux-arm64-sha "${linux_arm64_sha}"
go run ./cmd/devtools/homebrew_test_release allowlist \
  --input "${allowlist}" --output "${allowlist}" \
  --version "${FORMULA_VERSION}"

stable_after="$(sha256sum "${stable}" | awk '{print $1}')"
test "${stable_before}" = "${stable_after}"
(
  cd "${tap}"
  git diff --exit-code -- Formula/dbrain.rb
)

mapfile -d '' changed_paths < <(
  git -C "${tap}" diff --name-only -z
  git -C "${tap}" diff --cached --name-only -z
  git -C "${tap}" ls-files --others --exclude-standard -z
)
test "${#changed_paths[@]}" -gt 0
go run ./cmd/devtools/homebrew_test_release validate-paths "${changed_paths[@]}"`
}

func exactCommitTapRun() string {
	return `set -euo pipefail
git -C homebrew-tap add -- \
  Formula/dbrain-test.rb \
  audit_exceptions/github_prerelease_allowlist.json
git -C homebrew-tap diff --cached --exit-code -- Formula/dbrain.rb
git -C homebrew-tap config user.name "github-actions[bot]"
git -C homebrew-tap config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git -C homebrew-tap commit -m "Update dbrain test to ${DISPLAY_LABEL} (${SHORT_SHA})"`
}

func exactPushTapRun() string {
	return `set -euo pipefail
# Expansion must occur inside Git's credential-helper shell.
# shellcheck disable=SC2016
git -C homebrew-tap \
  -c credential.helper= \
  -c 'credential.helper=!f() { test "$1" = get || exit 0; printf "%s\n" "username=x-access-token" "password=${HOMEBREW_TAP_TOKEN}"; }; f' \
  push origin HEAD`
}

func assertNoExpressionsInRunScripts(p *workflowPolicy, node *yaml.Node) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			p.require(key.Value != "run" || !strings.Contains(value.Value, "${{"), "run script contains direct GitHub expression")
			assertNoExpressionsInRunScripts(p, value)
		}
		return
	}
	for _, child := range node.Content {
		assertNoExpressionsInRunScripts(p, child)
	}
}

func mappingNode(mapping *yaml.Node, key string) *yaml.Node {
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

func scalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}

func exactMappingKeys(node *yaml.Node, keys ...string) bool {
	if node == nil || node.Kind != yaml.MappingNode || len(node.Content) != len(keys)*2 {
		return false
	}
	got := make([]string, 0, len(keys))
	for i := 0; i < len(node.Content); i += 2 {
		got = append(got, node.Content[i].Value)
	}
	sort.Strings(got)
	sort.Strings(keys)
	return strings.Join(got, "\x00") == strings.Join(keys, "\x00")
}

func exactScalarMap(node *yaml.Node, want map[string]string) bool {
	if node == nil || node.Kind != yaml.MappingNode || len(node.Content) != len(want)*2 {
		return false
	}
	for key, value := range want {
		if scalarValue(mappingNode(node, key)) != value {
			return false
		}
	}
	return true
}

func exactNeeds(node *yaml.Node, want ...string) bool {
	if len(want) == 1 && scalarValue(node) == want[0] {
		return true
	}
	if node == nil || node.Kind != yaml.SequenceNode || len(node.Content) != len(want) {
		return false
	}
	for i := range want {
		if scalarValue(node.Content[i]) != want[i] {
			return false
		}
	}
	return true
}

func jobNodes(jobs *yaml.Node) map[string]*yaml.Node {
	result := map[string]*yaml.Node{}
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return result
	}
	for i := 0; i < len(jobs.Content); i += 2 {
		result[jobs.Content[i].Value] = jobs.Content[i+1]
	}
	return result
}

func stepNodes(job *yaml.Node) []*yaml.Node {
	steps := mappingNode(job, "steps")
	if steps == nil || steps.Kind != yaml.SequenceNode {
		return nil
	}
	return steps.Content
}

func exactStepNames(job *yaml.Node, names ...string) bool {
	steps := stepNodes(job)
	if len(steps) != len(names) {
		return false
	}
	for i := range names {
		if scalarValue(mappingNode(steps[i], "name")) != names[i] {
			return false
		}
	}
	return true
}

func namedStep(job *yaml.Node, name string) *yaml.Node {
	for _, step := range stepNodes(job) {
		if scalarValue(mappingNode(step, "name")) == name {
			return step
		}
	}
	return nil
}

func stepIndex(job *yaml.Node, name string) int {
	for i, step := range stepNodes(job) {
		if scalarValue(mappingNode(step, "name")) == name {
			return i
		}
	}
	return 1 << 30
}

func jobUsesAction(job *yaml.Node, action string) bool {
	for _, step := range stepNodes(job) {
		if scalarValue(mappingNode(step, "uses")) == action {
			return true
		}
	}
	return false
}

func marshalNode(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	data, _ := yaml.Marshal(node)
	return string(data)
}

func countScalarValue(node *yaml.Node, value string) int {
	if node == nil {
		return 0
	}
	count := 0
	if node.Kind == yaml.ScalarNode && node.Value == value {
		count++
	}
	for _, child := range node.Content {
		count += countScalarValue(child, value)
	}
	return count
}
