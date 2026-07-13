# Security Review Methodology

## Contents

- Standards use
- Evidence order
- Severity
- Confidence
- Verdicts
- Installed-tool lanes
- Dynamic verification

## Standards Use

Use OWASP ASVS 5.0.0, WSTG 4.2, OWASP API Security Top 10 2023, and applicable OWASP GenAI/agentic guidance to map observed controls and findings. These mappings are orientation and coverage aids, not certification, compliance, conformance, or completeness claims.

Map only after tracing the dbrain behavior. Record the exact standard identifier and why it applies; do not force local CLI or import-only behavior into a web category that obscures the actual boundary.

## Evidence Order

Prefer evidence in this order:

1. Isolated runtime reproduction at the affected boundary.
2. Focused real-seam test that reaches the actual guard and sink.
3. Complete current-source trace from entry point through guard to sink.
4. Resolved runtime artifact whose identity and configuration are proven.
5. Manually triaged scanner, advisory, or static-analysis output.
6. Documentation, plans, comments, changelog, tests that do not reach the seam, and history.

Lower-ranked evidence can orient or corroborate but does not override contradictory higher-ranked evidence. Happy-path tests do not prove rejection. A resolved artifact is evidence only for that labeled boundary; checkout source does not prove installed or production behavior.

## Severity

Assign severity from demonstrated impact and prerequisites, not category labels or scanner scores:

- **Critical:** practical compromise of the authoritative corpus, host, signing/release authority, or broadly exposed secret/remote control with minimal prerequisites and severe irreversible impact.
- **High:** substantial confidentiality, integrity, authority, or durable availability loss across an important boundary with realistic prerequisites.
- **Medium:** meaningful but bounded compromise requiring notable access, configuration, or user interaction.
- **Low:** limited impact, narrow prerequisites, or primarily local hardening value.
- **Informational:** verified observation useful to posture or maintenance without a demonstrated security weakness.

Record prerequisites, scope, reversibility, user interaction, default configuration, and whether exposure is opt-in. Do not inflate severity to compensate for uncertain evidence.

## Confidence

Confidence is independent from severity:

- **High:** runtime-reproduced or focused real-seam evidence with a complete trace and failed refutation.
- **Moderate:** strong source trace or tool observation, but one material runtime or environment fact remains unresolved.
- **Low:** plausible partial trace with multiple unresolved guards, prerequisites, or artifact assumptions.
- **Unknown:** insufficient evidence to estimate reliability.

## Verdicts

- **Confirmed vulnerability:** demonstrated impact, complete attack path, and refutation survived.
- **Probable vulnerability:** strong trace but verification is blocked by the authority boundary or a material unresolved fact.
- **Coverage gap:** an included boundary or control could not be evaluated; this is not evidence of weakness.
- **Defense-in-depth improvement:** a safer control is justified without demonstrated exploitability.
- **Refuted/false positive:** a dominating guard, unreachable path, incorrect prerequisite, or runtime evidence disproves the candidate.

## Installed-Tool Lanes

Use only relevant, already-installed lanes:

- `rg` and git history/diff for registrations, callers, sinks, guards, ownership, and artifact provenance.
- Focused Go tests and `go test -race` for real seams and concurrency-sensitive behavior.
- Repository Task targets, especially focused targets and the documented CI-like gates.
- `npm audit`, frontend tests, and frontend builds for the Svelte UI and lockfile when available without new downloads.
- `govulncheck`, `gosec`, and `staticcheck` when already installed.
- Manual secret, configuration, logging, workflow-permission, action-pin, release, and generated-artifact review.

Record tool path/version where material, command, scope, exit status, relevant output, and manual triage. Do not download rule packs, update dependency databases, run package installs, or install missing tools without approval. Absence, stale local advisory data, or blocked network is a coverage gap.

Scanner output creates a candidate. Confirm reachability, attacker control, active build tags/platform, guard dominance, and impact before assigning a vulnerability verdict.

## Dynamic Verification

Prefer temporary roots, minimal synthetic fixtures, `httptest`, fake transports, bounded streams, and subprocess stubs with controlled argv/environment. Prove both rejection and allowed behavior. Do not use real private data or increase load merely to demonstrate exhaustion; a small deterministic limit test is sufficient.
