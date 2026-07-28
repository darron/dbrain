# Semantic ANN Runtime Admission Verification

## Result

**Admitted for stacked PR 1 only.**

The runtime-admission implementation passed the standard untagged gates, the
complete uncached tagged race gate, and the representative-corpus runtime
checks on 2026-07-28. The normal tagged research command used USearch against
the evaluated 290,535-vector generation and returned evidence. The untagged
build remained free of a USearch dependency and preserved lexical retrieval.

The representative database SHA-256 remained byte-for-byte stable at the
current quiescent baseline:

```text
e292925fde8c1989eeefa33cc17644380e9e4f940c1420b89c36795971ce9b0f
```

This admits the runtime and capability slice. It does not complete automatic
semantic maintenance after sync, static release packaging, installed-binary
acceptance, or production activation.

## Tested Boundary

- Worktree:
  `/Users/darron/src/dbrain/.worktrees/semantic-ann-automatic-sync`
- Branch: `codex/semantic-ann-automatic-sync`
- Tested implementation SHA:
  `67e690fe0d335174f1824ffaee994585122d46a6`
- Copied database:
  `/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-data/dbrain/brain.db`
- Copied config:
  `/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-config/dbrain/config.yaml`
- Configured cache:
  `/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-data/dbrain/cache`
- Cache symlink target:
  `/private/tmp/dbrain-pr100-corpus.Co0jrU/ann-cache`
- Native development library:
  `/private/tmp/dbrain-usearch-v2.26.0-codex/extracted`

Before corpus status or research claims, the tagged branch binary resolved:

```json
{
  "cache_dir": "/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-data/dbrain/cache",
  "config_file": "/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-config/dbrain/config.yaml",
  "data_dir": "/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-data/dbrain",
  "database": "/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-data/dbrain/brain.db"
}
```

It did not resolve the production XDG database or the repository database.
No production command, activation, deployment, or merge occurred.

## Standard Untagged Gates

Fresh invocations on the final implementation tree all exited zero:

```text
$ task fmt
PASS: go fmt ./...

$ task lint
PASS: 0 issues.

$ task test-ci
PASS: go test -cover -race -timeout=20m ./...

$ task build
PASS: ./bin/dbrain rebuilt
```

The prescribed `task test-ci` target does not add `-count=1`; its fresh
invocation legitimately reused Go test cache entries. The separately required
tagged gate below used `-count=1`.

The rebuilt untagged binary was:

```text
./bin/dbrain: Mach-O 64-bit executable arm64
```

Its repository/dev status reported:

```json
"backend_capability": {
  "state": "unsupported"
}
```

`otool -L ./bin/dbrain` listed only:

```text
/usr/lib/libSystem.B.dylib
/usr/lib/libresolv.9.dylib
/System/Library/Frameworks/CoreFoundation.framework/Versions/A/CoreFoundation
/System/Library/Frameworks/Security.framework/Versions/A/Security
```

There was no USearch dependency.

## Complete Tagged Race Gate

The exact required command was:

```sh
env GOCACHE=/private/tmp/dbrain-final5-usearch-gocache \
  CGO_ENABLED=1 \
  CGO_CFLAGS="-I/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  CGO_LDFLAGS="-L/private/tmp/dbrain-usearch-v2.26.0-codex/extracted -lusearch_c" \
  DYLD_LIBRARY_PATH="/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  go test -count=1 -race -tags usearch -timeout=20m ./...
```

The first sandboxed run exited one because existing unrelated `httptest`
packages could not bind localhost. The repeated exact error was:

```text
httptest: failed to listen on a port: listen tcp6 [::1]:0: bind: operation not permitted
```

No package was filtered and no test was weakened. The identical complete
command was rerun outside the listener restriction on the final frozen code
and exited zero. The slowest package was:

```text
ok github.com/darron/dbrain/internal/store 315.339s
```

Both runs emitted the existing non-fatal warning:

```text
ld: warning: ignoring duplicate libraries: '-lusearch_c'
```

## Tagged Development Binary

The tagged macOS arm64 development binary was freshly rebuilt with the same
native flags and `GOCACHE=/private/tmp/dbrain-final5-usearch-gocache`.
The build exited zero and emitted the known duplicate-library warning. It also
reported a non-fatal sandbox denial while trying to persist a Go module stat
cache:

```text
go: writing stat cache: open /Users/darron/go/pkg/mod/cache/download/github.com/darron/dbrain/@v/v0.7.2-0.20260728004433-2d7ba53ce405.info443022718.tmp: operation not permitted
```

`file` confirmed:

```text
./bin/dbrain-usearch-dev: Mach-O 64-bit executable arm64
```

`otool -L` confirmed the intentional development dependency:

```text
@rpath/libusearch_c.dylib (compatibility version 0.0.0, current version 0.0.0)
```

This is a dynamic development gate. Static, checksum-verified Homebrew macOS
arm64 packaging remains later stacked work.

