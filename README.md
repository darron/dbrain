# dbrain

![Banner showing dbrain](docs/banner.jpg)

`dbrain` is a local-first second-brain scaffold for incremental imports from X
bookmarks, Apple Notes, GitHub stars, YouTube, Safari tabs and manually
submitted web links, with Markdown note rendering for Obsidian, Open Knowledge
Format export, and local query over the imported corpus.

## Documentation Map

- [README.md](README.md): install, quick orientation, safety model, and
  configuration notes.
- [COMMANDS.md](COMMANDS.md): full command index and detailed command/task
  reference.
- [TAILSCALE.md](TAILSCALE.md): remote web/MCP access, built-in `tsnet`,
  Tailscale Funnel, tailnet policy, DNS, and public-auth operations.
- [MCP.md](MCP.md): MCP tools, research semantics, transports, client config,
  and evals.
- [browser/chrome-extension/README.md](browser/chrome-extension/README.md) and
  [browser/safari-extension/README.md](browser/safari-extension/README.md):
  browser extensions for saving the active tab URL to dbrain.
- [docs/architecture.md](docs/architecture.md): package and data-flow architecture.
- [docs/research-harness.md](docs/research-harness.md): current research/chat
  harness behavior, limitations, and improvement roadmap.
- [docs/open-knowledge-format-plan.md](docs/open-knowledge-format-plan.md):
  OKF export design, profile scope, validation rules, and implementation notes.
- [docs/schema-migrations.md](docs/schema-migrations.md): SQLite schema and
  migration guidance.
- [docs/web-route-capabilities.md](docs/web-route-capabilities.md): web route
  capability and trust-boundary map.

## Install

Install the latest released `dbrain` CLI with Homebrew:

```sh
brew install darron/tap/dbrain
```

Or tap once and install by formula name:

```sh
brew tap darron/tap
brew install dbrain
```

Verify the installed binary:

```sh
dbrain version
```

## Requirements

Install the common local toolchain with Homebrew:

```sh
brew install go go-task/tap/go-task golangci-lint sqlite yt-dlp ffmpeg node deno ollama tesseract
brew install --cask google-chrome
```

Runtime tools and services:

- **Chrome or Chromium**: recommended for cookie-backed X and YouTube imports.
- **`summarize`**: required for source extraction and summary-backed answer synthesis. Verify with `summarize --help`.
- **`mw`**: MacWhisper CLI, required for `dbrain transcribe x-media` and the default X media step in `sync all`.
- **`ffprobe`**: required for X media transcription. It is installed by Homebrew's `ffmpeg` package.
- **`yt-dlp`**: required for `dbrain import youtube`.
- **`deno` or `node`**: recommended for YouTube challenge solving through `yt-dlp`.
- **`uv`**: recommended for `summarize` helper environments and transcriber setup flows.
- **`whisper-cli`**: optional fallback for YouTube audio transcription when captions are unavailable.
- **`~/.summarize/cache/whisper-cpp/models/ggml-base.bin`**: optional model file used by the `whisper-cli` fallback.
- **`ollama`**: optional local model runtime for source summaries, answer synthesis, OCR, and categorization.
- **`tesseract`**: optional local fallback for OCR.
- **`sqlite3`**: optional, but useful for inspecting `brain.db`.
- **`task`**: required for the top-level development tasks.
- **`golangci-lint`**: required for `task lint`.
- **`npm`**: required for `task web-install` and `task web-build`.
- **`caffeinate`**: optional macOS helper used automatically for long-running leaf commands when available.

Optional hosted services:

- **GitHub token**: `GITHUB_TOKEN` for `dbrain import github stars`.
- **OpenRouter**: `DBRAIN_OPENROUTER_API_KEY` or `OPENROUTER_API_KEY` for hosted categorization, OCR, and model calls.
- **S3-compatible storage / Cloudflare R2**: R2/S3 env or config values for media and SQLite archives.

Apple Notes import is local and direct-SQLite. On macOS it may require granting
Full Disk Access to the `dbrain` binary or, more reliably for local builds, to
the terminal or IDE app launching it. Rebuilding `bin/dbrain` may invalidate a
binary-specific permission grant.

For development in this checkout without touching installed state:

```sh
export DBRAIN_ROOT=.
task build
dbrain config paths
dbrain config env
```

## Commands

See [COMMANDS.md](COMMANDS.md) for the full command index and detailed command/task reference.

Common entry points:

- `dbrain serve web`
- `dbrain serve remote`
- `dbrain serve mcp`
- `dbrain sync all`
- `dbrain okf export`
- `dbrain research <question>`
- `dbrain search <query>`
- `dbrain get <source-key-or-id>`
- `dbrain config env`

## Safety And Trust Model

`dbrain` is local-first, but it stores high-signal personal data. Treat
`brain.db`, rendered vault notes, generated OKF bundles, media files, logs,
temp files, chat transcripts, and tsnet state as private local state. Keep
`data/`, `vault/`, `okf/`,
`tmp/`, `cache/`, `logs/`, `.env`, `.envrc`, `.gocache/`, `.gomodcache/`,
`web/ui/node_modules/`, and `bin/` out of git and public release archives unless
you intentionally scrub and include them.

Imports are intended to be import-only against upstream services and apps. X,
GitHub, YouTube, Apple Notes, and Safari tab flows materialize local evidence;
Apple Notes and Safari tabs read from dbrain-owned SQLite snapshots. Normal
imports should not mutate upstream apps or delete local memories just because an
upstream bookmark, tab, note, star, or video later disappears.

