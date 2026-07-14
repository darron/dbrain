---
name: dbrain-production-audit
description: Verify a dbrain release against the real installed production target with content-free pre/post health reports. Use after building or releasing dbrain, before and after a separately approved installation or restart, when checking for importer, OCR, transcription, archive, backup, scheduler, provenance, or upstream-parity regressions, or when comparing release health without querying SQLite or mutating production.
---

# dbrain Production Audit

Use the installed `dbrain audit` CLI as the only health-policy authority. Keep
the workflow read-only and content-free. Never substitute admin counters, MCP
stats, ad hoc SQL, logs, or model judgment for the stable audit report.

## Required workflow

Read [release-workflow.md](references/release-workflow.md) before running an
audit. Follow its gates and comparison rules exactly.

1. Record the authorized target and expected released commit. If production or
   the commit is ambiguous, stop and ask for the missing value.
2. Resolve the installed binary and its real config with `config paths --json`.
   Label the boundary as installed production only after the returned paths are
   verified. Reuse the explicit config path on every audit command and preserve
   `--root <root_dir>` when the resolved installation is self-contained.
3. Create a private evidence directory and retain raw, content-free JSON plus
   command exit codes. Never use `--include-identifiers`.
4. Before installation, run an exact-profile standard report. Require a fresh,
   complete SQLite backup check; do not create or repair a backup.
5. Report the pre-release gate. Stop for separate human approval and execution
   of installation, deployment, restart, or service changes. This skill does
   not own that authority.
6. After the operator confirms installation and restart are complete, resolve
   the installed binary and paths again. Stop if the target changed.
7. Run post-release standard and deep reports with `--expect-commit` set to the
   expected released commit. Do not widen archive limits without explicit
   approval.
8. Compare pre-standard with post-standard by exact check ID. Evaluate
   deep-only archive restore, media inventory, and seven upstream parity checks
   separately. Treat the audit's required/status/skip/error fields as
   authoritative.
9. Deliver a content-free release verdict with evidence paths, exact commands,
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
