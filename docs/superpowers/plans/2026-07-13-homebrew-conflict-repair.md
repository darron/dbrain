# Homebrew Reciprocal Conflict Repair Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore green `brew test-bot` runs by making the stable and candidate dbrain formula conflicts reciprocal and preventing future stable releases from dropping the stable declaration.

**Architecture:** Repair `Formula/dbrain.rb` once in `darron/homebrew-tap`. In dbrain, keep the candidate workflow's stable-formula immutability boundary, while teaching the stable release updater to insert the reciprocal conflict when absent and testing that exact updater policy.

**Tech Stack:** GitHub Actions YAML, embedded Ruby formula updater, Go workflow-policy tests, Homebrew `brew audit`/`brew test-bot`, GitHub CLI.

## Global Constraints

- Candidate publication must not modify `Formula/dbrain.rb`.
- The stable updater must add exactly one reciprocal conflict and preserve an existing declaration.
- No release assets, formula URLs/checksums, installed kegs, launchd services, or dbrain runtime data may change.
- Code changes require `task fmt`, `task lint`, and `task test-ci`.

---

### Task 1: Make Stable Release Updates Maintain The Reciprocal Conflict

**Files:**
- Modify: `internal/releaseautomation/workflows_test.go`
- Modify: `.github/workflows/release.yaml`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: the `Update formula` step in the stable `update-homebrew-tap` job.
- Produces: an idempotent embedded Ruby block that ensures `Formula/dbrain.rb` contains `conflicts_with "dbrain-test"`.

- [ ] **Step 1: Add the failing workflow-policy regression**

Add `exactStableConflictRepair()` returning this reviewed Ruby block:

```go
func exactStableConflictRepair() string {
	return `stableConflict = '  conflicts_with "dbrain-test", because: "both install the dbrain binary"'
unless text.match?(/^\s*conflicts_with\s+"dbrain-test"(?:,.*)?$/)
  license = text.match(/^  license "[^"]+"\n/)
  abort("stable formula is missing its license anchor") unless license
  text = text.sub(license[0], "#{license[0]}\n#{stableConflict}\n")
end`
}
```

In `validateStableReleaseWorkflow`, find the `Update formula` step with
`namedStep(job("update-homebrew-tap"), "Update formula")` and require its
normalized `run` text to contain `exactStableConflictRepair()`. Add a security
mutation that changes `stableConflict` to `disabledConflict`, proving the
policy rejects removal of the repair.

- [ ] **Step 2: Run the policy test and verify RED**

Run:

```bash
go test ./internal/releaseautomation -run 'TestStableReleaseWorkflowPolicy' -count=1
```

Expected: FAIL with `stable formula updater must maintain the reciprocal dbrain-test conflict` because `.github/workflows/release.yaml` does not yet contain the block.

- [ ] **Step 3: Add the minimal idempotent updater block**

Insert the exact `exactStableConflictRepair()` Ruby block in `.github/workflows/release.yaml` after `text = File.read(formula_path)` and before version/URL/checksum replacement. The broad declaration check prevents duplication; the exact inserted line supplies Homebrew's required reciprocal metadata.

- [ ] **Step 4: Add the changelog entry**

Under `Homebrew Test Release Channel (2026-07-13)`, add:

```markdown
- **Tap validation**: Stable formula updates now restore the reciprocal `dbrain-test` conflict required by Homebrew audit, preventing candidate formula publication from breaking subsequent `brew test-bot` runs.
```

- [ ] **Step 5: Verify GREEN and run the repository gates**

Run:

```bash
go test ./internal/releaseautomation -run 'TestStableReleaseWorkflowPolicy' -count=1
task fmt
task lint
task test-ci
```

Expected: all commands exit 0 with no test failures or lint errors.

- [ ] **Step 6: Commit the dbrain repair**

```bash
git add .github/workflows/release.yaml internal/releaseautomation/workflows_test.go CHANGELOG.md
git commit -m "fix: preserve reciprocal Homebrew formula conflict"
```

---

### Task 2: Repair And Verify The Live Homebrew Tap

**Files:**
- Modify in a temporary checkout: `Formula/dbrain.rb`

**Interfaces:**
- Consumes: current `darron/homebrew-tap` main at or after `f8497cfb51f3`.
- Produces: a narrowly scoped tap commit adding the stable formula's reciprocal conflict.

- [ ] **Step 1: Clone and confirm the live failure boundary**

```bash
git clone https://github.com/darron/homebrew-tap.git /private/tmp/dbrain-homebrew-tap-conflict-repair
cd /private/tmp/dbrain-homebrew-tap-conflict-repair
git status --short
rg -n 'conflicts_with' Formula/dbrain.rb Formula/dbrain-test.rb
```

Expected: clean checkout; `dbrain-test.rb` conflicts with `dbrain`; `dbrain.rb` has no reciprocal declaration.

- [ ] **Step 2: Add only the reciprocal stable declaration**

Insert after the stable formula's license:

```ruby
  conflicts_with "dbrain-test", because: "both install the dbrain binary"
```

- [ ] **Step 3: Verify the tap diff and local Homebrew audit**

```bash
git diff --check
git diff -- Formula/dbrain.rb
brew audit --strict Formula/dbrain.rb Formula/dbrain-test.rb
```

Expected: the diff contains only the reciprocal declaration; audit reports no asymmetric-conflict error.

- [ ] **Step 4: Commit and push the tap repair**

```bash
git add Formula/dbrain.rb
git commit -m "Fix reciprocal dbrain formula conflict"
git push origin main
```

Expected: the push creates exactly one new `main` commit and automatically triggers `brew test-bot`.

- [ ] **Step 5: Watch and verify the new workflow run**

```bash
gh run list --repo darron/homebrew-tap --workflow "brew test-bot" --limit 1 --json databaseId,headSha,status,conclusion,url
run_id="$(gh run list --repo darron/homebrew-tap --workflow "brew test-bot" --limit 1 --json databaseId --jq '.[0].databaseId')"
gh run watch "${run_id}" --repo darron/homebrew-tap --exit-status
gh run view "${run_id}" --repo darron/homebrew-tap --json status,conclusion,jobs,url
```

Expected: macOS ARM, macOS Intel, and Ubuntu jobs complete successfully. If a job fails for a different reason, inspect its exact failing log before making another change.

- [ ] **Step 6: Confirm final repository state**

```bash
git -C /private/tmp/dbrain-homebrew-tap-conflict-repair status --short
git status --short
git log -3 --oneline
```

Expected: both checkouts are clean; the dbrain history contains the design and implementation commits; the tap history contains the reciprocal-conflict repair.