`dbrain serve web` and `dbrain serve remote --web` are trusted read/write
administration surfaces. They can edit tags, queue links, save diagnostic chat
transcripts, trigger model-backed research/synthesis, and access archived media
helpers. `serve remote` relies on Tailscale/tsnet identity, ACLs, node tags, and
same-origin checks by default. Optional GitHub OAuth can add a dbrain session
gate for the web UI when `auth.enabled` is configured, but the default remains
the existing no-login local/trusted-network behavior. Do not expose the web UI
through Tailscale Funnel or a public reverse proxy unless you have explicitly
reviewed the full route surface and auth boundary. `--tsnet-funnel` is public
exposure on the same tsnet node identity, hostname, state directory, and auth
credentials; it is not a separate dbrain feature set. MCP surfaces are
read-only, but they still expose local brain content to connected clients.
Optional DB-backed MCP bearer auth can protect Streamable HTTP MCP endpoints
when `mcp.auth.enabled` is set; startup logs warn loudly when HTTP or tsnet MCP
is served without that guard.

Model-backed commands can send local evidence to the configured model provider.
Local Ollama calls stay on the configured Ollama endpoint. Hosted OpenRouter or
OpenAI-compatible calls may receive source extracts, note text, item text,
transcripts, OCR text, tags, and images depending on the command. Web, CLI, and
MCP research use model-assisted query planning by default when a planner or
summary model is configured; use `--no-planner`, `disable_planner=true`, or
retrieval-only modes when you want deterministic local retrieval without planner
model calls.

Archive features use S3-compatible storage only when configured. Media archives
and SQLite snapshots can contain personal content. A public media base URL makes
archived media links anonymously readable wherever that bucket policy allows;
without a public base URL, the web UI can still proxy or sign archive access for
trusted web users.

Local maintenance commands can delete, replace, or reset local dbrain state:
`dbrain archive media --prune-local` can remove local media files after archived
coverage is complete, `dbrain sqlite restore` replaces the active SQLite DB
after moving existing DB files aside, `dbrain tsnet reset` removes durable
Tailscale node state, `dbrain import apple-notes --forget-excluded` purges
indexed local content for notes that are now excluded, and `dbrain import
youtube` prunes deprecated `youtube_history` rows and orphaned legacy YouTube
sources as part of its import cleanup. `dbrain repair sources` clears selected
derived extraction/summary state so it can be rebuilt. Prefer `--dry-run` on
commands that offer it.

See [docs/architecture.md](docs/architecture.md) for the current package/state
architecture and [docs/web-route-capabilities.md](docs/web-route-capabilities.md)
for the web route capability matrix. See
[docs/schema-migrations.md](docs/schema-migrations.md) for SQLite migration,
backup, restore, and downgrade policy. See
[docs/maintenance-operations.md](docs/maintenance-operations.md) for local
delete, purge, prune, restore, and reset paths.

## Dev Tasks

- `task build`
- `task fmt`
- `task lint`
- `task test`
- `task test-mcp`
- `task web-build`
- `task web-install`

## Configuration And Layout

Installed/default layout:

- `~/.config/dbrain/config.yaml`: optional configuration file
- `~/.config/dbrain/categories.yaml`: tag rewrite/category vocabulary
- `~/.local/share/dbrain/brain.db`: local SQLite state
- `~/.local/share/dbrain/vault/items/...`: rendered Markdown notes for Obsidian
- `~/.local/share/dbrain/vault/sources/...`: rendered Markdown notes for linked sources
- `~/.local/share/dbrain/vault/entities/...`: derived entity notes and entity index
- `~/.local/share/dbrain/vault/topics/...`: generated topic/MOC notes
- `~/.local/share/dbrain/okf/current/...`: generated private OKF bundle
- `~/.local/share/dbrain/tmp`: temporary working files
- `~/.local/share/dbrain/cache`: cache files
- `~/.local/share/dbrain/logs`: log files

`dbrain` honors `XDG_CONFIG_HOME` and `XDG_DATA_HOME`; if set, the same
`dbrain` subdirectories are created under those bases.

To pin a command or service to one installed config file without inheriting a
checkout's `DBRAIN_ROOT`, pass `--config-file <path>` or set
`DBRAIN_CONFIG_FILE=<path>`. The config directory is the file's parent
directory; data, logs, cache, temp files, and the vault still default to the XDG
data layout unless separately configured by a feature-specific setting.

For local development or isolated runs, pass `--root <dir>` or set
`DBRAIN_ROOT=<dir>`. Explicit roots keep the original self-contained layout:

- `<dir>/config.yaml`
- `<dir>/categories.yaml`
- `<dir>/data/brain.db`
- `<dir>/vault/...`
- `<dir>/okf/current/...`
- `<dir>/tmp`, `<dir>/cache`, and `<dir>/logs`

For repo-local development, this keeps commands pointed at the checkout:

```sh
export DBRAIN_ROOT=.
```

Resolution order for config layout is `--config-file`, `--root`,
`DBRAIN_CONFIG_FILE`, `DBRAIN_ROOT`, then XDG defaults.

Configuration currently resolves in this order: shell environment, `.envrc` or
`.env` in the config/root directory, then `config.yaml`. The YAML file can use
exact environment-style keys under `env`, or cleaner grouped keys:

```yaml
summary:
  model: ollama/qwen3.6:35b-a3b
  language: English

openrouter:
  api_key: op://Private/dbrain/OPENROUTER_API_KEY
  base_url: https://openrouter.ai/api/v1

ollama:
  base_url: http://127.0.0.1:11434

http:
  user_agent: ""

source:
  reader:
    base_url: https://r.jina.ai/
    domains: canada.ca,open.canada.ca,fintrac-canafe.canada.ca
  wayback:
    enabled: true
    availability_url: https://archive.org/wayback/available?url={escaped_url}

archive:
  provider: r2
  bucket: dbrain-media
  upload: true

env:
  GITHUB_TOKEN: keychain://dbrain/github-token
```

Secret-bearing fields can be direct values or typed references. Supported
references are `env:NAME`, `op://vault/item/field`, and
`keychain://service/account`. References are resolved by `dbrain` only when a
command needs that secret, so they do not need to be exported into your whole
shell session.

For macOS Keychain, store a secret with:

