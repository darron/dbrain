# Changelog

This document tracks significant changes to `dbrain`. Dates use the local
development date for the change set.

## Recent Improvements

### Media download ceiling (2026-08-24)

- **Large media support**: Raised the default media response limit from 256 MiB
  to 4 GiB so large video assets can be downloaded and enriched again. The
  existing explicit `MaxBytes` option remains available to callers that need a
  narrower bound.

### Database pool diagnostics (2026-08-15)

- **Request-level contention evidence**: Web and MCP access logs now include
  the live database pool size, connection usage, cumulative wait count, and
  cumulative wait duration. This distinguishes pool head-of-line blocking
  from slow queries or process-wide scheduling problems during production
  sync observation.
- **Sync and intake diagnostics**: Deferred link admission logs report the
  dedicated admission pool, scheduled sync logs report their separate pool at
  run completion, and remote serving emits a warning when the process
  heartbeat is delayed beyond two seconds.

### Deferred capture fallback and follow-up hardening (2026-08-15)

- **Normal enrichment ownership after capture retries**: A valid deferred
  capture that exhausts its bounded intake attempts is preserved as an ordinary
  source, allowing the existing source-stage retry and enrichment policy to
  handle it. This fallback intentionally loses feed subscription/import
  semantics; re-saving the URL retries normal link handling. Residual parked
  captures expose their failure kind through `dbrain link capture dead-letters`
  and can be explicitly reopened with `dbrain link capture requeue`.
- **Contention follow-ups**: Admission timeout coverage now tests the actual
  SQLite busy-timeout mechanism; direct web and HTTP-based MCP entry points use
  the shared four-connection pool; bypass access-log entries say
  `auth="bypass"`; and pending duplicate saves preserve their current attempt
  budget.
- **Deferred timing contract**: Documentation names the next `sync all` and the
  configured scheduler interval rather than promising an hourly run. The
  process-wide starvation channel remains outside this change.

### Deferred link capture during sync contention (2026-08-14)

- **Fast extension admission**: The browser link saver now durably captures
  validated URLs and returns `202 Accepted` before feed discovery or source
  enrichment. A restart-safe worker drains captures with idempotent retry and
  preserves the authoritative semantic lease for source writes.
- **Interactive contention visibility**: Remote web/MCP stores use separate
  four-connection pools with per-connection SQLite pragmas; lease wait, queue
  wait, and web/MCP request duration are logged without raw URLs. The residual
  process-wide starvation channel remains explicitly measurable rather than
  claimed fixed.

### Lazy semantic runtime root reuse (2026-08-14)

- **Truthful cold and warm diagnostics**: Runtime construction now reads
  readiness under its 250 ms budget without opening native artifacts, and
  `generation_busy` now means only shared-generation admission contention.
  Cold callers wait up to five seconds for native import and report
  `root_load_timeout` while a detached import may still warm the process-local
  cache; artifact and query-time readiness failures report
  `native_root_artifacts_unavailable` and `runtime_readiness_unavailable`.
- **Safe lexical fallback and root lifetime**: Every unavailable semantic lane
  preserves lexical evidence. A cold import retains its acquired generation
  lease during native import, validation, and the pre-publication disposition
  decision; successful release is required before publication. Roots rejected
  before pending-cache admission close before release; pending roots that fail
  release or publication checks close afterward. Refresh/GC may wait during
  that guard-held portion; the five-second caller wait does not preempt a
  native call, and reader grace remains defense in depth. Semantic `used` still
  requires SQLite validation and exact reranking, not merely a warmed native
  root.
- **Lifecycle coverage**: Web trace comparison now reuses the server-owned
  semantic runtime, and runtime shutdown drains active builds before closing
  cached roots.

### Metrics JSONL rotation (2026-08-10)

- **Bounded local metrics**: Added size-based rotation for the configured
  `metrics.jsonl` path with a 32 MiB default, configurable retention of up to
  128 numeric backups, explicit zero-value disable behavior, and reopen-time
  repair of an already oversized active file. A single oversized event remains
  intact.
- **Safe concurrent reads/writes**: Writers and bounded readers share the
  existing cross-platform run lock; active data files are opened and closed
  per operation so rollover remains safe on Windows. Readers consume the
  active file plus canonical regular backups without following sibling
  symlinks or scanning arbitrary names.

### MCP 2026-07-28 compatibility (2026-08-10)

- **Stateless modern protocol path**: Added per-request protocol metadata,
  `server/discover`, result envelopes/cache hints, and response server identity
  while preserving the markerless legacy initialize and batch path.
- **Transport validation**: Modern stdio and Streamable HTTP reject malformed
  message shapes; HTTP validates mirrored version/method/name headers, Base64
  sentinel names, notification `202` responses, JSON-RPC `404` method errors,
  and `405` unsupported verbs without introducing sessions or SSE.

### Mastodon account authorization (2026-08-09)

- **Instance-scoped read-only OAuth**: Added canonical per-account Mastodon
  origins, typed secret references, PKCE login, verified-account status, and
  revoke-first logout.

### Mastodon bookmark import (2026-08-09)

- **Direct bookmark parity**: Added account/origin-scoped Mastodon bookmark
  import and `sync all` integration with verified identity, opaque Link cursor
  checkpoints, partial-page resume, bounded status/media payloads, typed
  unavailable counters, source discovery, and append-only local behavior.
- **Generic media pipeline**: Mastodon image, video/GIFV, and audio attachments
  now use the existing media download, retry, and content-addressed vault paths
  without sending bearer tokens to CDN origins; image OCR and video/GIFV
  transcription use the existing enrichment workers.
- **Schema and CLI**: Added migration-backed `mastodon_sync_state`, the
  `dbrain import mastodon bookmarks` command, scheduler settings, and JSON/
  progress metrics. An authenticated Hachyderm smoke test remains operator
  dependent and is not claimed here.
- **Retry and safety hardening**: Completed runs retry a bounded set of older
  Mastodon media failures; migration repair restores the account/origin and
  verified-identity constraints; redirect-added credential query parameters,
  expanded raw/error credential variants, and non-RFC timestamps are rejected
  or redacted; API-error, rate-limit, and retry counters are preserved in
  direct and sync summaries.
- **Explicit blocked-media recovery**: Production media validation now
  registers JPEG and PNG decoders alongside GIF and WebP, and explicit
  Mastodon import or sync `--force` runs retry terminal blocked media through
  the same exact-origin, credential-free download policy. Ordinary imports
  continue to leave terminal media blocked.
- **Shared media lifecycle**: Social-only syncs now order shared transcription
  and OCR after Bluesky and Mastodon imports; changed ordered media clears all
  canonical, compatibility, and search projections; archive/prune remains
  source-neutral for every linked media owner while social owners still gate
  OCR/transcription completeness. Explicit pruned-media repair uses the shared
  social source predicate and reconstructs the exact Mastodon media origin
  without forwarding API credentials; shared assets prefer a selected Mastodon
  owner before reconstructing that policy.

### Reusable Bluesky bookmark import (2026-08-08)

- **Profile-backed bookmark ingestion**: Added an opt-in `dbrain import bluesky
  bookmarks` path that reads `BSKY_STORAGE` from a read-only Chrome Local
  Storage LevelDB snapshot, follows the authenticated bookmark cursor chain,
  refreshes access tokens only in memory, and materializes idempotent
  `bsky_bookmark` items with rendered notes.
- **Sync and installer integration**: Added the independent Bluesky stage,
  shared and scheduled configuration, bounded smoke-run limits, dedicated API
  timeout, progress/metrics output, and documentation. Blocked and not-found
  post views remain explicit skip categories.

### Reachability-based semantic garbage collection (2026-08-05)

- **Safe cache and catalog reclamation**: Added dry-run-first `dbrain semantic
  gc`, retaining active, resumable, recent, and rollback roots while pruning
  only segments unreachable from every retained generation. Apply commits
  SQLite catalog and membership deletion before unlinking immutable cache
  directories and also sweeps aged filesystem-only artifacts.
- **Explicit physical database compaction**: Optional `--vacuum` is gated behind
  `--apply`, checkpoints the WAL, reports writer contention with recovery
  guidance, and keeps the archive, disk-headroom, and daemon-stop requirements
  explicit.

### Bounded native semantic admission (2026-08-05)

- **Research stays lexical while generation admission is busy**: A contended
  shared-generation admission reports `generation_busy` and preserves lexical
  evidence exactly. Native root loading now has the separate runtime behavior
  documented above.

### Semantic sync progress bars (2026-08-05)

- **Visible semantic progress**: Interactive `sync all` now renders projection,
  embedding, flush, compaction, verification, and readiness as in-place bars
  with stage-local counts, percentage, elapsed time, and an approximate ETA
  once measurable throughput exists. Redirected and scheduled output stays
  bounded, and failed or cancelled refreshes no longer look successfully
  completed.

### Semantic audit and admin visibility (2026-08-05)

- **Bounded semantic audit evidence**: Standard reports now use
  `dbrain.audit.v2` to expose semantic retrieval readiness, the latest attached
  refresh, and fixed stage activity without including paths, raw identifiers,
  or unbounded metric data. The authenticated System page renders the same
  semantic section; stored `dbrain.audit.v1` reports remain supported as
  explicitly legacy reports.

### Unified sync and semantic refresh observability (2026-08-04)

- **One truthful sync boundary**: `sync all` now keeps its lock, progress UI,
  metrics run, completion timestamp, and duration open through configured
  semantic refresh work instead of reporting the source pipeline as finished
  early.
- **First-class semantic stages**: Projection, embedding, flush, compaction,
  verification, and readiness now have bounded progress output, aggregate stage
  timings and counters, JSON lifecycle data, metrics events, and rows in the
  final sync summary. Scheduled syncs use the same lifecycle without dumping
  raw semantic checkpoints and identifiers into logs.

### Stable feed item refreshes (2026-08-04)

- **Existing feed entries keep their identity and tags**: Reprocessing a changed
  feed now preserves each materialized item's original saved time and generated
  user tags while merging configured feed-level tags. Unchanged entries no
  longer appear newly modified or trigger repeated LLM categorization merely
  because another entry changed the feed response.

### Semantic refresh root continuity (2026-08-04)

- **Incremental updates preserve the active ANN root**: Projection-dirty writes
  no longer mark a valid segmented generation stale while leaving its profile
  pointer behind. Ordinary chunk replacement and projection application now
  preserve that root as well: changed vectors continue through the exact L0
  tail, while deleted or superseded native members remain tombstones until the
  normal bounded flush and compaction thresholds are reached.
- **Candidate validation remains fail-safe**: Query-time joins filter stale and
  deleted immutable members; dirty parents suppress every sibling candidate
  until projection is current, and text-hash corruption still fails the entire
  semantic lane with its typed corruption error.
- **Existing dangling roots recover safely**: A resumed refresh can repair the
  legacy stale-pointer state only after proving the SQLite segment catalog and
  membership counts and verifying the immutable native root on disk. Missing or
  inconsistent roots still fail closed instead of being activated blindly.

### Slack scheduled-sync failure notifications (2026-08-04)

- **Typed-secret Slack delivery**: Scheduled hard-failure incidents can now
  fan out to Slack Incoming Webhooks using a `webhook_url_ref` backed by
  `env:`, `op://`, or `keychain://`, alongside the existing Buzz provider.
  Slack delivery accepts only Slack's official webhook hosts, records durable
  per-provider outbox state, and exposes `dbrain notify test slack` for a
  global-off onboarding test before explicit production activation.

### Typed scheduled-sync failure notifications (2026-08-03)

- **Typed, flap-safe incidents**: The still-running `serve remote` scheduler can
  now turn settled hard failures into per-error incidents, suppress identical
  recurrences for configurable `notifications.repeat_after` intervals, remind
  with accumulated counts, and consolidate recovery without rapid flap chatter.
- **Durable Buzz/Nostr delivery**: A dedicated-key Buzz provider authenticates
  with NIP-42 and publishes signed kind-9 channel events after durable
  per-provider state is recorded. New `dbrain notify test buzz` and `dbrain
  notify status` commands support pre-enable delivery testing and redacted
  operational inspection.

### Idempotent semantic segment recovery (2026-08-02)

- **Stale ANN segments can be reused safely**: Semantic refresh now treats an
  identical content-addressed segment publication as idempotent after corpus
  changes invalidate the active generation. Stored membership is still proved
  exactly before the segment can be attached to a new generation, preventing
  both uniqueness failures during recovery and silent immutable-member drift.

### Stable OKF concept generation timestamps (2026-08-02)

- **Data-derived `generated.at` values**: OKF 0.2 item and source concepts now
  retain their meaningful content timestamps, while derived entity and topic
  concepts use the newest timestamp among their referenced evidence. Hourly
  exports no longer rewrite unchanged concept metadata with the export clock;
  bundle-level `exported_at` continues to describe the export run itself.

### Lean durable documentation (2026-08-02)

- **Current contracts over implementation mirrors**: Removed completed plans,
  stale specs, and duplicate implementation narratives while retaining user and
  operator guides, architecture and trust-boundary contracts, security evidence,
  durable decision records, eval guidance, extension docs, and reusable skills.
- **Anti-regrowth policy**: Repository guidance now keeps temporary plans in
  issues, pull requests, or disposable workspaces and removes them once code and
  tests carry the behavior.

### Open Knowledge Format 0.2 exports (2026-08-02)

- **Current OKF metadata**: New bundles declare OKF 0.2 at the root index and
  use `generated` and `sources` frontmatter instead of the superseded v0.1
  `timestamp` field and body citation lists. dbrain emits only provenance it
  can substantiate and does not invent verification or attestation claims.
- **Legacy bundle compatibility**: Inspection and validation continue to accept
  dbrain OKF 0.1 manifests while newly generated bundles target 0.2.

### Public chat share deletion (2026-08-02)

- **Owner-scoped, idempotent deletion**: Added `DELETE /api/chat/shares/{slug}`
  behind the existing local/authenticated owner boundary and shared Origin
  guard. Missing and foreign-owner shares both return an empty `204`, avoiding
  an existence oracle; the public `/share/{slug}` surface remains read-only.
- **Deletion controls in Shares**: Share cards now require confirmation, show
  pending and failure states without discarding the card, disappear after a
  successful delete, and clear stale per-chat-turn share links for the removed
  slug.

### Faster X media transcription candidate selection (2026-08-02)

- **Item-first runnable-media lookup**: X media transcription candidate
  selection now resolves each item's media links before loading its video
  assets, avoiding repeated scans of the global media download-retry index when
  the transcription backlog is empty. Non-force selection also evaluates the
  shared pending predicate only once while preserving retry, force, and pruned
  media semantics.

### Faster X photo OCR candidate selection (2026-08-02)

- **Item-first runnable-media lookup**: X photo OCR candidate selection now
  resolves each item's media links before loading its photo assets, avoiding a
  repeated scan of the global media download-retry index when the OCR backlog
  is empty. Non-force selection also evaluates the shared pending predicate
  only once, while force mode retains all runnable unpruned photos.

### Faster native release verification (2026-08-01)

- **Scoped native race coverage**: macOS arm64 release and candidate builds still
  run the complete `usearch`-tagged test suite, then apply race instrumentation
  only to packages whose compiled sources change under the `usearch` build tag.
  Release policy tests keep that package list synchronized with build-constrained
  source files, avoiding a second full-repository race run without weakening the
  tagged compatibility gate.
### Homebrew 6 tap-trust compatibility (2026-08-01)

- **Public installs no longer load an untrusted sibling formula**: Stable and
  test formulae no longer declare cross-formula `conflicts_with` metadata.
  Homebrew 6 trusts only the fully qualified formula requested from a
  third-party tap, so loading the sibling formula during dependency resolution
  prevented a clean `brew install darron/tap/dbrain`.
