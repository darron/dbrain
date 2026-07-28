# Automatic Semantic ANN Maintenance After Sync

**Status:** approved design for the stacked Phase C follow-up to PR #100

**Base:** `codex/semantic-ann-lifecycle` at
`2d7ba53ce40524c427dd808345f74968b0be6be3`

## Purpose

PR #100 establishes and evaluates the scalable segmented semantic-index
lifecycle. This follow-up turns that lifecycle into a useful, explicitly
enabled product on the first supported target: Homebrew-installed macOS arm64.

The terminal product contract is:

1. semantic retrieval remains an explicit one-time opt-in;
2. after opt-in, every sync automatically and synchronously updates semantic
   state;
3. a supported sync exits successfully only when semantic readiness is
   `ready`;
4. normal `dbrain research --semantic` uses the segmented USearch backend after
   admission;
5. no recurring manual maintenance command is required;
6. unsupported builds skip the feature without failing; and
7. every failure leaves imported evidence intact, lexical retrieval available,
   and semantic work durably resumable.

This design preserves SQLite as the authority. Embeddings, segment payloads,
root manifests, and native graphs remain derived and rebuildable state.

## Scope

### In scope

- active-generation provenance validation and normal runtime admission;
- one resumable semantic refresh orchestrator;
- synchronous post-sync refresh for every `dbrain sync` subcommand;
- first-enable full-corpus backfill through the same post-sync path;
- strict success/error and checkpoint semantics;
- database-scoped cross-process maintenance and generation locks;
- Homebrew macOS arm64 native-backend distribution;
- explicit capability diagnostics and unsupported-platform skipping;
- user-facing progress, status, repair commands, and documentation;
- installed-binary and representative-corpus acceptance tests; and
- explicit, reversible production activation after the complete stack merges.

### Out of scope

- enabling semantic retrieval automatically on upgrade or install;
- production activation as a side effect of merging;
- native ANN support for Linux, Windows, or non-arm64 macOS in the first
  release;
- a daemon, resident watcher, or background maintenance service;
- query-triggered maintenance;
- upstream write-back to imported applications;
- treating segment payloads or model answers as authoritative evidence; and
- relaxing the existing projection, embedding, catch-up, or corruption gates.

## Configuration Contract

`research.semantic.mode` remains the single activation control:

- `off`: do not construct an embedding provider, build an ANN index, or run
  semantic post-sync maintenance;
- `shadow`: maintain complete semantic state automatically and run admitted
  comparisons without changing normal lexical output; and
- `on`: maintain complete semantic state automatically and use admitted
  semantic retrieval.

There is no separate maintenance toggle. A second switch could leave semantic
retrieval enabled while silently stale, which would violate the product
contract.

Changing the mode to `off` is the rollback mechanism. It immediately returns
queries and sync to lexical-only operation. Existing embeddings and derived
cache files may remain for later verified reuse; disabling does not delete
authoritative evidence or require a rebuild when the same valid profile is
re-enabled.

## Backend Capability Contract

Runtime capability is explicit and has three states:

| State | Meaning | Enabled sync behavior |
| --- | --- | --- |
| `unsupported` | This binary was built without the native backend for its platform. | Report a non-error skip and allow sync to succeed. |
| `supported_ready` | The binary contains the supported backend and its startup self-check passes. | Semantic maintenance is mandatory and must end `ready`. |
| `supported_broken` | The binary claims native support but initialization, ABI/version validation, or the backend self-check fails. | Return an error; do not silently downgrade. |

The first supported artifact is the Homebrew macOS arm64 binary. It statically
links the pinned, checksum-verified USearch v2.26.0 C implementation used by the
representative-corpus evaluation. Static linking keeps the supported target a
single binary and removes runtime dylib search-path failure as an operational
dependency. The compiled backend version remains visible in status and
diagnostics.

CGO-free release builds remain supported. They advertise `unsupported`, compile
without USearch, skip semantic maintenance even if the copied configuration
contains `shadow` or `on`, and preserve lexical behavior. They do not pretend
that semantic readiness is `ready`.

## Post-Sync Data Flow

Every `dbrain sync` subcommand uses one centralized post-sync integration
point. The hook runs even when the source sync reports no new rows, because it
may need to resume semantic work left by an earlier failed or cancelled sync.
The current command family exposes `sync all`; the integration belongs to the
command-family boundary so every future registered sync subcommand inherits the
same contract rather than opting in independently.

The sequence is:

1. run the source-specific sync;
2. commit all authoritative import changes;
3. close source write transactions and release shared maintenance leases;
4. resolve semantic configuration and backend capability;
5. skip when mode is `off` or capability is `unsupported`;
6. resume or create one semantic refresh run for the configured profile;
7. process projection work through the run watermark;
8. process due embeddings in bounded provider and persistence batches;
9. flush exact L0 before steady-state headroom would be exceeded;
10. perform bounded compaction until no overdue compaction debt remains;
11. verify counters, generation metadata, manifests, membership, checksums, and
    cache payloads;