```sh
security add-generic-password -U -s dbrain -a openrouter-api-key -w "..."
```

Then reference it from `config.yaml`:

```yaml
openrouter:
  api_key: keychain://dbrain/openrouter-api-key
```

[config.yaml.sample](config.yaml.sample) contains every currently supported grouped config value
with its matching environment variable comment on the same line:

```sh
cp config.yaml.sample ~/.config/dbrain/config.yaml
```

## Preflight Checks

`dbrain` runs lightweight preflight checks after resolving the active
configuration. The checks are meant to catch missing local vocabulary files and
missing secrets before a long import or enrichment run does partial work.

Missing `categories.yaml` is a warning, not a hard failure. Categorization can
still run, but it will not apply the canonical vocabulary rewrites and drops
from the category file. Homebrew/default installs should keep the file at:

```sh
~/.config/dbrain/categories.yaml
```

Development roots should keep it beside the root config:

```sh
<root>/categories.yaml
```

These selected features fail early when their required secrets are missing:

- GitHub imports require `GITHUB_TOKEN` or `github.token`.
- OpenRouter-backed categorization requires `DBRAIN_OPENROUTER_API_KEY`,
  `OPENROUTER_API_KEY`, or `openrouter.api_key`.
- OpenRouter-backed OCR requires the same OpenRouter key when the OCR model is
  an `openrouter/...` model.
- R2/S3 archive paths require an access key and secret when archive upload,
  bucket, endpoint, or public archive URL settings are configured.

Use `--config-file ~/.config/dbrain/config.yaml` for Homebrew/background
service runs when you want the installed binary to ignore checkout-local
environment overrides.

Every command help screen includes the effective configuration lookup summary.
Use this command for the authoritative env/config mapping:

```sh
dbrain config env
```

Use `dbrain config env --markdown` when you want a Markdown table for
docs or issue comments.

## Environment Variables

Lookup order is shell environment, `.envrc` or `.env` in the active config/root
directory, then `config.yaml`. `--root` wins over `DBRAIN_ROOT`.

Secret config values for GitHub import/OAuth, OpenRouter/OpenAI/Ollama API
keys, auth session signing, and R2/S3 credentials may be direct values or typed
references: `env:NAME`,
`op://vault/item/field`, or `keychain://service/account`.