- **Explicit switching remains the safety boundary**: Stable and test releases
  still install the same `dbrain` executable. The documented `brew unlink` and
  `brew link` sequence remains required when moving between their separate
  kegs, and release automation now removes legacy conflict metadata rather than
  restoring it.

### Faster current-schema test databases (2026-08-01)

- **Checkpointed SQLite fixtures**: Tests that need an isolated current-schema
  store now clone one process-local database template instead of replaying all
  schema migrations for every temporary database. Explicit empty-database and
  historical-schema migration tests still exercise the real migration path;
  the clean CI gate retains race detection and coverage.
- **Scaled semantic lifecycle coverage**: The real-SQLite two-flush recovery
  test now injects smaller test-only segment boundaries while preserving
  preflush ordering, activation, native verification failure, and idempotent
  resume assertions. Production remains fixed at 5,000 vectors per segment and
  a 10,000-vector hard L0 limit.

### Native semantic packaging for macOS arm64 (2026-07-28)

- **Self-contained Homebrew native backend**: macOS arm64 release and candidate
  archives now compile checksum-pinned USearch v2.26.0 with a macOS 12.0
  deployment target and statically link it into the tagged CGO binary. Release
  verification rejects an external USearch dynamic-library dependency and
  exercises the artifact's real native capability before publication.
- **Explicit cross-platform boundary**: Darwin amd64, Linux amd64/arm64, and
  Windows amd64 artifacts remain CGO-free, omit the native build tag, and
  explicitly report semantic native state as unsupported without failing sync.
  Release archives and Homebrew formulae include the USearch Apache-2.0 license
  and third-party notices.
- **Installed-artifact gate**: Stable and candidate tap updates are followed by
  a clean macOS arm64 `brew install`/`brew test` and installed-binary capability
  check, so a release cannot pass solely because a developer checkout can find
  a local native library.

### Cross-process semantic refresh safety (2026-07-28)

- **Database-scoped semantic leases**: Refresh projection, embedding, flush,
  compaction, verification, and readiness units now run under crash-released
  cross-process maintenance leases. Generation publication additionally takes
  the exclusive generation lease for the full flush or compaction unit,
  excluding admitted queries while immutable roots are built, published, and
  activated. Exclusive maintenance is what serializes competing refresh
  processes.
- **Purge-epoch-fenced projection commits**: Both ordinary and giant projection
  commits carry the refresh run's pinned purge epoch. Giant staging persists
  that epoch in every durable row and rejects stale resume or promotion after a
  purge. Migration 28 adds and repairs the durable epoch column, automatically
  discarding unverifiable legacy or epoch-mismatched derived staging so refresh
  can rebuild it without manual cleanup.
- **Authoritative-write and query leases**: Corpus transactions that can dirty
  retrieval projection now hold shared maintenance for their full
  commit/rollback lifetime. Semantic queries hold shared generation while
  admitting a native root and again after query embedding through SQLite
  validation, L0 merge, hydration, and evidence construction. Writer-intent
  ordering prevents later readers or source writers from barging ahead of
  queued refresh/activation, while process exit releases held leases and leaves
  no live writer intent.

### Automatic semantic refresh after sync (2026-07-28)

- **Automatic semantic maintenance**: Successful CLI `sync all` and scheduled
  sync runs now synchronously run the durable semantic refresh path when
  `research.semantic.mode` is `shadow` or `on`. The ordinary source store and
  metrics are closed before refresh begins; unchanged source runs also resume
  prior semantic work. Mode `off` and unsupported builds report explicit
  successful skips, while a supported-but-broken backend, cancellation, or any
  refresh failure makes the sync fail with the typed semantic error. CLI JSON
  emits one flattened sync document with either `semantic` or `semantic_error`.
  The coarse existing `sync-all.lock` covers the combined source-plus-semantic
  operation. Release/Homebrew native packaging and installed-corpus acceptance
  remain later stacked work.

### Resumable semantic refresh (2026-07-28)

- **Resumable semantic refresh**: Added a migration-backed per-profile refresh
  ledger, fixed-watermark projection and revision-checkpointed embedding batches,
  bounded provider retry, L0/compaction/verification/readiness orchestration,
  serialized progress, typed failures, `semantic refresh`, and latest-run
  status. Release packaging and installed-corpus activation remain in later
  stacked PRs.
- **Non-blocking refresh progress**: Five-second visible progress now continues
  while a semantic compaction transaction temporarily blocks the durable
  heartbeat write. A dedicated one-connection heartbeat handle keeps that wait
  out of the main SQLite pool; durable timestamps coalesce to the newest pending
  value, while checkpoint callbacks still expose only committed state.

### Semantic ANN runtime admission (2026-07-28)

- **Explicit native capability**: `dbrain semantic status` now reports whether
  the binary's native ANN backend is unsupported, supported and ready, or
  supported but broken, including the admitted backend and version when
  available.
- **Proven segmented serving**: A `usearch`-tagged runtime can admit and search
  a fully proven segmented USearch generation through the normal research
  command while retaining SQLite validation and exact reranking. Admission
  binds the cache root to the canonical descriptor reconstructed from SQLite,
  rejects active catalogs above a 1,024-segment hard safety ceiling, and fails
  closed before payload access when the root or segment set differs. Each
  native payload must contain exactly the manifest's member count.
- **Truthful native status**: A tagged `semantic status` opens and closes the
  runtime-equivalent native root before reporting it searchable. Missing or
  corrupt artifacts produce a stable path-free unavailable reason, while
  caller cancellation remains an error.
- **Cancellation-aware native loading**: Segment payloads are opened and read
  once with checksum verification, cancellation checks between stages, and
  cleanup of partially opened native indexes. The native load call itself
  remains non-preemptible. Candidate traversal checks cancellation around each
  native search and never leaks prior-stage partial hits after cancellation.
  Caller cancellation during native-root admission remains an error.
- **Safe unsupported behavior**: Normal CGO-free builds carry no USearch
  dependency, report `native_backend_unsupported`, and preserve ordinary
  lexical retrieval. Native capability diagnostics redact local filesystem
  paths without masking readiness states such as `corrupt`, `disabled`, or
  `needs_index`; native-root failures in research output and traces use a
  stable path-free reason.
- **Runtime-admission boundary**: Universal synchronous post-sync integration
  and cross-process locking are supplied by the following stacked work.
  Release/Homebrew native packaging and installed-corpus acceptance and
  activation remain later stacked work.

### Segmented ANN lifecycle foundation (2026-07-22)

- **Durable derived-cache roots**: Added content-addressed opaque segment
  payloads, deterministic member manifests, checksum-verified reopen, and
  atomic root publication under the configured cache directory. SQLite records
  the segment catalog, immutable member provenance, and generation references;
  it activates a root only after every new member still matches its ready
  embedding revision and vector hash.
- **Bounded L0 progression**: Added an internal, backend-injected 5,000-vector
  revision-prefix flush seam that publishes and reopens a root before SQLite
  activation. It preserves existing immutable segments in later roots and
  leaves newer vectors in exact L0. No CLI command, USearch runtime dependency,
  semantic retrieval serving, embedding backfill, or compaction is enabled by
  this groundwork.
- **Full-corpus rechunk cleanup performance**: Migration 24 adds the missing
  occurrence `chunk_id` index used by the retrieval-chunk cleanup trigger,
  preventing large parent replacements from repeatedly scanning the entire
  occurrence table.
- **Controlled native evaluation**: Added a USearch-tagged segment payload
  builder and explicit restored-corpus evaluator. It requires `--apply`, an
  explicit database/cache/report, and rejects both configured and candidate-root
  production databases before opening them. This is an operator-only evaluation
  boundary; it does not make native ANN a runtime dependency.
- **Membership-safe root activation**: L0 is now the exact complement of the
  active root's immutable `(chunk_id, revision, vector_hash)` membership rather
  than a revision tail. Root activation is compare-and-swap protected by its
  expected prior root, purge epoch, and snapshot; migration 23 repairs existing
  L0/tombstone counters from that authoritative membership. This enables the
  next compaction slice to return an undersized live remainder to L0 safely,
  but does not yet select or build compactions, serve ANN results, or change
  production data.
- **Deterministic compaction policy**: Added a pure, tested planner for
  tombstone cleanup, oldest same-class pairing, capped output packing, and
  exact-L0 remainders. It exposes no vector reads, root replacement, cache
  mutation, command, or serving behavior.
- **Active-root compaction facts**: Added a read-only SQLite snapshot for an
  active root's immutable segment metadata, stable creation order, and exact
  live/tombstone membership counts. It rejects catalog drift and has no vector
  payload reads, replacement-root activation, cache mutation, command, or
  serving behavior.
- **Bounded compaction member stream**: Added a read-only callback stream for
  the live rows of one or two CAS-checked active segments. It revalidates exact
  ready/current membership, encoded vector integrity, and catalog counts before
  yielding each vector. The stream itself creates no payload, root, cache,
  command, or serving change.
- **Incremental optional payload build**: The tag-gated USearch builder can now
  reserve a known segment size and ingest source vectors one at a time while
  preserving its existing flush interface. Native graph and final serialized
  payload memory remain evaluation concerns; no compactor calls this session,
  and no runtime behavior changes.
- **Bounded physical compaction**: Added an internal executor that selects one
  singleton/pair plan, streams only its current members, publishes/reopens
  replacement segments and a rewrite root, and uses existing root CAS
  activation. It retains unselected segments and leaves failed publications
  unreferenced; no command, cache cleanup, or serving path is enabled.
- **Verified optional native root loader**: Added a `usearch && cgo` internal
  loader that reopens the immutable root/segments, verifies provenance and
  checksums, and imports closeable native indexes.
- **Authoritative native candidate gate**: Added a bounded, tag-gated candidate
  searcher that resolves native ordinals through immutable manifests, validates
  their root CAS facts and current ready embeddings in SQLite, then exactly
  reranks survivors by cosine distance.
- **Exact L0 authority read**: Added a CAS-checked, bounded read for current
  ready embeddings absent from the active root. It rejects an oversized delta
  instead of returning a partial tail; tagged native search merges and exactly
  reranks it with validated root candidates, including a changed root member
  that has become a newer exact-L0 row.
- **Optional rooted runtime backend**: The normal CGO-free build now refuses an
  admitted active root rather than falling back to an unsafe whole-profile exact
  scan. A `usearch && cgo` build opens the verified cache root and uses the
  internal native-plus-L0 searcher; its unavailable state is explicitly reported
  as `native_backend_unavailable`. Production activation remains gated on an
  operator-built root and evaluation.
- **Semantic parent diversity**: Exact and tagged native searches now rank all
  validated candidates before applying a three-chunk cap per unpinned parent.
  Explicit parent-key filters remain protected anchors, so an intentional
  single-parent query is not artificially truncated.
- **Bounded native expansion**: Tagged native search now widens a filtered or
  parent-capped candidate window through 200, 500, and 2,000 global ANN-hit
  stages. Each authoritative SQLite validation read remains bounded to 190
  candidates and retains active-root CAS checks.
- **Content-free authority reads**: Native-candidate and exact-L0 validation
  projections no longer materialize chunk text. They retain only vector and
  filter metadata; the existing hydration step remains responsible for evidence
  text.
- **Pinned native authority session**: One tagged semantic search now holds a
  query-only SQLite snapshot across exact-L0 and every bounded native-candidate
  validation batch. Root CAS checks remain at each authority read; this does
  not yet add the planned cross-process generation lease or extend the snapshot
  through final evidence hydration.
- **CGO-free release isolation**: Moved the rejected `coder/hnsw` screening
  adapter out of the production semantic-index package so Windows, Linux, and
  Darwin release binaries remain CGO-free and do not compile an evaluation-only
  graph dependency. The optional `usearch && cgo` evaluator and runtime packages
  retain their separately tagged native build.
- **CI race-suite budget**: The PR workflow now runs the clean `task test-ci`
  gate and gives each race-enabled package a 20-minute timeout. The expanded
  SQLite store suite was still actively migrating independent test databases
  when Go's 10-minute default expired on Linux.
- **Restored-corpus flush repair**: Real batched embeddings now pass the
  5,000-vector lifecycle flush when multiple members share one atomic batch
  revision. The evaluator report also preserves its database, cache, profile,
  and completion status fields instead of silently omitting them.

### Optional native ANN backend screening (2026-07-22)

- **USearch candidate evidence**: Added a tag-gated, content-free USearch
  adapter and bakeoff command for a locally supplied native library. The default
  CGO-free build remains unchanged; the candidate neither opens SQLite nor
  enables semantic retrieval. The 286,619-vector screen passed the exact
  recall/reopen gate only with the recorded query-expansion parameter, so
  segmented lifecycle and release distribution remain separately gated work.

### Production-corpus semantic readiness foundation (2026-07-21)

- **Bounded projection and readiness**: Added a durable dirty-parent ledger,
  stable content-local chunk identity, occurrence provenance, bounded giant-item
  staging, aggregate readiness counters, and fail-closed migration/verification
  checks designed from the restored production corpus.
- **Scalable request admission**: Semantic readiness now uses indexed bounded
  work, an immutable 25,000-vector exact safety ceiling, and a 250 ms admission
  budget before provider construction. Larger complete profiles report
  `needs_index` and preserve lexical behavior until segmented ANN ships.
- **Resumable operators and explicit recovery**: Added durable
  `semantic chunk`/`embed --until-idle --max-duration` processing, bounded
  paged verification, and explicit transactional readiness-counter repair.
  Verification reports a clean empty result before the configured profile has
  been built. Semantic retrieval remains off by default and no request starts a
  backfill.

### Local hybrid retrieval foundation (2026-07-18)

- **Opt-in semantic retrieval**: Added deterministic evidence chunks, portable
  local Ollama embeddings, SQLite-authoritative exact vector search, RRF fusion,
  and content-free shadow comparisons. Lexical retrieval remains the default;
  `shadow` measures hybrid ordering without changing visible evidence, while
  `on` returns fused evidence and fails open to lexical results when the local
  semantic lane is unavailable or exceeds its effective exact limit, whose hard
  ceiling is 25,000 current ready embeddings counted before request filters.
- **Operational and transport controls**: Added `dbrain semantic status`,
  `semantic chunk`, and `semantic embed`, plus CLI and MCP/web per-request
  semantic overrides with conflict rejection. Direct MCP research remains
  read-only and trace-free, including in shadow mode.
- **Evidence provenance**: Research packs now expose effective
  `semantic_mode`, bounded `shadow_comparison`, chunk/content-section metadata,
  RRF scores, and full lexical/semantic retrieval-lane provenance. Exact-tag
  evidence remains a separate representative lane.
- **Paragraph-aware chunk profile**: Paragraph boundary selection now avoids
  splitting immediately before the configured target. This increments the
  deterministic retrieval chunker profile to v2; existing semantic chunks and
  embeddings must be rebuilt with `dbrain semantic chunk` followed by
  `dbrain semantic embed` before evaluating the new profile. Migration 15 now
  persists projection provenance on chunks; `semantic embed` refuses stale
  projection/chunker rows before calling the provider, and exact search rejects
  historically mislabeled vectors.
- **Revisioned embedding lifecycle**: Semantic embedding provider batches now
  commit atomically under one membership revision, preserve a valid active
  root by accounting changed rows in L0 plus tombstones, and treat
  operationally identical re-puts as revision/counter no-ops. Bounded
  `semantic verify` pages validate immutable profile, chunk provenance,
  purge-epoch, revision, root backend, vector bytes, and vector hashes before
  quarantining corrupt ready rows. Migration 19 replaces full-profile write
  scans with primary-key profile/chunk checks and rejects mixed legacy profile
  provenance.