## Tagged Representative-Corpus Status

The final tagged status command exited zero and reported:

```text
status: ready
searchable: true
mode: on
backend_capability.state: supported_ready
backend_capability.backend: usearch
backend_capability.version: 2.26.0
profile_id: embedding-profile-v1:189c8da80d3bc59c63865d8a2988ee28d65982e1362442cc38ce1c8ed43912d3
active_generation_id: semantic-root-v1:949bcf9e01faa7ff71e8c69acd6a148b
active_generation_valid: true
active_indexed_count: 290000
l0_ready_count: 535
active_tombstones: 0
active_snapshot_revision: 18153
active_generation_backend: usearch
active_generation_backend_version: 2.26.0
active_generation_distance_metric: cosine
active_generation_dimensions: 768
active_generation_root_descriptor_sha256: 97eff245e02ceff7dfd45e0bcb980336e64ca89f81546e4215cc6cdf3fa3639d
chunk_count: 290535
ready_embeddings: 290535
pending_embeddings: 0
blocked_embeddings: 0
error_embeddings: 0
```

There were no reported problems or next steps.

## Normal Tagged Semantic Query

The required normal CLI query was run with `/usr/bin/time -l`:

```sh
env \
  XDG_CONFIG_HOME="/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-config" \
  XDG_DATA_HOME="/private/tmp/dbrain-pr100-corpus.Co0jrU/xdg-data" \
  DYLD_LIBRARY_PATH="/private/tmp/dbrain-usearch-v2.26.0-codex/extracted" \
  ./bin/dbrain-usearch-dev --no-debug research \
    "Apple silicon local model memory pressure" \
    --semantic --retrieval-only --no-planner --no-trace --json
```

The sandboxed attempt was rejected as admission evidence. It returned eight
lexical evidence results but reported:

```text
semantic lane: disabled
reason: provider_unavailable
backend: exact
```

Its timing wrapper then exited one after printing `58.67 real`, with:

```text
time: sysctl kern.clockrate: Operation not permitted
```

The final frozen-code command was run directly outside the localhost
restriction, with `DYLD_LIBRARY_PATH` supplied to the tagged binary rather than
through the SIP-protected `/usr/bin/time` wrapper. It exited zero in
approximately 48 seconds and reported:

```text
semantic lane: used
provider: ollama
backend: usearch
generation: semantic-root-v1:949bcf9e01faa7ff71e8c69acd6a148b
semantic_readiness: ready
evidence_count: 8
```

There was no exact-only or lexical-only fallback in the accepted run. The
earlier accepted resource-characterization run on this same copied corpus
reported:

```text
60.10 real
38.89 user
25.73 sys
1340833792 maximum resident set size
1269926240 peak memory footprint
0 swaps
```

This proves the final real normal tagged CLI reached the evaluated segmented
USearch generation. It does not prove automatic post-sync maintenance.

## Untagged Copied-Corpus Behavior

The untagged status command used the same copied XDG roots without
`DYLD_LIBRARY_PATH`. It exited zero and reported:

```text
status: unavailable
reason: native_backend_unsupported
searchable: false
backend_capability.state: unsupported
```

The binary has no USearch dependency, so this path made no native load attempt.

The untagged retrieval-only research command omitted `--semantic`, exited zero,
and reported:

```text
lexical lane: used
semantic lane: disabled
semantic reason: native_backend_unsupported
exact_tag lane: used
evidence_count: 8
```

Lexical retrieval remained usable against the same copied corpus.

## Original Checksum Failure And Root Cause

The first Task 6 run at
`cfaefd35085498e0005084671507e2aed788b82b` passed every functional runtime
assertion but failed the required database-byte gate:

```text
before: 6669cf0f398202781ea707a87169856258225eb977889ff2a216c8548d347980
after:  e292925fde8c1989eeefa33cc17644380e9e4f940c1420b89c36795971ce9b0f
```

The size remained 4,216,524,800 bytes. The failure was traced to the normal
research path opening the store with writable `store.Open`. Writable startup
selected WAL mode and ran migration startup, including an unconditional
`PRAGMA user_version = 24`, even though migration 24 was already present.

Subsequent read-only byte comparison proved that only eight SQLite header bytes
changed:

```text
offsets 24-27: 00000047 -> 0000004a
offsets 92-95: 00000047 -> 0000004a
decimal counters: 71 -> 74
```

Virtually restoring only those eight bytes reproduced the original
`6669cf0f…7980` SHA-256 exactly. No logical corpus, index, vector, or other
SQLite page changed. The representative database was not restored or edited;
`e292925f…b0f` is its current quiescent baseline.

## Task 7 Read-Only Correction

Task 7 changed normal research from writable `store.Open` to
`store.OpenReadOnly` in `internal/app/research.go` and added
`TestResearchCommandRetrievalOnlyDoesNotModifyDatabase` in
`internal/app/app_test.go`.

