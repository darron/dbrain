# dbrain Security Review Skill And Audit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build and validate a reusable dbrain security-review skill, then use it to produce an evidence-driven security audit and an exact remediation plan for every confirmed finding.

**Architecture:** Keep the skill procedural and place detailed dbrain surface guidance in one-level reference files. Run the audit as a separate evidence phase that maintains a candidate ledger, uses isolated local tests and optional localhost behavior, and stops unsupported hypotheses from becoming findings. Because exact failing tests and production fixes cannot be designed honestly until findings are verified, this plan ends by generating and executing a finding-specific TDD remediation plan when confirmed issues exist.

**Tech Stack:** Markdown agent skills, Go 1.26, SQLite, `net/http`/`httptest`, Svelte/Vite/npm, Task, GitHub Actions YAML, repository-local `dbrain` CLI.

## Global Constraints

- Review code baseline `4269f53` on branch `security-pass`; preserve the approved design commit and all unrelated user work.
- Use only the checkout, isolated repo-local state, synthetic fixtures, and an optional checkout-built localhost server.
- Do not inspect production XDG state, the installed binary, launchd, live tsnet, real credentials, browser profiles, Notes data, archives, or paid providers.
- Do not probe production, public, or third-party endpoints; do not run destructive, denial-of-service, or high-load tests.
- Do not install scanners or download rule packs without new approval.
- Keep raw output under `/private/tmp/dbrain-security-review-2026-07-12-4269f53/`; commit only sanitized evidence.
- Maintain hypothesis, source-traced, behavior-observed, impact-demonstrated, refutation-survived, confirmed, and retested states.
- Never classify a missing test, scanner alert, or model assertion as a confirmed vulnerability.
- For production-code fixes, write and run a failing regression test before implementation.
- For final code changes run `task fmt`, `task lint`, `task test-ci`, and `task build`.
- Update `CHANGELOG.md` and affected public docs for material user-visible or operational behavior changes.

---

## File Structure

### Skill files

- `skills/dbrain-security-review/SKILL.md`: authority, target resolution, review workflow, evidence gates, and completion bar.
- `skills/dbrain-security-review/agents/openai.yaml`: discoverable UI metadata and default invocation prompt.
- `skills/dbrain-security-review/references/methodology.md`: standards, evidence hierarchy, severity/confidence, and tooling.
- `skills/dbrain-security-review/references/evidence-gates.md`: candidate lifecycle, attack paths, refutation, confirmation, retest, and coverage honesty.
- `skills/dbrain-security-review/references/dbrain-surfaces.md`: refreshable map of CLI, filesystem, import, web, MCP, tsnet, model, archive, and browser-extension boundaries.
- `skills/dbrain-security-review/references/supply-chain.md`: Go/npm, helpers/models, CI, embedded UI, release, Homebrew, extensions, and skills.
- `skills/dbrain-security-review/references/secrets-config-logging.md`: configuration precedence, secret refs, subprocesses, file modes, logging, metrics, traces, shares, and exports.
- `skills/dbrain-security-review/references/report-template.md`: full/targeted report and candidate-ledger schema.

### Audit and follow-up files

- `docs/security-reviews/2026-07-12-full-4269f53.md`: sanitized audit ledger, inventory, findings, refutations, commands, and coverage.
- `docs/superpowers/plans/2026-07-12-dbrain-security-remediation.md`: created only if confirmed findings exist; contains exact finding-specific RED/GREEN/REFACTOR steps.
- Existing source/test/docs files: modified only by the remediation plan after a finding is confirmed.

---

### Task 1: Establish The Skill Baseline Test

**Files:**
- Create temporarily: `/private/tmp/dbrain-security-review-2026-07-12-4269f53/skill-baseline.md`
- Inspect: `AGENTS.md`
- Inspect: `docs/superpowers/specs/2026-07-12-dbrain-security-review-design.md`

**Interfaces:**
- Consumes: approved scope and the current checkout.
- Produces: concrete omissions that the minimum skill must correct.

- [ ] **Step 1: Reconfirm the boundary**

Run:

```sh
git status --short --branch
git rev-parse HEAD
direnv exec . ./bin/dbrain --no-debug config paths --json
```

Expected: branch `security-pass`; the only commits after `4269f53` are audit artifacts; resolved database and output paths remain under `/Users/darron/src/dbrain`.