| Environment variable(s) | config.yaml key | Default | Purpose |
| --- | --- | --- | --- |
| `DBRAIN_ROOT` | `(env only)` | `` | CLI root override. `--root` wins when both are set. |
| `XDG_CONFIG_HOME` | `(env only)` | `~/.config` | Base directory for default config files. |
| `XDG_DATA_HOME` | `(env only)` | `~/.local/share` | Base directory for default database, vault, cache, tmp, and logs. |
| `GITHUB_TOKEN` | `github.token` or `env.GITHUB_TOKEN` | `` | GitHub API token for importing stars. |
| `DBRAIN_AUTH_ENABLED` | `auth.enabled` | `false` | Enable session-gated web UI login. Disabled by default. |
| `DBRAIN_AUTH_PROVIDERS` | `auth.providers` | `github when auth is enabled` | OAuth providers allowed for web login; currently only `github` is supported. |
| `DBRAIN_AUTH_BASE_URL` | `auth.base_url` | `http://127.0.0.1:8742` | Public origin used for OAuth callback URLs. Must be `https://` when auth is enabled for non-localhost deployments. |
| `DBRAIN_AUTH_SESSION_KEY` | `auth.session_key` | `` | Secret key used to sign OAuth state; must be at least 32 random characters. Generate with `openssl rand -hex 32`. |
| `DBRAIN_AUTH_GITHUB_CLIENT_ID` | `auth.github.client_id` | `` | GitHub OAuth app client ID for web UI login. |
| `DBRAIN_AUTH_GITHUB_CLIENT_SECRET` | `auth.github.client_secret` | `` | GitHub OAuth app client secret for web UI login. |
| `DBRAIN_MCP_AUTH_ENABLED` | `mcp.auth.enabled` | `false` | Require DB-backed Bearer tokens on MCP Streamable HTTP endpoints. Create tokens with `dbrain auth mcp token add NAME`. |
| `DBRAIN_SUMMARY_MODEL` / `SUMMARIZE_MODEL` | `summary.model` | `` | Default model for summarize-backed source and answer synthesis. |
| `DBRAIN_SUMMARY_LANGUAGE` / `DBRAIN_OUTPUT_LANGUAGE` / `SUMMARIZE_LANGUAGE` | `summary.language` | `en` | Output language for summaries; use `auto` to match source language. |
| `DBRAIN_CATEGORIZE_MODEL` | `categorize.model` | `openrouter/google/gemini-2.5-flash` | Default LLM model for item/source categorization. |
| `DBRAIN_OCR_MODEL` / `DBRAIN_X_PHOTO_OCR_MODEL` | `ocr.model` | `openrouter/google/gemini-3.1-flash-lite-preview` | Default model for X photo OCR. |
| `DBRAIN_OLLAMA_BASE_URL` / `OLLAMA_BASE_URL` / `OLLAMA_HOST` | `ollama.base_url` | `http://127.0.0.1:11434` | Ollama endpoint for local model calls. |
| `DBRAIN_OLLAMA_API_KEY` / `OLLAMA_API_KEY` | `ollama.api_key` | `ollama` | API key label used for Ollama-compatible local calls. |
| `OPENAI_BASE_URL` | `openai.base_url` or `env.OPENAI_BASE_URL` | `` | OpenAI-compatible base URL used by the summarize adapter when already exported. |
| `OPENAI_API_KEY` | `openai.api_key` or `env.OPENAI_API_KEY` | `` | OpenAI-compatible API key used by the summarize adapter when already exported. |
| `OPENAI_USE_CHAT_COMPLETIONS` | `openai.use_chat_completions` or `env.OPENAI_USE_CHAT_COMPLETIONS` | `` | Forces summarize/OpenAI-compatible calls onto chat completions when set. |
| `DBRAIN_USER_AGENT` | `http.user_agent` | `dbrain/<short-sha>` | User-Agent header for outbound API calls; source/web fetching keeps its own fetch headers. |
| `DBRAIN_OPENROUTER_BASE_URL` / `OPENROUTER_BASE_URL` | `openrouter.base_url` | `https://openrouter.ai/api/v1` | OpenRouter API endpoint. |
| `DBRAIN_OPENROUTER_API_KEY` / `OPENROUTER_API_KEY` | `openrouter.api_key` | `` | OpenRouter API key for hosted LLM/OCR/categorization calls. |
| `DBRAIN_OPENROUTER_REFERER` / `OPENROUTER_HTTP_REFERER` | `openrouter.referer` | `https://local.dbrain` | HTTP referer sent to OpenRouter for direct calls. |
| `DBRAIN_OPENROUTER_TITLE` / `OPENROUTER_X_TITLE` | `openrouter.title` | `dbrain` | HTTP title sent to OpenRouter for direct calls. |
| `DBRAIN_SOURCE_READER_DOMAINS` / `DBRAIN_HTTP_READER_DOMAINS` | `source.reader.domains` | `canada.ca` | Comma-separated domains routed through the reader/textifier path before summarize. |
| `DBRAIN_SOURCE_READER_BASE_URL` / `DBRAIN_HTTP_READER_BASE_URL` | `source.reader.base_url` | `https://r.jina.ai/` | Reader/textifier base URL for difficult domains. |
| `DBRAIN_SOURCE_WAYBACK_ENABLED` / `DBRAIN_WAYBACK_ENABLED` | `source.wayback.enabled` | `true` | Use Internet Archive Wayback as a final source extraction fallback before terminalizing repeated failures. |
| `DBRAIN_SOURCE_WAYBACK_AVAILABILITY_URL` / `DBRAIN_WAYBACK_AVAILABILITY_URL` | `source.wayback.availability_url` | `https://archive.org/wayback/available?url={escaped_url}` | Wayback Availability API URL template used for final source fallback. |
| `DBRAIN_OKF_EXPORT_ENABLED` / `DBRAIN_SYNC_OKF_EXPORT` | `okf.export.enabled` | `false` | Export a full private OKF bundle at the end of `sync all`; use `--skip-okf-export` for a one-off opt-out. |
| `DBRAIN_APPLE_NOTES_ENABLED` | `apple_notes.enabled` | `false` | Include Apple Notes import in `sync all` when enabled; the standalone import command remains explicit. |
| `DBRAIN_APPLE_NOTES_DB_PATH` | `apple_notes.db_path` | `` | Optional Apple Notes `NoteStore.sqlite` path override. |
| `DBRAIN_APPLE_NOTES_EXCLUDE_FOLDERS` | `apple_notes.exclude_folders` | `` | Comma-separated or YAML-list Apple Notes folders/paths to skip. |
| `DBRAIN_APPLE_NOTES_EXCLUDE_ACCOUNTS` | `apple_notes.exclude_accounts` | `` | Comma-separated or YAML-list Apple Notes accounts to skip. |
| `DBRAIN_APPLE_NOTES_EXCLUDE_SHARED` | `apple_notes.exclude_shared` | `false` | Skip shared Apple Notes during import. |
| `DBRAIN_APPLE_NOTES_INDEX_ATTACHMENTS` | `apple_notes.index_attachments` | `true` | Extract supported Apple Notes attachment files by default. Set false or use `DBRAIN_APPLE_NOTES_SKIP_ATTACHMENTS=true` to keep metadata only. |
| `DBRAIN_APPLE_NOTES_SKIP_ATTACHMENTS` | `(env only)` | `false` | One-off opt-out for Apple Notes attachment file extraction/OCR while keeping note bodies and metadata. |
| `DBRAIN_APPLE_NOTES_ATTACHMENT_OCR` | `apple_notes.attachment_ocr` | `true` | Run local OCR for Apple Notes image attachments when `tesseract` is available. |
| `DBRAIN_APPLE_NOTES_SKIP_ATTACHMENT_OCR` | `(env only)` | `false` | One-off opt-out for Apple Notes image OCR while keeping non-OCR attachment extraction. |
| `DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES` | `apple_notes.attachment_max_bytes` | `52428800` | Maximum attachment file size to extract. |
| `DBRAIN_APPLE_NOTES_TESSERACT_BINARY` | `apple_notes.tesseract_binary` | `tesseract` | Local Tesseract binary for Apple Notes image OCR. |
| `DBRAIN_SAFARI_TABS_ENABLED` | `safari_tabs.enabled` | `false` | Include Safari iCloud tabs import in `sync all` when enabled; the standalone import command remains explicit. |
| `DBRAIN_SAFARI_TABS_DB_PATH` | `safari_tabs.db_path` | `` | Optional Safari `CloudTabs.db` path override. |
| `DBRAIN_SAFARI_TABS_DEVICE` | `safari_tabs.device` | `` | Safari iCloud device name or UUID to import during `sync all`. |
| `DBRAIN_SAFARI_TABS_LIMIT` | `safari_tabs.limit` | `0` | Maximum Safari tabs to import after filtering; 0 means all matching tabs. |
| `DBRAIN_SAFARI_TABS_OLDER_THAN` | `safari_tabs.older_than` | `0` | Only import Safari tabs last viewed before this duration ago, for example `168h`. |
| `DBRAIN_SCHEDULER_SYNC_ALL_ENABLED` | `scheduler.sync_all.enabled` | `false` | Run `sync all` periodically from the long-running `serve remote` process. |
| `DBRAIN_SCHEDULER_SYNC_ALL_INTERVAL` | `scheduler.sync_all.interval` | `1h` | Interval between scheduled `sync all` runs when the scheduler is enabled. |
| `DBRAIN_SCHEDULER_SYNC_ALL_RUN_ON_START` | `scheduler.sync_all.run_on_start` | `false` | Run `sync all` once when `serve remote` starts, then continue on the interval. |
| `DBRAIN_SCHEDULER_SYNC_ALL_JITTER` | `scheduler.sync_all.jitter` | `0` | Optional bounded delay added to each interval so multiple nodes do not sync at exactly the same time. |
| `DBRAIN_SCHEDULER_SYNC_ALL_SOURCE_LIMIT` | `scheduler.sync_all.source_limit` | `0` | Optional scheduled source-worker limit; 0 uses the `sync all` default. |
| `DBRAIN_SCHEDULER_SYNC_ALL_SKIP_GITHUB` | `scheduler.sync_all.skip_github` | `false` | Skip GitHub import in scheduled `sync all` runs. |
| `DBRAIN_SCHEDULER_SYNC_ALL_SKIP_YOUTUBE` | `scheduler.sync_all.skip_youtube` | `false` | Skip YouTube import in scheduled `sync all` runs. |
| `DBRAIN_SCHEDULER_SYNC_ALL_SKIP_CATEGORIZE` | `scheduler.sync_all.skip_categorize` | `false` | Skip final categorization in scheduled `sync all` runs. |
| `DBRAIN_SCHEDULER_SYNC_ALL_OKF_EXPORT` | `scheduler.sync_all.okf_export` | `false` | Export a full private OKF bundle at the end of scheduled `sync all` runs. |
| `DBRAIN_SCHEDULER_SYNC_ALL_SKIP_OKF_EXPORT` | `scheduler.sync_all.skip_okf_export` | `false` | Skip OKF export for scheduled `sync all` runs when a broader OKF export setting is enabled. |
| `DBRAIN_MEDIA_PROXY_BASE_URL` / `DBRAIN_WEB_BASE_URL` | `media.proxy.base_url` | `http://127.0.0.1:8742` | Base URL for local archived-media proxy links in rendered notes and private OKF exports; private OKF export falls back to `DBRAIN_AUTH_BASE_URL` when these are unset. |
| `DBRAIN_AUTO_ARCHIVE_MEDIA` / `DBRAIN_ARCHIVE_AUTO` | `archive.auto` | `false` | Run media archive automatically at the end of `sync all`. |
| `DBRAIN_ARCHIVE_UPLOAD` / `DBRAIN_R2_UPLOAD` | `archive.upload` | `false` | Upload eligible media before marking/pruning in `archive media`. |
| `DBRAIN_ARCHIVE_PROVIDER` / `DBRAIN_R2_PROVIDER` | `archive.provider` | `cloudflare_r2` | Archive provider label. |
| `DBRAIN_R2_BUCKET` / `DBRAIN_ARCHIVE_BUCKET` / `DBRAIN_S3_BUCKET` | `r2.bucket` or `archive.bucket` | `` | S3-compatible bucket for media and SQLite archives. |
| `DBRAIN_R2_PUBLIC_BASE_URL` / `DBRAIN_MEDIA_PUBLIC_BASE_URL` | `r2.public_base_url` or `media.public_base_url` | `` | Public base URL for archived media links. |
| `DBRAIN_R2_ENDPOINT` / `DBRAIN_S3_ENDPOINT` | `r2.endpoint` | `` | S3-compatible endpoint, such as a Cloudflare R2 account endpoint. |
| `DBRAIN_R2_REGION` / `DBRAIN_S3_REGION` / `AWS_REGION` / `AWS_DEFAULT_REGION` | `r2.region` | `auto` | S3-compatible region. |
| `DBRAIN_R2_ACCESS_KEY_ID` / `DBRAIN_S3_ACCESS_KEY_ID` / `AWS_ACCESS_KEY_ID` | `r2.access_key_id` | `` | S3-compatible access key ID. |
| `DBRAIN_R2_SECRET_ACCESS_KEY` / `DBRAIN_S3_SECRET_ACCESS_KEY` / `AWS_SECRET_ACCESS_KEY` | `r2.secret_access_key` | `` | S3-compatible secret access key. |
| `DBRAIN_R2_SESSION_TOKEN` / `DBRAIN_S3_SESSION_TOKEN` / `AWS_SESSION_TOKEN` | `r2.session_token` | `` | Optional S3-compatible session token. |