- **Fail-closed generation provenance**: Generation activation now returns a
  typed error instead of manufacturing an index snapshot from the profile's
  latest embedding revision. Verification also rejects impossible tombstone
  counts and disagreement between profile and generation membership counts.
  Migration 20 queues legacy ready rows with a non-positive revision or empty
  vector hash for explicit re-embedding so upgraded databases cannot strand
  unverified vectors outside the worker selector.

### Production health audit corrections (2026-07-15)

- **False-failure corrections**: Scheduler continuity now ignores
  boundary-incomplete metrics records with no start timestamp, and preserved
  raw X media transcript text no longer prevents terminal outcomes such as
  `too_short` from being classified as terminal.
- **Sanitized remote diagnostics**: Standard media durability audits now retain
  fixed credential-resolution, configuration, timeout, cancellation, and read
  error codes without exposing provider errors, object keys, paths, or secrets.
- **Explicit pruned-media repair**: Added `dbrain repair pruned-media` as a
  read-only dry-run by default, with an explicit `--apply` path that restores
  archived/pruned media through normal download persistence. The command is not
  part of `sync all`; existing workers perform OCR and transcription afterward.
- **Actionable backup configuration**: The SQLite backup-configuration warning
  now carries a fixed privacy-safe instruction for enabling scheduled backups
  or explicitly requiring backup audits, and human audit output prints it
  directly below the non-passing finding.

### Production health audit foundation (2026-07-13)

- **Release audit skill**: Added a repo-local, content-free production release
  workflow that freezes the installed XDG or self-contained target, requires a
  fresh complete SQLite backup before installation, compares exact-profile
  standard reports after a separately approved cutover, verifies the expected
  commit, and evaluates CLI-only deep archive/media/upstream checks without
  granting deploy, restart, retry, repair, restore, import, or config authority.

- **Release audit boundary fixes**: Made `config paths` use the no-write target
  resolver, preserved root-only selection for self-contained installations,
  made abbreviated commit expectations match the embedded full revision, and
  ensured filtered/source audits actually include `boundary.runtime` whenever
  `--expect-commit` is supplied. The release skill includes a provenance-gated
  candidate bootstrap for the first audit-capable release.

- **Bounded upstream importer parity**: Added CLI-only deep reconciliation for
  Apple Notes, Safari Tabs, X Bookmarks, GitHub Stars, YouTube Liked, YouTube
  Watch Later, and enabled feeds, plus exact source commands for release and
  operational checks. Inventories are read-only, content-free, sequential,
  cancellation-aware, capped at 100,000 unique identities and 10,000 pages,
  and fail closed on credentials, schema, cursor, device, network, or
  completion ambiguity. Local app sources use dbrain-owned SQLite snapshots;
  remote adapters use fixed/configured safe origins or bounded proxy-free
  subprocesses. Source-only audits no longer resolve unrelated archive limits,
  and deep parity remains unavailable to scheduled, MCP, and admin runners.

- **Feed audit credential hardening**: Reject feed URLs containing userinfo at
  the audit-only policy boundary before the authenticated general feed fetcher
  can normalize or send the request.

- **Production health admin view**: Replaced ambiguous activity-derived health
  with the authenticated exact-profile standard audit presentation. The System
  page now separates fast local refresh from authoritative standard health,
  importer polls from arrivals, terminal pipeline outcomes from failures, and
  media/SQLite/OKF durability checks from legacy counters; it includes bounded
  typed finding evidence, recovery history, abortable on-demand polling, and a
  responsive 390px-safe observability layout. Legacy `backlog.drained` remains
  compatible but is labeled and scoped as source-processing backlog only.

- **Authenticated admin audit API**: Added fail-closed latest, compact history,
  bounded on-demand fast/standard run, and process-run status endpoints for the
  authenticated web administration surface. The routes are absent when web
  authentication is disabled, reject service-auth and cross-origin mutation,
  persist immutable reports before completion, and retain only bounded,
  sanitized in-process run state.

- **Bounded MCP health**: Added `dbrain_audit` with a fixed-deadline,
  process-singleflight local fast profile and a persisted exact-profile standard
  read path. The privacy-validated response is capped at 256 KiB, exposes no
  deep/category/path/URL/archive controls, uses a no-create/no-chmod report
  reader, and is available over HTTP/tsnet only when bearer auth is required.
  The ten-second wall-clock limit is enforced independently of runner context
  cancellation, without starting duplicate work when a stuck runner times out.

- **Scheduled health and regression alerts**: Added opt-in post-sync fast and
  six-hour standard audits to `serve remote`, private daily report retention,
  exact-profile freshness metadata, content-free transition state, compact
  completion metrics, and an exact-origin no-proxy/no-redirect JSON webhook
  with debounced escalation, repeat, configuration-resolution, and immediate
  recovery behavior. Scheduled audit failures remain separate from sync
  results and never receive archive-write or restore capability.

- **Scheduled SQLite durability**: Added an opt-in daily `serve remote`
  scheduler that uses the existing online SQLite snapshot/archive path,
  runs once on startup by default, durably rate-limits attempts across service
  restarts, serializes scheduled/manual archives and restores through one
  crash-released cross-process lease, applies bounded preflight-failure backoff,
  confines and durably syncs its private attempt marker, honors a full initial
  delay when startup runs are disabled, supports cancellation during snapshot
  compression, and emits content-free aggregate metrics without granting write
  capability to audits.
- **Bounded deep verification**: Added an explicit CLI-only deep audit that
  validates the newest compressed SQLite archive in a private temporary
  directory, performs complete bounded `media/` inventory reconciliation, and
  cleans up without invoking the active-database restore path.
- **Audit CLI**: Added `dbrain audit all|imports|pipeline|durability` with a
  stable, privacy-validated 55-check JSON registry, typed health exit codes,
  deterministic thresholds/sampling, and a no-write query-only target resolver.
- **Operational evidence**: Bounded JSONL reading now reconstructs scheduler
  runs, explicit lifecycle markers, seven source-family poll/arrival streams,
  and separate YouTube liked/watch-later results without retaining raw lines.
- **Least-authority durability**: Standard media and SQLite backup checks use
  exact-origin, DNS/IP-validated, no-proxy S3 HEAD/LIST clients that expose no
  upload, download, restore, or endpoint-override capability.
- **Read-only diagnostics**: All `stats` commands now open SQLite without applying migrations, and active-database inspection reports quick-check, foreign-key, core-schema, and migration compatibility as separate claims.
- **Truthful pipeline partitions**: Pipeline stats and workers share X transcription, photo OCR, source-repair, and media-archive predicates; terminal outcomes and invalid legacy states are reported separately from genuine failures, with explicit partition validation.
- **Actionable pending-age audits**: Pipeline audit snapshots now carry the
  oldest eligible timestamp for hydration, extraction, summary, transcription,
  and OCR work, so non-empty backlogs classify against the documented 24/72-hour
  thresholds instead of always degrading to unknown.
- **Confined artifact inspection**: Added consistent read-transaction snapshots, metadata-only vault inspection with sanitized errors, and root-confined aggregate OKF validation that requires a readable manifest and export timestamp.
- **Provenance and scheduler diagnostics**: Successful X media transcripts now retain deterministic input provenance, compatibility mirrors preserve authoritative enrichment metadata, migration-backed audit aggregates separate legacy gaps from regressions, and TSNet status reports sanitized scheduler authentication failures.
- **Audit truth and bounded execution**: Production audits now consume real store
  pipeline aggregates and closed grouped kinds, preserve partial SQLite archive
  listing progress, apply bootstrap/local/integrity/per-page timeout classes to
  their actual operations, and keep mixed source/category scopes truthful.
- **Local audit diagnostics**: Identifier-enabled CLI output now includes
  bounded real row/source identifiers for non-pass pipeline, provenance, and
  local-media checks plus exact credential-free media and SQLite archive
  targets; cleanup paths remain empty. OKF freshness reads only the manifest,
  while metrics files are opened nonblocking and validated by descriptor.
- **Audit bootstrap and timeout truth**: The actual bounded YAML and dotenv
  snapshots, feature resolution, and SQLite `BEGIN` now run inside the
  bootstrap deadline without shortening the later snapshot lifetime. Frozen
  lookup preserves shell, `.envrc`, `.env`, then YAML precedence without
  repeated reads, and overlapping same-root snapshots now clean up safely in
  any order without restoring stale YAML or dotenv values. Per-class overrides
  lower local, integrity, manifest, remote-check, and per-request ceilings,
  including media whole-check limits.

### Security hardening (2026-07-13)

- **Authenticated feeds**: Basic-auth feed URLs are stripped of userinfo before
  safe-HTTP validation and translated into an Authorization header that is
  retained only across exact-origin redirects and recovered only for same-origin
  subsequent polls after a sanitized resolved URL is stored.
- **Local filesystem containment**: Vault note/media reads, writes, uploads,
  OCR/transcription inputs, and cleanup now use root-confined filesystem
  operations so persisted traversal paths and symlink parents cannot escape the
  configured vault. Apple Notes attachment extraction is likewise confined to
  the selected Notes database container.
- **Restore identity**: SQLite archive restore now rejects corrupt, foreign,
  future-version, incomplete-migration, and unknown-migration databases before
  replacing the authoritative local database.
- **Network and remote surfaces**: Source/media fetches share a redirect- and
  DNS-rebinding-aware public-destination policy; Funnel startup fails closed
  unless each selected web/MCP surface has application authentication; web
  mutations share an Origin guard; service-auth nonces are single-use per
  process; and JSON-RPC batches are limited to 16 requests.
- **Safe source extraction**: Safe-fetched HTML and text are now extracted
  in-process instead of being passed to `summarize --extract` as unsupported
  local files; summary-mode subprocesses receive only the extracted text over
  stdin while dbrain retains validated URL provenance.
- **Public shares**: Anonymous share rendering rejects URL userinfo and removes
  recognized credential-like query data from newly generated and legacy stored
  shares, including renderer-created attributes, nested/encoded query values,
  and browser-normalized URL forms, while preserving ordinary query parameters.
- **Supply chain**: Release and PR workflow action selectors and tool versions
  are immutable, with a YAML-aware regression policy covering both `.yml` and
  `.yaml` workflow files.
- **Review workflow**: Added the repo-owned `dbrain-security-review` skill,
  security campaign design/remediation plans, and a sanitized evidence ledger.

### Homebrew Test Release Channel (2026-07-13)

- **Release testing**: Added an owner-dispatched Homebrew test channel that builds an exact commit into durable prerelease assets and one moving `dbrain-test` formula without changing stable `dbrain` distribution.
- **Release safety**: Stable publication now accepts only exact `vX.Y.Z` tags, serializes tap updates, and tests that candidate formula generation cannot modify `Formula/dbrain.rb` or add runtime-data cleanup hooks.
- **Tap validation**: Stable formula updates now restore the reciprocal `dbrain-test` conflict required by Homebrew audit, preventing candidate formula publication from breaking subsequent `brew test-bot` runs.

### dbrain Review Skill Distribution (2026-07-11)

- **Skill packaging**: Moved the `dbrain-review` skill into this repository alongside `dbrain-mcp` so the project is its authoritative source.
- **Registry publishing**: Release tags now publish both `dbrain-mcp` and `dbrain-review` to the nono registry under the `darron` namespace.

### Open-source whisper.cpp transcription (2026-07-11)

- **Speech to text**: Added whisper.cpp as a fully supported backend for X media and YouTube caption fallback, with automatic backend selection, Apple Silicon/Metal-compatible Homebrew installation, optional Silero VAD, language selection, and terminal no-speech handling.
- **Installer**: `dbrain install` now detects `whisper-cli`, writes shared transcription configuration, reports the exact Homebrew command when it is missing, and can download pinned, checksum-verified Whisper base and Silero VAD models with `--download-whisper-models`.
- **CLI/docs**: `dbrain transcribe x-media` accepts explicit backend, model, VAD, language, and binary flags; MacWhisper remains available as a compatibility backend.
- **Fresh-install UX**: When `whisper-cli` is detected and pinned models are missing, interactive setup now offers the verified downloads with yes selected and `--yes` accepts them automatically; Whisper transfers show byte progress and Ollama model preparation streams its native output.
- **Setup docs**: Moved the recommended `summarize`, Ollama, whisper.cpp, and Chrome prerequisites ahead of first-time setup so clean-machine installs are detected and configured on the first run.

### Research Harness Inspection And Evidence Flow (2026-07-11)

- **Inspection**: The runner now performs one bounded read-only hydration pass
  over the top primary evidence rows, reranks only that window using direct/raw
  support, preserves tail order, and leaves original retrieval scores intact.
- **Relevance**: Conservative required-concept intersection filtering now
  covers safe conjunctive query families while failing open for partial,
  compound, comparative, corrective, contradictory, chunked, media, and
  dependency-uncertain evidence.
- **Synthesis**: Uncited topic-brief aggregates are excluded from model context;
  source-key evidence remains available according to relevance and budget
  decisions.
- **Tracing and evals**: Added `evidence_flow.v1` with explicit retrieved,
  inspected, relevance-admitted/excluded, prompt-admitted, budget-dropped,
  partially-trimmed, and answer-cited stages plus lifecycle invariants and
  stage-specific eval assertions.
- **Location**: `internal/ask/`, `internal/brainresearch/`,
  `internal/researchrun/`, `internal/researchtrace/`, `internal/researcheval/`

### Chat Harness Relevance And Citation Semantics (2026-07-11)

- **Fixed**: Final synthesis citation metadata now contains only exact source
  keys actually cited in the answer; prepared prompt evidence remains recorded
  separately on `PreparedSynthesis` for traces and diagnostics.
- **Shares**: Public Original URLs, source metadata, and topic tags now treat
  answer citations as authoritative, preventing uncited prompt candidates from
  leaking through the structured citation array.
- **Synthesis guardrails**: Prompt v4 tells the model to ignore unrelated
  candidates silently, and verification rejects unsolicited unrelated-source
  inventories before a turn can be shared.
- **Retrieval**: Required phrase concepts preserve discriminative one-letter
  names such as `J space`, keep an exact quoted phrase lane through planner
  merges, and restrict synthesis context to rows satisfying every required
  concept when at least two direct matches are available. This avoids broad
  fallback plans such as `anthropic space` over-ranking unrelated material.
- **Regression coverage**: Added runtime-shaped tests for prepared-versus-used
  citations, public share filtering, category filtering, short-token concepts,
  and unrelated-source answer rejection.
- **Location**: `internal/brainresearch/`, `internal/researchrun/`,
  `web/chat_shares.go`, `docs/research-harness.md` (removed 2026-08-02;
  available in Git history)

### Installer Summarize Prerequisite (2026-07-11)

- **Installer**: First-time setup now detects the required `summarize` CLI and
  prints an actionable `brew install summarize` warning when it is missing; X
  setup now gives missing MacWhisper and media tools explicit install paths.
- **Docs**: Homebrew runtime requirements now appear before `dbrain install`,
  including the official `summarize` formula and optional Ollama and Chrome
  setup needed before installer detection, with MacWhisper linked prominently
  for X media transcription.

### Installer Import Selection (2026-07-09)

- **Installer**: Fresh installs now present an explicit checklist for X
  bookmarks, GitHub stars, YouTube Watch Later, liked YouTube videos, feeds,
  Apple Notes, and Safari tabs; no importer is silently selected.
- **Config**: Installer choices are persisted under `sync_all.imports` and are
  shared by manual and scheduled `sync all` runs, with environment and
  one-run CLI overrides plus backward-compatible legacy defaults.
- **Safety**: Declining X disables bookmark import, hydration, media
  transcription, and photo OCR together. Unselected importers no longer run
  their preflight checks, and install reports missing configuration only for
  selected sources.
