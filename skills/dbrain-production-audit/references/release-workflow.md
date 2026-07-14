# Release Audit Workflow

Use this checklist for an installed production release. The CLI owns check
definitions, thresholds, requiredness, privacy validation, and exit status.

## 1. Establish authority and evidence storage

Obtain the expected Git commit as 7-64 hexadecimal characters. Record what the
operator authorized: preflight only, post-install verification, or both. A
request to audit is not authority to install, restart, repair, restore, retry,
prune, import, or change configuration.

Resolve and freeze the installed binary path:

```sh
command -v dbrain
<installed-dbrain> --no-debug config paths --json
```

Do not use `./bin/dbrain`, a checkout database, or a guessed XDG path when the
target is production. From the JSON, record at least the root, config,
database, data, logs, media, vault, temp, and OKF paths. Preserve the selector
mode that produced those paths:

- for a normal split XDG installation, pass the returned config explicitly;
- when `root_dir` identifies a self-contained installation, pass both the
  returned config and `--root <root_dir>`.

Represent those frozen arguments as `<target-selectors>`:

```sh
<installed-dbrain> --no-debug <target-selectors> audit ...
```

Do not assume `--config-file` recreates a self-contained root layout: it moves
the config file, not the data layout. Reject any selector set whose subsequent
`config paths --json` differs from the recorded boundary.

Create a uniquely named evidence directory with `umask 077`, mode 0700, and
0600 report files. A private `mktemp -d` directory is acceptable. Retain:

- `pre-paths.json`
- `pre-standard.json` and its exit code
- `post-paths.json`
- `post-standard.json` and its exit code
- `post-deep.json` and its exit code
- a content-free comparison summary

Do not commit, upload, paste, or expose these files automatically. Never pass
`--include-identifiers`.

## 2. Pre-install standard gate

Run the installed production binary against the explicit config:

```sh
<installed-dbrain> --no-debug <target-selectors> \
  audit all --profile standard --json
```

Capture stdout even when the exit is nonzero. Validate `schema` is
`dbrain.audit.v1`, preserve the report, and record the process exit separately.

Require these backup facts before recommending installation:

- `durability.sqlite_backup_configuration` shows the intended production
  backup capability and requirement.
- `durability.sqlite_backup_age` is required, `pass`, complete, and fresh under
  the report's threshold.

If backup freshness is skipped, warn, fail, unknown, incomplete, or absent,
stop. Do not create an archive or weaken the requirement. A report-level fail
or unknown also blocks the release. A warning requires explicit operator
acceptance; do not silently promote it to pass.

Summarize the pre-release baseline by exact check ID, including:

- configured sources and their poll/arrival statuses;
- scheduler continuity;
- OCR, transcription, summary, hydration, and extraction partitions;
- current post-cutover provenance coverage;
- media, SQLite backup, and OKF durability.

Then stop. Ask for separate approval/execution of installation and restart.
Resume only after the operator confirms they are complete.

## 3. Re-resolve after installation

Run the newly installed `command -v dbrain` and `config paths --json` again.
Compare binary path, config path, database, data, media, vault, temp, logs, and
OKF paths with the pre-release values. Stop on any unexpected target change.
Do not assume a successful command means the same installation was audited.

## 4. Post-install standard and deep gates

Run both exact profiles with the expected released commit:

```sh
<installed-dbrain> --no-debug <target-selectors> \
  audit all --profile standard --expect-commit <commit> --json

<installed-dbrain> --no-debug <target-selectors> \
  audit all --profile deep --expect-commit <commit> --json
```

Deep has bounded read authority for remote archive/media inventories, a private
SQLite archive validation copy, local Apple Notes/Safari snapshots, fixed X and
GitHub origins, fixed YouTube playlist subprocesses, and enabled configured
feed origins. It does not import, delete, repair, restore, or mutate upstream.
Use default byte ceilings. Stop for explicit approval before any retry or limit
increase.

Require `boundary.runtime` to report `expected_commit_matched=true`. Require
the deep SQLite restore check when backup is required, and require every
configured deep upstream parity check to be complete. Missing local identities
are a fail; credential, schema, cursor, device, cap, timeout, or access
ambiguity is unknown.

## 5. Compare reports

Compare only like profiles directly: pre-standard to post-standard. Build maps
by exact check ID and report:

- IDs added or removed;
- status, requiredness, skip reason, or error-code changes;
- regressions and recoveries;
- configured-source changes;
- partition count changes for pending, current, blocked, terminal, and failed;
- backup/media/OKF durability changes;
- runtime boundary and commit evidence.

Then inspect deep-only checks as post-release acceptance evidence:

- `durability.sqlite_restore`
- `durability.media_remote_only`
- all configured `upstream.*.parity` checks

Do not compare a deep-only check with an absent standard check as a regression.
Do not infer missing rows from a quiet arrival window. Poll success proves the
poll path; arrivals describe observed changes and may legitimately be quiet.
Do not collapse terminal transcription/OCR outcomes into failures. Do not
query SQLite to explain an unknown; retain the unknown and its bounded error
code.

## 6. Exit interpretation

Interpret process exit and JSON independently:

| Exit | Meaning | Required action |
|---|---|---|
| 0 | Report pass | Continue to comparison. |
| 1 | Report warning | Preserve report; require explicit acceptance. |
| 2 | Report failure | Block release acceptance. |
| 3 with valid JSON | Report unknown | Block acceptance and report exact unknown checks. |
| 3 without valid JSON | Bootstrap/config/encoding failure | Block acceptance; report stderr category without exposing secrets. |

Never rerun automatically. A retry can change evidence and may prompt for
credentials or make upstream requests; require operator approval.

## 7. Handoff format

Report:

1. Boundary: installed binary, expected/observed commit, explicit config, and
   whether pre/post paths matched.
2. Pre gate: overall status and backup freshness evidence.
3. Post gates: standard and deep status plus exit interpretation.
4. Changes: exact check-ID regressions, recoveries, source/partition/durability
   changes, and commit match.
5. Unknowns/skips: exact IDs, error/skip codes, and why they remain unresolved.
6. Evidence: private directory and filenames, without embedding private data.
7. Authority: actions not taken and any separate approval needed next.

The verdict is `pass`, `pass with accepted warnings`, `fail`, or `unknown`.
Do not label production healthy if required checks fail or remain unknown.
