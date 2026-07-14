---
name: dbrain-production-audit
description: Use when verifying a dbrain release against the real production target with content-free pre/post health reports, especially after building or releasing dbrain, before and after a separately approved installation or restart, or when checking importer, OCR, transcription, archive, backup, scheduler, provenance, and upstream-parity regressions without querying SQLite or mutating production.
---

# dbrain Production Audit

Use a provenance-verified, audit-capable `dbrain` CLI as the only health-policy
authority. Prefer the installed binary; use the candidate bootstrap path only
when the installed release predates `dbrain audit`. Keep the workflow read-only
and content-free. Never substitute admin counters, MCP stats, ad hoc SQL, logs,
or model judgment for the stable audit report.

## Required workflow

Read [release-workflow.md](references/release-workflow.md) before running an
audit. Follow its gates and comparison rules exactly.

1. Record the authorized target selector mode and full expected released
   commit. If production, XDG versus self-contained layout, or the commit is
   ambiguous, stop and ask for the missing value.
2. Select and verify the audit executable. Prefer the installed binary when it
   supports the audit command. If it predates auditing, use only the explicit
   candidate-bootstrap path in the workflow reference and verify that binary's
   full commit before it reads the target.
3. Resolve the real paths with the chosen binary's read-only
   `config paths --json`. Use `--config-file` for a split XDG/config-file
   target, or `--root <root_dir>` alone for a self-contained target. Never pass
   both. Label the boundary as production only after the paths are verified.
4. Create a private evidence directory and retain raw, content-free JSON plus
   command exit codes. Never use `--include-identifiers`.
5. Before installation, run an exact-profile standard report. Require a fresh,
   complete SQLite backup check; do not create or repair a backup.
6. Report the pre-release gate. Stop for separate human approval and execution
   of installation, deployment, restart, or service changes. This skill does
   not own that authority.
7. After the operator confirms installation and restart are complete, resolve
   the installed binary and paths again. Stop if the target changed.
8. Run post-release standard and deep reports with `--expect-commit` set to the
   expected released commit. Do not widen archive limits without explicit
   approval.
9. Compare pre-standard with post-standard by exact check ID. Evaluate
   deep-only archive restore, media inventory, and seven upstream parity checks
   separately. Treat the audit's required/status/skip/error fields as
   authoritative.
10. Deliver a content-free release verdict with evidence paths, exact commands,
   exit interpretation, regressions, recoveries, and remaining unknowns.

## Authority limits

- Permit read-only config/path resolution, query-only audit snapshots, bounded
  upstream inventories, archive reads, and private evidence files.
- Stop for approval before deployment, installation, restart, retry, repair,
  restore, prune, import, reprocessing, secret changes, archive creation, or
  upstream mutation.
- Do not run SQLite queries, inspect corpus content, add identifiers, change
  config, invoke sync/import workers, or turn a skipped/unknown check into a
  pass by inference.
- Do not invoke deep work through MCP or the admin API. They intentionally have
  no deep or upstream authority.

## Exit and evidence rules

Interpret exit codes as `0=pass`, `1=warn`, `2=fail`, and `3=unknown or
bootstrap failure`. A nonzero command may still have emitted a valid
`dbrain.audit.v1` report; preserve and interpret that report before classifying
the exit. If no valid report exists, record a bootstrap failure.

Keep poll health separate from arrivals. A quiet source can be healthy when
polling succeeds and arrivals are optional or quiet. Keep `pending`, `blocked`,
`terminal`, and `failed` pipeline partitions distinct. Treat missing
post-cutover provenance, a changed commit, stale backup, incomplete inventory,
or unknown metrics as the explicit audit status—not as a reason to improvise a
database check.