- **Browser config**: X and YouTube selections now collect a shared browser and
  optional profile used by both manual and scheduled runs.
- **Updates**: Re-running install merges managed selections into an existing
  config instead of requiring a destructive full overwrite.

### Installer Local Model Profiles (2026-07-09)

- **Fixed**: Fresh installs now apply detected local model defaults before the
  interactive/noninteractive split. When the Ollama CLI is present, plain
  install now defaults to the dbrain Ollama profile and creates
  `dbrain:2026042701` before writing config that references it; LM Studio is
  only a fallback when Ollama is unavailable.
- **Fixed**: Installs without a local OCR tool/model or OpenRouter key now write
  configured `sync all` skips for hosted-only X photo OCR; installs with no
  categorization model or OpenRouter key likewise skip categorization instead of
  failing at first-run preflight.
- **Fixed**: Re-running install now reuses existing known `dbrain` Keychain
  secrets when prompt fields are left blank, including GitHub PATs, OpenRouter
  keys, Tailscale auth keys for Tailscale-enabled installs, GitHub OAuth client
  secrets, and existing web auth session keys.
- **CLI**: Added `dbrain install --local-model-profile
  dbrain|dbrain-ollama|dbrain-omlx|small-ollama|none` so first-run setup can
  write curated local model defaults without requiring users to know exact
  provider-qualified model strings.
- **Local models**: The dbrain profile targets the local wrapper tag
  `ollama/dbrain:2026042701`; install embeds and materializes the dbrain
  Modelfile, pulls `qwen3.6:35b-a3b-nvfp4` when needed, and creates the local
  wrapper tag before writing config that references it. The comparable oMLX
  profile targets `omlx/Qwen3.6-35B-A3B-MLX-4bit`, and the smaller Ollama
  profile writes and pulls `ollama/gemma4:12b-mlx`. Explicit `--summary-model`
  / `--categorize-model` flags still take precedence.
- **Docs**: Documented local wrapper creation from the embedded Modelfile so
  installs do not depend on redistributing or pushing the large
  Modelfile-derived artifact.

### Public Chat Share Sources (2026-07-09)

- **Fixed**: Public chat shares now list only cited sources and URLs present in
  the shared answer, rather than every retrieval-pack candidate considered
  during the chat turn.
- **Location**: `web/chat_shares.go`

### Web Archived Media Embeds (2026-07-08)

- **Fixed**: The web archived-media proxy now resolves R2/S3 credential secret
  references from config before building its client, so archived X photo/video
  embeds render through `/media/asset/{id}` instead of failing when production
  credentials are stored as `keychain://` or other secret refs.

### First-Run Installer (2026-07-08)

- **CLI**: Added `dbrain install` for first-time setup with XDG defaults or a
  user-supplied `--base-path`, bundled config/category templates, local helper
  detection, and noninteractive `--yes` support.
- **Setup**: The installer can enable Apple Notes, Safari tabs, scheduled
  `sync all`, Tailscale/tsnet transport, and GitHub web login while keeping
  Tailscale settings under `tsnet.*` and login settings under `auth.*`.
- **Secrets**: On macOS, prompted third-party secrets are stored as Keychain
  refs; generated web auth session keys are created automatically when GitHub
  login is enabled.
- **Location**: `internal/install/`, `internal/app/install.go`

### Documentation Reference Refresh (2026-07-06)

- **Docs/config**: Refreshed README, COMMANDS, MCP, TAILSCALE, and config
  sample references for the current MCP tool surface, visible CLI commands,
  scheduled `sync all` controls, launchd-backed remote serving, and local model
  backends including Ollama, LM Studio, oMLX, and configured OpenAI-compatible
  aliases.
- **Docs/roadmap**: Replaced the stale README checklist with a current public
  roadmap that separates live product gaps, pipeline gaps, evaluation backlog,
  and explicit non-goals.
- **Docs/diagram**: Replaced the README banner with a current architecture
  diagram covering import-only sources, local authority, enrichment/model lanes,
  MCP/web/CLI/Tailscale surfaces, launchd scheduling, and archive/restore.

### Chat Harness Anchored Synthesis (2026-07-04)

- **Fixed**: Web Chat and the research runner now preserve explicit
  handle/entity/source-key evidence through judged retries, keep raw current
  questions separate from composed retrieval context, and reject false "no
  sources" answers when prepared synthesis context contains cited protected
  anchor evidence.
- **Evals/traces**: Research evals can exercise the runner through judge/retry
  merge without model calls or persistent traces, and saved traces now retain
  attempt-specific planner input/output artifacts for initial and retry packs.
- **Location**: `internal/brainresearch/`, `internal/researchrun/`,
  `internal/researchtrace/`, `internal/researcheval/`, `web/`

### Scheduled Sync Log Timestamps (2026-07-04)

- **Observability**: Scheduled `sync all` logs emitted by `serve remote` now
  prefix each scheduler log line with an RFC3339 timestamp, making
  `launchd.err.log` useful without changing interactive `dbrain sync all`
  terminal output.

### X Hydration Candidate Selection (2026-07-04)

- **Fixed**: X hydration repair candidate selection now looks up linked media
  from the current item first, avoiding repeated `media_assets` status scans
  across large already-hydrated X corpora during `hydrate x` and `sync all`.

### Categorization Vault Tags (2026-07-04)

- **Fixed**: Applying item/source categorization now refreshes the rendered
  vault Markdown notes, and item notes now include `user_tags` frontmatter so
  DB, search, OKF, and human-facing notes stay aligned.

### Durable Sync Metrics (2026-07-03)

- **Observability**: Added opt-in append-only JSONL metrics for manual and
  scheduled `sync all` runs via `metrics.*` config / `DBRAIN_METRICS_*`,
  including run, stage, categorization, source-summary, and direct model-call
  timing events.
- **Privacy**: Metrics omit prompt/source text, summaries, tags, categories,
  URLs, titles, headers, API keys, and raw item/source keys by default; raw
  subject keys require explicit `metrics.include_subject_keys: true`.
- **Docs/config**: Documented metrics setup in `config.yaml.sample`, README,
  and `COMMANDS.md`.
- **Fixed**: Sync metrics now report resolved OCR and categorization models
  from config/env/defaults instead of empty raw CLI override fields.

### Remote Web Chat Streaming (2026-07-03)

- **Fixed**: `serve remote` no longer applies a 60-second absolute response
  write timeout, so long-running web Chat/Research SSE streams can use their
  own runner/stage/model timeouts instead of failing as browser network errors.

### Sync LLM Routing and Linked Article Categorization (2026-07-03)

- **Fixed**: `sync all` now resolves `summary.model` once and passes that same
  model into X media transcript summaries, avoiding mixed local backends during
  one run.
- **Fixed**: Item categorization now includes capped linked source evidence, so
  X article wrapper posts are categorized from the extracted article instead of
  the short URL shell.
- **Observability**: Item/source categorization debug logs now include the
  result model, making backend provenance visible in long sync runs.

### Scheduler Local Backend Controls (2026-07-03)

- **Docs/config**: Documented scheduled categorization concurrency, timeout,
  and `skip_categorize_images` controls so local text-only or slow
  OpenAI-compatible backends can avoid accidental image-heavy scheduled runs.

### oMLX Image Categorization (2026-07-03)

- Fixed `omlx/<model>` categorization so image-backed items can send
  OpenAI-compatible image parts to oMLX vision-capable models instead of being
  rejected as text-only before the request reaches oMLX.

### LLM Backend Abstraction (2026-07-01)

- Added a shared LLM backend registry/client for direct local and hosted model
  calls, including first-class `omlx/<model>` and configured
  OpenAI-compatible backend aliases, while preserving existing Ollama,
  OpenRouter, LM Studio, external `summarize` CLI, and OpenRouter/Gemini OCR
  behavior.
- Documented runner-specific setup and live-tested example model strings for
  Ollama, LM Studio, oMLX, OpenRouter, and configured OpenAI-compatible aliases.

### LM Studio Provider (2026-06-30)

- Added first-class LM Studio local model support for direct summaries, text categorization, and model bakeoff reports, including provider-qualified provenance and opt-in local parity metadata.
- Categorization provenance now records provider-qualified Ollama and OpenRouter model ids, matching summary provenance and avoiding ambiguous bare model names after provider swaps.

### What's New Review Feed (2026-06-21)

- **CLI/API/MCP**: Added a read-only `whats-new` review feed for newly
  imported, updated, enriched, failed, or blocked local evidence, exposed as
  `dbrain whats-new`, `GET /api/whats-new`, and MCP `dbrain_whats_new`.
- **Review semantics**: The feed is cursor-paged, normalizes event timestamps
  to UTC, supports `since` timestamps or relative durations, groups event
  filters into `imports`, `enrichments`, and `failures`, and preserves empty
  arrays in JSON/MCP structured output.
- **Agent review mode**: Added `view=entities` / `--view entities` for compact
  item/source grouping with preferred summaries and collapsed event kinds, so
  agents can answer "what should I pay attention to?" without reconstructing
  entities from raw pipeline events.
- **Schema**: Added review-event indexes for import, source activity, feed
  entry, and item-enrichment timestamps so existing databases can serve review
  pages without scanning the main corpus blindly.
- **Location**: `internal/store/review_events*`, `internal/app/whats_new*`,
  `web/whats_new_handlers.go`, `internal/mcpserver/*whats_new*`

### Public Chat Share Topics (2026-06-15)

- **Fixed**: Public chat share topic chips now come from weighted research-pack
  evidence tags, prioritizing answer-cited sources and applying the configured
  category vocabulary instead of using generic keyword/source-type guesses.
- **Hardening**: Share topic derivation now suppresses generic labels such as
  `research`, `media`, `software`, and source-platform noise, with regression
  coverage for citation weighting, coverage fallback, and vocabulary cleanup.
- **Location**: `web/chat_shares.go`, `web/chat_shares_test.go`

### Open Knowledge Format Export (2026-06-14)

- **OKF export**: Added `dbrain okf export` and `dbrain okf validate` for
  deterministic private Open Knowledge Format Markdown bundles with generated
  indexes, bundle metadata, path-collision checks, staging validation, export
  locking, and atomic replacement.
- **OKF concepts**: Exported items, linked sources, derived entities, and
  generated topic maps preserve stable dbrain producer ids, avoid volatile
  pipeline state in frontmatter, keep raw evidence separate from summaries, and
  reference uploaded/archive media URLs without leaking local media paths; when
  legacy rows have no `archive_url`, OKF derives the archived-media link from
  the configured public object base URL plus `archive_key`, or from the private
  dbrain media proxy/root URL plus the media asset id.
- **Fixed**: OKF export now uses a crash-safe advisory lock, omits empty
  extension frontmatter fields, records target diagnostics for omitted filtered
  links, strips generated local-media-path metadata from raw source/item body
  text, and rejects exports where every concept kind is explicitly disabled.
- **Sync integration**: `sync all --okf-export` or
  `DBRAIN_OKF_EXPORT_ENABLED=true` now writes a full private OKF bundle after
  the sync pipeline finishes; `--skip-okf-export` provides a one-off opt-out.
- **MCP**: Added read-only `dbrain_okf_search` and `dbrain_okf_get` tools for
  consuming an existing generated OKF bundle; export and validation remain CLI
  operations only.
- **Config**: Added `OKFDir` beside the rendered vault path so explicit-root
  installs use `<root>/okf/current/` and XDG installs use the matching
  data-dir sibling.
- **Dev**: Standardized the full test gate on `task test-ci`; `task test`
  remains available for local ambient-environment debugging.
- **Location**: `internal/okf/`, `internal/app/okf.go`,
  `internal/mcpserver/`, `internal/config/`

### Feed Parse Retry Classification (2026-06-02)

- **Fixed**: Feed parse failures now enter normal retry backoff instead of
  becoming permanently `blocked`, so transient truncated feed bodies do not
  silently remove a subscription from future `sync all` runs.
- **Fixed**: Feed-linked source extraction now recomputes extract status after
  merging feed-entry article text, preventing non-empty extracts from being
  saved as `empty` and summarized repeatedly by the source worker.
- **Source extraction**: MakerWorld model pages now use Bambu's public design
  API when the website blocks normal HTML fetches, and existing
  `dead/http_access_denied` MakerWorld sources are queued for repair.
- **Location**: `internal/feedimport/`, `internal/sourceenrich/`,
  `internal/store/`

### Research Exact-Tag Recall (2026-06-01)

- **Retrieval**: Exact user-tag matches now enter the primary research evidence
  candidate pool with explicit `exact_tag` lane provenance, helping relevant
  saved/tagged items survive over-specified lexical queries.
- **Retrieval**: Exact-tag examples are ranked by query-term coverage before
  capping, and tag search results use recency-aware ordering instead of
  arbitrary source-key ordering.
- **Retrieval**: Research query concepts now treat common app/software wording
  as equivalent where appropriate, including `app`/`tools` for software,
  `macos` for Mac, and `screen-recorder` as a tag alias for screen-recording
  queries.
- **Research runner**: Evidence-quality retry checks now focus missing-concept
  evaluation on the current synthesis question, so chat expansion terms from
  recent messages do not discard otherwise relevant evidence.
- **Dev**: Added `task test-ci` for running the full Go suite in a clean
  CI-like environment without ambient local `DBRAIN_*` or TSNet settings.
- **Location**: `internal/brainresearch/`, `internal/researchrun/`,
  `internal/store/`, `web/`, `Taskfile.yml`

### MCP Stats Schema Fix (2026-05-26)

- **MCP**: Empty stats bucket lists now serialize as JSON arrays instead of
  `null`, keeping `dbrain_stats_backlog` structured output compatible with its
  advertised schema.
- **Location**: `internal/mcpserver/`, `internal/store/`

### Retrieval Eval Follow-Up (2026-05-24)

- **Retrieval**: Source evidence titles now fall back to titles embedded in
  extracted reader text when the stored source title is blank, improving web
  evidence cards and eval assertions for reader-backed pages.
- **Source extraction**: Plain-text reader imports now recognize leading
  `Title:` lines as source titles for future stored sources.
- **Agent guidance**: The `dbrain-mcp` skill now spells out the trace-to-eval
  workflow for surprising Chat answers, including trace diffing, eval proposal
  generation, and keeping planner-enabled plus planner-disabled cases.
- **Location**: `internal/ask/`, `internal/sourceenrich/`

### Research Harness Lab And Retrieval Lanes (2026-05-23)

- **Web**: Chat is now the default home surface and the old Research mode is no
  longer in the primary mode tabs; Chat turns show a runner progress timeline
  instead of only the latest status line.
- **Harness Lab**: Added trace listing and comparison APIs plus a web Harness
  tab that loads saved research traces, shows old answer/current rerun slots,
  compares old/current evidence, and exposes the eval-proposal command.
- **Harness Lab**: Trace inspection now still returns the saved trace when the
  current diff/rerun fails, and Chat verification failures clearly mark generated
  answers as rejected without showing rejected citation chips as accepted output.
- **Web**: Chat and Harness Lab runner paths now use a longer retrieval stage
  budget so exact-tag-heavy queries do not fail at the old short deadline before
  synthesis starts.
- **Synthesis**: Research synthesis no longer asks models to print local note
  paths or a separate `Sources` section; citation metadata remains available to
  the UI through structured source-key data.
- **Retrieval**: Evidence rows now carry retrieval lane provenance, exact-tag
  examples are marked as their own lane, and the optional semantic lane is
  explicit but disabled until local hybrid retrieval has enough reviewed evals.
- **Location**: `internal/researchhybrid/`, `internal/researchtrace/`,
  `internal/brainresearch/`, `web/`, `web/ui/`, `skills/dbrain-mcp/`

### Research Evidence Roles And Chunk Windows (2026-05-23)

