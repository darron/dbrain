# dbrain Commands

This document is the detailed command and task reference for `dbrain`. Every command supports `--help`; the CLI help is the source of truth for flags.

## Command Index

- `dbrain archive media`
- `dbrain auth github approve <username>`
- `dbrain auth github list`
- `dbrain auth github remove <username>`
- `dbrain auth mcp token add <name>`
- `dbrain auth mcp token list`
- `dbrain auth mcp token revoke <id-or-name-or-fingerprint>`
- `dbrain categorize batch`
- `dbrain categorize item`
- `dbrain categorize repair`
- `dbrain categorize source`
- `dbrain categorize sources`
- `dbrain config env`
- `dbrain config paths`
- `dbrain entity generate <query>`
- `dbrain entity index`
- `dbrain entity map [query]`
- `dbrain eval mcp`
- `dbrain extract links`
- `dbrain extract sources`
- `dbrain feed add <url>`
- `dbrain feed check [feed-key-or-url]`
- `dbrain feed list`
- `dbrain feed status <feed-key-or-url>`
- `dbrain get <source-key-or-id>`
- `dbrain hydrate x`
- `dbrain import apple-notes`
- `dbrain import github stars`
- `dbrain import safari-tabs`
- `dbrain import x-bookmarks`
- `dbrain import youtube`
- `dbrain link add <url>`
- `dbrain ocr x-photos`
- `dbrain repair fts`
- `dbrain repair notes`
- `dbrain repair sources`
- `dbrain research <question>`
- `dbrain search <query>`
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
Usage:
  dbrain [flags]
  dbrain [command]

Available Commands:
  archive     Manage archived media and other durable storage tiers
  categorize  Categorize items or linked sources with an LLM
  config      Show active configuration and storage paths
  entity      Derive and render entities from the local brain
  eval        Run local retrieval quality checks
  extract     Extract and summarize linked sources
  get         Load an item or source note
  hydrate     Hydrate canonical source data
  import      Import source data into the brain
  launchd     Install or print a macOS launchd service for dbrain
  link        Add and manage manually submitted links
  ocr         Extract text from downloaded images
  repair      Repair derived local artifacts
  research    Research the local brain with evidence and local synthesis
  search      Search items and sources
  serve       Serve local interfaces
  sqlite      Manage the local SQLite database
  stats       Show database counts and import progress
  sync        Run multi-stage refresh flows
  topic       Build and write topic maps from the local brain
  transcribe  Transcribe downloaded local media
  version     Print build and version information
  worker      Run long-lived background-style worker loops

Environment:
  --config-file wins over --root, DBRAIN_CONFIG_FILE, and DBRAIN_ROOT.
  Defaults: config in ~/.config/dbrain, state in ~/.local/share/dbrain.
  Runtime values resolve from shell env, then .envrc/.env, then config.yaml.
  Run "dbrain config env" for the full environment/config key table.
```

### `dbrain config paths`

Prints the active config, categories, data, database, vault, media, temp, cache,
and log paths. Use `--json` for automation.

```sh
dbrain config paths
```

### `dbrain config env`

Prints the supported environment variables and matching `config.yaml` keys. Use
`--json` for automation. This command is the authoritative source for the table
in this README.

```sh
dbrain config env
dbrain config env --markdown
```

### `dbrain import x-bookmarks`

Direct X bookmark import path. Requires a supported browser profile with valid
X cookies. Chrome/Chromium is the best-tested path.

```sh
dbrain import x-bookmarks --limit 25
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
dbrain import apple-notes --dry-run --show-titles
dbrain import apple-notes --limit 25
dbrain import apple-notes
dbrain import apple-notes --force
dbrain import apple-notes --summarize=false
dbrain import apple-notes --exclude-folder Private
dbrain import apple-notes --exclude-folder Private --forget-excluded
dbrain import apple-notes --skip-attachment-ocr
```

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
dbrain import safari-tabs --device dfone --dry-run --show-titles
dbrain import safari-tabs --device dfone
dbrain import safari-tabs --device dfone --older-than 168h
dbrain import safari-tabs --device dfone --limit 100
```

### `dbrain sync all`