## Authentication

### Web UI GitHub OAuth

By default, `dbrain serve web` and `dbrain serve remote --web` keep the existing
trusted localhost/tailnet behavior and do not require a dbrain login. To require
login, create a GitHub OAuth app with this callback URL:

```text
<auth.base_url>/auth/github/callback
```

Then enable the allowlisted GitHub provider:

```yaml
auth:
  enabled: true
  providers: ["github"]
  base_url: "https://dbrain.example.ts.net"
  session_key: "env:DBRAIN_AUTH_SESSION_SECRET"
  github:
    client_id: "..."
    client_secret: "env:DBRAIN_AUTH_GITHUB_CLIENT_SECRET"
```

Only `github` is currently accepted in `auth.providers`. OAuth login is denied
unless the GitHub username has first been approved in the dbrain database:

```bash
dbrain auth github approve your-github-login
```

Approved usernames are matched case-insensitively and may be approved with or
without a leading `@`. The first successful login binds the approved database row
to the user's GitHub numeric ID and profile fields; future logins can match that
GitHub ID. Config/env allowlists such as `auth.allowed_github_users` are not the
authoritative allowlist for web login. Use `dbrain auth github list` to view
approved users and `dbrain auth github remove USERNAME` to remove an approval.
Removed approvals are checked against live web sessions, so a removed user must
log in again and will be denied unless reapproved.