- **Research**: Evidence rows now carry `evidence_role`, optional chunk
  metadata, and role-labelled `content_sections` so synthesis and UI surfaces
  can distinguish derived summaries from raw extract/OCR/transcript windows.
- **Retrieval**: Source evidence now falls back to a raw extracted-text window
  when an existing summary is present but misses the query terms, preserving the
  summary separately while returning the relevant raw chunk as evidence.
- **Synthesis/UI**: Research synthesis input labels content section roles and
  chunk metadata, and web evidence cards surface the evidence role beside the
  source type.
- **Location**: `internal/ask/`, `internal/retrieval/`, `internal/brainresearch/`, `web/ui/`

### Research Citation Verification (2026-05-23)

- **Research**: Strengthened bounded-runner answer verification: cited source
  keys must exist in the final pack with exact casing/prefixes, evidence-backed
  answers must contain source-key citations, and no-evidence packs cannot
  return normal completed answers.
- **Web**: Runner streams now return compact `verification_failed` diagnostics,
  top-level `answer_warnings`, and rejected-answer traces without emitting the
  answer as a normal Chat completion.
- **Review**: Added an optional advisory answer-review hook for runner mode,
  including `dbrain research --runner --answer-review`; review warnings are
  visible in runner output and persisted as trace events but do not override
  deterministic citation gates.
- **Location**: `internal/researchrun/`, `internal/app/`, `web/`, `web/ui/`

### Bounded Research Runner (2026-05-23)

- **Research**: Added `internal/researchrun`, a bounded research runner that
  builds an initial pack, judges coverage with typed verdicts/retry actions,
  performs at most one focused retry or related expansion, always prepares
  synthesis for user-facing runs, verifies citation source keys against the
  final evidence pack, and saves traces by default.
- **CLI/Web**: Added `dbrain research --runner` and `/api/research/run`; web
  Chat now uses the server-side runner and streams progress events for
  planning, retrieval, inspection, synthesis, verification, and trace
  persistence instead of looking hung.
- **Hardening**: Verification-failed turns are rejected by chat share/export
  paths, `verification_failed` survives UI session reload normalization, and
  the existing MCP `dbrain_research_pack` remains read-only and trace-free.
- **Location**: `internal/researchrun/`, `internal/app/`, `web/`, `web/ui/`

### Research Deterministic Query Families (2026-05-23)

- **Research**: Deterministic research strategy now classifies queries into
  named families such as entity overview, person/news event lookup,
  model/tool selection, software project lookup, comparison, timeline/history,
  media transcript/OCR lookup, corrective follow-up, and exact source lookup;
  the family is included in `query_plan.query_family`.
- **Evals**: `dbrain eval research` can assert `expect_query_family`, and the
  eval docs now list the maintained family names for planner-disabled
  regression coverage.
- **Location**: `internal/brainresearch/`, `internal/queryterms/`,
  `internal/researcheval/`, `evals/`

### Research Eval Harness (2026-05-23)

- **Research**: Added `dbrain eval research` for full research/chat harness
  regression cases covering query-plan assertions, planner-disabled baselines,
  retrieval signal counts, prepared synthesis answer status, and citation
  source-key coverage.
- **Diagnostics**: Added `dbrain eval research propose` for conservative eval
  proposals from saved chat transcripts or research traces, with transcript
  answer-text assertions omitted unless explicitly requested, plus
  `dbrain eval research diff` for comparing saved trace evidence keys against a
  fresh pack build.
- **Location**: `internal/researcheval/`, `internal/app/`, `evals/`

### Research Trace Persistence (2026-05-23)

- **Research**: Added Phase 1 trace persistence for the research/chat harness:
  CLI and web synthesis runs now save completed Markdown/JSON traces with
  planner and synthesis sidecars, redaction, completion markers, concurrent
  run-safe directories, and default retention pruning.
- **Web**: Chat synthesis sends continuity metadata, receives the saved trace
  path, and shows a compact trace-saved status on completed turns.
- **Location**: `internal/researchtrace/`, `internal/brainresearch/`,
  `internal/app/`, `web/`, `web/ui/`

### Research Harness Documentation (2026-05-23)

- **Docs**: Added a current-state and roadmap document for the research/chat
  harness, covering `internal/brainresearch`, web Chat/Research, MCP usage,
  eval coverage, known gaps, and a phased improvement plan that makes Chat the
  default web surface, saves traces by default, and centers `dbrain eval
  research`, trace retention, citation verification UX, and Harness Lab
  comparison work; updated agent guidance for non-indexed diagnostic research
  traces.
- **Location**: `docs/research-harness.md` (removed 2026-08-02; available in
  Git history), `AGENTS.md`

### Inline Media Evidence For Search And Chat (2026-05-20)

- **Search**: Item/source search snippets now prefer the actual FTS match window, so transcript- and OCR-backed hits show the matching passage instead of an unrelated summary.
- **Research/MCP**: Search results, research evidence, and `dbrain_get` payloads now include sanitized media references for related photos, videos, GIFs, and audio without exposing local paths or archive storage keys.
- **Web**: Search, research, and chat evidence cards now render inline media previews/players for media-backed results, with compact title/author/type metadata and clickable summary/transcript-match context under each media block.
- **Location**: `internal/store/`, `internal/retrieval/`, `internal/ask/`, `internal/brainresearch/`, `internal/mcpserver/`, `web/`

### Launchd Full Disk Access Probe Auth (2026-05-16)

- **Fixed**: `dbrain launchd restart` now authenticates its service-process Full Disk Access probe when web OAuth is enabled, so `/api/doctor/full-disk-access` no longer returns a diagnostic-only `401` after the GitHub OAuth rollout.
- **Hardening**: The doctor API remains protected for normal web/API callers; the local CLI uses a short-lived HMAC service header derived from `auth.session_key` for this narrow restart probe.
- **Location**: `internal/app/`, `web/`, `internal/serviceauth/`

### Public Share URL Cleanup (2026-05-16)

- **Fixed**: Public chat share "Original URLs" now strips Markdown syntax and boilerplate labels such as "What It Is" from source snippets, and cleans adjacent Markdown URL artifacts such as encoded `][https://...` joins, including for already-created shares.
- **Location**: `web/`

### Browser Link Saver Extensions (2026-05-16)

- **Added**: Unpacked Manifest V3 Chrome extension that saves the active tab URL to dbrain through the existing `POST /api/links` API, using the current browser session for OAuth-protected web installs.
- **Added**: Safari Web Extension packaging path with an Xcode converter script, reusing the same WebExtension source for Safari's required host-app project.
- **Fixed**: The remote web origin guard now permits Chrome/Safari extension origins only for `POST /api/links`, so the link saver can use the authenticated browser session without opening other write endpoints to extension-origin POSTs.
- **Fixed**: Browser extension host permissions now preserve non-default dbrain ports such as `127.0.0.1:8742`, and error handling avoids surfacing raw server response text in the toolbar tooltip while throttling repeated login-tab opens.
- **Operations**: Extension options store the dbrain base URL and request host access only for that configured origin when supported; toolbar clicks show short success/error badges and open dbrain login on `401`.
- **Location**: `browser/chrome-extension/`, `browser/safari-extension/`

### Public Chat Shares (2026-05-15)

- **Added**: Completed browser Chat answers can now be shared as stable public `/share/{slug}` pages, with deterministic summaries, categories, original external URLs, and per-owner share history in the web UI.
- **Hardening**: Public share pages bypass web auth only for `/share/{slug}`, render with server-side escaping, avoid booting the authenticated SPA, and redact internal source keys, lookup IDs, note paths, local filesystem paths, and protected app routes.
- **Display**: Public share pages now render the sanitized chat answer as Markdown-derived HTML without duplicating the raw Markdown summary excerpt, convert source URLs into real links, include source titles/summaries beside full original URL links, and clean trailing URL punctuation/backticks including `%60`-encoded backticks so copied links do not 404, while keeping raw HTML inert.
- **Schema/Tests**: Added DB-backed share storage with HMAC slugs derived from a local random share salt, plus migration and web auth boundary regression tests.
- **Location**: `internal/store/`, `web/`, `web/ui/`

### Authenticated Access Logging (2026-05-14)

- **Operations**: Web requests now emit app-layer access logs with the authenticated GitHub identity after session validation, which keeps Funnel traffic attributable even without Tailscale identity headers.
- **Operations**: MCP Streamable HTTP requests now emit app-layer access logs with Bearer token name/fingerprint metadata when token auth is enabled, without logging raw token secrets.
- **Location**: `web/`, `internal/mcpserver/`, `internal/store/`, `internal/remote/`

### Tailscale Funnel Toggle (2026-05-14)

- **Added**: Optional `--tsnet-funnel`, `tsnet.funnel`, and `DBRAIN_TSNET_FUNNEL` support for serving the existing built-in tsnet listener through Tailscale Funnel.
- **Behavior**: Funnel uses the same tsnet node identity, hostname, state directory, and Tailscale auth credentials as normal `serve remote`; it is a listener mode, not a separate feature set.
- **Hardening**: Funnel mode requires TLS and one of Tailscale's supported Funnel ports (`:443`, `:8443`, or `:10000`) and prints public-exposure warnings for web/MCP surfaces.
- **Docs**: Added Tailscale policy `nodeAttrs` examples for granting Funnel publishing to one user or a dbrain tag, and clarified that app auth still controls public access.
- **Docs**: Split detailed remote, tsnet, and Funnel operations into [TAILSCALE.md](TAILSCALE.md) and added a README documentation map.
- **Docs**: Split the detailed command index and command/task reference into [COMMANDS.md](COMMANDS.md) so the README stays shorter and easier to scan.
- **Location**: `internal/remote/`, `internal/app/`, [README.md](README.md), [COMMANDS.md](COMMANDS.md), [TAILSCALE.md](TAILSCALE.md), [MCP.md](MCP.md), [config.yaml.sample](config.yaml.sample)

### MCP Bearer Token Auth (2026-05-14)

- **Added**: Optional DB-backed Bearer-token auth for MCP Streamable HTTP endpoints behind `mcp.auth.enabled` / `DBRAIN_MCP_AUTH_ENABLED`.
- **CLI**: `dbrain auth mcp token add NAME` creates a one-time displayed MCP bearer token while storing only its SHA-256 hash and fingerprint in SQLite.
- **Hardening**: MCP HTTP requests now return `401 WWW-Authenticate: Bearer` when auth is enabled and the token is missing or invalid.
- **Operations**: HTTP and tsnet MCP startup now prints a loud warning when MCP is served without dbrain bearer-token auth, explicitly calling out Tailscale Funnel/public proxy exposure.
- **Location**: `internal/mcpserver/`, `internal/store/`, `internal/app/`, `internal/remote/`, `README.md`, `MCP.md`

### GitHub OAuth Web Login (2026-05-13)

- **Added**: Optional GitHub OAuth login for the web UI behind `auth.enabled`, preserving the existing no-login localhost/tailnet behavior by default.
- **Auth**: `dbrain auth github approve USERNAME` now stores approved GitHub web users in the local DB; first successful login binds the row to the GitHub numeric ID and profile fields for future sessions.
- **CLI**: `dbrain auth github list/remove` manage approved web users, and `dbrain auth mcp token list/revoke` manage MCP bearer token records without revealing raw token secrets.
- **Hardening**: Auth config validates the provider whitelist, requires a strong session signing key, requires HTTPS for non-localhost OAuth base URLs, and keeps `GITHUB_TOKEN` scoped to imports instead of web login.
- **Operations**: Web startup now logs whether auth is enabled or disabled, explicitly notes that web sessions are in-memory, and cleans expired in-memory sessions in the background.
- **Hardening**: Funnel web auth now rejects localhost/default `auth.base_url` so GitHub OAuth callbacks must use the public HTTPS origin.
- **Schema/Tests**: Added and repaired the `auth_users` schema migration, README/config/env docs, and focused store/web tests for provider validation, route protection, OAuth binding, and unapproved-user rejection.
- **Location**: `internal/app/`, `internal/store/`, `web/`, `README.md`, `config.yaml.sample`

### Category Vocabulary Automation (2026-05-10)

- **Added**: `dbrain categorize vocab` analyzes existing item/source tags, asks a local Ollama model for conservative `categories.yaml` cleanup suggestions, and can `--apply --repair` the resulting safe vocabulary changes.
- **Safety**: LLM vocabulary suggestions now pass through a hard filter that accepts lexical cleanup but rejects broad topical collapses before display or file updates.
- **Defaults**: `categorize vocab` now uses a smaller default token sample and a 5-minute local LLM timeout so unflagged runs fit local Ollama latency better.
- **Categories**: Added another round of high-confidence alias/drop cleanup from the current tag corpus and repaired existing local item/source `user_tags`.
- **Location**: `internal/app/`, `internal/categoryvocab/`, `categories.yaml`, `README.md`

### Search Lookup Robustness (2026-05-09)

- **Fixed**: `dbrain search` now resolves exact item/source lookup keys such as `src:...` before FTS, so operator-visible keys work the same way as `dbrain get`.
- **Search**: Multi-term searches that miss strict FTS and phrase fallback now get a relaxed FTS pass, helping queries with extra context still surface partial high-signal matches.
- **Location**: `internal/store/`

### Sync Run Lock (2026-05-09)

- **Fixed**: Manual `dbrain sync all` and scheduled `serve remote` sync runs now share a local lock, so one path fails or records a skipped run instead of overlapping the same pipeline.
- **Fixed**: Generated launchd plists now set a Homebrew-aware `PATH`, so scheduled background runs can find helper tools such as `ffprobe` and `mw` for X media transcription.
- **Fixed**: The web admin "Pending Summaries" card now uses the same model-agnostic summary coverage policy as `dbrain stats pipeline`, instead of treating valid summaries from another backend as pending work.
- **Status**: `dbrain tsnet status` now renders human output as readable tables and includes detailed scheduled `sync all` state from the remote web API when reachable.
- **Status**: Scheduled `sync all` timestamps in `dbrain tsnet status` now include relative hints such as `55 minutes ago` or `45 minutes from now`.
- **Added**: `dbrain doctor full-disk-access` diagnoses the launchd target binary, can probe Apple Notes through that binary, and opens macOS Full Disk Access settings for manual approval.
- **Added**: `dbrain launchd restart` now asks the restarted web service process to verify Full Disk Access and opens the approval pane when the upgraded background binary is not covered.
- **Docs**: Documented the scheduler lock path and the Apple Notes Full Disk Access/skip-stage choice for background runs.
- **Location**: `internal/app/`, `internal/runlock/`, `README.md`

### Feed Local Testing (2026-05-09)

- **Added**: `dbrain feed refresh FEED` fetches one feed and immediately enriches linked article sources, with `--force --summarize` for feed QA and reprocessing.
- **Added**: `dbrain feed add/check --allow-private-network` plus `feeds.allow_private_network` / `DBRAIN_FEEDS_ALLOW_PRIVATE_NETWORK` for explicitly testing localhost or private-network feeds while keeping public-IP-only fetching as the default.
- **Fixed**: Verify-only `feed add` no longer caches feed validators before importing entries or marks the feed not due, so the next `sync all` or `feed check` can materialize entries instead of reporting an unchanged feed with zero entries seen.
- **Fixed**: `dbrain feed check --force` now skips conditional feed request headers so it can really refetch and process an otherwise unchanged feed body.
- **Fixed**: Feed CLI and JSON output now redact Basic Auth passwords in feed URLs.
- **Changed**: Feed fetches now use the standard versioned `dbrain/<short-sha>` user agent, including `+dirty` for local modified builds, instead of the generic `dbrain feed importer` string.
- **Changed**: Feed entry content is now stored as local article text so linked feed sources can be summarized from the feed reader's parsed content before falling back to URL refetching.
- **Changed**: Sources linked from feed entries now fetch the linked article URL through dbrain's HTTP reader with Markdown `Accept` negotiation, merge explicit feed-entry context into the source summary/search text, and only fall back to local feed text or the external summarize CLI fetch path when URL fetch fails.
- **Location**: `internal/feedimport/`, `internal/app/`, `README.md`, `config.yaml.sample`

