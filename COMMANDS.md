# dbrain Commands

This document is the detailed command and task reference for `dbrain`. Every command supports `--help`; the CLI help is the source of truth for flags.

## Command Index

- `dbrain archive media`
- `dbrain auth github approve <username>`
- `dbrain auth github list`
- `dbrain auth github remove <username>`
- `dbrain auth mastodon login <account-key>`
- `dbrain auth mastodon status <account-key>`
- `dbrain auth mastodon logout <account-key>`
- `dbrain auth mcp token add <name>`
- `dbrain auth mcp token list`
- `dbrain auth mcp token revoke <id-or-name-or-fingerprint>`
- `dbrain categorize analyze`
- `dbrain categorize batch`
- `dbrain categorize item`
- `dbrain categorize repair`
- `dbrain categorize source`
- `dbrain categorize sources`
- `dbrain categorize vocab`
- `dbrain completion <shell>`
- `dbrain config env`
- `dbrain config paths`
- `dbrain doctor full-disk-access`
- `dbrain entity generate <query>`
- `dbrain entity index`
- `dbrain entity map [query]`
- `dbrain eval mcp`
- `dbrain eval research`
- `dbrain eval research diff`
- `dbrain eval research propose`
- `dbrain extract links`
- `dbrain extract sources`
- `dbrain feed add <url>`
- `dbrain feed check [feed-key-or-url]`
- `dbrain feed disable <feed-key-or-url>`
- `dbrain feed enable <feed-key-or-url>`
- `dbrain feed list`
- `dbrain feed refresh <feed-key-or-url>`
- `dbrain feed status <feed-key-or-url>`
- `dbrain get <source-key-or-id>`
- `dbrain hydrate x`
- `dbrain import apple-notes`
- `dbrain import apple-notes probe`
- `dbrain import apple-notes snapshot --dir <path>`
- `dbrain import bluesky bookmarks`
- `dbrain import github stars`
- `dbrain import mastodon bookmarks --account <account-key>`
- `dbrain import safari-tabs`
- `dbrain import safari-tabs devices`
- `dbrain import x-bookmarks`
- `dbrain import youtube`
- `dbrain install`
- `dbrain launchd install`
- `dbrain launchd plist`
- `dbrain launchd restart`
- `dbrain launchd uninstall`
- `dbrain link add <url>`
- `dbrain notify status`
- `dbrain notify test buzz`
- `dbrain notify test slack`
- `dbrain ocr x-photos`
- `dbrain okf export`
- `dbrain okf validate <dir>`
- `dbrain repair fts`
- `dbrain repair notes`
- `dbrain repair pruned-media`
- `dbrain repair sources`
- `dbrain research <question>`
- `dbrain search <query>`
- `dbrain semantic status`
- `dbrain semantic chunk`
- `dbrain semantic embed`
- `dbrain semantic verify`
- `dbrain serve mcp`
- `dbrain serve remote`
- `dbrain serve web`
- `dbrain sqlite archive`
- `dbrain sqlite restore`
- `dbrain stats activity`
- `dbrain stats backlog`
- `dbrain stats items`
- `dbrain stats pipeline`
- `dbrain stats sources`
- `dbrain sync all`
- `dbrain topic generate <topic>`
- `dbrain topic index`
- `dbrain topic map <topic>`
- `dbrain topic refresh [topic]`
- `dbrain transcribe x-media`
- `dbrain tsnet reset`
- `dbrain tsnet status`
- `dbrain version`
- `dbrain whats-new`
- `dbrain worker sources`

On macOS, `dbrain` will automatically use `caffeinate` when the command is
available, so long-running leaf commands keep the machine awake by default.
Use `--no-caffeinate` to disable that behavior for a specific run. You can
still pass `--caffeinate` to force it explicitly.

Structured debug logging is enabled by default. Use `--no-debug` when you want
quiet CLI output.

## Command Reference

Every command supports `--help`; the help screen includes usage, command flags,
global flags, and the environment/config lookup footer. The root help currently
looks like:

```text
Local-first second-brain tooling for importing, enriching, searching, and serving a personal research corpus.

Usage:
  dbrain [flags]
  dbrain [command]

Available Commands:
  archive     Manage archived media and other durable storage tiers
  audit       Inspect production health without modifying the target
  auth        Manage authentication approvals and tokens
  categorize  Categorize items or linked sources with an LLM
  completion  Generate the autocompletion script for the specified shell
  config      Show active configuration and storage paths
  doctor      Diagnose local dbrain runtime issues
  entity      Derive and render entities from the local brain
  eval        Run local retrieval quality checks
  extract     Extract and summarize linked sources
  feed        Manage RSS, Atom, and JSON Feed subscriptions
  get         Load an item or source note
  help        Help about any command
  hydrate     Hydrate canonical source data
  import      Import source data into the brain
  install     Run first-time local dbrain setup
  launchd     Install or print a macOS launchd service for dbrain
  link        Add and manage manually submitted links
  notify      Inspect and test hard-failure notifications
  okf         Export and inspect Open Knowledge Format bundles
  ocr         Extract text from downloaded images
  repair      Repair derived local artifacts
  research    Research the local brain with evidence and local synthesis
  search      Search items and sources
  semantic    Build and inspect semantic retrieval state
  serve       Serve local interfaces
  sqlite      Manage the local SQLite database
  stats       Show database counts and import progress
  sync        Run multi-stage refresh flows
  topic       Build and write topic maps from the local brain
  transcribe  Transcribe downloaded local media
  tsnet       Inspect or reset built-in Tailscale state
  version     Print build and version information
  whats-new   Show newly imported, enriched, blocked, or failed local evidence
  worker      Run long-lived background-style worker loops

Flags:
      --caffeinate           Force keep-awake behavior while the command is running
      --config-file string   Config file path override (wins over --root, DBRAIN_CONFIG_FILE, and DBRAIN_ROOT)
      --debug                Enable structured debug logging to stderr (default true)
  -h, --help                 help for dbrain
      --no-caffeinate        Disable automatic keep-awake behavior for this command
      --no-debug             Disable structured debug logging to stderr
      --root string          Brain root directory override (defaults to DBRAIN_ROOT, then ~/.config/dbrain and ~/.local/share/dbrain)

Use "dbrain [command] --help" for more information about a command.

Environment:
  --config-file wins over --root, DBRAIN_CONFIG_FILE, and DBRAIN_ROOT.
  Defaults: config in ~/.config/dbrain, state in ~/.local/share/dbrain.
  Runtime values resolve from shell env, then .envrc/.env, then config.yaml.
  Run "dbrain config env" for the full environment/config key table.
```

### `dbrain config paths`

Prints the active config, categories, data, database, vault, media, temp, cache,
and log paths without creating directories, running startup preflight, or
cleaning legacy files. Use `--json` for automation.

```sh
dbrain config paths
```

### `dbrain config env`

Prints the supported environment variables and matching `config.yaml` keys. Use
`--json` for automation. This command is the authoritative source for the table
in `README.md`.

```sh
dbrain config env
dbrain config env --markdown
```

### `dbrain notify`

Inspects redacted typed-incident state and explicitly tests configured
notification providers. These commands use the normal target resolution, so
pass the same `--config-file` or `--root` used by the `serve remote` process you
intend to inspect.

```sh
dbrain --config-file ~/.config/dbrain/config.yaml notify status
dbrain --config-file ~/.config/dbrain/config.yaml notify status --json
dbrain --config-file ~/.config/dbrain/config.yaml notify test buzz
dbrain --config-file ~/.config/dbrain/config.yaml notify test buzz --json
dbrain --config-file ~/.config/dbrain/config.yaml notify test slack
dbrain --config-file ~/.config/dbrain/config.yaml notify test slack --json
```

`notify status` is read-only: it does not create or chmod notification state
when no state exists. Human and JSON output report whether automatic delivery
is enabled, the effective repeat interval, configured provider names, typed open
and cooling incidents, pending delivery count, and redacted last provider
status. Status never reports the relay URL, channel UUID, secret reference,
private key, notification body, or external event ID.

`notify test buzz` sends one clearly labeled synthetic kind-9 message directly
through the Buzz provider without creating or changing incident/outbox state.
It is deliberately available while `notifications.enabled` is false, provided
`notifications.buzz.enabled` and the complete provider config are set. JSON
success has this shape:

```json
{"provider":"buzz","status":"accepted","external_id":"<nostr-event-id>","accepted_at":"<rfc3339>"}
```

`accepted` means the relay returned a positive Nostr `OK`; it is not a human
delivery or read receipt. The command makes a real network publish when the
provider is configured. An unconfigured provider exits with
`notification_provider_not_configured` and performs no relay call.

`notify test slack` likewise sends one clearly labeled synthetic message
directly through the configured Slack Incoming Webhook without creating or
changing incident/outbox state. It works while `notifications.enabled` is
false, provided `notifications.slack.enabled` and `webhook_url_ref` are set.
Slack success means only that the webhook HTTP request was accepted. The JSON
result calls its dbrain-local notification ID `correlation_id`; Slack Incoming
Webhooks do not supply a remote message ID, and acceptance is not a delivery,
human acknowledgement, or read receipt.

Keep the global switch off during provider onboarding:

```yaml
notifications:
  enabled: false
  repeat_after: "6h"
  buzz:
    enabled: true
    relay_url: "wss://buzz.example"
    channel_id: "00000000-0000-4000-8000-000000000001"
    private_key_ref: "op://Private/dbrain-notifications-buzz/private-key"
    allow_private_origin: false
  slack:
    enabled: true
    webhook_url_ref: "keychain://dbrain/notifications-slack-webhook"
```

Use a dedicated Nostr identity, admit its public key to the relay/channel when
required, and keep its 64-character hexadecimal secret or `nsec` behind a typed
`env:`, `op://`, or `keychain://` reference. Inline private keys are rejected.
The relay must be an origin-only `wss://` URL; private/localhost origins require
the exact-origin opt-in. Buzz channel IDs are UUIDs for this integration even
though generic NIP-29 group identifiers are not necessarily UUIDs. Buzz's
[NOSTR guide](https://github.com/block/buzz/blob/main/NOSTR.md) defines its
NIP-42 authentication, kind-9, required `h` tag, membership, and UUID contract.
Kind-9 channel content is not end-to-end encrypted.

For Slack, `webhook_url_ref` is required and must be a typed secret reference:
`env:DBRAIN_SLACK_WEBHOOK`, `op://Private/dbrain-notifications-slack/webhook-url`,
or `keychain://dbrain/notifications-slack-webhook`. Do not put a webhook URL in
YAML. Its resolved value must be an HTTPS Slack Incoming Webhook on the
official `hooks.slack.com` or `hooks.slack-gov.com` host; arbitrary, private,
and localhost webhook origins are rejected.

After the provider test succeeds, production activation is separate: set
`notifications.enabled: true`, restart the installed launchd service, then run
`notify status --json` using the same explicit production config. Repository
builds and sample edits do not activate the installed process.

Automatic incidents come only from hard failures settled by the still-running
scheduled `sync all` process. The first occurrence of a typed error sends
immediately; `repeat_after` suppresses that same type independently, accumulates
occurrences, and sends one reminder at the boundary. A different type remains
eligible immediately. Success consolidates recovery for notified open types.
If a type fails again during its cooling window, it reopens silently, so rapid
fail/recover flaps cannot generate two messages per cycle. Notification delivery
failure does not fail sync or prevent the post-sync audit. Delivery records are
durable before provider attempts: temporary failures remain pending for retry,
permanent failures stop retrying, and ambiguous outcomes remain recorded without
asserting that Slack posted or did not post a message. With Buzz and Slack both
enabled, every future eligible incident has one delivery path for each provider;
failure in one path does not prevent an attempt through the other.

Manual syncs, lock/overlap skips, graceful cancellation, and tolerated per-item
errors do not notify. Process death, startup failure before readiness, and
scheduler stalls need a separate watchdog and are deferred. Human
acknowledgements/read receipts and inbound Buzz commands are also deferred.

See [Scheduled hard-failure notifications](README.md#scheduled-hard-failure-notifications)
for the complete onboarding and activation sequence.

### `dbrain audit`

Runs a bounded, read-only health audit against the resolved target. `audit all`
emits the closed 55-check registry; category commands limit the scope without
changing check semantics. When `--expect-commit` is set on a category or source
command, `boundary.runtime` is added explicitly so the commit assertion cannot
be silently filtered out. A 7-or-more-character hexadecimal commit prefix is
accepted, though release workflows should use the full artifact commit.

```sh
dbrain audit all --profile standard --json
dbrain audit imports --since 7d
dbrain audit pipeline --json
dbrain audit durability --expect-commit <sha> --json
dbrain audit all --profile deep --json
dbrain audit apple-notes --json
dbrain audit safari-tabs --json
dbrain audit x-bookmarks --json
dbrain audit github-stars --json
dbrain audit youtube-liked --json
dbrain audit youtube-watch-later --json
dbrain audit feeds --json
```

Standard is the default profile; fast omits expensive integrity and remote
checks. Deep is an explicit CLI-only profile that validates the newest remote
SQLite archive in a private temporary directory and performs complete,
paginated reconciliation of the configured `media/` archive inventory. It
never replaces the active database and is not exposed through the web UI or
MCP. Exit codes are 0 pass, 1 warn, 2 fail, and 3
unknown/bootstrap failure. A produced JSON report is written before a non-zero
health exit. `--include-identifiers` selects a local-only wrapper with bounded
row IDs, source keys, and cleanup paths; the shared `dbrain.audit.v1` report
never contains paths, object keys, corpus content, titles, URLs, or secrets.

Deep imports reconcile the configured Apple Notes, Safari Tabs, X Bookmarks,
GitHub Stars, YouTube Liked, YouTube Watch Later, and subscribed-feed
inventories against the query-only local snapshot. Inventories run
sequentially with a five-minute ceiling per source and fixed maxima of 100,000
unique identities and 10,000 pages. The report exposes counts only. It fails
when a complete upstream inventory contains identities missing locally and is
unknown when credentials, snapshot access, schema parsing, pagination, or
completion cannot be proven.

The seven source-specific commands default to deep and reject an explicitly
selected fast or standard profile. Each command forces only its named parity
check, including when normal scheduled import for that source is disabled; in
that case the poll check remains skipped with `feature_disabled`. Source
commands do not expose or resolve archive byte controls. They never import,
delete, repair, retry, restore, prune, close tabs, modify Notes, or write feed
state. Apple Notes and Safari Tabs copy their SQLite DB/WAL/SHM inputs into
interruptible dbrain-owned snapshots. X and GitHub use fixed upstream origins,
YouTube uses fixed playlist URLs with a bounded proxy-free `yt-dlp` process,
and feeds are restricted by the existing safe-network policy, including exact
configured private origins. Deep parity cannot be invoked through scheduled
audits, MCP, or the admin API.

The audit resolver follows the normal target precedence but creates no
directories, applies no migrations, runs no startup repair/preflight, and
opens SQLite in query-only mode with one consistent snapshot.

Deep defaults to 20 GiB compressed archive, 100 GiB decompressed database, and
120 GiB aggregate temporary-space ceilings. `audit.max_archive_bytes`,
`audit.max_database_bytes`, and `audit.max_temp_bytes` (or the matching
`DBRAIN_AUDIT_MAX_*` variables) may only lower those defaults. The deep-only
`--max-archive-bytes`, `--max-database-bytes`, and `--max-temp-bytes` flags may
explicitly raise them for one run; passing those flags with fast or standard
is a usage error. Remote inventory is also bounded to 1,000,000 objects and
10,000 pages, with a two-hour whole-run deadline and cleanup on every exit.

Per-class timeouts can be lowered with `audit.timeouts.bootstrap`,
`local_query`, `metrics_or_manifest`, `sqlite_or_okf_integrity`,
`remote_metadata`, and `remote_request`, or their
`DBRAIN_AUDIT_TIMEOUT_*` equivalents. Built-in ceilings are respectively 10s,
5s fast/30s standard, 10s, 2m, 2m per check, and 30s per request/page. Invalid
or non-positive durations fail bootstrap; larger values are clamped.

`serve remote` can own continuous read-only audit scheduling when
`audit.enabled: true`. A fast profile runs after each actual scheduled sync
result, after its process lock and status settle; a standard profile runs every
`audit.standard_interval` (6h by default). The profiles share one non-overlap
guard, deep is never scheduled, and audit failure does not change the sync
result. Reports rotate daily under `<log_dir>/audit/reports` with private
permissions, 90-day/256-MiB retention, and exact-profile history.

Transition alerts are optional. Configure `audit.alert.webhook_url` and, when
needed, a typed `bearer_token_ref`. Public webhooks require HTTPS; private,
loopback, and link-local delivery requires `allow_private_origin: true` and is
confined to that exact origin. Redirects and environment proxies are disabled.
No webhook is required for reports, freshness, metrics, or later admin and MCP
visibility.

### `dbrain install`

Runs first-time local setup. By default, it uses the existing XDG split layout:
config in `~/.config/dbrain` and data, database, vault, logs, cache, temp, and
OKF output in `~/.local/share/dbrain`. Use `--base-path <dir>` for a
single-directory install where the current user has write permission.
If you pass global `--config-file <path>` instead, the installer writes the
config and categories next to that file while data, logs, cache, temp files,
and the vault keep the normal XDG data layout. Use `--base-path` when you want
everything colocated under one directory.

Before running install, the recommended Homebrew setup is `brew install
summarize ollama whisper-cpp` plus `brew install --cask google-chrome`. The
installer detects local helpers and runtimes including `summarize`, whisper.cpp
(`whisper-cli`), MacWhisper (`mw` and the macOS app), Ollama, LM Studio, oMLX, `tesseract`,
`ffprobe`, `yt-dlp`, 1Password CLI, and macOS `security`. Local model endpoints
are checked separately from command presence so the generated config can
reflect tools that are installed but not currently serving models. When tool
detection runs and `summarize` is absent, install reports the exact
`brew install summarize` repair command before the first sync can fail. When X
is selected without a ready whisper.cpp or MacWhisper backend, install reports
both the preferred `brew install whisper-cpp` repair and the optional
MacWhisper compatibility path.

When `whisper-cli` is detected and either configured model file is absent,
interactive install offers the pinned, checksum-verified Whisper base and
Silero VAD downloads with yes selected. `--yes` accepts that default. Model
downloads use the same terminal byte-progress UI as SQLite archive transfers,
and Ollama pulls/creates stream their native command output.

Interactive install begins with an explicit source checklist for X bookmarks,
GitHub stars, YouTube Watch Later, liked YouTube videos, subscribed feeds,
Bluesky bookmarks, Apple Notes, and Safari tabs. All choices begin unchecked on
a fresh install.
The resulting positive selections are written under `sync_all.imports` and are
shared by manual and scheduled `sync all` runs. Selecting X enables its whole
family of bookmark import, hydration, media transcription, and photo OCR;
leaving X unchecked prevents all four stages. Re-running install without
`--force` merges these managed selections into the existing YAML while
preserving unrelated config. When X, either YouTube list, or Bluesky bookmarks
is selected, install also asks for the shared browser and optional profile
written to `sync_all.browser` and `sync_all.profile`. X and YouTube read
cookies; Bluesky reads its session from Chrome Local Storage.

When the Ollama CLI is detected, plain `dbrain install` uses the local dbrain
Ollama profile by default: it writes the embedded Modelfile, pulls the public
base model if needed, creates `dbrain:2026042701`, and writes
`ollama/dbrain:2026042701` to `summary.model` and `categorize.model`. If
Ollama is not available, served local model endpoints are fallback defaults,
with LM Studio used only after Ollama/oMLX options. If no local OCR model/tool
is selected and no OpenRouter key is configured, generated installs skip X
photo OCR for configured `sync all` runs instead of failing on the hosted OCR
default.

For local model setup, `--local-model-profile dbrain` writes the local Ollama
dbrain wrapper tag `ollama/dbrain:2026042701` to `summary.model` and
`categorize.model`, materializes the embedded dbrain Modelfile under the config
directory, pulls `qwen3.6:35b-a3b-nvfp4` when needed, and creates the local
`dbrain:2026042701` tag. `--local-model-profile dbrain-omlx` writes the tested
oMLX equivalent
`omlx/Qwen3.6-35B-A3B-MLX-4bit`, and `--local-model-profile small-ollama`
writes and pulls `ollama/gemma4:12b-mlx` for lower-memory installs. The oMLX
profile expects the selected oMLX server to already expose that model.

Tailscale/tsnet transport setup and GitHub web login setup are intentionally
separate. Tailscale options write `tsnet.*`; GitHub login writes `auth.*`.
On macOS, prompted third-party secrets are stored in Keychain and referenced as
`keychain://dbrain/...` instead of being written directly to YAML. The generated
web auth session key is stored in Keychain when available; otherwise it is
generated into the `0600` config file and reported as a warning.
If Keychain is disabled or unavailable, prompted third-party secrets such as
GitHub tokens, OpenRouter keys, Tailscale auth keys, and GitHub OAuth client
secrets are skipped rather than written to config. Re-running install with
`--force` and GitHub login can rotate the generated session key and log out
existing web sessions unless an existing `dbrain/auth-session-key` Keychain
entry is already present. Existing known `dbrain` Keychain secrets are reused
when the matching prompt field is left blank.

```sh
dbrain install
dbrain install --yes
dbrain install --base-path ~/dbrain --yes
dbrain install --yes --enable-github-stars --enable-feeds
dbrain install --yes --enable-youtube-watch-later --enable-youtube-liked
dbrain install --yes --enable-apple-notes --enable-safari-tabs --safari-tabs-device phone --enable-scheduler
dbrain install --yes --enable-tailscale --tsnet-hostname dbrain
dbrain install --yes --enable-github-login --auth-base-url https://dbrain.example.ts.net --github-client-id Iv1.example
dbrain install --yes --enable-scheduler --install-launchd
dbrain install --yes --local-model-profile dbrain
dbrain install --yes --local-model-profile dbrain-omlx
dbrain install --yes --local-model-profile small-ollama
dbrain install --yes --summary-model ollama/dbrain:2026042701 --categorize-model ollama/dbrain:2026042701
```

### `dbrain import x-bookmarks`

Direct X bookmark import path. Requires a supported browser profile with valid
X cookies. Chrome/Chromium is the best-tested path.

```sh
dbrain import x-bookmarks --limit 25
```

### `dbrain import bluesky bookmarks`

Imports Bluesky bookmarks through the official bookmark XRPC endpoint. The
importer reads the signed-in session from the selected Chrome profile's
`Local Storage/leveldb` `BSKY_STORAGE` entry using a read-only bounded snapshot;
it does not read or write browser cookies and never writes refreshed tokens back
to Chrome. It follows the complete cursor chain, prefers the session PDS URL,
refreshes an expired access token in memory at most once, and upserts bookmarks
as `bsky_bookmark` items with stable `at://` identities. Blocked and not-found
post views are reported as skipped rather than failing the run. Re-running the
command is idempotent.

```sh
dbrain import bluesky bookmarks --profile Default --limit 25
dbrain import bluesky bookmarks --profile Default --json
```

### `dbrain import mastodon bookmarks`

Imports bookmarks from one configured Mastodon account through its canonical
HTTPS origin. The bearer token is resolved only from the account's typed secret
reference and is used only for same-origin API requests; public media URLs are
downloaded without the bearer token. The importer verifies the account before
reading bookmarks, follows only validated server-provided `Link` cursors, and
stores opaque checkpoint state in the database. `--limit` is a smoke-run bound;
partial pages retain a bounded resume offset so repeated limited runs continue
through the backfill. Text-only, card-only, and supported media-only statuses
are retained, while malformed or unsupported statuses are counted explicitly.
The import is append-only when a remote bookmark disappears. A live Hachyderm
run requires the operator to complete OAuth first; this documentation does not
claim an authenticated smoke test. Completed runs also perform a bounded
retry sweep for older retryable Mastodon media failures, and JSON/human output
includes API-error, rate-limit, and retry counters.

```sh
dbrain import mastodon bookmarks --account hachyderm --limit 25
dbrain import mastodon bookmarks --account hachyderm --json
```

### `dbrain import apple-notes`

Imports Apple Notes directly from the local Notes SQLite store through a
dbrain-owned snapshot. The importer is read-only against Apple's files,
materializes decoded notes as `apple_note` items, preserves raw decoded text,
renders Markdown notes, indexes discovered URLs, and summarizes notes by
default with the normal local summarization path. Use `--summarize=false` for a
materialization-only run. Attachment metadata and text already
exposed by Notes are indexed with the note; supported text/PDF attachment files
are extracted locally, and image attachments use local `tesseract` OCR when
available. Password-protected notes are skipped by default. Use account/folder
exclusions or `[[dbrain-ignore]]` inside a note for opt-out privacy.
Normal command output prints per-note progress only for notes that need work;
unchanged-current rows are counted in the final stats but not printed one by
one. In applied mode, `--limit` counts notes that need work, so repeated
limited runs skip unchanged-current notes and advance through the backlog.

```sh
dbrain import apple-notes probe
dbrain import apple-notes snapshot --dir /tmp/dbrain-notes-snapshot
dbrain import apple-notes --dry-run --show-titles
dbrain import apple-notes --limit 25
dbrain import apple-notes
dbrain import apple-notes --force
dbrain import apple-notes --summarize=false
dbrain import apple-notes --exclude-folder Private
dbrain import apple-notes --exclude-folder Private --forget-excluded
dbrain import apple-notes --skip-attachment-ocr
```

`import apple-notes probe` checks database access and schema shape without
decoding note bodies. `import apple-notes snapshot --dir <path>` copies the
Notes DB/WAL/SHM triplet into a dbrain-owned directory for local debugging.

### `dbrain import safari-tabs`

Imports Safari iCloud tabs from the local Safari `CloudTabs.db` through a
dbrain-owned snapshot. The importer is read-only against Safari's files,
targets one device by name or UUID, materializes matching HTTP(S) tabs as
`safari_tab` items, and leaves Safari untouched. Imported tab URLs then flow
through normal link discovery, source extraction, source summaries, rendering,
and categorization. Only tabs Safari has materialized into `CloudTabs.db` are
visible to dbrain; Private Browsing tabs, Start Page tabs, and not-yet-synced
iCloud changes may not appear. In practice, macOS may not refresh that local
database until Safari is running on the machine doing the import; launching
Safari can make newly synced tabs appear in a follow-up import within seconds.

```sh
dbrain import safari-tabs devices
dbrain import safari-tabs --device phone --dry-run --show-titles
dbrain import safari-tabs --device phone
dbrain import safari-tabs --device phone --older-than 168h
dbrain import safari-tabs --device phone --limit 100
```

### `dbrain sync all`

Runs the regular incremental refresh pipeline in one command: optional Apple
Notes import, optional Safari tabs import, optional Bluesky bookmark import,
optional Mastodon bookmark import, direct X bookmark import, X
hydration, X media audio transcription, X photo OCR, link
discovery/enrichment, GitHub stars import, YouTube, RSS/Atom/JSON Feed
import, and an optional source-backlog worker batch. It then categorizes
uncategorized items and linked sources with the same categorizer used by
`dbrain categorize batch` and `dbrain categorize sources`, unless
`--skip-categorize` is passed. If enabled, the media archive stage runs after
categorization so image categorization can still use local photo files before
they are uploaded/pruned. If enabled, OKF export runs as the final stage and
writes a full private bundle under the configured OKF directory. Image
categorization is enabled for items by default; oMLX receives image parts when
the selected oMLX model supports them. Use `--categorize-images=false` to
disable image embedding for text-only models; check the oMLX UI or model card
when unsure whether a selected oMLX model is vision-capable.
`--categorize-limit` is applied separately to items and sources, so
`--categorize-limit 25` can process up to 25 item rows and 25 source rows.

The durable `sync_all.imports` map controls which source importers both manual
and scheduled runs may contact. Existing configs without this map retain the
legacy defaults: X, GitHub stars, both YouTube lists, and feeds are enabled;
Apple Notes and Safari tabs continue to follow their existing `*.enabled`
settings, Bluesky bookmarks, and Mastodon bookmarks are disabled by default. Environment variables
named `DBRAIN_SYNC_ALL_IMPORT_*` override the map, and explicit CLI flags remain
one-run overrides. Scheduler-specific
`skip_*` settings are applied afterward as scheduled-run-only restrictions.

X hydration uses `--x-limit`. X media transcription and X photo OCR can be
bounded independently with `--x-media-limit` and `--x-photo-ocr-limit`; either
limit falls back to `--x-limit` when left at 0. In the default configuration
this combines the requirements of X bookmark import, X hydration, X media
transcription, X photo OCR, link/source enrichment, YouTube import, and
categorization. A practical local setup usually includes a supported
Chrome/Chromium profile with valid cookies plus a configured model backend such
as Ollama, LM Studio, oMLX, or OpenRouter, along with `mw`, `ffprobe`,
`summarize`, and `yt-dlp`. It supports `--skip-*` flags when
you only want part of the pipeline. Apple Notes is not run by default; enable it
with `--apple-notes` or
`DBRAIN_APPLE_NOTES_ENABLED=true`. Safari tabs are also disabled by default;
enable them with `--safari-tabs --safari-tabs-device <device>` or
`DBRAIN_SAFARI_TABS_ENABLED=true` plus `DBRAIN_SAFARI_TABS_DEVICE=<device>`.
Feeds are enabled in `sync all` by default. If no feeds are subscribed or due,
the feed stage reports that there is no feed work. Use `--skip-feeds` to skip
the stage or `--feed-limit` to cap checks in one run.

```sh
dbrain sync all --length short --timeout 5m
dbrain sync all --apple-notes --length short --timeout 5m
dbrain sync all --safari-tabs --safari-tabs-device phone --length short --timeout 5m
dbrain sync all --bluesky-bookmarks --bluesky-bookmarks-limit 25 --bluesky-bookmarks-timeout 30s --mastodon-bookmarks --mastodon-bookmarks-limit 25 --mastodon-bookmarks-timeout 30s --length short --timeout 5m
dbrain sync all --skip-categorize --length short --timeout 5m
dbrain sync all --okf-export --length short --timeout 5m
dbrain sync all --categorize-limit 25 --categorize-concurrency 2 --length short --timeout 5m
dbrain sync all --watch --poll-interval 1m --idle-exit-after 30m --length short --timeout 5m
```

Use `--skip-okf-export` to suppress a configured OKF export for one run.
`DBRAIN_OKF_EXPORT_ENABLED=true` / `okf.export.enabled: true` enables the same
final export stage by default.

Durable local metrics can be enabled without adding external infrastructure:

```yaml
metrics:
  enabled: true
  path: metrics.jsonl
  detail: model_call
  rotate_max_bytes: 33554432
  rotate_keep_files: 5
```

`metrics.path` defaults to `<log_dir>/metrics.jsonl`, and relative paths are
resolved under `log_dir`. Rotation keeps the active path unchanged and uses
canonical numeric siblings `metrics.jsonl.1` through `.128`; it rotates before
an append crosses `metrics.rotate_max_bytes` (32 MiB by default), retains five
backups by default, and treats `rotate_max_bytes: 0` or
`rotate_keep_files: 0` as explicit disable/no-retention settings. Detail
`stage` writes `sync.run.*` and
`sync.stage.completed` events. Detail `item` also writes per-item/per-source
categorization and source-summary completion events. Detail `model_call` adds
`llm.call.completed` events for direct shared-client model calls. Metrics omit
prompt text, source text, summaries, tags, categories, titles, URLs, headers,
API keys, and raw item/source keys by default; set
`metrics.include_subject_keys: true` only when raw dbrain keys are needed for a
controlled local analysis.

### `dbrain feed`

Subscribes to RSS, Atom, and JSON Feed URLs and materializes linked entries as
normal `feed_entry` items. Each entry keeps raw feed metadata, links its
canonical article URL into the normal `sources` table when available, and is
updated only when its stable identity is unchanged but its content hash changes.
Entries disappearing from a feed are not deleted locally.

```sh
dbrain feed add https://example.com/feed.xml
dbrain feed add https://example.com/feed.xml --check
dbrain feed add http://localhost:8080/feed.atom --allow-private-network
dbrain feed list
dbrain feed status feed:abc123def456
dbrain feed check
dbrain feed check feed:abc123def456 --force
dbrain feed refresh feed:abc123def456 --force --summarize
dbrain feed disable feed:abc123def456
dbrain feed enable feed:abc123def456
```

`feed add` stores the subscription by default and leaves the feed due, so the
next `sync all` imports its entries. Add `--check` when you want to fetch and
import current entries immediately.

`feed refresh FEED` is the manual feed QA path: it fetches one feed, processes
its entries, then extracts and summarizes the linked article sources from those
entries. Use `--force` when you want to reprocess an unchanged feed body and
rerun linked source enrichment.

When a feed entry has both its own content and a linked article URL, source
enrichment keeps both signals: the linked page is fetched as the primary source
text, and the feed entry text is included as explicit feed-entry context for
summary and search. If the feed entry has no useful body, the linked page stands
on its own.

Feed fetching blocks localhost, private, link-local, and multicast IPs by
default. For local feed development, pass `--allow-private-network` to
`feed add` / `feed check` / `feed refresh`, or set
`feeds.allow_private_network: true` in `config.yaml` /
`DBRAIN_FEEDS_ALLOW_PRIVATE_NETWORK=true`. The plural
`DBRAIN_FEEDS_ALLOW_PRIVATE_NETWORKS` is also accepted for compatibility.
This escape hatch applies to the explicitly configured feed fetch. Linked
article/source and media URLs discovered from feed content still pass the
public-destination policy, including redirect and DNS checks.

`feed enable` clears previous feed health diagnostics and makes the feed
eligible for an immediate check. `feed disable` stops future checks without
removing already imported feed entries, items, sources, or rendered notes.

### `dbrain archive media`

Optional manual archive/prune pass for finalized media. It can either just
mark/prune already-uploaded media or upload directly to an S3-compatible bucket
first when `--upload` or archive-upload env vars are configured. `--prune-local`
deletes a local media file only after all rows sharing that `local_path` are
archived.

### `dbrain okf export`

Writes a deterministic private Open Knowledge Format bundle from the local
SQLite brain. The default output directory is the configured `okf/current`
projection beside the rendered vault. Export uses a lock, staging directory,
validation, and atomic replacement before publishing the new bundle.
New exports target OKF 0.2, declare the version on the root index, and record
generation and source provenance in frontmatter. Validation continues to accept
existing dbrain OKF 0.1 bundles.

```sh
dbrain okf export
dbrain okf export --entities --topics --json
dbrain okf export --limit 100 --out /tmp/dbrain-okf-smoke
```

Important flags:

- `--profile`: export profile; currently only `private` is implemented.
- `--items`, `--sources`, `--entities`, `--topics`: choose concept kinds.
  With no kind flags, items and sources are included by default.
- `--include-raw`: include raw evidence sections in private export; default
  `true`.
- `--max-raw-chars`: cap each raw evidence section; `0` means unlimited.
- `--source-type`: repeatable source-type filter.
- `--limit`: smoke-test limit for items/sources.

Private OKF bundles may include raw evidence, OCR text, transcripts, Apple
Notes content, and archived-media links. Treat the output like `data/` and
`vault/` unless it has been deliberately scrubbed.

### `dbrain okf validate`

Validates an existing generated OKF bundle. This is a local structural check; it
does not regenerate the bundle and does not call external reference tools.

```sh
dbrain okf validate okf/current
dbrain okf validate okf/current --json
```

### `dbrain sqlite archive`

Creates a consistent SQLite snapshot with SQLite itself, compresses it as gzip,
and uploads it to the configured S3-compatible bucket under
`archive/db/brain-<timestamp>.db.gz`.

### `dbrain sqlite restore`

Finds the newest archived SQLite snapshot under `archive/db`, asks for
confirmation, moves any local `brain.db`, `brain.db-wal`, and `brain.db-shm`
files aside with a timestamped suffix, then installs the restored database.

### `dbrain serve web`

Serves the local read/write UI plus authenticated archived-media helpers. It can
update item/source tags, queue links, run model-backed research/synthesis, and
save non-indexed chat transcript diagnostics under `data/chat-transcripts/`.
When archive credentials are configured, `/media/asset/<media-asset-id>` streams
archived objects through the local server and `/api/media/signed-url?id=<id>`
returns a short-lived direct URL for one-off access. See
[docs/web-route-capabilities.md](docs/web-route-capabilities.md) for the current
route capability map.
Bind this to localhost or another trusted interface unless you have reviewed
the route surface and trust boundary.

```sh
dbrain serve web
```

### `dbrain serve mcp`

Serves the local brain over MCP with read-only tools, resources, and prompts
for search, note access, research packs, topic maps, and pipeline status.
Stdio is the default local-agent transport. Stateless Streamable HTTP is
available as a parallel daemon transport for remote agents, usually behind
Tailscale Serve. MCP-only built-in tsnet serving is also available when you
want the binary to expose MCP directly on the tailnet.

```sh
dbrain serve mcp
dbrain serve mcp --transport http --addr 127.0.0.1:8743 --path /mcp
dbrain serve mcp --transport tsnet --tsnet-hostname dbrain
tailscale serve --bg 8743
```

Important flags:

- `--transport`: `stdio`, `http`, or `tsnet`; default `stdio`.
- `--addr`: HTTP listen address for `--transport http`; default
  `127.0.0.1:8743`.
- `--path`: Streamable HTTP MCP endpoint path for `http` or `tsnet`; default
  `/mcp`.
- `--allow-origin`: additional trusted HTTP `Origin`; repeatable. Empty
  `Origin` and same-host `Origin` requests are accepted by default.
- `--tsnet-*`: same state, auth, TLS, tag, and timeout settings as
  `dbrain serve remote`, used only with `--transport tsnet`.

Set `mcp.auth.enabled=true` before exposing HTTP or tsnet MCP outside a private
localhost/trusted-tailnet boundary. Authenticated clients must send
`Authorization: Bearer <token>`.

### `dbrain serve remote`

Serves the existing read/write web UI and/or the read-only MCP endpoint on a
built-in Tailscale `tsnet` node. This is the usual way to reach the web UI or
MCP from another device without SSH or a separately configured `tailscale serve`
proxy.

```sh
dbrain serve remote --web --mcp
dbrain serve remote --web --mcp=false
dbrain serve remote --web=false --mcp
dbrain serve remote --web --mcp --tsnet-funnel
dbrain serve remote --tsnet-hostname dbrain-dev --tsnet-tls=false --tsnet-listen :80
```

The remote web UI is the same trusted read/write administration surface as
`serve web`. Funnel is public internet exposure, so use GitHub OAuth for public
web exposure and `mcp.auth.enabled=true` for public MCP exposure. Full remote,
Funnel, tailnet policy, DNS, and smoke-test guidance lives in
[TAILSCALE.md](TAILSCALE.md).

Important flags:

- `--web`: mount the full read/write web UI at `/`; default `true`.
- `--mcp`: mount read-only MCP Streamable HTTP at `/mcp`; default `true`.
- `--mcp-path`: MCP endpoint path; default `/mcp`.
- `--tsnet-hostname`: stable tailnet machine name; default `dbrain`.
- `--tsnet-state-dir`: durable tsnet state directory; default
  `<data_dir>/tsnet/<hostname>`.
- `--tsnet-listen`: listener address; default `:443` with TLS/Funnel and `:80`
  only when TLS is disabled.
- `--tsnet-tls`: use Tailscale HTTPS through `ListenTLS`; default `true`.
- `--tsnet-funnel`: expose the same tsnet listener through Tailscale Funnel;
  default `false`.
- `--tsnet-startup-timeout`: maximum time to wait for `tsnet.Up`; default
  `45s`.
- `--tsnet-auth-key-ref`: typed bootstrap secret ref, such as `env:NAME`,
  `op://Private/dbrain/tsnet-auth-key`, or `keychain://dbrain/tsnet-auth-key`.
- `--tsnet-allow-secret-command`: opt in to YAML-only
  `tsnet.auth_key_command` execution.
- `--tsnet-advertise-tags`: comma-separated Tailscale tags to request.
- `--tsnet-control-url`: experimental alternate Tailscale control server URL;
  HTTPS/cert and Funnel behavior may differ from Tailscale SaaS.

### `dbrain launchd`

Prints, installs, or removes a per-user macOS LaunchAgent for
`dbrain serve remote`. The generated service uses the same config resolution as
the command you run: default XDG paths unless you pass `--config-file` or
`--root`. If `DBRAIN_CONFIG_FILE` or `DBRAIN_ROOT` are present in the install
environment, the generated plist records the matching explicit flag so launchd
does not depend on your shell startup files.

Stable Homebrew service:

```sh
dbrain --config-file ~/.config/dbrain/config.yaml launchd plist \
  --bin /opt/homebrew/bin/dbrain

dbrain --config-file ~/.config/dbrain/config.yaml launchd install \
  --bin /opt/homebrew/bin/dbrain
```

Development service with a separate root, label, and configured
`tsnet.hostname` such as `dbrain-dev`:

```sh
go run ./cmd/dbrain --root /path/to/dbrain-checkout launchd plist \
  --label com.darron.dbrain-dev \
  --bin /path/to/dbrain-checkout/bin/dbrain

go run ./cmd/dbrain --root /path/to/dbrain-checkout launchd install \
  --label com.darron.dbrain-dev \
  --bin /path/to/dbrain-checkout/bin/dbrain
```

The plist is written to `~/Library/LaunchAgents/<label>.plist`, with stdout and
stderr logs under the active dbrain log directory. Use `--no-start` to write the
plist without loading it.

```sh
dbrain launchd restart
dbrain launchd restart --label com.darron.dbrain-dev

dbrain launchd uninstall
dbrain launchd uninstall --label com.darron.dbrain-dev
```

`dbrain launchd restart` also asks the restarted `serve remote` web process to
check whether it can read the Apple Notes SQLite store. If the service-process
probe fails, it opens Full Disk Access settings so the newly upgraded Homebrew
binary can be enabled before the next scheduled sync. When web OAuth is enabled,
the local CLI authenticates this narrow diagnostic call with a short-lived
service signature derived from `auth.session_key`; the doctor API is still not
publicly callable without auth. Use
`--check-full-disk-access=false` to skip the check or
`--open-full-disk-access=false` to report the failure without opening System
Settings.

### Scheduled SQLite archives

`dbrain serve remote` can run the existing online SQLite snapshot/archive path
as a separate daily scheduler. It is disabled by default and uses the same
S3-compatible configuration as `dbrain sqlite archive`:

```yaml
scheduler:
  sqlite_archive:
    enabled: true
    interval: 24h
    run_on_start: true
```

The scheduler is independent from scheduled sync and health audits. It alone
receives upload capability; audit remains read-only. Scheduled and manual
archives plus active restores share a cross-process lease, so only one of those
operations can run at a time. Cancellation of `serve remote` cancels and waits
for an in-flight scheduled archive. A private local last-attempt marker is
written before snapshotting so a restart loop cannot create or retry multiple
archives within one interval. When `run_on_start` is false, the first attempt
waits one full configured interval after `serve remote` becomes ready even if
an older persisted marker is already overdue. Metrics distinguish startup from
interval runs
and report only attempt/start/completed, failed, lock-skip, overlap-skip, or
interval-skip timing and byte/count aggregates; object keys, local paths,
credentials, and provider error bodies are excluded.

### Scheduled `sync all`

When `dbrain serve remote` is kept alive through launchd, it can also run
`sync all` on an internal interval. The scheduler uses the same resolved
config/root, opens the local database for each run, and skips a tick if a
previous scheduled sync is still active.

```yaml
scheduler:
  sync_all:
    enabled: true
    interval: 1h
    run_on_start: false
    jitter: 5m
    source_limit: 100
    source_concurrency: 2
    categorize_limit: 25
    categorize_concurrency: 2
    categorize_timeout: 90s
    skip_categorize_images: true
    skip_github: false
    skip_youtube: false
    skip_categorize: false
```

The scheduled run uses the normal `sync all` preflight checks, so secret-backed
providers still need their configured `env:`, `op://`, or `keychain://`
references to resolve. Use `skip_categorize_images: true` when scheduled runs
target a text-only or slow local categorization backend. Use the other `skip_*`
fields for stages you do not want the background service to run. If the
background service does not have macOS Full
Disk Access for Apple Notes, either grant access to the binary/service context
that launchd runs or set `scheduler.sync_all.skip_apple_notes: true`.
Scheduled runs use the same `metrics.*` config as manual `sync all` and mark
their metric envelope with `invocation: "scheduler:interval"`.
Typed hard-failure notification behavior and its explicit provider setup live
under [`dbrain notify`](#dbrain-notify). Notifications do not monitor the
`serve remote` process itself.

`dbrain doctor full-disk-access` helps with the macOS approval loop. It reads
the LaunchAgent plist, reports the binary that launchd runs, optionally probes
the Apple Notes SQLite path through that target binary so macOS can attribute
the denied access to the right executable, and opens System Settings to Full
Disk Access. The running web service also exposes
`/api/doctor/full-disk-access` so restart checks can verify the actual
background process rather than an interactive child process. macOS still
requires the final approval in System Settings; dbrain does not write the TCC
database directly.

```sh
dbrain doctor full-disk-access
dbrain doctor full-disk-access --bin /opt/homebrew/bin/dbrain
dbrain doctor full-disk-access --probe=false --open=false
```

Scheduled and manual `sync all` runs share a local lock at
`<data_dir>/locks/sync-all.lock`. A manual `dbrain sync all` fails fast when the
scheduled service is already running, and the scheduler records a skipped run
when a manual sync already holds the lock.

Scheduler state is available from the running web surface:

```sh
curl -s https://dbrain.<tailnet>.ts.net/api/scheduler/sync-all
```

The response includes whether the scheduler is enabled, whether a run is active,
the next scheduled run time, and the last run's start, finish, status, and
error.

### `dbrain tsnet status`

Prints the resolved tsnet hostname, state directory, lock path, local state,
control URL, and active health using the same config/env/flag resolution as
`serve remote`. Status accepts the same target-shaping flags that affect
health output, including `--web`, `--mcp`, `--mcp-path`, `--tsnet-listen`,
`--tsnet-tls`, and `--tsnet-control-url`.

When a running `dbrain` process holds the state lock, status probes only the
configured web and MCP surfaces. Web probes expect `2xx`/`3xx`; MCP probes
accept `200` or `405` because browser-style `GET` may be rejected while
JSON-RPC `POST` is healthy. It reports `running`, `reachable`,
`web_reachable`, `mcp_reachable`, `cert_health`, `needs_login`, and
`control_url`. If MagicDNS lookup is unavailable to Go, status can use local
Tailscale peer status as a best-effort tailnet IP fallback while preserving TLS
certificate validation.

Human output is grouped into tables for node state, endpoint health, and, when
the remote web surface can answer `/api/scheduler/sync-all`, scheduled `sync
all` state. The scheduler table includes enabled/running state, interval,
jitter, current run reason/start/elapsed time, last run timestamps/status/error,
and next run time. JSON output keeps the same structured fields and adds a
`sync_all` object when the scheduler API is reachable.

```sh
dbrain tsnet status
dbrain tsnet status --json
```

### `dbrain tsnet reset`

Removes the resolved tsnet state directory after confirmation. It refuses to
run if another `dbrain` process holds the state lock. Interactive reset prints
the resolved hostname and state directory and requires typing `reset`; use
`--yes` only for scripts.

```sh
dbrain tsnet reset
dbrain tsnet reset --yes
```

### `dbrain hydrate x`

Requires a supported browser profile with valid X cookies. Chrome/Chromium is
the best-tested path. On macOS you may see a Keychain prompt the first time
cookie decryption is used. Structured hydrate progress is logged by default;
use `--no-debug` to quiet operational debug output.

```sh
dbrain hydrate x --limit 50
```

### `dbrain transcribe x-media`

Requires `mw` and `ffprobe`. `mw` performs the transcription and `ffprobe`
checks whether a downloaded X video or animated GIF has an audio stream worth
transcribing. Normal runs skip already classified items; use `--force` when you
explicitly want to retry failures or reprocess existing transcript items.

```sh
dbrain transcribe x-media --limit 50
```

### `dbrain ocr x-photos`

Extracts text from downloaded X photos. Hosted OCR defaults to the configured
OpenRouter/Gemini model, with local fallback support where configured.
You do not need Ollama, LM Studio, oMLX, or any configured local backend for the
default OpenRouter/Gemini OCR path. Fresh installs that do not configure
OpenRouter or a local OCR tool skip X photo OCR for configured `sync all` runs
until an OCR model or OpenRouter key is provided.

```sh
dbrain ocr x-photos --limit 50
```

For a read-only OCR bakeoff against the downloaded X photo corpus, use the
devtool. It defaults to the currently configured OCR model as the baseline and
compares it with `ollama/deepseek-ocr:3b`; it writes a Markdown report without
changing persisted OCR state.

```sh
go run ./cmd/devtools/ocr_model_compare --limit 30 --output /tmp/dbrain-ocr-compare.md
```

Useful variants:

```sh
go run ./cmd/devtools/ocr_model_compare --root . --limit 30 --download-missing --output /tmp/dbrain-ocr-compare.md
go run ./cmd/devtools/ocr_model_compare --limit 30 --json > /tmp/dbrain-ocr-compare.json
go run ./cmd/devtools/ocr_model_compare --limit 10 --models openrouter/google/gemini-3.1-flash-lite-preview,ollama/deepseek-ocr:3b,tesseract
```

### Model Bakeoffs

For read-only summary and categorization comparisons, use the model bakeoff
devtool. It runs the existing summary or categorization prompt against explicit
targets and models, reports timing and side-by-side outputs, and does not save
summaries, categories, or tags.

```sh
go run ./cmd/devtools/model_bakeoff \
  --mode source-summary \
  --lookup src:47acb64df52e \
  --model ollama/qwen3.6:27b \
  --model ollama/gemma4:31b \
  --output /tmp/dbrain-summary-bakeoff.md

go run ./cmd/devtools/model_bakeoff \
  --mode categorize-item \
  --lookup x:2052235121416188114 \
  --model ollama/qwen3.6:27b \
  --model openrouter/google/gemini-2.5-flash \
  --output /tmp/dbrain-categorize-bakeoff.md

go run ./cmd/devtools/model_bakeoff \
  --mode categorize-source \
  --lookup src:47acb64df52e \
  --model ollama/qwen3.6:27b \
  --model ollama/gemma4:31b \
  --json > /tmp/dbrain-categorize-source-bakeoff.json
```

For explicit local-provider parity checks across Ollama, LM Studio, oMLX, or a
configured OpenAI-compatible alias, pass `--parity-preset dbrain-modelfile`.
The report records provider, API model, transport, local/hosted flag, and
parameter strictness. Discover runner-specific model IDs before comparing:

```sh
ollama list
curl -s http://localhost:1234/v1/models
curl -s -H "Authorization: Bearer $DBRAIN_OMLX_API_KEY" http://127.0.0.1:8000/v1/models
```

Run providers in separate invocations when memory co-residency would bias
timing:

```sh
go run ./cmd/devtools/model_bakeoff \
  --mode source-summary \
  --lookup "$SOURCE_KEY" \
  --model lmstudio/qwen/qwen3.6-35b-a3b \
  --parity-preset dbrain-modelfile \
  --timeout 5m \
  --output /tmp/dbrain-source-summary-lmstudio.md

go run ./cmd/devtools/model_bakeoff \
  --mode source-summary \
  --lookup "$SOURCE_KEY" \
  --model ollama/dbrain:2026042701 \
  --model lmstudio/qwen/qwen3.6-35b-a3b \
  --model omlx/Qwen3.6-35B-A3B-MLX-4bit \
  --parity-preset dbrain-modelfile \
  --output /tmp/dbrain-local-backends.md

go run ./cmd/devtools/model_bakeoff \
  --mode categorize-source \
  --lookup "$SOURCE_KEY" \
  --model localai/test-model \
  --json > /tmp/dbrain-localai-bakeoff.json
```

### `dbrain import youtube`

Requires a browser profile with valid YouTube cookies, `yt-dlp`, and
`summarize`. When `--profile` is omitted, `dbrain` will try the bare browser
cookie source first and then retry discovered local Chromium-style profiles
such as `Default` and `Profile N`. A working local setup may also need `uv`.
For transcriptless videos, the best current setup is also `deno` or `node`,
plus `whisper-cli` and the `ggml-base.bin` model.

The importer pulls authenticated `Watch Later` and liked-video signals, stores
each feed entry as an item, stores the canonical video URL once as a source, and
keeps re-runs idempotent. YouTube source enrichment is transcript-first; when
captions are missing, `--transcriber auto` tries local audio transcription
before falling back to a skipped/no-content outcome.
At the start of each run it also removes deprecated `youtube_history` rows and
orphaned legacy YouTube sources from older importer versions; command output
reports those counts as `Items deleted` and `Sources deleted`.

```sh
dbrain import youtube --watch-later --liked --browser chrome --profile Default --limit 10 --transcriber auto
dbrain import youtube --watch-later --transcriber macwhisper
dbrain import youtube --watch-later --transcriber macwhisper:mlx:large-v3-turbo
summarize transcriber setup
```

### `dbrain import github stars`

Requires `GITHUB_TOKEN`. It uses the GitHub API directly, imports the star as
an append-only signal, stores the repo as a canonical `github` source, and
optionally creates and summarizes a linked homepage `web` source. The default
timeout is `2m` because local CLI-backed repo summaries can take longer than a
normal GitHub API round trip.

```sh
dbrain import github stars
```

### `dbrain extract links`

Requires `summarize`. It will prefer cached item `article_text` when
available, but still uses `summarize` for normalization and summarization. Use
`--concurrency` to run multiple source extract/summarize jobs in parallel after
discovery. The default concurrency is `4`, matching `sync all` and
`worker sources`; pass `--concurrency 1` for strictly sequential debugging.

```sh
dbrain extract links --discover-limit 100 --limit 25 --concurrency 4 --summarize=false
dbrain extract links --discover-limit 25 --limit 10 --concurrency 4 --length short
```

### `dbrain link add`

Adds one or more manually submitted URLs to the same source backlog used by
discovered links. By default it queues the source for the normal
`extract sources`, `worker sources`, or `sync all` flow; pass `--enrich` to
extract and summarize immediately.

```sh
dbrain link add "https://example.com/article"
dbrain link add "https://example.com/article" --enrich --length short
```

### `dbrain extract sources`

Requires `summarize`. This is the global source-backlog worker for already
known sources that still need extraction or summarization. Use `--concurrency`
to run multiple source extract/summarize jobs in parallel. The default is `4`;
pass `--concurrency 1` for strictly sequential debugging. Source freshness is
tracked with extract timestamps, summary timestamps, prompt versions, content
hashes, and summarize tool versions so refreshes can be policy-aware. Repeated
terminal extraction failures run a final Internet Archive Wayback fallback when
enabled; usable snapshots are saved as `extract_tool=wayback`, while short
archive shells are kept raw but skipped for summarization.

```sh
dbrain extract sources --limit 50 --concurrency 4 --length short
dbrain --no-caffeinate extract sources --limit 50 --length short --timeout 5m
```

### `dbrain worker sources`

Requires `summarize`. This is the long-running source-backlog worker: it
repeatedly runs `extract sources`-style batches until the queue is drained, and
can optionally keep polling for new source work with `--watch`. It supports
bounded parallelism via `--concurrency`. Use `--limit` to cap the total number
of sources processed in a single worker run, and `--batch-limit` to control
per-cycle batch size.

```sh
dbrain worker sources --limit 100 --concurrency 4
dbrain worker sources --watch --poll-interval 1m --idle-exit-after 30m --concurrency 4 --length short --timeout 5m
```

### `dbrain topic map`

No external tools required. Builds a topic graph from the local brain using
search plus the item/source link graph.

```sh
dbrain topic map "agent memory" --json
```

### `dbrain topic generate`

No external tools required. Writes a synthesized topic/MOC note under
`vault/topics/...` from the local brain, including sections like `Summary`,
`What This Topic Is`, `Main Angles`, entity pivots, open questions, and the
supporting note graph when that evidence exists.

```sh
dbrain topic generate "vector database"
```

### `dbrain topic refresh`

No external tools required. Rebuilds generated topic notes from their stored
frontmatter settings and refreshes the topic index.

```sh
dbrain topic refresh
dbrain topic refresh "vector database"
```

### `dbrain topic index`

No external tools required. Rebuilds the browsable topic index note from the
generated topic note set.

```sh
dbrain topic index
```

### `dbrain entity map`

No external tools required. Derives stable entities from local item/source
metadata and searches them by name, key, alias, or domain.

```sh
dbrain entity map "example"
dbrain entity map "example/project" --kind project --json
```

### `dbrain entity generate`

No external tools required. Writes matching entity notes under
`vault/entities/...` and refreshes the entity index.

```sh
dbrain entity generate "example/project" --kind project
```

### `dbrain entity index`

No external tools required. Re-derives all entities, writes their notes, and
rebuilds `vault/entities/index.md`.

```sh
dbrain entity index
```

### `dbrain stats items`

No external tools required. Reads item counts from `brain.db`.

```sh
dbrain stats items
dbrain stats items --source-type github_star --group-by none
```

### `dbrain stats sources`

No external tools required. Reads source counts from `brain.db`.

```sh
dbrain stats sources --source-type github --extract-tool github-api --group-by summary-status
```

### `dbrain stats activity`

No external tools required. Shows the latest item/source write timestamps plus
recent write counts inside a configurable time window.

```sh
dbrain stats activity
dbrain stats activity --window 30m
```

### `dbrain stats backlog`

No external tools required. Shows remaining queued work by pipeline stage and
whether the current queues are drained.

```sh
dbrain stats backlog
```

### `dbrain stats pipeline`

No external tools required. Shows policy-aware enrichment coverage across the
main pipeline stages.

### `dbrain whats-new`

No external tools required. Reads a cursor-paged review feed from `brain.db`
for newly imported, updated, enriched, failed, or blocked evidence. Pass
exactly one of `--since` or `--cursor`. `--since` accepts RFC3339 timestamps,
local-offset RFC3339 timestamps, or relative durations such as `24h` and `7d`.
Use `--view entities` for compact item/source groups when asking what changed
or what deserves attention; the default `events` view preserves raw pipeline
event chronology. Pagination and `--limit` still apply to raw review events, so
callers that combine multiple pages should de-duplicate entity rows by
`entity_key`. Continue pagination only while `truncated` is true; `next_cursor`
is still emitted on the final page for high-watermark bookkeeping.

```sh
dbrain whats-new --since 24h
dbrain whats-new --since 2d --view entities
dbrain whats-new --since 2026-06-21T15:00:00Z --json
dbrain whats-new --since 24h --view entities --json
dbrain whats-new --cursor "$CURSOR" --limit 100 --types imports,enrichments
```

### `dbrain eval mcp`

No external tools required. Runs read-only retrieval regression checks against
a JSON case file using the same retrieval path exposed through MCP research
tools. Use `--write-example <path>` to generate a starter case file and
`--json` for structured CI-friendly output.

```sh
dbrain eval mcp --write-example evals/local/mcp.json
dbrain eval mcp --file evals/local/mcp.json
```

### `dbrain eval research`

No external tools required unless the cases exercise model-backed synthesis.
Runs regression cases against the full research/chat harness. Use
`--write-example <path>` to create a starter file, `--file <path>` to run a
case set, and `--json` for structured output. The `propose` subcommand can
draft candidate cases from saved transcripts or research traces, and `diff`
compares a saved trace against a fresh pack build.

```sh
dbrain eval research --write-example evals/local/research.json
dbrain eval research --file evals/local/research.json
dbrain eval research propose --from-trace data/research-runs/<run-id> --output evals/local/research.proposed.json
dbrain eval research diff --trace data/research-runs/<run-id>
```

### `dbrain version`

No external tools required. Prints build metadata including commit, build time,
git status, Go version, git version, build platform, and module info. Use
`--json` for structured output.

```sh
dbrain version
dbrain version --json
```

### `dbrain research`

Research is read-only and works directly from `brain.db`. It returns a research
pack with evidence, query/tag planning metadata, coverage notes, and optional
related evidence or topic brief data, then synthesizes a grounded local answer
by default. Web Research, Chat, CLI research, and MCP research packs use
model-assisted planning by default when a summary model is configured. The
harness asks the configured local model for a small bounded query plan with
aliases, alternate phrasings, and title-like variants, then validates and merges
it with deterministic fallback concepts before retrieval. Research packs expose
the planner metadata, query variants, and concept coverage signals so broad
natural-language questions can retry with stronger terms and prefer directly
matching evidence over broad near-misses. Use `--no-planner` or
`disable_planner=true` to force deterministic planning, and `--retrieval-only`
when you only want the evidence pack.
Synthesis requires `--model` or a configured `DBRAIN_SUMMARY_MODEL` /
`SUMMARIZE_MODEL`; it will not silently let the external summarizer choose a
hosted fallback.

Semantic retrieval defaults to configured mode `off`. `--semantic` force-enables
effective `on`; `--no-semantic` force-enables effective `off`; passing both is
an error. Effective `shadow` or `on` requires a configured local Ollama model
and positive dimensions. `shadow` computes bounded, content-free comparisons
but preserves lexical evidence/order/synthesis exactly. `on` RRF-fuses lexical
and semantic candidates while preserving protected evidence and provenance.
Provider/search failures, an unavailable native root, or more than the
configured exact-scan cap (default 25,000 current ready embeddings for the
configured profile), fail the semantic lane open to lexical evidence with an
explicit status and reason. The cap is counted before request filters are
applied. A semantic lane is `used` only after candidates pass current SQLite
validation and exact reranking; a loaded root alone is not a ranking-quality
claim.

```sh
dbrain research "What validates Kubernetes manifests?"
dbrain research "Show me GitHub repos about vector databases" --source-type github
dbrain research "What is Agent Memory?" --include-related --related-limit 2
dbrain research "What do I have in my brain about Mark Carney?" --retrieval-only --json
dbrain research "What do I know about local models?" --model ollama/qwen3.6:35b
dbrain research "Calgary father killed two kids" --retrieval-only
dbrain research "K8s Helm alternatives" --planner-model ollama/qwen3.6:35b --retrieval-only
dbrain research "K8s Helm alternatives" --no-planner --retrieval-only
dbrain research "K8s Helm alternatives" --semantic --retrieval-only --json
```

### `dbrain semantic`

The semantic subsystem exposes five operational commands. It uses deterministic
SQLite retrieval chunks, local Ollama embeddings, segmented native USearch roots
on supported tagged builds, exact SQLite validation/reranking, and an exact-scan
fallback for bounded profiles.

```sh
dbrain semantic status
dbrain semantic status --json
dbrain semantic refresh
dbrain semantic refresh --max-duration 30m
dbrain semantic refresh --json
dbrain semantic chunk --limit 100
dbrain semantic chunk --until-idle --max-duration 20m
dbrain semantic embed --limit 100 --batch-size 16
dbrain semantic embed --until-idle --max-duration 20m
dbrain semantic verify --limit 1000 --resume <chunk-id>
dbrain semantic verify --repair-counters --limit 1000
```

`status` is read-only and diagnoses configuration/readiness. `chunk` creates,
updates, and deletes deterministic derived chunks from a durable dirty-parent
queue. `embed` generates missing embeddings for the configured model/dimension
profile with keyset pagination. `--until-idle` drains observed work across
pages, while `--max-duration` returns an `interrupted` resumable result instead
of losing progress. `verify` checks bounded pages of ready vectors and
provenance; the explicit `--repair-counters` option transactionally rebuilds
the readiness aggregates before verification. An unbuilt configured profile is
a valid empty verification result. Ordinary status and research do not repair
state implicitly. There
is no implicit migration between chunker profiles: after a chunker version
change, run `semantic chunk` to replace derived chunks, then `semantic embed`
to build embeddings for the new profile before comparing retrieval quality.
There is no standalone semantic purge command. The current deletion integration
is item-only: Apple Notes `--forget-excluded` calls the explicit item
indexed-content purge, which synchronously deletes that item's retrieval chunks,
embeddings, and stale affected generation metadata. This does not claim that
every delete path or future parent kind has the same integration.

Runtime construction reads SQLite readiness under a 250 ms budget before query
provider construction, without importing a native root. After embedding, the
query has a separate 250 ms shared-generation lease-acquisition budget;
`generation_busy` means only contention in that acquisition. A cold native
root is then imported lazily and the caller waits up to five seconds:
`root_load_timeout` returns lexical evidence while the detached load may still
warm the process-local runtime cache. Warm requests reuse the validated
in-memory root but still take the query-time generation lease and SQLite
validation/reranking path. `native_root_artifacts_unavailable` reports native
artifact or root-validation failure; `runtime_readiness_unavailable` reports a
query-time readiness snapshot that cannot be read or is no longer a stable
searchable root. All four reasons fail open to lexical evidence. A cold import
retains the shared lease until it publishes or discards the root, so refresh or
GC may wait; the five-second value does not interrupt native loading, and
reader grace remains defense in depth. Configuration may lower, but cannot
raise, the hard 25,000-vector exact scan ceiling. Complete larger profiles
report `needs_index` and stay lexical. The readiness planner is also bounded:
it rejects more than 4,096 evidence sections, limits exact materialization to
8 MiB, and limits allocation-free preflight scanning to 128 MiB. Full `status`
and `verify` remain the authoritative maintenance surfaces for detecting
aggregate drift.

`semantic refresh` runs the durable projection, embedding, flush, compaction,
verification, and readiness pipeline. Successful manual and scheduled `sync
all` runs invoke it automatically when semantic mode is `shadow` or `on`; the
command remains the explicit diagnostic and recovery surface. Other embedding
providers and default-on behavior remain deferred.

### `dbrain search`

No external tools required. Searches items and sources from local SQLite/FTS,
including indexed user tags and derived item text.

```sh
dbrain search kubernetes
```

### `dbrain get`

No external tools required. Loads an item or source by source key or numeric
ID, with DB-first evidence sections used by MCP and CLI research flows.

```sh
dbrain get x:2045912259210485815
```

### `dbrain categorize analyze`

No external tools required. Reads existing item/source tags and reports token
frequency so you can decide what belongs in `categories.yaml`. Use `--draft`
when you want a starter YAML shape instead of the plain frequency report.

```sh
dbrain categorize analyze --min-count 3
dbrain categorize analyze --draft
```

### `dbrain categorize item`

Sends a single item's full content bundle (post text, summary, transcript, OCR
text, article body, and images when enabled) to the configured categorization
LLM and returns suggested categories and tags. Use `--apply` to save the result
directly to the item's `user_tags` field and re-index FTS. Image categorization
is enabled by default and embeds local or R2-stored photos as base64 for
vision-capable models; use `--images=false` to disable it. The model is
resolved from `--model`, `DBRAIN_CATEGORIZE_MODEL`, or the default
`openrouter/google/gemini-2.5-flash`.

```sh
dbrain categorize item --lookup x:1844700656625406274
dbrain categorize item --lookup x:1844700656625406274 --apply
dbrain categorize item --lookup x:1844700656625406274 --apply --images=false --model ollama/qwen2.5:7b-instruct
```

### `dbrain categorize batch`

Same as `dbrain categorize item` but processes multiple items in one pass. By
default only items with an empty `user_tags` field are selected; use `--force`
to re-categorize everything. `--limit` and `--concurrency` control throughput.
Use `--apply` to save results and `--json` for structured output. Saved
categorizer tags are merged with existing `user_tags` without duplicate
entries; existing tags are not overwritten. `dbrain sync all` runs this same
apply path for item rows at the end of the sync pipeline unless
`--skip-categorize` is passed.

```sh
dbrain categorize batch --limit 50 --concurrency 4 --model ollama/qwen2.5:7b-instruct --apply
dbrain categorize batch --force --limit 100 --concurrency 2 --model ollama/qwen2.5:7b-instruct --apply
dbrain categorize batch --limit 50 --json
```

### `dbrain categorize source`

Sends one linked source's metadata, summary, description, and extracted text to
the same categorizer. Use `--apply` to save the result to the source's own
`user_tags` field and re-index source search. Source tags are distinct from the
tags on saved items that backlink to the source.

```sh
dbrain categorize source --lookup src:db9d3b4551dd
dbrain categorize source --lookup https://www.example.com/ --apply
```

### `dbrain categorize sources`

Batch-categorizes linked sources. By default only sources with empty
`user_tags` are selected; use `--force` to re-categorize existing source tags.
This is useful when you want a source-centric view of linked articles,
repositories, papers, and videos rather than only the tags on the saved item
that referenced them. `dbrain sync all` runs this same apply path for source
rows at the end of the sync pipeline unless `--skip-categorize` is passed.

```sh
dbrain categorize sources --limit 50 --concurrency 2 --apply
dbrain categorize sources --force --limit 100 --json
```

### `dbrain categorize vocab`

Analyzes existing item/source `user_tags`, sends the highest-frequency unmapped
tokens to a local Ollama model, and asks for conservative `categories.yaml`
cleanup suggestions. Unlike the main summary/research/categorization paths,
this vocabulary cleanup helper is currently Ollama-only. The default is
review-only; pass `--apply` to merge safe suggestions into `categories.yaml`,
and `--repair` to immediately rewrite existing item/source tags with the updated
vocabulary.

The command intentionally keeps a hard safety filter around LLM output. It
accepts boring lexical cleanup such as plural/singular variants and near-typos,
but rejects broad semantic collapses like `python -> programming-languages` or
`software-development -> software-engineering`.

```sh
dbrain categorize vocab --model ollama/dbrain:latest
dbrain categorize vocab --limit 350 --min-count 5 --timeout 5m --apply --repair
```

### `dbrain categorize repair`

Repairs existing item and source `user_tags` using the configured category
rewrite vocabulary. This is useful after adding aliases or normalizing tag
forms.

```sh
dbrain categorize repair
```

### `dbrain repair notes`

No external tools required. Rebuilds rendered Markdown notes from `brain.db`,
which is useful if antivirus or sync tooling removed files from `vault/`.

```sh
dbrain repair notes
dbrain repair notes --missing-only=false --sources
```

### `dbrain repair pruned-media`

Finds archived media that was pruned locally but is now needed by pending OCR
or X media transcription work. The command is a read-only dry-run by default:
it opens the existing database without migrations, lists candidate counts, and
performs no network, database, or media writes. Pass `--apply` explicitly to
re-download matching media through the normal media-download persistence path.

With neither category flag, both OCR and transcription candidates are
selected. Use `--ocr` or `--transcripts` to limit the category. `--limit`
defaults to 5,000 and is capped at 5,000 independently for each selected
category; `--timeout` defaults to 45 seconds per media download. `--json`
emits the same bounded aggregate counters as JSON.

The repair command restores media only. Existing OCR and transcription workers
perform enrichment afterward; `sync all` does not run this repair
automatically.

```sh
dbrain repair pruned-media
dbrain repair pruned-media --ocr --limit 500 --json
dbrain repair pruned-media --apply --timeout 45s
```

### `dbrain repair sources`

No external tools required. Clears extraction and summary state for selected
sources so they can be reprocessed. Use `--domain <domain>` for a whole domain
or `--source <id>` for specific rows. Additional filters such as
`--source-type`, `--extract-status`, `--summary-status`, `--failure-kind`, and
`--min-failures` combine with AND semantics, which is useful for retrying a
known failed class without resetting unrelated rows. The command prints the
number of matched sources first and asks for confirmation unless `--dry-run` or
`--yes` is passed. For X article repair, add `--rehydrate-x-articles` to also
clear the linked X item hydration cache so the next `hydrate x` / `sync all`
run refetches article metadata instead of replaying stale previews.
This is a local derived-state reset, not an upstream deletion.

```sh
dbrain repair sources --domain canada.ca --dry-run
dbrain repair sources --domain canada.ca --yes
dbrain repair sources --source-type web --extract-status error --extract-status dead --dry-run
dbrain repair sources --source-type web --extract-status error --extract-status dead --yes
dbrain repair sources --source-type x_article --extract-status dead --summary-status error --failure-kind x_article_shell --min-failures 3 --dry-run
dbrain repair sources --source-type x_article --extract-status dead --summary-status error --failure-kind x_article_shell --min-failures 3 --rehydrate-x-articles --yes
```

### `dbrain repair fts`

No external tools required. Rebuilds the SQLite full-text search index from the
current item/source/enrichment rows.

```sh
dbrain repair fts
```

### `task web-install`

Requires `npm`. Installs the Svelte/Vite dependencies used to rebuild the web
UI source.

### `task web-build`

Requires `npm`. Rebuilds the embedded `web/ui/dist` assets from the Svelte
source tree. `task build` embeds the currently tracked `web/ui/dist` assets but
does not rebuild them, so run `task web-build` and commit the dist changes when
UI source or UI build configuration changes. See
[docs/release-build.md](docs/release-build.md) for the release checklist.

### `task fmt`

Requires `task` and `go`.

### `task lint`

Requires `task`, `go`, and `golangci-lint`.

### `task test`

Requires `task` and `go`. Runs the same `go test -cover -race ./...` command as
`task test-ci`, but with the current shell environment still present. Use it for
local ambient-env debugging rather than as the standard gate.

### `task test-ci`

Requires `task` and `go`. This is the standard full test gate. It runs
`go test -cover -race ./...` with a clean CI-like environment so local secrets,
provider variables, and personal config do not hide test isolation bugs.
