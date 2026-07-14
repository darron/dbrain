# dbrain Security Review Skill And Remediation Campaign Design

Date: 2026-07-12
Review baseline: `security-pass` at `4269f53`

## Purpose

Create a reusable `dbrain-security-review` skill, validate it against realistic
review tasks, then use it to perform an evidence-driven security review of the
current dbrain checkout. Document candidates while they are investigated, but
promote only verified weaknesses to findings. For each confirmed issue, add a
failing regression test before changing production code, implement the smallest
appropriate fix, and retest both the exploit path and the authorized path.

This work targets the repository and isolated development runtime. It is not a
review of the production XDG database, installed binary, launchd service, live
tsnet identity, real credentials, or paid/provider-backed operations.

## Principles

- Resolve the exact runtime boundary before drawing conclusions.
- Treat imported content, database fields, model output, remote responses, and
  restored state as untrusted at every downstream sink.
- Trace candidates from entry point through transformations and guards to the
  affected sink or side effect.
- Separate severity from confidence.
- Search for a dominating guard and write an explicit falsification statement
  before confirming a finding.
- Prefer rejection-path evidence over inferences from happy-path tests.
- Preserve raw evidence without placing secrets or private corpus data in git.
- Distinguish a missing test from an exploitable vulnerability.
- Do not weaken local-first or import-only product guarantees while hardening
  individual surfaces.

## Scope And Authority

### Included

- Go CLI commands and configuration resolution.
- SQLite, vault, OKF, media, cache, temporary, trace, and log paths.
- Apple Notes and Safari snapshot readers and attachment handling.
- URL intake, feeds, source enrichment, media download, OCR, transcription,
  and external helper execution.
- Web routes, optional GitHub OAuth, sessions, service authentication, public
  shares, archived media, and browser-extension requests.
- MCP stdio and HTTP, bearer tokens, request bounds, and read-only guarantees.
- tsnet routing, listener modes, Origin handling, security headers, and Funnel
  configuration as represented in checkout code and isolated tests.
- SQLite and media archive/restore behavior with synthetic local fixtures and
  fake services.
- Model/provider prompt, output, privacy, and resource boundaries without live
  paid calls.
- Go and npm dependencies, embedded frontend assets, GitHub Actions, release
  archives, Homebrew updates, browser extensions, and skill publishing.
- An optional checkout-built localhost development server using isolated repo
  configuration and synthetic data.

### Excluded Without New Approval

- Production XDG data or configuration inspection.
- The installed/Homebrew binary, launchd jobs, or live tsnet state.
- Real OAuth, provider, archive, browser-cookie, Notes, or Tailscale secrets.
- Production, public, or third-party endpoint probing.
- Paid model calls, real archive uploads/restores, credential rotation, share
  publication, destructive tests, denial-of-service tests, and high-load scans.
- Installing scanners or downloading rule packs.

When a candidate cannot be verified within these boundaries, classify it as a
probable finding or coverage gap and state the missing evidence. Do not silently
expand authority.

## Skill Architecture

Create the skill under `skills/dbrain-security-review/` with this structure:

```text
skills/dbrain-security-review/
├── SKILL.md
├── agents/
│   └── openai.yaml
└── references/
    ├── methodology.md
    ├── evidence-gates.md
    ├── dbrain-surfaces.md
    ├── supply-chain.md
    ├── secrets-config-logging.md
    └── report-template.md
```

`SKILL.md` remains concise and procedural. It defines the authority boundary,
target-resolution step, review modes, inventory workflow, verification order,
finding classifications, and completion bar. Detailed surface orientation and
checklists live in the reference files so targeted reviews do not load the full
corpus unnecessarily.

No helper script is planned initially. Existing `task`, Go test, npm, and
repository inspection commands provide the deterministic execution lanes. A
script should be added only after repeated use demonstrates a stable inventory
operation worth automating.

The skill will adapt the Converge skill's evidence and refutation machinery,
not mechanically rename its SaaS-specific surface map. Billing, Postgres,
multi-tenant provider isolation, admin roles, and Converge API scopes are not
dbrain review categories.

## Skill Validation

Treat skill authoring as a test-driven documentation change:

1. Give a fresh subagent a realistic dbrain security-review task without the
   new skill and capture omissions, unsupported conclusions, and review-shape
   failures.