### Feed Ingestion (2026-05-08)

- **Added**: RSS, Atom, and JSON Feed subscriptions with `dbrain feed add/list/status/check/enable/disable`.
- **Sync**: `sync all` now checks due feeds by default, materializes feed entries as local items, links canonical article URLs into normal sources, and preserves raw feed fetch/entry data for later reprocessing.
- **Visibility**: Feed list/status output now shows due state, last check, next scheduled check, and recent errors; `sync all` explains configured feeds that are not due or are backing off instead of only saying none were checked.
- **Safety**: Feed fetch audit rows are stored separately from entry/item/source transactions, unchanged bodies skip entry processing, feed disappearance never deletes local memory, retryable failures use exponential backoff, and dead feeds require repeated terminal-looking failures over time.
- **Identity**: Feed entry fallback identity follows the documented Markdown/text/summary/title/enclosure order, GUID/link changes reuse existing entries, and GUID/link conflicts are surfaced without merging rows.
- **CLI**: `dbrain feed add` now stores a subscription by default; pass `--check` to import current entries immediately.
- **Location**: `internal/feedimport/`, `internal/store/`, `internal/app/`, `internal/syncjob/`, `web/`, `README.md`, `docs/feed-ingestion.md` (removed 2026-08-02; available in Git history)

### Scheduled Sync All (2026-05-08)

- **Added**: `serve remote` can now run `sync all` periodically from config via `scheduler.sync_all`, with optional startup runs, non-overlap protection, and live status at `/api/scheduler/sync-all`.
- **Config**: Added scheduler config/env documentation and sample config fields for interval, jitter, source limits, and common stage skips.
- **Location**: `internal/app/`, `internal/runtimeenv/`, `README.md`, `config.yaml.sample`

### Startup Preflight Checks (2026-05-08)

- **Added**: Startup now warns when the configured `categories.yaml` is missing so Homebrew installs do not silently categorize without canonical vocabulary rewrites.
- **Validation**: GitHub imports, R2/S3 archive paths, OpenRouter-backed OCR, and OpenRouter-backed categorization now fail early when their required secrets are missing.
- **Categories**: Added conservative vocabulary cleanup for duplicate tags found during repair/analyze.
- **Release workflow**: Publishing release assets no longer regenerates release notes when the release already exists, avoiding duplicated "What's Changed" sections.
- **Location**: `internal/app/`, `categories.yaml`, `README.md`, `.github/workflows/release.yaml`

### X Article Canonical URLs (2026-05-07)

- **Fixed**: Kept X article source canonical URLs on the stable `https://x.com/i/article/<id>` route instead of rewriting them to author-scoped article URLs that X may redirect to non-existent status pages.
- **Migration**: Existing `x_article` sources are repaired from their stored normalized URLs without reimporting or rehydrating data.
- **Location**: `internal/store/`

### Launchd Restart Command (2026-05-07)

- **Added**: `dbrain launchd restart` to restart the loaded background `serve remote` LaunchAgent without reinstalling the plist.
- **Location**: `internal/app/`, `README.md`

### Web Version Footer (2026-05-07)

- **Added**: The web interface now exposes the running release and commit SHA as unobtrusive footer links to the GitHub release tag and commit.
- **Location**: `web/`, `web/ui/`

### Release Web Asset Build (2026-05-07)

- **Fixed**: The GitHub release workflow now rebuilds the Svelte web UI before compiling release binaries, ensuring embedded web assets match the tagged source.
- **PR guard**: Pull request CI now rebuilds the web UI and fails if `web/ui/dist` is stale.
- **Location**: `.github/workflows/release.yaml`, `.github/workflows/pr-ci.yaml`, `docs/release-build.md`

### Model Bakeoff Devtool (2026-05-07)

- **Added**: A read-only `cmd/devtools/model_bakeoff` tool for comparing source-summary, item-categorization, and source-categorization models side by side without saving derived state.
- **Skill**: Added a shareable Codex skill that documents the model bakeoff workflow for future agent runs.
- **Location**: `cmd/devtools/model_bakeoff/`, `internal/modelbakeoff/`, `internal/sourceenrich/`, `README.md`, `skills/dbrain-model-bakeoff/`

### Launchd Service Command (2026-05-06)

- **macOS background service**: Added `dbrain launchd` commands to print, install, and uninstall a per-user launchd service that runs `dbrain serve remote` with the active config layout.
- **Config-file selector**: Added global `--config-file` / `DBRAIN_CONFIG_FILE` support so installed services can pin `~/.config/dbrain/config.yaml` while ignoring checkout-local `DBRAIN_ROOT` from direnv.
- **Location**: `internal/config/`, `internal/runtimeenv/`, `internal/app/`, `README.md`, `config.yaml.sample`

### Homebrew Tap Automation (2026-05-06)

- **Release automation**: The tag release workflow can now update `darron/homebrew-tap` after publishing release assets when `HOMEBREW_TAP_TOKEN` is configured.
- **Location**: `.github/workflows/release.yaml`, `docs/release-build.md`

### Secret References In Config (2026-05-06)

- **Keychain and 1Password config refs**: Secret-bearing config values for GitHub, OpenRouter/OpenAI/Ollama API keys, and R2/S3 credentials can now use `env:`, `op://`, or `keychain://` references that are resolved only by `dbrain`.
- **Location**: `internal/runtimeenv/`, `internal/app/`, `README.md`, `config.yaml.sample`

### Startup Schema Repair (2026-05-06)

- **Media schema repair**: Startup now repairs legacy or partially created `media_assets` columns before creating indexes that reference retry-state columns, so default installs with older local DBs do not fail during the baseline migration.
- **Location**: `internal/store/`

### Source Failure Activity Timestamps (2026-05-06)

- **Failure ordering**: Source extraction failures now honor `FetchedAt` when provided, matching successful extractions and keeping source activity ordering stable in CI and the web UI.
- **Location**: `internal/store/`, `web/`

### Homebrew Install Docs (2026-05-05)

- **Install instructions**: Added top-level README instructions for installing `dbrain` from the `darron/tap` Homebrew tap and verifying the installed binary.
- **Location**: `README.md`

### PR CI Workflow (2026-05-05)

- **Pull request gates**: Added a PR-only CI workflow that runs `task fmt`, verifies formatting produced no diff, then runs `task lint` and `task test` in order for branch protection.
- **Location**: `.github/workflows/pr-ci.yaml`

### PR Diff Stats Workflow (2026-05-05)

- **Pull request sizing**: Added a PR diff stats workflow adapted for `dbrain`, with sticky same-repo PR comments and bucket rules for docs, tests, schema files, generated/runtime surfaces, lockfiles, packaging, skills, and CI/config changes.
- **Location**: `.github/workflows/pr-diff-stats.yaml`

### Agent Testing Guidance (2026-05-05)

- **CI-safe tests**: Updated `AGENTS.md` to require regression tests to fake or skip local-only dependencies such as browser profiles, helper tools, model services, network access, and OS-specific paths so GitHub Actions remains reliable.
- **Location**: `AGENTS.md`

### Source Summary Blocking (2026-05-05)

- **Empty extract summaries**: Stored empty source extracts are now converted to blocked summaries without falling through to live re-extraction, preventing retryable summary errors and requeue loops.
- **Location**: `internal/sourceenrich/`

### Release Workflow (2026-05-05)

- **GitHub release action**: Added the release workflow under `.github/workflows/`, using `task lint`, `task test`, and `task build`; release archives are named for `dbrain` and include README, license, and third-party notices.
- **Location**: `.github/workflows/release.yaml`

### Startup Visibility (2026-05-05)

- **Version banner**: `sync all` and serve startup paths now print the running short commit, status, and build platform, plus `release=<tag>` only for tag/release builds.
- **Release metadata**: `task build` now injects tag-derived `release_version` metadata instead of the local `git --version` tool version.
- **Migration progress**: Writable startup paths now emit schema migration running/applied lines when missing SQLite migrations are applied during startup.
- **Location**: `internal/startuplog/`, `internal/store/`, `internal/app/`, `internal/remote/`, `web/`

### Architecture Documentation (2026-05-05)

- **Current architecture guide**: Added `docs/architecture.md` as the concise reader-facing package/state architecture guide and linked it from the README.
- **Schema migration policy**: Added `docs/schema-migrations.md` to document SQLite migration behavior, backup/restore expectations, and the supported downgrade path.
- **Maintenance operation audit**: Added `docs/maintenance-operations.md` to document local delete, purge, prune, restore, and reset paths, including the narrow YouTube legacy cleanup exception that can run during `sync all`.
- **Release build notes**: Added `docs/release-build.md` to document tracked embedded web assets, when `task web-build` is required, and how to avoid stale `web/ui/dist` assets in Go releases.
- **Cleanup tracker reset**: Updated the cleanup tracker to separate actual remaining release work from completed splits, stale license-audit items, and design backlog so future work had a concrete stop condition.
- **Location**: `docs/architecture.md`, `docs/schema-migrations.md`, `docs/maintenance-operations.md`, `docs/release-build.md`, `docs/architecture-cleanup.md` (removed 2026-08-02; available in Git history), `README.md`

### Architecture Cleanup And Open-Source Readiness (2026-05-04)

- **Source FTS reliability**: Source FTS delete/insert failures now propagate instead of silently succeeding, with regression tests around source tag reindexing failures.
- **Source predicate guardrails**: Source enrichment worker selection, backlog counts, and pipeline pending counts now share a named source-enrichment predicate policy, with regression coverage for retry cooldowns and summary staleness.
- **Source categorization evidence gate**: Batch and sync source categorization now only queues sources with extracted text or summary evidence, so failed or metadata-only sources are not auto-tagged from URL/title context alone.
- **Source tag cleanup**: `dbrain categorize repair --clear-source-tags-without-evidence` can clear previously generated source tags on rows without extracted or summarized evidence, with `--dry-run` preview support.
- **Source status constants**: Source extract statuses, summary statuses, and failure-kind strings now have shared model constants used by source enrichment, source activity reporting, and core store policy paths, reducing raw-string drift during retry/stat refactors.
- **Pipeline kind constants**: Pipeline aggregate and item-level stage kinds now use named store constants at row assembly points.
- **Item enrichment constants**: Item summary, OCR, X media transcript statuses, and the synthesized X media transcript marker now use shared model constants across worker persistence, stats, candidate selectors, source local-extract policy, and media archive gating.
- **Media status constants**: Media download/archive status values now use shared model constants across download policy, archive/prune selectors, pipeline stats, and note rendering while preserving the existing SQLite values.
- **Source enrichment fallback guardrails**: Added process-order regression tests for stored-extract-before-reader and terminal-preflight-before-reader fallback behavior, and bundled per-source process inputs to reduce fallback-flow argument sprawl.
- **Sync stage option grouping**: `syncjob` now projects the flat command options into grouped per-stage option structs before orchestration, keeping existing CLI/caller behavior while preparing the explicit stage-plan refactor.
- **Sync stage plan**: `sync all` now runs through an explicit ordered internal stage plan with stage IDs, ordering metadata, enabled predicates, run functions, and plan-order regression tests.
- **Sync summary alignment**: The `sync all` summary table now right-aligns the Duration column for easier scanning.
- **Projection renderer boundary**: Added an internal renderer helper for synchronous item/source note refreshes and moved direct production vault rendering callers behind it so notes are projected from stored DB state after writes.
- **Retrieval DTO boundary**: Added `internal/retrieval` for shared evidence-document, content-section, related-document, and retrieval-signal DTOs, with `ask` and MCP using aliases/typed related payloads while web detail/tag responses now use explicit DTOs that omit unused raw JSON and internal diagnostic fields.
- **Item enrichment mirror**: Added a versioned `item_enrichments` current-state table for item summaries, OCR, and X media transcripts, with migration backfill, dual-writes from existing compatibility columns, point item reads that prefer the mirror with compatibility fallback, plus FTS indexing/search snippets, item-level pipeline stats, and enrichment candidate selectors that hydrate from the mirror.
- **Source rate-limit cooldowns**: Source extraction failures with HTTP 429 are now classified as `rate_limited` and stay on the normal retry cooldown instead of being treated as unknown failures that can immediately requeue for a final attempt.
- **X media transcription stats**: Pipeline stats now classify untranscribed but locally pruned archived X videos as blocked instead of pending, matching the media transcription worker's runnable-media selector.
- **Sync limits**: `dbrain sync all` now supports separate `--x-media-limit` and `--x-photo-ocr-limit` controls while preserving `--x-limit` as the default fallback.
- **Research temp files**: Brain research planner and synthesis inputs now use the configured dbrain temp directory instead of the process temp directory.
- **Chat follow-up retrieval**: Chat retrieval now treats prior evidence title/metadata/source-key sections as synthesis context instead of primary search terms, preventing follow-ups such as "what about Litestream?" from being crowded out by stale prior Marmot/API/Safari title hints.
- **Search reopen FTS**: Reopening an already-migrated database now refreshes FTS availability, restoring multi-term item/source searches such as `litestream sqlite replication` instead of silently falling back to exact-phrase `LIKE` matching.
- **MCP metadata**: MCP initialize responses now use build-derived dbrain version metadata instead of a hardcoded server version.
- **Summarize provenance**: Failed `summarize` version probes are no longer cached as empty tool versions, preventing later valid probes from losing summary provenance in long test or agent runs.
- **Web/MCP/store/source/retrieval structure**: Split the web server into focused route files, separated chat transcript HTTP handling from Markdown/research-pack/evidence helper rendering, and separated archive media handlers from S3 proxy, archived-asset lookup/URL, and response header/error helpers, separated MCP transport/protocol/tools/tool-family handlers/result-formatting/filtering/schema families/resource catalogs/resource readers/prompt handlers/helpers and eval case/retrieval/report helpers, moved store open/schema/schema-init/search/search-scan/tag-search/search-count/predicate/item-read/item-scan/item-write/item-write-enrichment/time-helper/item-enrichment/item-FTS/source-enrichment/source-failure/X-hydration/X-media-transcription/media/media-archive/stats/stats-pipeline/pipeline-X-media-OCR/source-activity-scan/source-activity-SQL and source schema/link/enrichment/lookup/evidence/relation/tag/search/repair code into focused files, split runtime environment scalar/bool/list/env-file/YAML config helpers, split link extraction candidate collection/URL normalization/source classification helpers, split X hydration orchestration from fetch policy, quote-tree persistence, client/cookie handling, GraphQL and syndication fetch paths, TweetResult request metadata, GraphQL/syndication snapshot parsing, and X bookmark GraphQL fetch/parse/item helpers, extracted source-enrichment options, workers, persistence, summary execution/prompt/policy/content-policy/media-policy, failure persistence/classification/preflight, extract-validation, YouTube fallback/transcriber helpers, process/fallback flow, HTTP reader, Wayback, Sucuri protected fetch, WordPress recovery, and HTML extraction code, split syncjob types/options/progress/merge/X-frontier/stage-execution/runner-hook helpers, split ask query hints/evidence/scoring/prompt/entity/entity-scoring/excerpt/excerpt-window helpers, split brain research pack types/strategy/concept/variant/evidence ranking/scoring/topic/coverage/exact-tag/next-step helpers, planner parse/sanitize/merge helpers, and synthesis run/prompt-input/budget/evidence-format helpers, split MCP get payload/section/evidence-window/related/format helpers, split MCP HTTP lifecycle/POST handling from path/origin helpers, split MCP research pack building from readable formatting, split remote serve orchestration from handler assembly, listen/error helpers, tsnet node, identity logging, and URL rendering helpers, split app-level tsnet status/reset command wiring from status/probe/cert/status-health/status-types/flag/reset/probe-URL/IP-lookup/HTTP-TLS helpers, split Apple Notes reader row-loading/document/attachment-row/attachment-path helpers plus run-setup/run-progress/run-apply/snapshot/attachment helpers from orchestration and decode flows, split entity indexing derivation/relationships/identity-token/builder/path helpers, split topic map graph/entity-scoring/pivot/format/type/synthesis and topic signal phrase/stopword helpers, split vault item rendering/item-frontmatter/item-source sections/media/quoted-post/entity-write/entity-render/topic rendering/topic index/frontmatter/YAML/text/option helpers, split GitHub importer transport/item/source/extract helpers, split YouTube importer feed/process cleanup/item/enrichment/browser helpers, split Safari tabs run/query/item/device/progress/snapshot-DB and app output helpers, split X photo OCR orchestration/persistence/provider/options/helpers and compare sample/input/run/scoring/report helpers, split restore-pruned media devtool query/loading helpers, split X media transcription option/media/audio-command/transcript/summary-input/summary-error/persistence helpers, split media download policy/HTTP/path helpers, split SQLite archive archive/restore/SQLite/file/progress helpers, split media archive option/archive-result/prune/note-refresh helpers, split summarize CLI direct-provider/direct-input/direct-target/direct-response/command/version/env helpers, split item categorization batch/content/photo/LLM-transport/LLM-response/config/tag helpers, and moved sync flag binding, sync env resolution, option assembly, summary rendering, stats command bodies/output rendering, stats pipeline output rendering, sync UI formatting/stage animation/log-line mechanics, topic map/generate/refresh/index helpers, research output rendering, SQLite archive/restore command helpers, Apple Notes import output/debug subcommands, serve MCP/remote/web wiring, repair source reset command flow, source categorization command wiring, and categorize analysis counting/draft helpers out of larger command bodies without changing route, tool, store, sync, retrieval, X hydration, remote serving, archive, or enrichment behavior.
- **Schema migrations**: Added a recorded SQLite baseline migration runner with deterministic column backfills, `schema_migrations`, `PRAGMA user_version`, and tests for fresh create, idempotent reopen, existing current-schema adoption, and read-only opens.
- **Open-source review**: Added architecture cleanup, current web route capability, and dependency-license review docs for pre-release cleanup planning.
- **Open-source safety docs**: README now calls out private local state, import-only upstream behavior, read/write web and remote trust boundaries, model-call surfaces, archive exposure, and local delete/reset maintenance paths before the command reference.
- **Web/MCP payload privacy**: Web bootstrap, item/detail media, chat transcript save, note-read error, signed media URL, and MCP search/get/resource responses no longer expose root/vault/DB paths, absolute transcript paths, archive bucket/key values, or local media source paths.
- **Sync X frontier settle**: `sync all` no longer repeats bounded X settle passes just because the same unchanged hydrated post keeps retrying a failed media download or a link-discovery scan queues no sources.
- **X media download retries**: failed X media downloads now keep retry metadata, back off for 24 hours between attempts, and become terminal `blocked` rows after 3 consecutive failures instead of retrying on every `sync all`.
- **Sync summary timing**: sync summary durations now render as seconds with 2 decimal places instead of full Go duration precision.
- **Large X media downloads**: X media downloads now use a separate 30-minute per-file timeout via `hydrate x --media-timeout` and `sync all --x-media-download-timeout`, and large downloads emit byte/percent progress logs while preserving the highest-quality media variant.
- **Project license**: Added the MIT root license, README license section, and `THIRD_PARTY_NOTICES.md`; updated the license review to keep clean-checkout audits and release-archive license-file handling as the remaining publication tasks.
- **Location**: `internal/store/`, `internal/sourceenrich/`, `internal/runtimeenv/`, `internal/linkextract/`, `internal/app/sync.go`, `internal/syncjob/`, `internal/xapi/`, `internal/ask/`, `internal/brainresearch/`, `internal/remote/`, `internal/entities/`, `internal/topics/`, `internal/vault/`, `internal/githubimport/`, `internal/youtubeimport/`, `internal/safaritabs/`, `internal/xphotoocr/`, `internal/xmediatranscribe/`, `internal/sqlitearchive/`, `internal/mediaarchive/`, `internal/summarizecli/`, `internal/itemcategorize/`, `internal/mcpserver/`, `internal/mcpeval/`, `cmd/devtools/restore_pruned_pending_x_media/`, `web/`, `README.md`, `docs/`