12. calculate readiness from one authoritative snapshot; and
13. exit zero only when the supported enabled state is `ready`.

If source sync itself fails, its error is returned and post-sync semantic
maintenance does not begin. Any partial authoritative work the source sync
intentionally committed remains governed by that source's existing semantics
and becomes semantic work on the next successful sync.

Research, MCP, web, and query paths never invoke refresh.

## Initial Backfill

The first successful sync after changing semantic mode from `off` to `shadow`
or `on` performs the entire initial projection, embedding, and index build
before it returns.

The initial backfill:

- has no short total-duration default;
- may legitimately run for hours;
- emits bounded progress at least every five seconds;
- remains context-cancellable;
- stores every completed batch and stage checkpoint before reporting progress;
- resumes without repeating committed batches;
- incrementally publishes immutable segment payloads while readiness remains
  ineligible; and
- admits the first root only after the complete profile passes final
  verification.

Until that root is admitted, semantic queries fail open to the unchanged
lexical path. A cancelled or failed first backfill makes the sync command exit
non-zero. The next successful source sync automatically resumes it.

## Refresh Run And Checkpoint Model

The orchestrator stores a durable refresh-run row keyed by database and profile.
At most one resumable run for the same profile may be active.

Each run records:

- opaque run ID;
- profile ID and purge epoch;
- fixed monotonic projection-work watermark;
- current stage;
- stage-specific resume cursor or checkpoint identity;
- aggregate bounded counters;
- current generation/root identity when applicable;
- created, updated, and last-progress timestamps;
- terminal state;
- stable error code and concise error text; and
- final readiness state.

The authoritative stage tables remain the source of item-level progress.
Refresh-run state coordinates the stages; it does not duplicate an array of
parents, chunks, vectors, or segments.

If configuration selects a different embedding profile, the old run cannot be
resumed into the new profile. It becomes `superseded`, and the next sync starts
a new run while preserving the old profile's derived rows for explicit future
cleanup.

The watermark prevents continuous ingestion from starving one run. Work dirtied
above the watermark belongs to the next run. Because a supported enabled sync
must finish `ready`, the orchestrator repeats with a new watermark before
returning if new committed debt appeared during refresh.

## Refresh Stages

### Projection

Projection consumes the existing durable dirty-parent queue through the fixed
watermark. It preserves current chunker and empty-parent semantics. A parent
changed again during processing fails the old revision's final validation and
remains dirty for the next watermark.

### Embedding

Embedding uses the configured profile and existing resumable batch APIs.
Provider requests and persistence batches remain capped at 5,000 rows.
Completed vectors are never regenerated merely because a later stage failed.

Retryable provider errors use bounded exponential backoff. Three consecutive
retryable batch failures trip the circuit breaker and fail the sync. A
successful batch resets the consecutive-failure counter. Configuration,
provenance, dimensions, authentication, and terminal provider errors fail
immediately.

### Flush

During steady-state maintenance, exact L0 has a 5,000-vector ready target and a
10,000-vector absolute catch-up ceiling. Before an embedding persistence batch
would exceed the remaining 10,000-row headroom, refresh flushes at least 5,000
current L0 rows. If that flush cannot finish, refresh fails before making the
provider call.

Initial backfill may construct multiple immutable segments while semantic
readiness remains ineligible. It does not expose an incomplete root as
searchable.

### Compaction

Refresh repeatedly runs the existing bounded compaction planner/executor until
no overdue same-class or tombstone compaction is required for `ready`, or until
an operation fails. Each physical compaction remains capped at 200,000 live
vectors and publishes immutable replacements before activation.

### Verification And Admission

Final verification proves:

- the embedding profile and backend provenance;
- dimensions, distance metric, purge epoch, and source snapshot revision;
- generation state and active-root identity;
- root and segment manifest checksums;
- active membership uniqueness and counts;
- indexed, L0, and tombstone aggregates;
- cache payload presence and checksum health;
- projection and embedding counters; and
- all normal readiness invariants.

The runtime readiness path uses the same proven generation facts. It no longer
classifies a root created by the supported lifecycle as unproven merely because
ordinary admission omitted manifest and generation validation.

## Success And Error Semantics

For a `supported_ready` binary with semantic mode `shadow` or `on`, sync exits
zero only when the final readiness state is `ready`.

The following conditions return non-zero:

- backend capability changes to `supported_broken`;
- provider circuit breaker opens or a terminal provider error occurs;
- projection or embedding debt remains;
- classified terminal blocks exceed existing policy;
- exact L0 exceeds the ready target;
- tombstones or segment fanout exceed the ready gates;
- required compaction cannot finish;
- root publication, activation, verification, or admission fails;
- a lock cannot be acquired within its operation bound;
- the command is cancelled; or
- final readiness is any state other than `ready`.

A semantic failure never rolls back successfully committed imported data.
Instead it:

- preserves the durable run and stage checkpoints;
- returns a typed error with stage code, run ID, checkpoint, readiness state,
  cause, and remaining aggregate debt;
- keeps semantic retrieval failed open where state is ineligible; and
- leaves lexical retrieval available.

The sync command does not report a false success merely because import
succeeded.

## Query Consistency During Refresh

During incremental maintenance, queries may continue using the previous
admitted immutable root.

- Dirty parents are excluded immediately from segment candidates, exact L0,
  hydration, and final results.
- New or dirty evidence remains available lexically.
- Publication never mutates a loaded segment.
- Root activation is atomic.
- A query pins one admitted generation through candidate search, exact SQLite
  validation, hydration, and final evidence construction.
- If the pinned generation becomes invalid or cannot be proven, only the
  semantic lane fails; lexical ordering remains the same as semantic `off`.

During the first backfill there is no admitted semantic root, so queries remain
lexical-only until sync reaches `ready`.

## Cross-Process Concurrency

`internal/runlock` provides two database-scoped lock families under the
semantic cache root:

- `maintenance`: shared by authoritative corpus writers and exclusive for
  projection, embedding persistence, flush, compaction, repair, garbage
  collection, and purge;
- `generation`: shared by admitted queries and exclusive for root activation or
  segment/root deletion.

Lock order is always maintenance before generation. Lock upgrade is prohibited.

Source sync holds shared maintenance only around authoritative write
transactions. It releases those locks before invoking refresh, which acquires
exclusive maintenance one bounded unit at a time. ANN publication takes
exclusive generation only after exclusive maintenance. Queries take shared
generation and hold it through final validation.

Acquisition is context-cancellable. Cross-process writer-preference intent
prevents continuous sync writes from starving refresh and continuous queries
from starving root activation. Process exit releases OS locks; abandoned intent
is recovered only after proving the owner lease is no longer held.

Every projection, embedding, and ANN publication unit records the purge epoch
under which it began and refuses final commit if the epoch changes.

## Operator Experience

Routine operation requires only:

1. explicitly set semantic mode to `shadow` or `on`; and
2. run the same sync commands already used today.

Sync output streams compact stage progress at least every five seconds. Final
human output includes:

- backend capability and version;
- refresh run ID;
- stage counts and timing;
- profile and active generation;
- indexed, L0, tombstone, and segment counts;
- final readiness; and
- a checkpoint and stable error code on failure.

JSON and JSONL output remain bounded. They never include arrays proportional to
corpus size or elapsed time.

`dbrain semantic status` reports configuration, capability, active/resumable
run, current stage, last progress, remaining debt, root health, readiness, and
what the next sync will resume.

Manual `semantic refresh`, index, verify, and repair commands remain available
for diagnostics and emergency recovery. They are not required for normal
operation and use the same orchestrator and locks as automatic post-sync work.

## Native Distribution

The first native release target is Homebrew macOS arm64.

The release pipeline:

1. fetches the pinned USearch source/archive by immutable version and checksum;
2. builds the static C library for macOS arm64;
3. links it into the tagged `dbrain` binary;
4. runs the backend initialization/self-search test;
5. runs the tagged Go package tests;
6. verifies the final binary has no external USearch dylib dependency;
7. publishes the binary through the existing Homebrew release path; and
8. installs and executes a bottle smoke test on a clean macOS arm64 runner.

Other release targets keep the CGO-free implementation and advertise
`unsupported`. Their sync and lexical behavior must not regress.

## Acceptance Gates

The full stack is useful only when the installed Homebrew macOS arm64 binary
passes all of these gates against the representative corpus:

| Gate | Required result |
| --- | --- |
| Exact top-1 agreement | 100% on the fixed evaluation set |
| Mean recall@10 | at least 0.95 |
| Warm semantic-query p95 | at most 2.0 seconds |
| Cold root open | at most 2.5 seconds |
| Peak process RSS during open/query evaluation | at most 5 GiB |
| Derived semantic cache | at most 1.25 GiB |
| SQLite integrity | `ok` |
| Final readiness | `ready` |
| Pending/retryable failures | zero |
| Terminal blocks | within the existing 99.9% ready / 0.1% blocked policy |
| Incremental sync | no duplicate completed work; successful run ends `ready` |
| Progress | at least one bounded update every five seconds during active work |
| Runtime evidence | normal installed command reports `backend=usearch` |