2. Initialize the skill using the installed skill-creator tooling.
3. Write the minimum guidance needed to correct observed baseline failures.
4. Validate frontmatter and `agents/openai.yaml` with the installed validator.
5. Give fresh subagents the same or equivalent tasks using the skill.
6. Check whether they resolve the runtime boundary, distinguish hypotheses from
   findings, trace and refute candidates, and expose coverage gaps.
7. Revise only for observed failure modes, then validate again.

Forward-testing is read-only. Validation agents may inspect the checkout but
must not edit source or probe external systems.

## Review Architecture

### 1. Resolve The Contract

Record the commit, branch, worktree status, date, allowed action classes, and
exclusions. Resolve the repo-local paths with:

```sh
direnv exec . ./bin/dbrain --no-debug config paths --json
```

Use isolated test directories for runtime verification. Never infer production
paths from repo-local configuration.

Read the current architecture, route-capability, maintenance, migration,
release, remote/tsnet, importer, and research-harness documents alongside
relevant recent history. Historical plans orient the review but do not prove
current controls.

### 2. Build A Surface And Control Inventory

Inventory more than HTTP routes. Each row identifies a surface, identity or
caller, untrusted inputs, guard, sink or side effect, bounds, existing evidence,
and verification status.

Surface kinds include:

- CLI commands and destructive confirmations;
- HTTP routes and representations;
- MCP tools, resources, prompts, and transports;
- filesystem reads, writes, renames, and deletions;
- SQLite source snapshots and restored databases;
- subprocesses and native parsers;
- outbound HTTP endpoints and redirect behavior;
- model/provider calls and prompt/output propagation;
- archive and restore operations;
- browser-extension actions;
- launchd and tsnet configuration surfaces;
- CI, release, Homebrew, browser-extension, and skill-distribution workflows.

### 3. Build The Threat Model

For each surface, identify assets, attacker capabilities, privilege transitions,
irreversible side effects, and trust boundaries. Primary attacker/input classes
are:

- another local user;
- a remote caller reaching an intentionally or accidentally exposed listener;
- an authenticated but untrusted browser or MCP client;
- a hostile browser extension or web origin;
- malformed or adversarial imported Notes, Safari, feed, X, YouTube, GitHub, or
  web content;
- a compromised or malicious remote server, redirect, archive, model provider,
  helper binary, workflow dependency, or restored database;
- a corrupted local database row or filesystem symlink.

The review assumes dbrain is single-corpus rather than multi-tenant. Optional
GitHub users are administrators of the same brain unless current product code
proves a narrower contract.

### 4. Investigate Required Abuse Hypotheses

The skill will require deliberate attempts to disprove at least these families:

- DB-derived path traversal or symlink escape in vault, media, trace, attachment,
  archive, restore, upload, and deletion paths.
- Apple Notes attachment paths escaping the intended Notes container.
- private, loopback, link-local, metadata, or credential-bearing outbound
  requests through links, feeds, enrichers, redirects, model endpoints, archive
  endpoints, or configurable readers.
- unbounded downloads, decompression, parsing, request batches, transcripts,
  SSE work, model calls, or retained rows.
- restore acceptance of corrupted, foreign, or malicious but SQLite-valid data.
- overly broad local filesystem permissions for private data and generated
  artifacts.
- listener exposure that relies on warnings rather than enforced authentication,
  including non-loopback web, MCP HTTP, tsnet, Funnel, and reverse-proxy modes.
- CSRF, DNS rebinding, Host trust, no-Origin requests, and browser-extension
  exceptions on mutation routes.
- OAuth state/session lifecycle, approval removal, Secure-cookie decisions, and
  service-auth replay.
- MCP bearer-token enforcement, revocation, logging, batch amplification,
  operation scope, and genuine read-only behavior.
- public-share, media, trace, error, metrics, log, OKF, Markdown, and rendered
  frontend leakage or active-content handling.
- subprocess path/argument/environment/output handling and secret resolution.
- prompt injection, model-visible data minimization, output handling, and
  cross-stage contamination.
- mutable CI dependencies, token permissions, embedded-asset drift, dependency
  vulnerabilities, release provenance, Homebrew mutation, browser-extension
  packaging, and skill-publishing integrity.

These are investigation requirements, not prewritten findings.

### 5. Verify Candidates

Maintain each candidate through these states:

1. Hypothesis.
2. Source traced.
3. Behavior observed.
4. Impact demonstrated.
5. Refutation survived.
6. Confirmed.
7. Retested after remediation.