### OCR Model Comparison Devtool (2026-05-02)

- **Read-only OCR bakeoff**: Added `cmd/devtools/ocr_model_compare` to sample downloaded X photos, run the configured OCR model beside candidates such as `ollama/deepseek-ocr:3b`, and write Markdown/JSON reports with timings, output sizes, errors, and baseline word-overlap signals without changing stored OCR state; `--download-missing` can fetch pruned corpus images into temp files for the audit only.
- **Location**: `cmd/devtools/ocr_model_compare/`, `internal/xphotoocr/`, `README.md`

### Brain Research Pack Surfaces (2026-04-30)

- **Shared research core**: Added `internal/brainresearch` so MCP, web, and CLI research flows share one retrieval-pack builder with query/tag plans, exact-tag evidence, corpus coverage, semantic next steps, and optional topic briefs.
- **CLI and web**: Added `dbrain research` and `/api/research`; the web Explore page now uses a Research tab for evidence packs.
- **Local synthesis**: Added `/api/research/synthesize` as an SSE endpoint plus default-on web and CLI synthesis over research packs, with `--retrieval-only` for evidence-only CLI runs and explicit model/config checks to avoid silent hosted fallback.
- **Accuracy framing**: Research synthesis and MCP prompts now frame the corpus as intentionally selective while prioritizing factual accuracy, source-claim separation, and explicit uncertainty over performative objectivity.
- **Citation navigation**: Research synthesis now turns both bracketed citations and bare source IDs in generated source lists into clickable detail lookups.
- **Citation key handling**: Research citation links now preserve colon-delimited IDs such as `src:apple-note:default:<id>` and `src:rcmp:<id>` instead of linking only the first segment.
- **Citation prompt**: Research synthesis now tells local models to cite exact source keys from the research pack, including `apple-note:*` keys, instead of inventing or shortening prefixes.
- **Citation lookup repair**: Research citation clicks normalize common model-prefixed forms such as `src:apple-note:*` and `src:src:*` back to real dbrain lookup keys.
- **Apple Notes detail view**: Apple Note details now show the full decoded note body inside dbrain, keep indexed attachment text separate, and no longer offer a broken `apple-notes://` external open link.
- **Research UI flow**: Research now keeps typing as a draft until explicit submit, removes the graph/list explorer from the Research view, and shows compact clickable evidence below the synthesis instead.
- **Mobile Research layout**: The Explore Research view now uses normal page scrolling on phones, keeps controls compact, stacks the detail panel below results, auto-scrolls selected evidence into view, and wraps long source-key/citation chips so mobile browsers do not overflow horizontally.
- **Browser-session chat**: Added a Chat mode to the web Explore page that runs a fresh local research/synthesis turn for each follow-up, persists only in browser `sessionStorage`, reuses prior evidence context, never treats previous model answers as evidence, and keeps per-turn evidence collapsed while source-key citations expose inline pin controls.
- **Chat retrieval tuning**: Follow-up chat retrieval now carries compact prior evidence titles as query-focus context while keeping full prior evidence only for synthesis, improving clarified searches without letting broad prior tags or summaries dominate retrieval.
- **Chat query repair**: Chat retrieval no longer injects raw prior source keys into the search text, only adds prior questions/evidence metadata for likely follow-ups, avoids anchoring corrective turns to prior bad evidence, and treats escaped newlines, punctuation, source-key fragments, common inflections, and words such as "kids" as normalized retrieval terms.
- **Research harness**: Research packs now expose query variants and required concept groups, use the configured local model by default for bounded query planning, run the variants through retrieval, and rerank merged evidence by concept coverage so ambiguous factual searches prefer directly matching source clusters over broad near-misses; deterministic planning remains the fallback and explicit opt-out path.
- **Research latency**: Research coverage counts now use FTS-backed indexes when available, variant retrieval skips repeated broad tag-table scans, and evidence packing prefers stored summaries/snippets over raw extracts so web Chat reaches local synthesis instead of timing out on large source bodies.
- **Research fallback cleanup**: Deterministic query planning now strips corpus-framing phrases like "in my research", modal preference words like "should/favored", and plural `models` noise so planner timeouts fall back to cleaner searches such as `model hermes agent`, with extra model-stack/model-name variants for agent model-selection questions.
- **Chat transcript export**: Web Chat can save the current browser-session conversation, retrieval questions, citations, pins, and research packs as a local non-indexed Markdown diagnostic transcript under `data/chat-transcripts/` for later prompt/retrieval review.
- **Ask removal**: Removed the old `dbrain ask`, `/api/ask`, and `dbrain_ask` MCP surfaces instead of preserving aliases.
- **Regression coverage**: Added tests for source exact-tag evidence, `/api/research`, research synthesis budgeting/SSE behavior, removed Ask routes/tools, and a source-enrichment progress logger race.
- **Location**: `internal/brainresearch/`, `internal/queryterms/`, `internal/app/`, `internal/mcpserver/`, `web/`, `README.md`, `skills/dbrain-mcp/`

### Apple Notes Materialized Import (2026-04-30)

- **Import command**: Added `dbrain import apple-notes` with read-only DB/WAL/SHM snapshotting, schema probing, default materialization, `--dry-run` preview mode, opt-out folder/account/shared-note exclusions, and `[[dbrain-ignore]]` support.
- **Materialization**: Apple Notes are imported as `apple_note` items, rendered to Markdown, included in search, and can be locally summarized with an Apple Notes-specific prompt; note URLs and URL attachments feed normal source discovery, and summaries are skipped on unchanged content unless `--force` is used.
- **Attachments**: The importer indexes attachment metadata and Notes-provided attachment text, extracts supported text/PDF attachment files locally, and OCRs image attachments through optional local `tesseract`; unsupported, missing, or oversized files are marked blocked.
- **Safety**: The importer opens copied snapshots instead of live Apple Notes files, skips password-protected notes by default, explains macOS Full Disk Access failures, and supports explicit `--forget-excluded` purging for notes that become excluded.
- **Sync integration**: `dbrain sync all --apple-notes` or `DBRAIN_APPLE_NOTES_ENABLED=true` includes configured Apple Notes import before link extraction/source work.
- **Operator feedback**: Apple Notes imports now print per-note progress only for notes that need work; unchanged-current rows are counted in final stats without spamming output, and applied `--limit` batches skip unchanged-current notes so repeated limited runs advance to new or stale work.
- **Summary default**: Standalone `dbrain import apple-notes` now summarizes by default; use `--summarize=false` for materialization-only imports.
- **Pipeline stats**: `dbrain stats pipeline` now includes Apple Notes materialization and item-summary coverage so imported notes are visible alongside source extraction and media summary stages, and aggregate `ALL` rows include appended item-level stages.
- **Summary prompt**: The Apple Notes summary prompt now labels note shape, such as authored notes, research link lists, checklists, logs, scratchpads, or mixed notes, so rough lists are not overread as polished prose.
- **Docs**: README and `config.yaml.sample` document Apple Notes config/env keys, command usage, and the Full Disk Access requirement.
- **Location**: `internal/applenotes/`, `internal/app/import_apple_notes.go`, `internal/syncjob/`, `internal/store/`, `README.md`, `config.yaml.sample`

### Safari Tabs Import (2026-05-01)

- **Import command**: Added `dbrain import safari-tabs --device <name-or-uuid>` to snapshot Safari's local `CloudTabs.db` read-only and materialize iCloud tabs from a targeted device as `safari_tab` items.
- **Device review**: Added `dbrain import safari-tabs devices` to list visible Safari iCloud tab devices and tab counts before importing.
- **Link pipeline**: Safari tab items now feed normal link discovery, source extraction, summaries, rendering, and categorization without mutating or closing upstream Safari tabs.
- **Stats**: `dbrain stats pipeline` now reports Safari tab item materialization in the Extraction table while linked pages remain counted under their normal source types.
- **Sync integration**: `dbrain sync all --safari-tabs --safari-tabs-device <device>` or `DBRAIN_SAFARI_TABS_ENABLED=true` plus `DBRAIN_SAFARI_TABS_DEVICE=<device>` includes configured Safari tabs import before link extraction/source work.
- **Operator feedback**: Safari sync summaries now separate created, updated, unchanged, rendered, skipped, and linked rows so unchanged `CloudTabs.db` snapshots do not look like new imports every run.
- **Filters**: The importer supports `--older-than`, `--limit`, `--dry-run`, and `--show-titles` so large tab backlogs can be imported or previewed safely.
- **Location**: `internal/safaritabs/`, `internal/app/import_safari_tabs.go`, `internal/store/`

### Source Retry Controls And Failure Classification (2026-04-30)

- **Retry targeting**: `dbrain repair sources` can now filter by source type, extract status, summary status, failure kind, and minimum failure count before resetting enrichment state.
- **X article repair**: `dbrain repair sources --rehydrate-x-articles` also clears linked X item hydration markers so bad cached article previews are rebuilt by the next `hydrate x` / `sync all` run.
- **Failure accounting**: Source failure counts and first/last failure timestamps remain visible through source JSON/MCP, and rendered source notes now show extract failure count/kind metadata.
- **Terminal classification**: Repeated access-denied, timeout, unsupported-file, and generic fetch failures now become terminal `dead` source extraction outcomes after defined thresholds instead of retrying indefinitely.
- **Wayback fallback**: Repeated source extraction failures now check the Internet Archive Wayback Availability API before terminalizing, log both checks and misses, and final-attempt rows bypass the normal retry cooldown so unclassified failures stop after 5 attempts when no archive recovery succeeds.
- **Wayback quality gate**: Very short Wayback extracts and archive/browser shells now keep raw extracted text but skip summarization instead of generating plausible summaries from weak evidence.
- **Extraction throughput**: Standalone `extract links` and `extract sources` now default to four concurrent source extract/summarize jobs, matching `sync all` and `worker sources`, so one slow URL does not serialize the whole batch.
- **Failure metadata**: Consecutive failure counts are now preserved when a retry changes from an older `unknown` class into a more specific terminal class.
- **Placeholder repair loop**: Short redirect/loading placeholder extracts selected for summary repair are now marked `skipped` instead of being repeatedly summarized as successful work.
- **Summary timeout loop**: Source summary timeouts and context-limit failures now persist as `blocked` instead of retryable `error`, so oversized stored extracts do not hot-loop in `worker sources` or `sync all`.
- **Media extract guard**: Direct image/video/audio/archive URLs and binary-looking extracts are now marked `skipped` for text summarization, preventing Wayback image captures from being repeatedly sent to local LLM summarization.
- **Docs**: README now documents the failed web-source rebaseline flow using `repair sources` plus source extraction/sync retries.
- **Location**: `internal/store/`, `internal/sourceenrich/`, `internal/app/repair.go`, `internal/vault/source.go`, `README.md`

### Sync Source Categorization (2026-04-30)

- **Sync pipeline**: `dbrain sync all` now categorizes uncategorized linked sources as well as items before the media archive stage.
- **Visibility**: Sync progress, debug logs, JSON stats, and the summary table now separate item and source categorization counts while preserving aggregate categorize totals.
- **Docs**: Updated sync and categorization command docs to describe the source apply path.
- **Location**: `internal/syncjob/`, `internal/app/sync.go`, `README.md`