For internet-exposed deployments, `auth.base_url` must be the public `https://`
origin registered in the GitHub OAuth app; `--tsnet-funnel --web` rejects the
default localhost origin when web auth is enabled. Generate a random session key
with `openssl rand -hex 32` and store it via a secret ref. Sessions are
in-memory and expire after 24 hours, so restarting the web process logs users
out.
Authenticated web requests emit app-layer access logs with the GitHub identity,
which is the useful identity source when Funnel traffic does not carry tailnet
identity headers.
`GITHUB_TOKEN` is still only the GitHub import token; it is not used for web UI
OAuth.

### MCP Bearer Auth

MCP bearer auth is optional and only applies to Streamable HTTP MCP endpoints:
`dbrain serve mcp --transport http`, `dbrain serve mcp --transport tsnet`, and
the MCP surface mounted by `dbrain serve remote`. Local stdio MCP is unchanged.

Create a token:

```sh
dbrain auth mcp token add laptop
```

The raw token is shown once. Store it in the MCP client secret store and send it
as:

```text
Authorization: Bearer <token>
```

Use `dbrain auth mcp token list` to list token records by ID, name, fingerprint,
status, and timestamps without revealing the raw token. Use
`dbrain auth mcp token revoke ID_OR_NAME_OR_FINGERPRINT` to revoke a token;
names must be unique when used as the revocation selector. Add `--all` to list
revoked token records too.

Enable enforcement with config or env:

```yaml
mcp:
  auth:
    enabled: true
```

```sh
export DBRAIN_MCP_AUTH_ENABLED=true
```

When bearer auth is disabled, HTTP and tsnet MCP startup prints a warning that
the endpoint is acceptable only on private localhost/trusted tailnet paths and
must not be exposed through Tailscale Funnel or a public reverse proxy.
When bearer auth is enabled, MCP HTTP access logs include the token record name
and fingerprint, never the raw token.

### Import Credentials

For GitHub stars, use a fine-grained PAT with:

- `User permissions`: `Starring: Read`
- `Repository permissions`: `Metadata: Read`
- `Repository permissions`: `Contents: Read`

`dbrain` reads `GITHUB_TOKEN` from the shell, `.envrc`, `.env`, or
`config.yaml`. Cookie-backed X and YouTube flows require a supported browser
profile with an active logged-in session; Chrome is the best-tested option.

## Optional Media Archive Env

To automatically offload finalized media to S3-compatible storage at the end of
`dbrain sync all`, export:

- `DBRAIN_AUTO_ARCHIVE_MEDIA=1`
- `DBRAIN_R2_BUCKET=<bucket>`
- `DBRAIN_R2_ENDPOINT=https://<account>.r2.cloudflarestorage.com`
- `DBRAIN_R2_ACCESS_KEY_ID=<key>`
- `DBRAIN_R2_SECRET_ACCESS_KEY=<secret>`

Optional:

- `DBRAIN_R2_REGION=auto`
- `DBRAIN_R2_SESSION_TOKEN=<token>`
- `DBRAIN_ARCHIVE_PROVIDER=cloudflare_r2`
- `DBRAIN_R2_PUBLIC_BASE_URL=https://...` when archived media should render as
  anonymously readable URLs in notes. Leave this unset for authenticated-only
  buckets.
- `DBRAIN_MEDIA_PROXY_BASE_URL=http://127.0.0.1:8742` when archived media
  should render as links or playable embeds backed by the local web proxy.
  This defaults to `http://127.0.0.1:8742` unless explicitly disabled with
  `DBRAIN_MEDIA_PROXY_BASE_URL=off`.

`sync all` only runs the archive stage automatically when
`DBRAIN_AUTO_ARCHIVE_MEDIA=1` or `--archive-media` is set. The archive stage
uploads eligible media after OCR/transcription reaches a terminal state, marks
the object as archived in the DB, and prunes the local file once every row
sharing that same `local_path` is safely archived.

The same S3-compatible credentials are used by `dbrain sqlite archive` and
`dbrain sqlite restore` for compressed database snapshots. SQLite archives are
stored under `archive/db/` by default; override with `--prefix` if needed.

## Optional Source Reader Env

Some sites are known to behave badly when handed directly to `summarize
--extract`, either because they hang, block automation, or need a textified
reader view. `dbrain` can route selected domains through a short Go fetch path
before summarization so those sources do not spend the full extraction timeout
in an external helper.

- `DBRAIN_SOURCE_READER_DOMAINS=canada.ca`
  Comma-separated domains that should bypass direct `summarize --extract`.
  Subdomains are included, so `canada.ca` also covers `open.canada.ca` and
  `fintrac-canafe.canada.ca`.
- `DBRAIN_SOURCE_READER_BASE_URL=https://r.jina.ai/`
  Reader/textifier base URL. The default is `https://r.jina.ai/`. A base URL
  may also include `{url}` or `{escaped_url}` placeholders for services that
  need a different URL shape.

For reader domains, `dbrain` first fetches the reader URL with text-oriented
headers. If the reader service rejects the request, it falls back to fetching
the original page directly with browser-style headers and extracting readable
HTML locally. Only the extracted raw text is then passed to `summarize` for the
derived summary.

When direct extraction reaches its terminal retry threshold, `dbrain` checks
the Internet Archive Wayback Availability API before marking the source
terminal. If a usable snapshot exists, the archived HTML is extracted and saved
with `extract_tool=wayback`; otherwise the source is marked `dead` or `gone`
according to the failure classification. Disable this final fallback with
`DBRAIN_SOURCE_WAYBACK_ENABLED=false`.

Wayback extracts are quality-gated before summarization. Very short archived
extracts and obvious archive/browser shells, such as `Loading...` or frame
fallback pages, keep their raw extract but get `summary_status=skipped` instead
of a model-generated summary. This avoids turning title-only or boilerplate
snapshots into plausible-looking knowledge.

Current source extraction terminal thresholds are: `gone` immediately for
404/410 responses; `dead` after 1 DNS NXDOMAIN or unsupported-file failure;
`dead` after 3 TLS, Cloudflare edge, connectivity, X article shell,
access-denied, or timeout failures; and `dead` after 5 generic fetch, HTTP 5xx,
or unclassified failures. Rows that are one failure away from a terminal state
bypass the normal 12-hour retry cooldown so Wayback recovery or terminal
classification happens on the next source enrichment pass.