The regression test seeds a current database through public store APIs, closes
all writable handles, records its SHA-256, runs the real normal retrieval-only
command, and requires the same SHA-256 afterward. Mutation back to
`store.Open` reproduced the failure:

```text
research modified database bytes:
before=0876c63fb77df9f462714939802d045d7f2f30f8211d147d3453b710bb640467
after=497fd3baa5df4e1ea693533757c1ed233f151fe5b7bad105b0967cc74625648b
```

The final read-only implementation passed the focused regression, relevant
lexical and semantic app tests, readiness/admission tests, standard gates, and
tagged package race tests before commit
`9c5c68155fb976c72ef0aa9a342974bff9cbb019`.

Task 7 also established that SQLite `mode=ro` can leave a zero-byte WAL and a
32,768-byte SHM bookkeeping file for a WAL-mode database. Those sidecars do not
imply database-byte mutation and must not be deleted while authoritative WAL
state could exist.

## Fresh Stable Checksum And Sidecars

Before any copied-XDG CLI command in this retry:

```text
database SHA-256:
e292925fde8c1989eeefa33cc17644380e9e4f940c1420b89c36795971ce9b0f
database size: 4216524800
database mtime: 2026-07-28T00:15:54-0600
database inode: 89888471
header offsets 24-27: 0000004a
header offsets 92-95: 0000004a
brain.db-wal: absent
brain.db-shm: absent
```

After tagged path resolution, tagged status, both tagged query attempts,
untagged status, and untagged lexical retrieval:

```text
database SHA-256:
e292925fde8c1989eeefa33cc17644380e9e4f940c1420b89c36795971ce9b0f
database size: 4216524800
database mtime: 2026-07-28T00:15:54-0600
database inode: 89888471
header offsets 24-27: 0000004a
header offsets 92-95: 0000004a
brain.db-wal: present, 0 bytes
brain.db-wal SHA-256:
e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
brain.db-shm: present, 32768 bytes
brain.db-shm SHA-256:
fd4c9fda9cd3f9ae7c962b0ddf37232294d55580e1aa165aa06129b8549389eb
```

The database checksum, size, mtime, inode, and both previously changed header
counters remained identical. The read-only bookkeeping sidecars were captured
and preserved without deletion or alteration.

## Final Adversarial Review Corrections

The final whole-branch review found and closed three admission-boundary gaps
before the frozen-code gates and corpus rerun:

- readiness status precedence now preserves `corrupt`, `disabled`, and
  `needs_index` instead of masking those states with native capability;
- capability diagnostics redact embedded POSIX, file-URI, Windows, NT, UNC,
  mixed-separator, and Unicode filesystem paths while preserving explicit
  non-path diagnostics; and
- SQLite now reconstructs the canonical active-root descriptor from the
  authoritative database/profile/generation/revision/purge and sorted segment
  catalog, and the native loader compares that proven hash before opening any
  segment payload.

The SQLite catalog proof has a 1,024-segment hard metadata safety ceiling and
uses the generation-segment primary-key order without a temporary sort. The
representative 58-segment root is inside that ceiling. This ceiling is a
fail-closed resource guard, not the later measured ready-fanout policy.

Native root opening is cancellation-aware before filesystem work, during the
single streamed payload read, between segments, and immediately before and
after the native load. The native `LoadBuffer` call itself is not preemptible.
The existing 250 ms timeout remains the SQLite readiness-proof budget; it is
not a promise that all native segment imports finish within 250 ms.

The final follow-up review also required and verified that:

- status opens and closes the same fully validated native root as normal
  runtime before reporting it searchable, maps artifact failures to a stable
  path-free reason, and propagates caller cancellation instead of exiting
  successfully;
- each imported native segment contains exactly the manifest's member count,
  so an independently checksummed short or extra-vector payload is rejected;
- candidate expansion checks caller cancellation before and after each
  non-preemptible native search and between segments, and never returns partial
  hits after cancellation in a later native or SQLite expansion stage; and
- provider construction and provenance failures close an already opened
  native searcher exactly once while preserving the primary error.
- native-root failures exposed through normal research and saved traces use the
  stable `native_root_artifacts_unavailable` reason instead of serializing
  filesystem paths, while caller cancellation and deadlines remain errors
  rather than successful lexical fallbacks.

After those corrections, the final standard gates, complete uncached tagged
race gate, tagged status/query, untagged fallback status/query, and database
byte-stability check all passed at `67e690fe0d335174f1824ffaee994585122d46a6`.

## Residual Scope

This first stacked PR implements runtime admission and explicit capability
only. It does not implement:

- automatic refresh after sync;
- durable refresh runs or resumable stage checkpoints;
- universal synchronous integration with every sync subcommand;
- strict sync ready-or-error behavior;
- cross-process maintenance or generation locks;
- static Homebrew macOS arm64 packaging;
- installed-binary full-corpus acceptance; or
- production activation.

Those remain later stacked work. The complete automatic-sync product goal is
not complete.