### Source-Level Tags And Categorization (2026-04-29)

- **Source tags**: Added first-class `sources.user_tags` so linked articles, repos, videos, and papers can be categorized independently from the saved items that reference them.
- **Categorization**: Added `dbrain categorize source` and `dbrain categorize sources`; `categorize repair` and `categorize analyze` now include both item and source tags.
- **Retrieval**: Source tags are indexed for search, included in exact-tag matching/counts, returned through MCP evidence, and visible/editable on source detail pages in the web UI.
- **Docs and skill**: Updated README, MCP docs, and the dbrain MCP skill to distinguish source tags from backlink item tags.
- **Location**: `internal/store/`, `internal/itemcategorize/`, `internal/app/`, `internal/ask/`, `internal/mcpserver/`, `web/`, `MCP.md`, `README.md`

### Built-In tsnet Remote Serving (2026-04-29)

- **Remote web and MCP**: Added `dbrain serve remote` to expose the existing read/write web UI and read-only MCP endpoint from one built-in Tailscale `tsnet` node.
- **MCP compatibility**: Added `dbrain serve mcp --transport tsnet` for MCP-only tailnet serving while keeping stdio as the default local-agent transport.
- **State safety**: Added durable tsnet state under `<data_dir>/tsnet/<hostname>`, 0700 state-dir validation, advisory locking, sync-folder warnings, and guarded `dbrain tsnet status` / `dbrain tsnet reset` commands.
- **Status health**: `dbrain tsnet status` now reports active running/reachable health, probes only configured web/MCP surfaces with surface-specific status handling, respects non-default listen ports, reports `control_url`, checks certificate health, and exposes machine-readable JSON fields for automation.
- **Security**: Added typed bootstrap secret refs (`env:`, `op://`, `keychain://`), YAML-only command escape hatch with explicit opt-in, remote-only Origin checks, security headers, first-run auth URL logging, custom-control warnings, and best-effort Tailscale identity logging through `WhoIs`.
- **Remote hardening**: Added typed tsnet lock errors, Unix/Windows lock separation with a clear Windows unsupported path, injected request-log output, short-TTL cached `WhoIs` identity logging, bounded remote HTTP timeouts, and a `serve mcp --transport tsnet --mcp-path` alias.
- **Operator UX**: Browser `GET /mcp` now returns a clear JSON-RPC POST diagnostic, repeated tsnet auth log lines are deduped while preserving the login URL, and `tsnet reset` requires a literal `reset` confirmation unless `--yes` is used.
- **Docs and skill**: Expanded MCP docs and the dbrain MCP skill with built-in tailnet serving examples, remote Streamable HTTP client config shapes, first-run auth/state guidance, and `tsnet status` troubleshooting.
- **Regression coverage**: Added fake tsnet orchestration tests for startup timeout, prepared state-dir use, listen mode selection, startup unwind, shutdown order, concurrent user logs, and locked status/reset behavior.
- **Location**: `internal/remote/`, `internal/app/serve.go`, `internal/app/tsnet.go`, `config.yaml.sample`, `docs/tsnet-transport.md` (removed 2026-08-02; available in Git history), `TAILSCALE.md`, `README.md`

### Streamable HTTP MCP Transport (2026-04-29)

- **MCP transport**: Added `dbrain serve mcp --transport http` as a parallel stateless Streamable HTTP transport while keeping stdio as the default local-agent path.
- **Remote access**: Documented the Tailscale Serve pattern for exposing the localhost MCP HTTP endpoint to remote agents without granting SSH access.
- **Security**: HTTP mode binds to `127.0.0.1:8743` by default, rejects untrusted browser `Origin` headers, and stays read-only with no MCP session state.
- **Location**: `internal/mcpserver/`, `internal/app/serve.go`, `MCP.md`, `README.md`, `skills/dbrain-mcp/SKILL.md`

### Legacy Import Cleanup And MCP Docs Split (2026-04-28)

- **Removed**: Dropped the obsolete `dbrain import ft` command and the legacy FT importer package.
- **Source cache naming**: Cached item article text used for source extraction is now recorded as `item-cache` instead of `ft-bookmarks`.
- **API attribution**: Outbound API calls now send a versioned `User-Agent` header such as `dbrain/<short-sha>`; override with `DBRAIN_USER_AGENT` or `http.user_agent`.
- **Docs**: Split detailed MCP agent guidance into `MCP.md`, kept README focused on requirements and command reference, and moved examples under their command sections.
- **Location**: `internal/app/`, `internal/version/`, `internal/summarizecli/`, `internal/itemcategorize/`, `internal/xphotoocr/`, `internal/githubimport/`, `internal/store/`, `internal/linkextract/`, `MCP.md`, `README.md`

### Configuration Layout, Config File Support, And Env Documentation (2026-04-28)

- **Added**: Default installed layout now uses `~/.config/dbrain` for config and categories, and `~/.local/share/dbrain` for SQLite data, vault files, media, temp files, cache files, and logs.
- **Development override**: Added `DBRAIN_ROOT` as an environment-level equivalent to `--root` for local checkout development; explicit `--root` still wins.
- **Config resolution**: Runtime values now resolve from shell environment, then `.envrc` / `.env`, then `config.yaml`, with grouped YAML keys such as `openrouter.api_key`, `summary.model`, and `source.reader.base_url`.
- **Operator visibility**: Added `dbrain config paths` and `dbrain config env` so users and agents can inspect active storage locations and supported env/config keys directly from the CLI.
- **Help text**: All Cobra help screens now include an environment/config footer that points to `dbrain config env`; `config env` now renders a terminal-width-aware Charmbracelet table by default with `--markdown` for docs.
- **Module path**: Updated the Go module and build linker paths to `github.com/darron/dbrain`.
- **Repo hygiene**: Anchored generated-directory ignore rules so source packages such as `internal/vault` are no longer hidden by the root `vault/` ignore.
- **Docs**: Added `config.yaml.sample`, reorganized the README requirements/config/env/command sections, alphabetized the command index, documented dev tasks, and moved TODOs near the bottom with completed MCP work marked.
- **Contributor workflow**: Updated `AGENTS.md` to require changelog entries for features, fixes, CLI/config changes, pipeline changes, MCP/tooling changes, and other user-visible behavior changes.
- **Verification**: `go test ./internal/summarizecli ./internal/runtimeenv ./internal/app`
- **Location**: `internal/config/`, `internal/runtimeenv/`, `internal/app/`, `internal/summarizecli/`, `config.yaml.sample`, `README.md`

### MCP Retrieval Hardening And Agent Workflow Support (2026-04-28)

- **DB-first retrieval**: MCP detail reads now use DB-backed content modes by default instead of depending on rendered Markdown freshness.
- **Research workflow**: Added and tuned the MCP research pack path for broad corpus questions, including query plans, exact tag evidence, score explanations, topic brief integration, and follow-up suggestions.
- **Evidence quality**: Search and research now include user tags, hyphenated tag aliases, image OCR, media transcripts, linked-source context, source backlinks, and query-windowed excerpts.
- **Operational clarity**: MCP request logs are emitted on stderr so stdio protocol output stays clean, and the repo includes an agent skill at `skills/dbrain-mcp/SKILL.md`.
- **Regression coverage**: Added MCP eval and test support for retrieval behavior, tool advertisement, exact-tag evidence, and source-agnostic importer expectations.
- **Location**: `internal/mcp/`, `internal/mcpeval/`, `skills/dbrain-mcp/`, `README.md`, `Taskfile.yml`

### Sync Categorization And Media Ordering (2026-04-28)

- **Added**: `sync all` can categorize newly discovered uncategorized items after the normal import/enrichment stages, reusing the same apply path as `dbrain categorize batch`.
- **Tag behavior**: Categorizer output merges into existing `user_tags` without duplicates instead of replacing user-entered tags.
- **Image support**: Image categorization is enabled by default for categorization commands and `sync all`; pass `--images=false` or `--categorize-images=false` for text-only models.
- **Archive ordering**: Media archive/prune now runs after categorization so local photos remain available to vision-capable categorization before they are uploaded/pruned.
- **Location**: `internal/app/sync.go`, `internal/itemcategorize/`

### Reader Fallbacks For Difficult Government Domains (2026-04-27)

- **Added**: Known-problem source domains can be routed through a reader/textifier URL before summarize-backed extraction.
- **Default behavior**: `canada.ca` is routed through `https://r.jina.ai/` by default to avoid known long hangs and forced timeout kills.
- **Config knobs**: Added `DBRAIN_SOURCE_READER_DOMAINS` and `DBRAIN_SOURCE_READER_BASE_URL`, now also configurable through `source.reader.domains` and `source.reader.base_url`.
- **Fallback behavior**: If the reader service rejects a request, `dbrain` falls back to a local browser-style direct fetch and readable HTML extraction before summarization.
- **Location**: `internal/sourceenrich/`, `README.md`

### Version Command And Build Metadata (2026-04-27)

- **Added**: `dbrain version` prints commit, short commit, build time, git status, Go version, git version, build platform, module path, and module version.
- **Automation**: `--json` returns the same metadata in structured form.
- **Build wiring**: `task build` now passes git version and build platform values into the binary while preserving Go build VCS metadata.
- **Location**: `internal/version/`, `internal/app/`, `cmd/dbrain/`, `Taskfile.yml`

## Historical Changes From Git Commits

The sections below are generated from the committed history through
`35b7ea1`. They intentionally overlap with some Recent Improvements entries
above, but keep the commit-derived timeline visible.

### MCP Retrieval, Tags, Categorization, And UI Assets (2026-04-28)

- **UI assets** (`35b7ea1`): Refreshed built web UI asset artifacts.
- **Saved-item backlink tags** (`6113ee0`): Surfaced tags from saved item backlinks on source details, improving source-node context in MCP and the web UI.
- **Exact tag examples** (`5fb4360`): Added representative exact-tag evidence examples to MCP research packs and eval expectations.
- **Actionable MCP errors** (`e26675d`): Returned structured tool errors for missing lookups, unsupported modes, and unknown tools.
- **Deterministic MCP coverage** (`3d02a51`): Added deterministic retrieval test coverage and `task test-mcp` support.
- **Focused query ranking** (`b79252e`): Improved retrieval scoring so focused query terms are preferred over broad tag-only matches.
- **MCP evals and windowed evidence** (`7abef9c`): Added retrieval eval command support and query-windowed evidence excerpts.
- **Batch get and score signals** (`e35f98c`): Added `dbrain_get_many` plus retrieval score signals for agent follow-up workflows.
- **DB-backed get** (`65e209d`): Made `dbrain_get` DB-first with content modes, section caps, related context, and recall signals.
- **Tag-aware research packs** (`df253eb`): Made research packs read-only by default and tag-aware, including hyphenated tag aliases.
- **Image categorization default** (`5004ebf`): Enabled image categorization by default for categorization commands and sync.
- **Tag vocabulary repair** (`22a3019`): Added canonical category vocabulary loading, analyze, and repair commands.

### Sync Categorization, User Tags, Versioning, And Reader Fallbacks (2026-04-27)

- **Sync categorization** (`d7a3255`): Ran categorization at the end of `sync all`, including merged tag persistence and sync progress integration.
- **User tags and categorizer** (`d20e7f2`): Added `user_tags`, item categorization commands, model-backed category suggestions, and web detail tag display.
- **Version command** (`fa9bdb6`): Added CLI build metadata reporting with text and JSON output.
- **Reader fallback** (`af4aa59`): Added source reader fallback routing for difficult domains such as `canada.ca`, plus source repair support.
- **Local Ollama Modelfile** (`7331732`): Added the dbrain Ollama Modelfile for local model experimentation.
- **Web detail enrichments** (`83345f2`): Improved web detail views for video transcripts, image text, and direct URL results.
- **Full tweet search and web links** (`6c9f6b5`): Indexed full tweet text, added links inside tweet notes, and improved web frontend detail behavior.

### Web UI, Link Injection, Source Freshness, Sync UI, And Backups (2026-04-26)

- **Web UI fixes** (`9e131a4`): Cleaned up the web app and API behavior after the alternate interface pass.
- **Graph-oriented interface** (`a72a380`): Explored a richer web interface with graph visualization and redesigned detail/list panels.
- **Ollama vision OCR** (`db75b0c`): Added Ollama vision support for X photo OCR.
- **Model-agnostic summary freshness** (`c213b5a`): Made summary freshness checks policy-aware and model-agnostic by default.
- **Summary language defaults** (`b8e864d`): Defaulted summaries to English with env/config overrides and `auto` language mode support.
- **Manual link injection** (`f7d3731`): Added `dbrain link add` and web add-link support to submit URLs directly into the source backlog.
- **Sync progress UI** (`ca0f698`): Added terminal progress UI for `sync all`.
- **SQLite archive and restore** (`a4c34a3`): Added S3-compatible SQLite archive and restore commands with confirmation UI.
- **Frontier settling and hung process handling** (`30c54c7`): Settled X quote hydration before downstream media work and killed hung summarize subprocesses.

### X Quote Hydration, Media Archive, OCR, Native Bookmarks, And Policy-Aware Stats (2026-04-25)

- **Quote semantics docs** (`2805555`, `30e8945`, `c7ec0d7`): Clarified X quote hydration semantics, counters, and future work in README/AGENTS.
- **Quoted X posts and media repair** (`c8cefd1`): Hydrated quoted posts as first-class items, added quote links, and added a devtool to restore pruned pending X media.
- **Archived media proxy** (`998ff32`): Added web proxy and signed URLs for archived media.
- **Media archival tier** (`ae896f3`): Added S3-compatible media archive state, upload/mark/prune logic, and reference-aware archive tracking.
- **Invalid summary repair** (`83a9395`): Requeued invalid source summaries for repair.
- **Cancellation hardening** (`81ebad9`): Honored cancellation across enrichment pipelines.
- **Contributor rules** (`3afad6a`): Added contributor guidance for tests, models, content handling, and pipeline semantics.
- **X transcript summaries and photo OCR** (`2d831de`): Added item enrichment fields, transcript summaries, X photo OCR, and pipeline coverage stats.
- **Native X bookmark sync** (`b2a8e62`): Replaced the legacy FT-backed X import path with native cookie-backed X bookmark sync.
- **Policy-aware stats** (`7d58b8d`): Made summary freshness stats policy-aware by default so backend swaps do not make the corpus look stale.

### Direct Summaries And X Article Extraction (2026-04-24)

- **Direct OpenRouter summaries** (`3a305d0`): Added direct OpenRouter summary path and smarter note sync behavior.
- **X article downloads and protected fetches** (`b92edf2`): Improved X article downloads, protected source fetch handling, Ollama-backed summarization, source stats, and X media transcription.

### Web, MCP, HTTP Server, Media Downloads, And Query Surfaces (2026-04-22)

- **Web cleanup** (`92fda9b`): Added web interface fixes, operational panels, activity/backlog stats, and YouTube/source reliability improvements.
- **MCP and HTTP surfaces** (`068495c`): Added the first MCP server, web HTTP server, `ask`, entity/topic/query surfaces, media downloads, source worker loops, note repair, sync job orchestration, and Svelte UI scaffolding.

### Initial Import, Source Enrichment, YouTube, GitHub, And Stats Foundation (2026-04-20)

- **Initial scaffold** (`92476a2`): Added the Go CLI, SQLite store, FT import, source extraction, summarize integration, X API helpers, models, and tests.
- **Command split and YouTube import** (`35496c9`): Split app commands into dedicated files, added get/search/hydrate/import/extract commands, item hashing, source enrichment, and YouTube import.
- **GitHub stars and stats** (`d6bb415`): Added GitHub star import, retry handling, item/source stats, source enrichment improvements, and broader test coverage.