To rebaseline old failed web-source rows after improving extraction logic,
reset only the failed web sources and let them enter the normal extraction
pipeline again:

```sh
dbrain repair sources --source-type web --extract-status error --extract-status dead --dry-run
dbrain repair sources --source-type web --extract-status error --extract-status dead --yes
dbrain extract sources --limit 500 --concurrency 4 --timeout 5m
```

This clears stale extract and summary state for currently failed web sources
without touching successful sources. Retryable failures start with fresh failure
counts; once they reach their terminal threshold, `dbrain` performs the Wayback
final-attempt check before marking the source `dead` or `gone`. `sync all` will
continue that retry progression naturally. For an urgent one-off row, use
`dbrain extract sources --source <source_key> --force` to bypass cooldown for
that specific source.

## Operational Notes

### X hydration counters

- `Requested` means remote X fetches were actually attempted.
- `Hydrated` means items were processed and ended in an `ok_*` X hydration
  state.
- Those counters are intentionally different. A run can show a nonzero
  `Hydrated` count with `Requested: 0` if it is only reconciling already-stored
  local state.
- New top-level bookmarks can legitimately cause more hydrated items than the
  import count because quote children are stored and repaired as first-class
  `x_quote` items.

### Quoted X posts

- Quoted posts are stored as first-class `x_quote` items linked through
  `quoted_post`, not only as nested parent JSON.
- `dbrain sync all` performs bounded quote-only follow-up hydrate passes after
  the main X hydrate step so quote-of-quote tails can drain automatically
  without a separate manual `hydrate x` run.

### Link discovery counters

- `items_scanned` means X items with non-empty `links_json` that still need a
  discovery pass.
- `sources_queued` means new canonical source rows actually created after URL
  filtering and deduplication.
- Those counters are intentionally different. Many scanned items can still
  produce zero new sources.

## Model Backends

When no `--model` flag is provided, `dbrain` checks `DBRAIN_SUMMARY_MODEL` /
`SUMMARIZE_MODEL` or `summary.model` in `config.yaml`; otherwise the external
`summarize` tool chooses its own default. Pass `--model ollama/<name>` to test
a local GPU-backed model, or `--model openrouter/<provider>/<model>` for a
hosted catch-up run. `dbrain` sends direct Ollama summaries to the native
Ollama chat API with thinking disabled, and defaults to
`http://127.0.0.1:11434`. Override the target with
`DBRAIN_OLLAMA_BASE_URL`, `OLLAMA_BASE_URL`, or `OLLAMA_HOST` if the daemon is
elsewhere. The X photo OCR stage also honors `DBRAIN_OCR_MODEL` /
`DBRAIN_X_PHOTO_OCR_MODEL`; the current default is
`openrouter/google/gemini-3.1-flash-lite-preview`. If you already export
`OPENAI_BASE_URL` or `OPENAI_API_KEY`, `dbrain` leaves those alone. When
`--model` is set, it also takes precedence over `--cli`, so local-model runs do
not accidentally inherit the default CLI provider.

For a new machine or GPU-backed A/B run, start with small scoped commands
before pointing a whole sync at Ollama. A practical progression is:

```sh
dbrain research "What validates Kubernetes manifests?" --model ollama/qwen3.5:9b
dbrain extract sources --limit 10 --concurrency 2 --model ollama/qwen3.5:9b --timeout 10m
dbrain sync all --source-limit 25 --model ollama/qwen3.5:9b --timeout 10m
```

Good starting local models to compare on a stronger Mac are `qwen3.5:9b`,
`qwen2.5:7b-instruct`, and `gemma4:e4b`. Compare wall-clock time, summary
quality, and whether long GitHub/web extracts stay coherent before switching
the default workflow over.

## MCP

`dbrain serve mcp` exposes the local corpus over read-only MCP stdio for agent
research, browsing, topic maps, retrieval packs, and operational stats. The
server is DB-first by default, tag-aware, and includes OCR/transcript evidence
when those enrichments exist.

The MCP surface also exposes read-only `dbrain_okf_search` and `dbrain_okf_get`
tools for agents that need to inspect the generated OKF bundle. These tools read
the existing bundle under the configured OKF directory; they do not regenerate
or validate it. Use `dbrain okf export` or `sync all --okf-export` to refresh
the bundle first.

See [MCP.md](MCP.md) for the full agent workflow, tool contract, eval setup,
client configuration, importer contract, logging behavior, and skill setup.

## Open Knowledge Format

`dbrain okf export` writes a deterministic private Open Knowledge Format bundle
under the configured OKF directory, defaulting to `okf/current` beside the
rendered vault. The bundle is regenerated through a staging directory, validated,
and atomically swapped into place.

```sh
dbrain okf export --entities --topics
dbrain okf validate "$(dbrain config paths --json | jq -r .okf_dir)"
dbrain sync all --okf-export
```

OKF export is intentionally output-only. It does not import OKF bundles back
into dbrain. Private bundles may include raw evidence, OCR text, transcripts,
Apple Notes content, and archived-media links, so treat `okf/` like `data/` and
`vault/` unless you deliberately scrub it for sharing.

## Skill

This repo includes Codex skills for agents:

- `skills/dbrain-mcp/SKILL.md` helps agents query the local dbrain corpus
  through MCP, including read-only inspection of generated OKF bundles. See
  [MCP.md](MCP.md#skill) for installation notes and the recommended Codex MCP
  configuration.
- `skills/dbrain-model-bakeoff/SKILL.md` helps agents compare summary and
  categorization models with the read-only bakeoff devtool.

## License

`dbrain` is licensed under the MIT License. See [LICENSE](LICENSE).
Third-party dependency notices are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## TODO

### MCP TODO

- [x] Add deterministic fixture coverage for MCP retrieval tests covering tags,
  OCR text, transcript text, linked sources, and source-type filters.
- [x] Add protocol-level tool-surface coverage so the core agent workflow tools
  (`dbrain_research_pack`, `dbrain_get`, `dbrain_get_many`, `dbrain_related`,
  maps, and search) stay advertised by `tools/list`.
- [x] Return structured, actionable MCP tool errors so clients and agents can
  recover from missing lookups, unsupported modes, or unknown tools.
- [x] Add a representative exact-tag evidence lane so broad entity questions
  expose saved tagged items even when linked source documents dominate ranking.
- [x] Add exact-tag evidence assertions to local MCP eval cases so users can
  catch regressions in the representative tagged-item lane.
- [x] Add a `task test-mcp` command so CI and open-source users can validate MCP
  retrieval behavior without a private corpus.
- [x] Keep model-backed summary tests deterministic when local summary-model
  environment variables are set.
- [x] Document the importer contract for new data sources: when importers
  populate the common item/source/text/tag/enrichment fields, MCP should
  discover them without source-specific code.
- [x] Add example local eval recipes for entity/tag, OCR, transcript, difficult
  domain, and broad-topic/noisy-result retrieval cases.
- [x] Show tags from saved-item backlinks when inspecting source nodes, so a
  selected `src:...` result exposes the user's tags from items that reference it.
- [x] Add stateless Streamable HTTP as a parallel MCP transport so remote agents
  can query the same read-only brain over a Tailscale-protected endpoint.
- [x] Add built-in `tsnet` serving for read-only MCP and the read/write web UI,
  including persistent state, lock protection, typed bootstrap secrets, and
  guarded state reset/status commands.

### Product TODO

- [ ] Continue improving topic/MOC synthesis quality and better periodic refresh workflows as the corpus fills out.
- [x] Add optional embedded `tsnet` serving for remote web and MCP access
  without requiring users to configure `tailscale serve` themselves.
- [x] Add source-level `user_tags`, source categorization commands, and
  source-tag search/MCP visibility separate from backlink item tags.
- [ ] Keep breaking the web UI into smaller Svelte components with a thin shared API client layer instead of letting the browser surface collapse into one large page component.
- [ ] Improve the web note reader further with richer Markdown rendering, better code-block presentation, and cleaner outbound link handling for vault notes.
- [x] Make external links in the web UI open in a new window/tab with safe defaults (`target="_blank"` plus `rel="noopener noreferrer"`), so note exploration does not constantly navigate away from the local brain surface.
- [x] Add URL-backed state and deeper note-to-note navigation in the web UI so searches, selected notes, and related pivots survive refreshes and remote sessions.
- [ ] Improve web UI tag visibility in search, graph, list, and detail views so selected items and linked sources show their own tags plus backlink tags without extra discovery.
- [ ] Expand the web operations/dashboard view with deeper worker drill-down and richer backlog trend views so repeated failures are easier to triage.
- [ ] Add first-class filters and browsing controls in the web UI for source type, kind, status, tag, and recency so the corpus is easier to slice than with one text box.
- [ ] Add semantic retrieval on top of SQLite/FTS, likely embeddings plus related-item expansion.
- [ ] Add a translation stage for non-English X content, storing both original and translated text.
- [ ] Broaden media ingestion beyond the current X image/video downloads, with content-hash deduplication across repeated saves and reposted duplicates.
- [ ] Add Apple Podcasts as a first-class imported signal/source type so podcast episodes can enter the same item/extract/summary pipeline as YouTube and web sources.

### Pipeline TODO

- [ ] Tighten X link-discovery candidate selection so items whose only links are X self-links like `/photo/1` or `/video/1` do not get rescanned and inflate `items_scanned` without producing real source candidates.
- [x] Harden the YouTube pipeline for transcript-missing videos and improve the fallback/transcription path.
- [ ] Audit X media transcription throughput by recording per-video duration/bytes/transcript chars and testing cautious MacWhisper parallelism; avoid raising default concurrency until local GPU/CPU contention is understood.
- [x] Add an OCR bakeoff/audit command that can run the same image set through multiple OCR backends, report side-by-side output quality and timings, and avoid changing persisted item OCR state.
- [x] Add a summary/categorization bakeoff devtool that can run the same source extract or content bundle through multiple models/backends, report side-by-side outputs and timings, and avoid changing persisted summary/tag state.
- [x] Improve provider provenance so stored summaries always record the exact backend/model used.
- [x] Make backlog/admin summary freshness stats policy-aware instead of exact-model-aware, so switching between acceptable local/hosted summary models does not make the whole corpus look stale.
- [ ] Add explicit source-of-truth audit commands such as `dbrain audit github-stars`, `dbrain audit youtube-watch-later`, `dbrain audit x-bookmarks`, and `dbrain audit all --json`, while treating the local DB as append-only by default.
- [ ] Add a pre-summary staging path for oversized extracts so giant PDFs and long documents can be chunked, pre-compressed, or locally preprocessed before hosted summary calls hit provider context limits.
- [ ] Add an oversized-X-video policy for media download/transcription with byte-size and/or duration gating, lower-bitrate transcription variants, and terminal `too_large` / `too_long` states instead of endless retry.
- [ ] Maybe reclassify non-actionable X media transcript outcomes like `no_audio`, `noise`, and `too_short` out of the generic failed bucket so transcription stats distinguish real pipeline errors from terminal no-content cases.
- [ ] Add an optional X thread expansion path when a bookmarked post is clearly part of a longer thread.
- [x] Add a config-driven scheduler inside `serve remote` so launchd-backed installs can run `sync all` periodically and skip overlapping runs.
- [x] No longer needed for now: keep `Obscura` (`https://github.com/h4ckf0r0day/obscura`) only as an external reference if source extraction gets stuck again. The current protected-fetch and Wayback fallback path covers the original gap well enough.