Initial backfill has no fixed wall-clock gate because local provider throughput
varies. It must remain cancellable, make durable forward progress, and resume
without repeating committed batches.

A successful request that silently used lexical or exact fallback does not
pass native-backend acceptance.

## Test Strategy

Every stacked PR uses test-driven development and adds the narrowest regression
that fails on the previous stack head.

Required coverage includes:

- capability state and build-tag behavior;
- active-generation provenance admission;
- refresh watermark, checkpoint, stage transition, and profile supersession;
- provider retry/circuit-breaker behavior;
- initial backfill and incremental refresh;
- L0 headroom flush ordering;
- compaction-to-ready behavior;
- strict sync exit status and typed error output;
- every registered `sync` subcommand invoking the centralized hook;
- unchanged sync resuming earlier semantic debt;
- cancellation and automatic next-sync resume;
- subprocess query/sync/activation lock behavior;
- corrupt root, segment, checksum, membership, and counter failures;
- CGO-free Linux, Windows, and macOS builds skipping cleanly;
- supported macOS arm64 backend self-check failure;
- Homebrew bottle install and execution;
- full-corpus exact/ANN quality and resource measurements; and
- an installed-binary end-to-end case that enables semantic mode, runs sync to
  `ready`, then proves normal research used USearch.

The standard code gates remain:

```text
task fmt
task lint
task test-ci
task build
```

Tagged USearch tests, release-matrix builds, Homebrew smoke tests, and the
representative-corpus evaluation are additional gates for the applicable PRs.

## Stacked Delivery

PR #100 remains unchanged and mergeable as the evaluated lifecycle foundation.
The follow-up is split into these reviewable stacks:

1. **Runtime admission and capability**
   - prove generation/root provenance in ordinary readiness;
   - expose explicit backend capability;
   - make a valid evaluated root usable by normal tagged research.
2. **Resumable refresh**
   - durable run/checkpoint model;
   - projection, embedding, flush, compaction, verification, and final
     readiness orchestration;
   - progress and typed errors.
3. **Universal sync integration**
   - centralized post-sync hook for every sync subcommand;
   - strict ready-or-error behavior;
   - first-enable full backfill and automatic resume.
4. **Cross-process concurrency**
   - maintenance/generation locks, writer preference, epoch fences, and
     subprocess tests.
5. **macOS arm64 distribution**
   - static native build, capability self-check, Homebrew packaging, and
     unsupported-target behavior.
6. **Installed full-corpus acceptance**
   - first backfill, incremental sync, interruption/resume, corruption,
     concurrency, quality, latency, RSS, disk, rollback, and final runbook.

Each intermediate PR remains explicit-off by default and fails open to lexical
retrieval when its slice is incomplete. The complete stack—not an intermediate
PR—is the user-useful release.

## Implementation Status

As of 2026-07-28, only stacked PR 1, **Runtime admission and capability**, is
implemented and representative-corpus admitted on
`codex/semantic-ann-automatic-sync`. The verified implementation at
`67e690fe0d335174f1824ffaee994585122d46a6`:

- reports one explicit native-backend capability decision through both
  `semantic status` and normal research admission;
- admits a provenance-valid segmented USearch `2.26.0` generation in the
  tagged macOS arm64 development build;
- binds the native root to the canonical descriptor reconstructed from the
  authoritative SQLite generation and bounded active-segment catalog;
- loads segment payloads once with checksum verification, cancellation checks,
  and cleanup of partially opened native indexes;
- validates runtime-equivalent native artifacts before status reports them
  searchable, rejects native vector/member cardinality drift, and returns no
  partial semantic hits after cancellation;
- keeps native-root failures path-free in research output and traces while
  propagating caller cancellation and deadlines as errors;
- preserves unsupported CGO-free operation and lexical retrieval without a
  USearch dependency; and
- opens the normal research store read-only, preserving the representative
  database bytes during runtime admission.

This is not completion of the automatic-sync design. Stacked PRs 2–6 remain
pending, including resumable refresh orchestration, universal synchronous
post-sync integration, cross-process locks, static Homebrew distribution, and
installed full-corpus acceptance. No production root was built or activated by
this runtime-admission work.

## Production Rollout And Rollback

Merging the stack does not activate production.

After all acceptance gates pass, production rollout is:

1. install the supported Homebrew macOS arm64 binary;
2. confirm capability is `supported_ready`;
3. set semantic mode to `shadow`;
4. run the normal sync command and require `ready`;
5. observe shadow quality and resource gates;
6. change mode to `on`;
7. run normal research and prove USearch participation; and
8. verify the next incremental sync returns `ready`.

Rollback sets mode to `off`. No database restore, cache deletion, or manual
index mutation is required. Lexical retrieval remains authoritative throughout.