Runs the regular incremental refresh pipeline in one command: optional Apple
Notes import, optional Safari tabs import, direct X bookmark import, X
hydration, X media audio transcription, X photo OCR, link
discovery/enrichment, GitHub stars import, YouTube, RSS/Atom/JSON Feed
import, and an optional source-backlog worker batch. It then categorizes
uncategorized items and linked sources with the same categorizer used by
`dbrain categorize batch` and `dbrain categorize sources`, unless
`--skip-categorize` is passed. If enabled, the media archive stage runs after
categorization so image categorization can still use local photo files before
they are uploaded/pruned. Image
categorization is enabled for items by default; use `--categorize-images=false`
to disable it for text-only models. `--categorize-limit` is applied separately
to items and sources, so `--categorize-limit 25` can process up to 25 item rows
and 25 source rows.

X hydration uses `--x-limit`. X media transcription and X photo OCR can be
bounded independently with `--x-media-limit` and `--x-photo-ocr-limit`; either
limit falls back to `--x-limit` when left at 0. In the default configuration
this combines the requirements of X bookmark import, X hydration, X media
transcription, X photo OCR, link/source enrichment, YouTube import, and
categorization. A practical local setup usually includes a supported
Chrome/Chromium profile with valid cookies plus Ollama or an OpenRouter key,
`mw`, `ffprobe`, `summarize`, and `yt-dlp`. It supports `--skip-*` flags when
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
dbrain sync all --safari-tabs --safari-tabs-device dfone --length short --timeout 5m
dbrain sync all --skip-categorize --length short --timeout 5m
dbrain sync all --categorize-limit 25 --categorize-concurrency 2 --length short --timeout 5m
dbrain sync all --watch --poll-interval 1m --idle-exit-after 30m --length short --timeout 5m
```

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

`feed enable` clears previous feed health diagnostics and makes the feed
eligible for an immediate check. `feed disable` stops future checks without
removing already imported feed entries, items, sources, or rendered notes.

### `dbrain archive media`

Optional manual archive/prune pass for finalized media. It can either just
mark/prune already-uploaded media or upload directly to an S3-compatible bucket
first when `--upload` or archive-upload env vars are configured. `--prune-local`
deletes a local media file only after all rows sharing that `local_path` are
archived.

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
    skip_github: false
    skip_youtube: false
    skip_categorize: false
```

The scheduled run uses the normal `sync all` preflight checks, so secret-backed
providers still need their configured `env:`, `op://`, or `keychain://`
references to resolve. Use the `skip_*` fields for stages you do not want the
background service to run. If the background service does not have macOS Full
Disk Access for Apple Notes, either grant access to the binary/service context
that launchd runs or set `scheduler.sync_all.skip_apple_notes: true`.

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

### `dbrain eval mcp`

No external tools required. Runs read-only retrieval regression checks against
a JSON case file using the same retrieval path exposed through MCP research
tools. Use `--write-example <path>` to generate a starter case file and
`--json` for structured CI-friendly output.

```sh
dbrain eval mcp --write-example evals/local/mcp.json
dbrain eval mcp --file evals/local/mcp.json
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

```sh
dbrain research "What validates Kubernetes manifests?"
dbrain research "Show me GitHub repos about vector databases" --source-type github
dbrain research "What is Agent Memory?" --include-related --related-limit 2
dbrain research "What do I have in my brain about Mark Carney?" --retrieval-only --json
dbrain research "What do I know about local models?" --model ollama/qwen3.6:35b
dbrain research "Calgary father killed two kids" --retrieval-only
dbrain research "K8s Helm alternatives" --planner-model ollama/qwen3.6:35b --retrieval-only
dbrain research "K8s Helm alternatives" --no-planner --retrieval-only
```

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

### `dbrain categorize item`

Sends a single item's full content bundle (post text, summary, transcript, OCR
text, article body, and images when enabled) to a local Ollama or OpenRouter
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
cleanup suggestions. The default is review-only; pass `--apply` to merge safe
suggestions into `categories.yaml`, and `--repair` to immediately rewrite
existing item/source tags with the updated vocabulary.

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

Requires `task` and `go`.