- [ ] **Step 2: Run a fresh-agent baseline without the new skill**

Dispatch one read-only subagent with no inherited conversation and this prompt:

```text
Perform a security review of /Users/darron/src/dbrain. Return your review scope,
attack-surface inventory, candidate findings, evidence requirements, commands,
and completion criteria. Do not edit files or access production/runtime secrets.
```

Require the agent to return prose only. Do not tell it the intended skill shape or suspected bugs.

- [ ] **Step 3: Score the baseline**

Write sanitized observations to the temporary baseline file using these exact headings:

```markdown
# Baseline Skill Test

## Boundary Resolution
## Surface Coverage
## Hypothesis Versus Finding Discipline
## Attack-Path Evidence
## Refutation Behavior
## Coverage Honesty
## dbrain-Specific Omissions
```

Record verbatim rationalizations only when they contain no private data. Expected baseline failure is omission of one or more local-data, filesystem, importer, archive, subprocess, listener-mode, or supply-chain boundaries, or premature promotion of code smells.

- [ ] **Step 4: Commit**

Do not commit the temporary baseline. Confirm `git status --short` remains unchanged.

---

### Task 2: Initialize The Repository-Owned Skill

**Files:**
- Create: `skills/dbrain-security-review/SKILL.md`
- Create: `skills/dbrain-security-review/agents/openai.yaml`
- Create: `skills/dbrain-security-review/references/`

**Interfaces:**
- Consumes: baseline failures from Task 1.
- Produces: valid skill skeleton and UI metadata for Tasks 3–4.

- [ ] **Step 1: Initialize with the installed generator**

Run:

```sh
python3 /Users/darron/.codex/skills/.system/skill-creator/scripts/init_skill.py \
  dbrain-security-review \
  --path skills \
  --resources references \
  --interface 'display_name=dbrain Security Review' \
  --interface 'short_description=Audit dbrain security with verified evidence' \
  --interface 'default_prompt=Use $dbrain-security-review to perform an evidence-driven security review of the current dbrain checkout.'
```

Expected: generator creates only the named skill directory, `SKILL.md`, `agents/openai.yaml`, and `references/`.

- [ ] **Step 2: Verify metadata shape**

`agents/openai.yaml` must contain exactly:

```yaml
interface:
  display_name: "dbrain Security Review"
  short_description: "Audit dbrain security with verified evidence"
  default_prompt: "Use $dbrain-security-review to perform an evidence-driven security review of the current dbrain checkout."
```

Do not add MCP dependencies: the review uses repository and shell evidence, not dbrain corpus retrieval.

- [ ] **Step 3: Validate the generated skeleton fails for incompleteness**

Run:

```sh
rg -n 'TODO|\[TODO' skills/dbrain-security-review
```

Expected: matches in the generated template. This is the skill RED state; do not treat generator validity as semantic completeness.

---

### Task 3: Write The Minimum dbrain Security Review Skill

**Files:**
- Modify: `skills/dbrain-security-review/SKILL.md`
- Create: `skills/dbrain-security-review/references/methodology.md`
- Create: `skills/dbrain-security-review/references/evidence-gates.md`
- Create: `skills/dbrain-security-review/references/dbrain-surfaces.md`
- Create: `skills/dbrain-security-review/references/supply-chain.md`
- Create: `skills/dbrain-security-review/references/secrets-config-logging.md`
- Create: `skills/dbrain-security-review/references/report-template.md`

**Interfaces:**
- Consumes: baseline omissions and approved design.
- Produces: a complete skill whose references can drive the real audit.

- [ ] **Step 1: Write `SKILL.md` frontmatter and contract**

Use this frontmatter exactly:

```yaml
---
name: dbrain-security-review
description: Perform evidence-driven, read-only security and supply-chain reviews of dbrain. Use for full or targeted audits of its Go CLI, SQLite and local files, Apple Notes and Safari imports, URL and media ingestion, subprocesses and model providers, web/OAuth/public shares, MCP, tsnet/Funnel, archives, browser extensions, secrets/config/logging, GitHub Actions, embedded Svelte UI, releases, Homebrew, or published skills; also use when verifying security regressions and designing remediation tests.
---
```

The body must contain these sections in order:

```markdown
# dbrain Security Review
## Core Principle
## Safety And Authority
## Required References
## Workflow
### 1. Resolve The Real Target
### 2. Record Scope And Action Classes
### 3. Build The Surface And Control Inventory
### 4. Build The Threat Model
### 5. Trace dbrain-Specific Abuse Cases
### 6. Verify Rejection And Containment Paths
### 7. Run Proportionate Automated Checks
### 8. Perform Isolated Dynamic Verification
### 9. Triage, Refute, And Retest Findings
### 10. Produce The Report
## Completion Bar
```

Require explicit repo/dev versus production resolution, provenance labels, falsification statements, exact commands, and separate confirmed/probable/gap/defense/refuted verdicts. Keep `SKILL.md` under 500 lines and move detailed lists into references.

- [ ] **Step 2: Write `methodology.md`**

Include a contents list and these contracts:

- OWASP ASVS 5.0.0, WSTG 4.2, API Security 2023, and GenAI/agentic guidance are mappings, not certification claims.
- Evidence order: isolated runtime reproduction; focused real-seam test; complete current source trace; resolved runtime artifact; manually triaged scanner output; documentation/history.
- Severity: Critical/High/Medium/Low/Informational based on demonstrated impact and prerequisites.
- Confidence: High/Moderate/Low/Unknown independent from severity.
- Installed-tool lanes: `rg`, git, focused Go tests, race tests, Task targets, npm audit/test/build, `govulncheck`, `gosec`, `staticcheck`, secret/workflow review when available.
- Tool absence is a coverage gap; no downloads without approval.

- [ ] **Step 3: Write `evidence-gates.md`**

Define the seven candidate states and provenance values:

```text
Model asserted
Context cited
Source traced
Tool observed
Runtime reproduced
Retest verified
```

Require attack paths, dominating-guard searches, explicit refutation records, both exploit-path and authorized-path retests, and inspected/uninspected coverage lists.

- [ ] **Step 4: Write `dbrain-surfaces.md`**

Organize the reference under:

```markdown
## Refresh Rule
## Runtime And Identity Boundaries
## Local Data And Filesystem
## Importers And Native Parsers
## Outbound HTTP And Models
## Web, OAuth, Public Shares, And Media
## MCP And tsnet
## Archive And Restore
## Required Abuse Hypotheses
## High-Value Code Locations
```

Include the required hypotheses from the approved design. State that orientation is not a frozen route inventory and must be refreshed from current registrations and callers.

- [ ] **Step 5: Write `supply-chain.md` and `secrets-config-logging.md`**

`supply-chain.md` must cover action pinning, workflow permissions/events, runtime installs, dependency reachability, npm lock integrity, embedded `web/ui/dist`, helper/model provenance, release checksums/attestations/SBOMs, Homebrew token boundaries, extension packaging, third-party notices, and skill OIDC publishing.

`secrets-config-logging.md` must cover runtime precedence, plaintext/env/`op://`/`keychain://`, explicit command escape hatches, argv without shells, process environment and output, direct secret CLI flags, file modes, access logs, metrics, traces, errors, public shares, OKF, backups, and redaction.

- [ ] **Step 6: Write `report-template.md`**

Require this finding block:

```markdown
### SEC-NNN: Concise title
- Classification
- Severity
- Confidence
- Candidate state
- Affected boundary
- Standards
- Prerequisites
- Evidence and provenance
- Attack path
- Impact
- Reproduction
- Root cause
- Falsification statement
- Refutation attempt
- Remediation
- Regression test
- Residual risk
```

Also require executive summary, scope/limitations, surface inventory, threat model, verification summary, candidate ledger, confirmed/probable findings, coverage gaps, verified positive controls, commands/outcomes, remediation order, and final assessment.

- [ ] **Step 7: Validate content and structure**

Run:

```sh
python3 /Users/darron/.codex/skills/.system/skill-creator/scripts/quick_validate.py skills/dbrain-security-review
rg -n 'TODO|TBD|FIXME|\[TODO' skills/dbrain-security-review
wc -l skills/dbrain-security-review/SKILL.md
git diff --check
```

Expected: validator exits zero; placeholder search returns no matches; `SKILL.md` is below 500 lines; diff check is clean.

- [ ] **Step 8: Commit**