For every candidate, write:

```text
attacker and prerequisite
  -> entry point
  -> parsing or transformation
  -> expected guards
  -> sink or side effect
  -> concrete impact
```

Then state what would make the candidate false and actively search current
middleware, storage predicates, validators, transaction constraints, build
conditions, tests, and final output encoding for that guard.

Classify the result as confirmed vulnerability, probable vulnerability,
coverage gap, defense-in-depth improvement, or false positive. Confidence is
high, moderate, low, or unknown and is independent from severity.

### 6. Use Proportionate Tooling

Start with repository-supported commands and installed tools. Expected lanes
include focused Go tests, `task lint`, `task test-ci`, `task build`, frontend
tests/build checks, dependency audits when available, secret/workflow review,
and clean embedded-asset comparison.

Scanner output creates hypotheses only. A dependency advisory must be checked
for reachability. Missing tools or blocked network access are reported as
coverage gaps unless obtaining them is separately approved.

### 7. Perform Isolated Dynamic Checks

Prefer `httptest` for precise identity, route, Origin, body-limit, redirect,
and side-effect assertions. When browser- or server-visible behavior matters,
build the checkout and start a localhost development server against synthetic
repo-local state. Confirm the server process and resolved paths before requests.

Do not enable real remote transport, Funnel, OAuth, archive, or paid-provider
traffic. Represent those boundaries with injected fakes and focused tests.

## Documentation During The Review

Write the durable report at:

```text
docs/security-reviews/2026-07-12-full-4269f53.md
```

Update the report as evidence changes, using explicit candidate states. This
preserves the investigation trail without presenting an early suspicion as a
verified defect. When a candidate is refuted, retain a compact false-positive
record with the dominating guard and evidence.

Store raw command output only under an ephemeral directory such as:

```text
/private/tmp/dbrain-security-review-2026-07-12-4269f53/
```

Only sanitized evidence belongs in the repository. Never commit raw tokens,
cookies, provider keys, personal corpus content, local Notes data, browser
profiles, private prompts, or archive credentials.

The report contains:

- boundary, commit, action classes, exclusions, and confidence;
- refreshed surface/control inventory and threat model;
- verification summary by boundary;
- candidate ledger with attack path, falsification statement, evidence, and
  verdict;
- confirmed and probable findings;
- coverage gaps and verified positive controls;
- exact commands and outcomes;
- remediation order and residual risks.

## Remediation Workflow

Only confirmed findings with an understood root cause enter implementation.
Probable findings may be fixed only when the same change is independently
justified as safe defense in depth; the report must preserve the distinction.

For each fix:

1. Select the narrowest real behavior test that reproduces the boundary
   violation.
2. Run it and verify it fails for the expected security reason.
3. Implement the minimal control at the shared seam that dominates all affected
   callers.
4. Run the focused test and verify it passes.
5. Retest the original exploit/rejection path.
6. Retest the authorized happy path and relevant sibling callers.
7. Update the candidate to retested and record exact evidence.
8. Update user-visible docs and `CHANGELOG.md` for material behavior changes.

Avoid unrelated refactoring. Schema changes require migration-history review
and representative upgrade coverage. Web changes require rendered or browser
verification where data-structure tests cannot prove the user-visible result.

## Verification And Completion Bar

After all accepted fixes:

```sh
task fmt
task lint
task test-ci
task build
```

Also run relevant frontend, browser, dependency, or artifact checks identified
by the changed boundary. Read the complete fresh output before claiming success.

The campaign is complete only when:

- the skill passes structural validation and fresh-agent forward tests;
- the surface/control inventory covers every included boundary;
- every reported vulnerability has a traced path, observed behavior, impact,
  refutation attempt, regression test, and retest evidence;
- probable findings and coverage gaps remain visibly separate;
- fixes preserve authorized behavior and local-first/import-only guarantees;
- standard repository gates pass from the final checkout;
- skipped tools, untested integrations, and residual risks are explicit;
- the final worktree diff contains no secrets, private corpus artifacts, or
  unrelated user changes.

## Deliverables

- `skills/dbrain-security-review/` with validated metadata and references.
- `docs/security-reviews/2026-07-12-full-4269f53.md`.
- Focused regression tests and minimal fixes for confirmed findings.
- Relevant architecture, route, operational, security, and changelog updates.
- Final verification evidence and an explicit residual-risk summary.
