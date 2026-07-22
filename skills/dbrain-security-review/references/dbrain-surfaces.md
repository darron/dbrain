# dbrain Surface Orientation

## Refresh Rule

This is orientation, not a frozen route, command, tool, schema, or caller inventory. Refresh it from current CLI registrations, web router construction, MCP registrations, config structs/defaults, migrations, workers, archive callers, workflows, generated artifacts, and recent history. Inventory actual callers and sinks; do not claim coverage from this list alone.

## Runtime And Identity Boundaries

- CLI command registration, confirmation and force flags, config selection, environment/secret precedence, maintenance/repair commands, and source enablement.
- Repo-local versus production XDG roots, installed/Homebrew versus checkout-built binary, generated launchd plists and environment, filesystem user/group, and subprocess identity.
- Local web, reverse proxy, GitHub OAuth sessions, service auth, MCP stdio/HTTP bearer identity, tsnet identity/routing, non-loopback listeners, Funnel, Host/Origin trust, and browser-extension exceptions.
- Single-corpus administration assumptions versus any narrower identity/capability contract current code actually enforces.

## Local Data And Filesystem

- SQLite migrations, query-derived paths, restored rows, FTS/search inputs, foreign keys/transactions, and destructive repair or purge operations.
- Vault notes, media, raw extracts, transcripts, OCR, OKF bundles, research traces, cache, temp files, metrics, logs, config, backups, symlinks, modes, rename/delete/upload operations, and reference-aware media cleanup.
- Lexical containment versus canonical/symlink containment; archive/media paths referenced by multiple rows; generated Markdown/HTML active content; raw evidence separated from summaries.

## Importers And Native Parsers

- Apple Notes dbrain-owned DB/WAL/SHM snapshots, schema probes, protobuf/native decoding, attachment resolution, offloaded/encrypted/protected handling, attachment OCR/text, and source-container escape.
- Safari/iCloud tab snapshots and explicit device selection; imported URLs, titles, and append-only semantics.
- X bookmarks, quoted-post recursion, hydration, media variants, photo OCR, video transcription, stable identity, and local-media completion.
- Feeds, GitHub, YouTube, links, files, PDFs, HTML, Markdown, image/audio/video metadata, SQLite/JSON/XML/native decoders, and malformed or adversarial content.

## Outbound HTTP And Models

- Link/feed/source enrichment, media downloads, redirects, DNS/IP changes, proxy/environment behavior, credentials in URLs/headers, timeouts, decoded/compressed sizes, response retention, and private/loopback/link-local/metadata destinations.
- Configurable model/provider/archive endpoints, OpenRouter/Ollama/local helpers, prompt construction, cross-stage imported/model-output propagation, raw-versus-derived evidence, data minimization, moderation fallbacks, context/response limits, retries, cancellation, and error logging.
- OCR/transcription/summarization helpers: executable provenance, argv without shell expansion, environment inheritance, stdin/stdout/stderr bounds, temp files, exit behavior, and hostile helper output.

## Web, OAuth, Public Shares, And Media

- Route registration, methods, body and batch limits, identity/capability middleware, CSRF, Origin/Host/DNS-rebinding behavior, CORS, extension origins, security headers, cookies, error representations, SSE and long-running work.
- GitHub OAuth state, callback binding, approval removal, session rotation/expiry/revocation, Secure-cookie decision, service-token replay, and logout.
- Public-share creation/lookup/rendering, source filtering, public URLs, archived-media proxying, trace/error/metrics leakage, Markdown/HTML sanitization, URL parsing, active content, and protected/internal route references.
- Embedded Svelte asset provenance and checkout-source versus `web/ui/dist` behavior.

## MCP And tsnet

- MCP tools, resources, prompts, stdio and HTTP transports, bearer enforcement/revocation, request/body/batch bounds, operation scope, logging, and genuine read-only behavior.
- Confirm tools cannot mutate upstream apps or convert model answers into authoritative evidence.
- tsnet state storage/modes, local versus tailnet routing, listener binding, Funnel/reverse-proxy configuration, warning-only versus enforced authentication, Origin/Host handling, and generated service configuration.