```sh
git add skills/dbrain-security-review
git commit -m "feat: add dbrain security review skill"
```

---

### Task 4: Forward-Test And Refine The Skill

**Files:**
- Modify if needed: `skills/dbrain-security-review/*.md`
- Modify if needed: `skills/dbrain-security-review/references/*.md`
- Append temporarily: `/private/tmp/dbrain-security-review-2026-07-12-4269f53/skill-forward-tests.md`

**Interfaces:**
- Consumes: validated skill from Task 3.
- Produces: evidence that fresh agents apply the contract to new dbrain surfaces.

- [ ] **Step 1: Run three fresh-context technique tests**

Dispatch read-only agents without intended answers:

```text
Use $dbrain-security-review at /Users/darron/src/dbrain/skills/dbrain-security-review
to review the web, OAuth, MCP, and tsnet boundary in /Users/darron/src/dbrain.
Return evidence and coverage gaps; do not edit files.
```

```text
Use $dbrain-security-review at /Users/darron/src/dbrain/skills/dbrain-security-review
to review local imports, filesystem paths, subprocesses, and archives in
/Users/darron/src/dbrain. Return evidence and coverage gaps; do not edit files.
```

```text
Use $dbrain-security-review at /Users/darron/src/dbrain/skills/dbrain-security-review
to review dependencies, GitHub Actions, embedded UI, releases, browser
extensions, and published skills in /Users/darron/src/dbrain. Do not edit files.
```

- [ ] **Step 2: Score forward tests**

For each, require: correct target boundary; surface/control inventory; hypotheses not mislabeled; at least one complete attack path; falsification statement; dominating-guard search; explicit limitations; no unauthorized action.

- [ ] **Step 3: Refine only observed gaps**

If an agent omits or misuses a required contract, patch the smallest relevant skill section, rerun that scenario with a fresh agent, and record the before/after behavior. Do not add speculative prose unrelated to an observed failure.

- [ ] **Step 4: Revalidate and commit**

Run the Task 3 validation commands, then:

```sh
git add skills/dbrain-security-review
git commit -m "docs: harden dbrain security review workflow"
```

Skip the commit when no refinement was required.

---

### Task 5: Create The Audit Ledger And Inventory

**Files:**
- Create: `docs/security-reviews/2026-07-12-full-4269f53.md`
- Inspect: `web/server.go`
- Inspect: `internal/mcpserver/`
- Inspect: `internal/remote/`
- Inspect: `internal/app/`
- Inspect: `.github/workflows/`

**Interfaces:**
- Consumes: validated skill and approved scope.
- Produces: report scaffold, exact inventory, threat model, and candidate IDs.

- [ ] **Step 1: Create the sanitized report scaffold**

Use the skill report template and record:

```text
Code baseline: 4269f53
Audit-artifact branch head: current HEAD
Boundary: checkout and isolated repo-local tests
Allowed: local reads, builds, tests, localhost requests, synthetic state
Excluded: production XDG, installed binary, launchd, live tsnet, real secrets,
paid providers, external probing, destructive/high-load tests, tool installation
```

- [ ] **Step 2: Build the surface/control inventory**

Create separate tables for CLI/filesystem, import/network/model, web/OAuth/public, MCP/tsnet, archive/restore, and supply chain. Each row must include caller, input, guard, sink/side effect, bounds, evidence, and status.

- [ ] **Step 3: Write the threat model**

Cover local user, remote caller, authenticated client, hostile origin/extension, hostile imported content, malicious remote service/model/archive/helper, corrupt database, and symlink attacker classes.

- [ ] **Step 4: Seed candidate hypotheses**

Create stable candidate IDs for every required hypothesis from `dbrain-surfaces.md`, plus any new source-backed candidates. Set all initial states to Hypothesis and provenance to Context cited or Source traced as appropriate; do not assign confirmed severity.

- [ ] **Step 5: Commit the review scaffold**

```sh
git add docs/security-reviews/2026-07-12-full-4269f53.md
git commit -m "docs: start dbrain security review ledger"
```

---

### Task 6: Execute Independent White-Box Review Lanes

**Files:**
- Modify centrally: `docs/security-reviews/2026-07-12-full-4269f53.md`
- Inspect: repository source, tests, docs, workflows, generated assets, and manifests.

