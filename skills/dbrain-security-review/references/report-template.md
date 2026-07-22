# Security Review Report Template

Use this structure for full reviews; targeted reviews may collapse empty sections but must retain the candidate ledger, findings/gaps/positive controls, commands, and final assessment.

## Executive Summary

State the target, strongest verified conclusions, confirmed/probable counts, material coverage gaps, and overall confidence. Do not lead with unverified scanner totals.

## Scope And Limitations

Record date, repository root, branch, commit/baseline, worktree state, review mode, allowed action classes, included/excluded boundaries, artifact provenance labels, raw-output directory, tool/network constraints, and confidence in target resolution.

## Surface Inventory

Use a table with: boundary/surface; entry point; identity/caller; untrusted input; transformations; expected/observed guard; sink/side effect; bounds; evidence; status. Follow it with exact inspected files/artifacts and included-but-uninspected surfaces.

## Threat Model

List assets, attacker/input classes, trust/privilege transitions, irreversible effects, local-first/import-only invariants, and key assumptions that would change verdicts.

## Verification Summary

Summarize runtime seams reproduced, focused tests, static/scanner lanes manually triaged, integrations mocked/faked, checks blocked or skipped, tools unavailable, and why the evidence is proportionate.

## Candidate Ledger

For every candidate list ID, title, current candidate state, verdict, severity, confidence, boundary, provenance, falsification condition, refutation result, and next evidence. Retain compact refuted candidates.

## Confirmed And Probable Findings

Use one block per finding:

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

For **Evidence and provenance**, cite exact files/lines, commands/outcomes, artifact identity, and one of the defined provenance values. For **Attack path**, show every attacker-to-impact edge. For **Reproduction**, use synthetic data and state rejection/side effects observed. For **Regression test**, require a verified pre-fix failure plus exploit/rejection and authorized-path retests when remediation occurs.

## Coverage Gaps

List each uninspected included boundary, missing/unavailable tool, network/advisory limitation, production-only prerequisite, skipped high-risk action, or unresolved guard. State what evidence would close it. Do not assign vulnerability severity to a gap.

## Verified Positive Controls

List controls actually source traced or runtime reproduced, their scope, provenance, rejection evidence, and limits. Do not generalize one guarded caller to sibling callers without tracing dominance.

## Commands And Outcomes

Record exact command, working directory, relevant environment/boundary, exit status, concise sanitized outcome, and interpretation. Separate commands run from commands recommended. Keep raw output in the declared temporary directory.

## Remediation Order

Order accepted work by demonstrated impact, prerequisites/exposure, shared-seam leverage, regression-testability, operational risk, and dependencies. Keep defense-in-depth work separate from confirmed-finding fixes.

## Final Assessment

State confirmed and probable risk, strongest refutations/positive controls, residual uncertainty, skipped gates, whether authorized behavior and local-first/import-only guarantees were preserved, and the exact boundary to which conclusions apply.