## Archive And Restore

- SQLite and media archive selection, object keys, endpoint/bucket configuration, credentials, checksums/metadata, compression/decompression bounds, download/upload limits, temp modes, atomic replacement, quick/integrity checks, schema/application identity, rollback, and cleanup.
- Corrupted, foreign, malicious-but-SQLite-valid, symlinked, or path-manipulated restored state at every later sink.
- Reference-aware deletion and archive/prune only after terminal OCR/transcription coverage; synthetic fakes instead of real archives.

## Required Abuse Hypotheses

Attempt to disprove each applicable family; these are requirements, not findings:

- DB-derived path traversal or symlink escape in vault, media, trace, attachment, archive, restore, upload, and deletion paths.
- Apple Notes attachment paths escaping the intended Notes container.
- Private, loopback, link-local, metadata, or credential-bearing requests through links, feeds, enrichers, redirects, model/archive endpoints, configurable readers, and media downloads.
- Unbounded downloads, decompression, parsing, request batches, transcripts, SSE work, model calls, retained rows, or helper output.
- Restore acceptance of corrupted, foreign, or malicious-but-SQLite-valid data.
- Overly broad permissions for config, databases, raw/derived private data, generated artifacts, temporary files, tsnet state, and backups.
- Listener exposure based on warnings instead of enforced authentication across non-loopback web, MCP HTTP, tsnet, Funnel, and reverse-proxy modes.
- CSRF, DNS rebinding, Host trust, no-Origin requests, CORS, and browser-extension exceptions on mutation routes.
- OAuth state/session lifecycle, callback binding, approval removal, Secure cookies, revocation, logout, and service-auth replay.
- MCP bearer enforcement/revocation, logging, amplification, scope, and read-only guarantees.
- Leakage or active content through public shares, media, traces, errors, metrics, logs, OKF, Markdown, rendered UI, URLs, and exported artifacts.
- Subprocess executable/argv/environment/stdin/stdout/stderr/temp handling and secret resolution.
- Prompt injection, provider-visible data minimization, model-output handling, raw/derived separation, and cross-stage contamination.
- Mutable CI dependencies, workflow permissions/events, runtime installs, dependency reachability, embedded-asset drift, release provenance, Homebrew mutation, browser-extension packaging/signing, and skill-publishing integrity.
- Launchd template/generated-plist drift, secret/environment exposure, file modes, binary/config identity, and unsafe listener enablement represented in checkout artifacts.

## High-Value Code Locations

Refresh names with `rg --files` and registrations/callers before use. Start with:

- `cmd/`, `internal/config/`, install/service templates, `Taskfile.yml`, and migration/store packages for commands, precedence, runtime roots, launchd, destructive operations, and persisted trust.
- `web/`, `web/ui/`, embedded `web/ui/dist`, remote-serving packages, and browser-extension directories for routes, OAuth, sessions, shares, media, Origins, rendering, and packaging.
- `internal/mcpserver/` and remote/tsnet packages for MCP registrations, HTTP transport, auth, bounds, routing, and read-only scope.
- `internal/applenotes/`, Safari/X/import/source-enrichment/feed/media/OCR/transcription/model packages for snapshots, parsers, URLs, redirects, prompts, subprocesses, and resource limits.
- SQLite/media archive and restore packages plus vault/media rendering and cleanup packages for object trust, path containment, validation, deletion, and reference semantics.
- `.github/workflows/`, Go modules/sums, npm manifests/lockfiles, release configuration, Homebrew automation, extension manifests/build outputs, `skills/`, and publishing workflows for supply-chain provenance.
- `docs/`, `CHANGELOG.md`, current tests, and recent git history for intended contracts only; verify behavior in current source and real seams.