**Interfaces:**
- Consumes: candidate ledger and inventory.
- Produces: traced paths, refutations, test proposals, and bounded dynamic checks.

- [ ] **Step 1: Dispatch non-overlapping read-only lanes**

Use one lane each for:

1. web/OAuth/public/MCP/tsnet;
2. local data/importers/filesystem/subprocess/archive;
3. dependencies/CI/release/extensions/skills.

Give each agent the validated skill and report path, but instruct it to return evidence rather than edit the shared report.

- [ ] **Step 2: Independently verify every returned claim**

The primary agent must reread cited source and tests, trace all callers and guards, and reject unsupported conclusions. Add exact current line references and falsification statements to the report.

- [ ] **Step 3: Run focused static and dependency lanes**

Discover installed tools first:

```sh
command -v govulncheck
command -v gosec
command -v staticcheck
command -v gitleaks
command -v semgrep
```

Run installed relevant tools only. Also run:

```sh
go list -m all
npm audit --json
```

Run `npm audit --json` with `/Users/darron/src/dbrain/web/ui` as the working directory.

If the npm audit requires network and is blocked, request the narrow network approval or record the gap. Manually triage every tool lead for reachability before changing candidate status.

- [ ] **Step 4: Check generated and distribution artifacts**

Run repository-supported clean frontend generation and compare tracked output:

```sh
task web-build
git diff --exit-code -- web/ui/dist
```

Inspect workflow `uses:`, permissions, events, runtime installs, release checksums, Homebrew update commands, package manifests, extension permissions, and the skill publish matrix. Record mutable dependencies and absent provenance as gaps or defense-in-depth unless exploit impact is demonstrated.

- [ ] **Step 5: Commit evidence updates**

```sh
git add docs/security-reviews/2026-07-12-full-4269f53.md
git commit -m "docs: trace dbrain security review candidates"
```

---

### Task 7: Perform Isolated Behavioral Verification

**Files:**
- Modify: `docs/security-reviews/2026-07-12-full-4269f53.md`
- Add temporary tests only under `/private/tmp` unless they become confirmed regression tests in the remediation plan.
- Use existing package tests in `web/`, `internal/remote/`, `internal/mcpserver/`, importer, archive, media, config, and runtime packages.

**Interfaces:**
- Consumes: traced candidates from Task 6.
- Produces: observed behavior, demonstrated impact or refutation, and confirmed/probable/gap verdicts.

- [ ] **Step 1: Prioritize candidates**

Test candidates in this order: cross-boundary file access/deletion; private-network/credential exposure; unauthenticated remote data or write access; archive integrity; stored active content/public leakage; secret exposure; resource exhaustion; supply-chain hardening.

- [ ] **Step 2: Add noncommitted reproductions or focused tests**

Use synthetic temp directories, `httptest.Server`, fake transports, fake subprocesses, fake S3 clients, and fixture databases. Never point tests at repo/dev or production data when a temp fixture can prove the behavior.

- [ ] **Step 3: Optionally start the isolated dev server**

Only when HTTP/browser behavior cannot be proven with `httptest`:

```sh
task build
direnv exec . ./bin/dbrain --no-debug config paths --json
direnv exec . ./bin/dbrain --no-debug serve web --addr 127.0.0.1:18742
```

Confirm the process uses repo-local paths. Do not enable remote transport, OAuth, external enrichment, archives, or paid providers. Stop the server after the bounded checks.

- [ ] **Step 4: Apply the evidence and refutation gates**

For each candidate, record observed request/result or test output, concrete impact, the guard that would refute it, the search for that guard, and the resulting classification. Keep unexecuted high-impact source traces probable rather than confirmed.

- [ ] **Step 5: Reproduce confirmed candidates cleanly**

Repeat each confirmed reproduction from a clean isolated fixture. A result that cannot be repeated is inconclusive, not confirmed.

- [ ] **Step 6: Commit the verified report**

```sh
git add docs/security-reviews/2026-07-12-full-4269f53.md
git commit -m "docs: verify dbrain security review findings"
```

---

### Task 8: Create The Finding-Specific Remediation Plan

**Files:**
- Create when needed: `docs/superpowers/plans/2026-07-12-dbrain-security-remediation.md`
- Modify: `docs/security-reviews/2026-07-12-full-4269f53.md`

