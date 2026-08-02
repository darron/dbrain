# Homebrew Reciprocal Conflict Repair

> Superseded on 2026-08-01. Homebrew 6's per-formula trust model made the
> reciprocal declarations break clean third-party installs. The current design
> removes both declarations and relies on the explicit unlink/link procedure in
> `docs/release-build.md`.

## Problem

The moving `dbrain-test` formula declares `conflicts_with "dbrain"`, but the
stable `dbrain` formula does not declare the reciprocal conflict. Homebrew's tap
audit rejects that asymmetric relationship, so every `brew test-bot` run since
the test formula was introduced fails before formula installation tests run.

## Design

Repair the live tap once by adding
`conflicts_with "dbrain-test", because: "both install the dbrain binary"` to
`Formula/dbrain.rb`.

Make the repair durable in dbrain's stable release workflow. Its formula update
step will require the reciprocal conflict declaration and insert it immediately
before the stable formula's platform block when it is absent. This preserves
Homebrew's required component order by keeping `livecheck` before
`conflicts_with`. Existing declarations must not be duplicated. The candidate
workflow will continue treating the stable formula as immutable; it is not
permitted to modify the stable formula while publishing a test candidate.

## Verification

- Add a regression test that fails when the stable release workflow cannot
  restore the reciprocal conflict declaration.
- Run the focused release-automation tests, then `task fmt`, `task lint`, and
  `task test-ci`.
- Audit the repaired tap and push its narrowly scoped formula commit.
- Verify a fresh `brew test-bot` run completes successfully on the tap.

## Scope

No release assets, candidate formula URLs, runtime data, launchd services, or
installed Homebrew kegs are changed. The repair affects only formula metadata
and the stable formula updater that maintains it.
