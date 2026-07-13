---
name: dbrain-security-review
description: Perform evidence-driven, read-only security and supply-chain reviews of dbrain. Use for full or targeted audits of its Go CLI, SQLite and local files, Apple Notes and Safari imports, URL and media ingestion, subprocesses and model providers, web/OAuth/public shares, MCP, tsnet/Funnel, archives, browser extensions, secrets/config/logging, GitHub Actions, embedded Svelte UI, releases, Homebrew, or published skills; also use when verifying security regressions and designing remediation tests.
---

# dbrain Security Review

## Core Principle

Trace untrusted input across the current checkout to a security-relevant sink, then try to falsify the candidate with the strongest applicable guard and isolated rejection-path evidence. A source smell, missing test, warning, advisory, or scanner alert is not a confirmed vulnerability.

Preserve dbrain's local-first, import-only, append-only, raw-evidence, and read-only MCP guarantees. Hardening must not silently add upstream mutation, remote authority, data loss, or model prose as evidence.

## Safety And Authority

Default to a read-only review of the named checkout and synthetic isolated fixtures. Before inspecting runtime state, record the user-authorized action classes and exclusions. Never expand a checkout review into production XDG state, an installed/Homebrew binary, launchd jobs, live tsnet/Funnel, real secrets, browser profiles, Notes data, archives, paid providers, external probing, destructive checks, denial-of-service/high-load tests, downloads, or tool installation without explicit approval.

Keep raw output under `/private/tmp`; commit only sanitized evidence. Do not expose tokens, cookies, private corpus content, prompts, Notes data, browser data, or archive credentials.

Use these verdicts distinctly: **confirmed vulnerability**, **probable vulnerability**, **coverage gap**, **defense-in-depth improvement**, and **refuted/false positive**. Report severity and confidence separately.

## Required References

Read only the references needed for the requested boundary, but always read:

- [methodology.md](references/methodology.md) for evidence order, severity, confidence, standards, and tool lanes.
- [evidence-gates.md](references/evidence-gates.md) for candidate states, provenance, attack paths, refutation, and coverage accounting.
- [report-template.md](references/report-template.md) before recording candidates or drafting the report.

Then read the boundary-specific references:

- [dbrain-surfaces.md](references/dbrain-surfaces.md) for product surfaces and required abuse hypotheses.
- [supply-chain.md](references/supply-chain.md) for dependencies, workflows, embedded assets, releases, Homebrew, extensions, and skill publication.
- [secrets-config-logging.md](references/secrets-config-logging.md) for secret resolution, subprocesses, permissions, logs, exports, shares, and backups.

## Workflow

### 1. Resolve The Real Target

Record date, repository root, branch, exact commit, worktree status, baseline/range, review mode, and whether generated or installed artifacts are in scope. Label every artifact **checkout source**, **checkout-built**, **repo-local runtime**, **installed artifact**, **production state**, **synthetic fixture**, or **external service**.

For repo-local CLI work, prefer:

```sh
direnv exec . ./bin/dbrain --no-debug config paths --json
```

Do not infer production paths from repo-local output. If the target cannot be resolved within authority, stop that lane and record a coverage gap.

### 2. Record Scope And Action Classes

List included and excluded boundaries and permitted actions: source inspection, local commands, focused tests, builds, synthetic fixtures, localhost servers, or installed scanners. Record prohibited live, destructive, high-load, network, secret-bearing, or paid actions. Record tool availability without installing anything.

### 3. Build The Surface And Control Inventory

Refresh registrations and callers from the current checkout; historical docs are orientation only. For every included surface record: boundary, entry point, identity/caller, untrusted input, transformations, expected and observed guards, sink/side effect, resource bounds, existing evidence, files inspected, and verification status. Enumerate included-but-uninspected surfaces rather than hiding them in prose.

### 4. Build The Threat Model

For each surface identify assets, attacker/input classes, trust and privilege transitions, authority gained, irreversible effects, privacy impact, and availability/resource exposure. Treat imported content, restored rows, paths, symlinks, remote responses, redirects, model input/output, helper output, browser origins, and generated artifacts as untrusted at each downstream sink.

### 5. Trace dbrain-Specific Abuse Cases

Use the required hypotheses in [dbrain-surfaces.md](references/dbrain-surfaces.md). For each candidate write the complete edge-by-edge path:

```text
attacker and prerequisite
  -> entry point
  -> parsing or transformation
  -> expected guard
  -> sink or side effect
  -> concrete impact
```

Do not convert the required hypotheses into prewritten findings.

### 6. Verify Rejection And Containment Paths

For every candidate state a falsification condition, search for a dominating guard across middleware, shared helpers, storage predicates, validators, transaction constraints, build conditions, tests, and final encoding, and record the result. Prefer a focused real-seam rejection test with synthetic data. Also verify the authorized path so a rejection does not merely prove that all behavior is broken.

### 7. Run Proportionate Automated Checks

Start with installed repository-supported lanes and focused tests. Save exact commands, exit status, relevant output, and interpretation. Manually triage scanner and advisory output for reachability and compensating controls. Tool absence or blocked network is a coverage gap, not permission to install or a finding.

### 8. Perform Isolated Dynamic Verification

Use temporary roots, synthetic databases/files, fake services, injected clients, and `httptest`. A checkout-built localhost server is allowed only when authorized and verified to use isolated repo-local state. Do not enable real OAuth, remote transports, Funnel, archives, browser profiles, Notes, secrets, or paid providers. Avoid destructive, high-load, and external tests.

### 9. Triage, Refute, And Retest Findings

Advance candidates only through observed evidence gates. Preserve explicit refutation records. A confirmed vulnerability requires demonstrated impact and survival of the refutation attempt; otherwise use probable, gap, defense in depth, or refuted. After remediation, rerun both exploit/rejection and authorized-path tests and label the evidence **Retest verified**.

### 10. Produce The Report

Use [report-template.md](references/report-template.md). Include exact files inspected, commands and outcomes, provenance labels, mocked integrations, skipped checks, uninspected included surfaces, falsification statements, verdicts, and residual risk. Keep scanner output, missing tests, and orientation documents visibly separate from confirmed runtime evidence.

## Completion Bar

The review is complete only when:

- the target and authority boundary are explicit and every evidence item has provenance;
- every included surface is inspected or named as uninspected;
- each candidate has an attack path, falsification statement, dominating-guard search, evidence state, severity, confidence, and distinct verdict;
- confirmed findings have observed impact plus exploit/rejection-path and authorized-path evidence;
- refuted candidates retain the guard and evidence that disproved them;
- exact commands, files, outcomes, mocks, skipped lanes, tool gaps, and limitations are recorded;
- report conclusions follow the evidence gates and do not promote missing tests, scanner alerts, or hypotheses;
- raw/private evidence stays out of git and local-first/import-only guarantees remain intact.