**Interfaces:**
- Consumes: only confirmed findings and separately justified defense-in-depth changes from Task 7.
- Produces: exact tests, functions, files, commands, minimal fixes, and retests for implementation.

- [ ] **Step 1: Decide whether remediation exists**

If there are no confirmed findings and no approved defense-in-depth changes, do not create an empty remediation plan. Complete Task 10 with an evidence-backed no-confirmed-findings result and explicit gaps.

- [ ] **Step 2: Invoke the writing-plans workflow for confirmed findings**

For each finding, include exact current files and function signatures, the minimal regression test code, the expected RED failure, minimal production implementation, focused GREEN command, exploit retest, authorized-path test, sibling-caller tests, docs/changelog edits, and commit message.

The remediation plan header must require `superpowers:subagent-driven-development` or `superpowers:executing-plans`, preserve all Global Constraints above, and order shared-seam fixes before caller-specific hardening.

- [ ] **Step 3: Self-review the remediation plan**

Check every confirmed finding has a task and every task has a real RED/GREEN cycle. Search for placeholders and verify all named functions/types exist in the checkout.

- [ ] **Step 4: Commit the plan**

```sh
git add docs/superpowers/plans/2026-07-12-dbrain-security-remediation.md
git commit -m "docs: plan verified security remediations"
```

---

### Task 9: Execute And Review Confirmed Remediations

**Files:**
- Modify: exact source, test, docs, and changelog files named by the Task 8 plan.
- Modify: `docs/security-reviews/2026-07-12-full-4269f53.md`

**Interfaces:**
- Consumes: finding-specific remediation plan.
- Produces: regression-tested fixes and retested report rows.

- [ ] **Step 1: Execute each finding task with TDD**

For every finding: write the test, run and observe the expected failure, implement the minimal shared-seam fix, run focused GREEN tests, refactor only after green, and commit independently.

- [ ] **Step 2: Review each completed task**

Perform specification-compliance review first and code-quality/security review second. Independently inspect diffs and rerun focused tests; do not trust subagent completion claims.

- [ ] **Step 3: Retest the attack and authorized paths**

Update candidate provenance to Retest verified only after the prior exploit fails for the intended reason and legitimate behavior still passes across relevant caller classes.

- [ ] **Step 4: Update durable documentation**

Update `CHANGELOG.md`, route/architecture/operations docs, and the report with root cause, fix, tests, residual risk, and any migration or compatibility effects.

---

### Task 10: Final Verification And Assessment

**Files:**
- Modify: `docs/security-reviews/2026-07-12-full-4269f53.md`
- Inspect: all final diffs and generated artifacts.

**Interfaces:**
- Consumes: validated skill, audit evidence, and any remediations.
- Produces: final security assessment and clean verified branch.

- [ ] **Step 1: Run skill validation**

```sh
python3 /Users/darron/.codex/skills/.system/skill-creator/scripts/quick_validate.py skills/dbrain-security-review
rg -n 'TODO|TBD|FIXME|\[TODO' skills/dbrain-security-review
```

Expected: validator passes and placeholder scan is empty.

- [ ] **Step 2: Run final repository gates**

When production code changed:

```sh
task fmt
task lint
task test-ci
task build
```

For documentation/skill-only outcomes, run `git diff --check`, skill validation, focused report/Markdown inspection, and any audit commands needed to support the final claims.

- [ ] **Step 3: Inspect the final diff for leakage and scope**

```sh
git status --short --branch
git diff 4269f53 --check
git diff --stat 4269f53
git log --oneline 4269f53..HEAD
```

Search changed files for credentials, cookies, private prompts, absolute production data paths, corpus text, browser-profile paths, and untracked evidence artifacts. Remove or redact any hit before completion.

- [ ] **Step 4: Finalize the report**

State one of: Ready from reviewed scope; Ready after listed blockers were fixed and retested; Not ready because confirmed risk remains; Inconclusive because named gaps remain. Include overall confidence, commands and exit status, untested surfaces, and exact reassessment conditions.

- [ ] **Step 5: Commit final report updates**

```sh
git add docs/security-reviews/2026-07-12-full-4269f53.md
git commit -m "docs: finalize dbrain security review"
```

All source, test, changelog, other documentation, and skill changes must already
be committed by their owning tasks; do not stage them broadly here.
