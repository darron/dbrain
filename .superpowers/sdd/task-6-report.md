# Task 6 Report: Scheduled Production Audits And Alerts

## Result

Implemented the disabled-by-default production-health audit scheduler under
`serve remote`. It owns post-sync fast audits and periodic standard audits,
keeps audit outcomes separate from sync outcomes, never schedules deep, and
permits only one audit at a time.

The slice adds:

- immutable, content-free UTC-daily JSONL reports under private fixed paths;
- exact-profile history and non-persisted freshness presentation;
- 90-day and 256-MiB deterministic retention;
- content-free, atomic per-profile/check alert state;
- the approved debounce, escalation, de-escalation, repeat, recovery, and
  exact six-check immediate-failure transition rules;
- a constrained exact-origin webhook with lazy typed secret resolution,
  proxy and redirects disabled, dial-time address policy, ten-second timeout,
  and a conservative 64-KiB total controlled-request/response ceiling;
- compact privacy-safe completion metrics;
- bounded startup configuration resolution and separate read-only scheduled
  audit capabilities, with no archive writer, archive reader, restore, or deep
  authority.

Configuration templates, environment documentation, command documentation,
maintenance/capability docs, README, and changelog were updated together.

## TDD Evidence

The first focused RED run failed to compile because the report store,
freshness presentation, alert transition engine, webhook, scheduler, and audit
completion metric constructors did not exist. Subsequent RED runs separately
proved missing redirect-disable behavior and missing scheduler/sync-post-run
wiring before those implementations were added.

Focused regressions then drove the private store and scheduler contracts:

- strict report validation precedes writes; generated paths are descriptor-
  relative and no-follow; malformed/trailing JSON is isolated; file modes are
  repaired; UTC rotation and exact-profile reads are deterministic;
- age and byte retention remove oldest eligible files first, propagate delete
  failures, ignore unrelated names, and retain the complete cutoff UTC day;
- post-sync fast runs only after an actual scheduled sync result, lock release,
  and status settlement; lock/overlap skips do not trigger it;
- shutdown cancels and waits for active post-sync work, and concurrent Stop
  calls share the same completion signal;
- report persistence precedes completion metrics and alert state; metric sink
  errors become a fixed sanitized audit-scheduler failure without changing the
  completed sync result.

The adversarial completion review found two Important defects. Both were
reproduced with failing local tests before correction:

- an optional non-pass persisted for display could become required and notify
  on its first required observation, bypassing debounce for checks outside the
  six-ID exception list; optional-to-required now rebases confirmation and the
  first required observation establishes the ordinary pending counter;
- the 64-KiB webhook request ceiling counted only JSON, so an oversized lazily
  resolved bearer token or configured path could exceed the outbound budget;
  the pre-network budget now conservatively includes method/framing, full
  target/path, Host, Content-Type, Authorization, Content-Length framing,
  fixed client headers, CRLFs, and JSON body. Both oversized-token and
  oversized-path tests prove zero requests reach the local server and errors
  contain no secret.

Additional RED/GREEN hardening covered complete-cutoff-day retention and
idempotent concurrent scheduler shutdown. The final read-only review returned
READY with no remaining Critical or Important findings.

## Verification

- focused audit/app/safehttp/metrics tests — pass
- focused race-enabled audit/app/safehttp/metrics tests — pass
- `task fmt` — pass
- `task lint` — pass, 0 issues
- `task test-ci` — pass (`go test -cover -race ./...`)
- `task build` — pass
- Windows compile-only audit/app/safehttp tests plus production metrics build
  — pass
- `git diff --check` — pass
- final read-only adversarial review — READY, no Critical or Important issues

## Residual Risk

The Windows report store uses NT handle-relative traversal/open/create/delete,
`OBJ_DONT_REPARSE`, `FILE_OPEN_REPARSE_POINT`, explicit reparse rejection,
append-only access, write-through creation, and atomic replacement by handle.
It was inspected and cross-compiled, but this macOS host could not execute its
Windows runtime behavior.

## Boundary

Implementation and verification used only the isolated worktree based at
`55ae77623814bab974503fad656d06561a441585`, temporary stores, local fakes, and
loopback test servers. No production database, active local database, remote
archive, external webhook/network destination, upload, restore, deployment,
or push was touched.
