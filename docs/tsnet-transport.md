# tsnet Remote Transport Proposal

Status: v1 implemented
Date: 2026-04-29

## Summary

`dbrain` supports a localhost web UI, local stdio MCP, and localhost
Streamable HTTP MCP. The next remote-access step should add a built-in
`tsnet` remote transport so `dbrain` can expose the existing read/write web UI
and the read-only MCP endpoint from one Tailscale node without requiring users
to configure `tailscale serve`.

This transport should be secure by default:

- HTTPS is enabled by default through `tsnet.Server.ListenTLS`.
- Plain HTTP over the tailnet is an explicit development escape hatch.
- Node state and certificate-related state persist across restarts.
- Bootstrap secrets resolve through typed secret references before any generic
  command execution is considered.
- The web UI and MCP endpoint share one tsnet node, one persisted state
  directory, one certificate path, and one ACL target.
- Tailnet access control remains a Tailscale admin responsibility; dbrain
  should log caller identity via `LocalClient.WhoIs` where possible and provide
  optional guardrails, but should not try to replace Tailscale ACL policy.
- The web UI remains the full local read/write interface. Remote web exposure
  is an intentional administration surface and must print a clear startup
  warning.
- The MCP surface remains read-only.

Recommended first-run shape:

```sh
dbrain serve remote \
  --web \
  --mcp \
  --tsnet-hostname dbrain \
  --tsnet-state-dir ~/.local/share/dbrain/tsnet/dbrain
```

`tsnet.Server.Dir` is the local equivalent of a Kubernetes PVC plus node
secret. It should be created with `0700` permissions, kept stable across
restarts, and kept out of iCloud or other multi-device sync folders.

This design is defensive infrastructure work. The security scope is protecting
a private read/write web UI and read-only MCP endpoint, preserving local node
identity safely, and avoiding accidental public exposure or weak secret
handling.

## Design Decisions

- `dbrain serve remote` is tsnet-only in v1. The `--transport` flag remains on
  `dbrain serve mcp` for stdio, localhost HTTP, and MCP-only tsnet
  compatibility.
- Remote web uses the existing web handler. It is read/write and includes
  existing mutating routes such as tag edits and link adds. A separate
  read-only remote web mux is explicitly out of scope for the first pass.
- MCP remains read-only.
- `dbrain serve remote --web --mcp` is the preferred combined command for
  exposing both surfaces. `dbrain serve mcp --transport tsnet` remains
  available for MCP-only compatibility.
- Tailscale ACLs and tags are the primary access-control boundary. dbrain logs
  direct tsnet caller identity with `LocalClient.WhoIs`, but it does not try to
  validate organization-specific ACL policy.
- First-run auth URLs must be surfaced through `tsnet.Server.UserLogf`, which
  is always wired to stderr. `tsnet.Server.Logf` remains debug-only behind a
  verbose flag.
- Startup should be explicit: resolve secrets once, create and validate
  `Server.Dir`, acquire a process lock, construct `tsnet.Server` with all
  fields set, call `Up(ctx)` with a bounded timeout, cache `LocalClient`, then
  listen and serve HTTP.
- HTTP shutdown should drain the `http.Server` first, then close the
  `tsnet.Server`.
- MCP routes must be registered before the web UI catch-all, and tests must
  prove `/mcp` paths cannot be swallowed by the SPA fallback.
- Use an outer dispatcher on the remote listener so `mcp_path` and
  `mcp_path + "/"` are routed to MCP before the web handler is consulted.
- `serve remote --web=false --mcp=false` fails fast.
- `mcp_path` remains configurable, but only with strict validation.
- `dbrain tsnet status` and `dbrain tsnet reset` resolve the same config,
  flags, and env as `serve remote`; the resolved state directory is the primary
  identity.
- Secret resolution uses typed refs first. `tsnet.auth_key_command` is an
  advanced YAML-only argv escape hatch and is disabled unless explicitly
  configured.
- macOS `keychain://` uses Go keyring first in v1. The `security` CLI is only a
  user-configured command escape hatch.
- `--tsnet-control-url` is experimental. Custom control servers, including
  Headscale, may not provide the same `.ts.net` DNS and `ListenTLS` certificate
  behavior as Tailscale SaaS.
- dbrain-side allowlists, custom `ipn.StateStore`, and service-mode listeners
  are out of scope for the current product direction. Revisit only if a clear
  multi-user or operational need appears.

## Background

Current transports:

- `web`: `dbrain serve web` exposes the local web UI on localhost.
- `stdio`: local agents launch `dbrain serve mcp`.
- `http`: `dbrain serve mcp --transport http` listens on localhost and can be
  exposed by `tailscale serve`.

Proposed additional transport:

- `remote tsnet`: `dbrain` embeds Tailscale directly, listens inside the
  tailnet, and mounts web plus MCP on one HTTPS server.

Relevant upstream docs:

- `tsnet.Server.Dir` stores persistent tsnet state:
  https://tailscale.com/docs/reference/tsnet-server-api
- `tsnet` uses persistent state so a program can reconnect after restart:
  https://tailscale.com/docs/features/tsnet/how-to/create-basic-tsnet-app
- `ListenTLS` serves HTTPS using Tailscale certificate support:
  https://tailscale.com/docs/reference/tsnet-server-api

## Goals

- Keep stdio and localhost HTTP stable and supported.
- Add tsnet as a parallel remote transport, not a replacement.
- Support web-only, MCP-only, and combined web+MCP serving.
- Reuse the existing read-only MCP server and tool registry.
- Reuse the existing read/write web HTTP handler.
- Expose remote web and MCP over HTTPS by default.
- Persist node identity and certificate-related state across restarts.
- Avoid SSH access to the user's machine for remote agents.
- Avoid public exposure by default.
- Support macOS Keychain and 1Password for bootstrap secrets.
- Keep open-source setup understandable for non-macOS users.

## Non-Goals

- Do not make MCP write-capable in this work.
- Do not build a separate read-only web UI in this work.
- Do not replace Tailscale Serve for users who prefer the external daemon.
- Do not store the whole tsnet node state in 1Password or Keychain initially.
- Do not put node state in iCloud.
- Do not require macOS-only APIs for non-macOS users.
- Do not expose web or MCP through Tailscale Funnel by default.
- Do not promise production support for Headscale/custom-control HTTPS behavior
  in the first pass.

## Transport Matrix

| Transport | Use Case | Listener | Persistence | Notes |
| --- | --- | --- | --- | --- |
| `stdio` | Local Codex/Claude | stdin/stdout | none | Default and safest local path. |
| `web` | Local browser UI | `127.0.0.1:8742` | none | Existing localhost web server. |
| `mcp http` | Local daemon behind Tailscale Serve | `127.0.0.1:8743` | none | Requires external Tailscale daemon and `tailscale serve`. |
| `remote tsnet` | Built-in tailnet web and/or MCP | tailnet IP/DNS | `tsnet.Server.Dir` | One binary, HTTPS by default. |

The `tsnet` listener uses Tailscale's userspace network stack. Binding `:443`
inside tsnet does not require root or `CAP_NET_BIND_SERVICE` because the OS is
not binding that privileged port directly.

## URL Layout

One tsnet listener should mount both components:

| Path | Component | Notes |
| --- | --- | --- |
| `/` | Web UI | Same read/write handler as `dbrain serve web`. |
| `/mcp` | MCP Streamable HTTP | Same handler as `dbrain serve mcp --transport http`. |

Recommended remote URLs:

```text
https://dbrain.<tailnet>.ts.net/       web UI
https://dbrain.<tailnet>.ts.net/mcp    MCP Streamable HTTP
```

This is preferable to running two tsnet nodes because it gives the user one
tailnet identity, one persistent state directory, one HTTPS certificate path,
one set of ACLs, and one process to supervise.

The web UI path exposes the full local web application, including mutating
routes such as tag updates and link creation. This is intentional; remote web is
an administration surface for trusted tailnet users, not a read-only viewer.

Route dispatch matters. The remote listener should use an outer dispatcher that
routes `mcp_path` and `mcp_path + "/"` subtrees to MCP before the web handler is
consulted. Tests must prove `/mcp` paths never fall through to the web SPA
fallback.

The existing localhost HTTP MCP command already uses `/mcp` as its default
endpoint path, so the remote tsnet path should match existing Streamable HTTP
client configs.

## Proposed CLI

Default secure mode:

```sh
dbrain serve remote --web --mcp
```

Expanded equivalent:

```sh
dbrain serve remote \
  --web \
  --mcp \
  --tsnet-hostname dbrain \
  --tsnet-state-dir ~/.local/share/dbrain/tsnet/dbrain \
  --tsnet-listen :443 \
  --tsnet-tls
```

MCP-only compatibility mode:

```sh
dbrain serve mcp --transport tsnet
```

Web-only mode:

```sh
dbrain serve remote --web --mcp=false
```

Development-only plain HTTP:

```sh
dbrain serve remote \
  --web \
  --mcp \
  --tsnet-hostname dbrain-dev \
  --tsnet-listen :80 \
  --tsnet-tls=false
```

Suggested flags:

| Flag | Default | Purpose |
| --- | --- | --- |
| `--web` | `true` for `serve remote` | Mount the web UI at `/`. |
| `--mcp` | `true` for `serve remote` | Mount MCP Streamable HTTP at `/mcp`. |
| `--mcp-path` | `/mcp` | Remote MCP endpoint path. Must pass strict path validation. |
| `--tsnet-hostname` | `dbrain` | Stable tailnet machine name. |
| `--tsnet-state-dir` | `~/.local/share/dbrain/tsnet/<hostname>` | Durable tsnet node state. |
| `--tsnet-listen` | `:443` when TLS is on, `:80` when TLS is off | Tailnet listener address. |
| `--tsnet-tls` | `true` | Use `ListenTLS` for HTTPS. |
| `--tsnet-startup-timeout` | `45s` | Maximum time to wait for `Up(ctx)` before failing startup. |
| `--tsnet-auth-key` | empty | Optional direct bootstrap auth key. Prefer refs. |
| `--tsnet-auth-key-ref` | empty | Typed secret reference for bootstrap auth key. |
| `--tsnet-allow-secret-command` | `false` | Required before command refs are executed. |
| `--tsnet-advertise-tags` | empty | Optional comma-separated Tailscale tags. |
| `--tsnet-control-url` | empty | Experimental alternate control server. HTTPS/cert behavior may differ from Tailscale SaaS. |
| `--tsnet-verbose` | `false` | Enable tsnet logs on stderr. |

`serve remote` does not need a `--transport` flag in v1 because remote means
tsnet. `dbrain serve mcp --transport tsnet` remains the MCP-only compatibility
path.

`serve remote --web=false --mcp=false` should fail fast with a clear error
instead of starting a tsnet node that serves no routes.

If `--mcp-path` remains configurable, validate it strictly:

- Must start with `/`.
- Must not be `/`.
- Must not contain spaces.
- Must not contain `..`.
- Must be cleaned/canonicalized with trailing slashes normalized away, so
  `/mcp/` becomes `/mcp`.

`dbrain serve web` and `dbrain serve mcp` should continue to exist as local or
single-surface commands. `dbrain serve remote` is the preferred command for
tailnet exposure because it centralizes remote security defaults, persistent
state, and route mounting.

## Proposed Config

The tsnet feature should be fully configurable through `config.yaml` under the
`tsnet:` key. Most values should also have normal `DBRAIN_TSNET_*` environment
variable and CLI flag equivalents where that representation is unambiguous.

The one exception is `tsnet.auth_key_command`: keep it YAML-only because it is
an argv array. Do not add `DBRAIN_TSNET_AUTH_KEY_COMMAND` or
`--tsnet-auth-key-command`; those would require shell-style splitting or custom
string decoding and would make secret execution harder to reason about.

```yaml
tsnet:
  enabled: false
  hostname: dbrain
  state_dir: ~/.local/share/dbrain/tsnet/dbrain
  listen: :443
  tls: true
  startup_timeout: 45s
  web: true
  mcp: true
  mcp_path: /mcp
  auth_key: ""
  auth_key_ref: ""
  auth_key_command: []
  allow_secret_command: false
  advertise_tags: []
  control_url: "" # experimental
  verbose: false
```

Environment variables:

| Env | Config Key | Purpose |
| --- | --- | --- |
| `DBRAIN_TSNET_WEB` | `tsnet.web` | Mount the web UI on the remote tsnet listener. |
| `DBRAIN_TSNET_MCP` | `tsnet.mcp` | Mount MCP on the remote tsnet listener. |
| `DBRAIN_TSNET_MCP_PATH` | `tsnet.mcp_path` | MCP endpoint path, default `/mcp`. |
| `DBRAIN_TSNET_HOSTNAME` | `tsnet.hostname` | Tailnet node hostname. |
| `DBRAIN_TSNET_STATE_DIR` | `tsnet.state_dir` | Persistent tsnet state directory. |
| `DBRAIN_TSNET_LISTEN` | `tsnet.listen` | Tailnet listen address. |
| `DBRAIN_TSNET_TLS` | `tsnet.tls` | Enable HTTPS via `ListenTLS`. |
| `DBRAIN_TSNET_STARTUP_TIMEOUT` | `tsnet.startup_timeout` | Maximum time to wait for `Up(ctx)`. |
| `DBRAIN_TSNET_AUTH_KEY` | `tsnet.auth_key` | Direct auth key. Prefer refs. |
| `DBRAIN_TSNET_AUTH_KEY_REF` | `tsnet.auth_key_ref` | Typed secret reference. |
| `DBRAIN_TSNET_ALLOW_SECRET_COMMAND` | `tsnet.allow_secret_command` | Permit command resolver execution. |
| `DBRAIN_TSNET_ADVERTISE_TAGS` | `tsnet.advertise_tags` | Comma-separated Tailscale tags. |
| `DBRAIN_TSNET_CONTROL_URL` | `tsnet.control_url` | Experimental alternate control server URL. |
| `DBRAIN_TSNET_VERBOSE` | `tsnet.verbose` | Verbose tsnet logging. |

`--tsnet-advertise-tags` and `DBRAIN_TSNET_ADVERTISE_TAGS` should parse as
comma-separated strings. YAML config should use a string array.

`tsnet.auth_key_command` intentionally has no environment variable row.

## Persistence Design

Default state path:

```text
~/.local/share/dbrain/tsnet/<hostname>/
```

In code, derive this from the resolved dbrain data directory, for example
`filepath.Join(cfg.DataDir, "tsnet", hostname)`, instead of hardcoding a home
directory string. The path above is the default after config resolution.

Rules:

- Expand a leading `~` in `state_dir`.
- Resolve `state_dir` to an absolute path before use.
- Resolve symlinks before sync-folder warnings, ownership checks, and lock
  acquisition.
- Create the directory with `0700`.
- Create and stat the directory before constructing `tsnet.Server`; do not rely
  on tsnet and the process umask to create it securely.
- Only the leaf tsnet state directory must be `0700`; parent directories may be
  looser.
- Refuse to use a directory that is group/world writable.
- Reject state directories not owned by the current user where the OS supports
  the check.
- Resolve symlinks where possible, then warn if the path appears to be under
  iCloud Drive, Dropbox, OneDrive, or another obvious sync root.
- Use a stable hostname by default.
- Do not set `tsnet.Server.Ephemeral`.
- Run only one dbrain process per tsnet state directory.
- Do not delete or rotate this state except through an explicit reset command.

This directory stores sensitive tailnet node state. It belongs under
`~/.local/share/dbrain/tsnet/<hostname>` rather than `~/.config/dbrain`
because it is mutable runtime identity data, not user-authored configuration.

Single-process enforcement needs an application-level lock because tsnet does
not provide one. Create `<state-dir>/dbrain.lock`, open it, and hold an
OS-level advisory lock such as `flock` or `fcntl` on that open file descriptor
for the full process lifetime before starting tsnet. Do not treat lock-file
existence as ownership; the file may remain on disk after exit. Lock the
canonical resolved state-dir path after symlink resolution. If the lock is
already held, fail with a clear message that includes the state directory and
hostname. Release the lock only after `http.Server.Shutdown(ctx)` and
`tsnet.Server.Close()` complete.

## Secrets Design

There are two different secret classes:

1. Bootstrap secrets such as a Tailscale auth key or OAuth client secret.
2. Persistent tsnet node state managed by `tsnet.Server.Dir`.

Bootstrap secrets can come from config, env vars, typed refs, 1Password, or
macOS Keychain. Persistent tsnet node state should remain in
`tsnet.Server.Dir` initially.

Resolution order:

1. `--tsnet-auth-key`
2. `DBRAIN_TSNET_AUTH_KEY`
3. `tsnet.auth_key`
4. `--tsnet-auth-key-ref`
5. `DBRAIN_TSNET_AUTH_KEY_REF`
6. `tsnet.auth_key_ref`
7. YAML config-file command resolver only when explicitly allowed
8. interactive/login URL flow from tsnet

Direct config/env auth keys work, but should be documented as less preferred
than secret references.

If multiple auth-key sources are configured, the higher-precedence source wins.
Log a warning that lower-precedence auth-key sources were ignored, but do not
log any secret values.

### Typed Secret References

Typed refs avoid arbitrary shell execution while supporting common secret
stores.

Supported first-pass schemes:

| Ref | Example | Resolver |
| --- | --- | --- |
| `env:` | `env:TS_AUTHKEY` | Read one environment variable. |
| `op://` | `op://Private/dbrain/tsnet-auth-key` | Execute `op read <ref>` without a shell. |
| `keychain://` | `keychain://dbrain/tsnet-auth-key` | Read macOS Keychain via Go keyring; unsupported platforms return a clear error. |

Example config:

```yaml
tsnet:
  auth_key_ref: "op://Private/dbrain/tsnet-auth-key"
```

macOS Keychain example:

```yaml
tsnet:
  auth_key_ref: "keychain://dbrain/tsnet-auth-key"
```

The `keychain://service/account` form maps to:

- service: `dbrain`
- account: `tsnet-auth-key`

### Command Escape Hatch

Command execution is useful, but it should not be the normal secure path.

If included, make it explicit and argv-based:

```yaml
tsnet:
  allow_secret_command: true
  auth_key_command:
    - op
    - read
    - op://Private/dbrain/tsnet-auth-key
```

Command execution rules:

- Disabled unless `allow_secret_command` is true.
- YAML config-file only. Do not provide `DBRAIN_TSNET_AUTH_KEY_COMMAND` or
  `--tsnet-auth-key-command`; env vars and scalar CLI flags do not have a
  native argv encoding and should not trigger shell splitting or ad-hoc JSON
  parsing.
- Do not run through a shell.
- Run only when direct auth keys and typed refs are empty.
- Trim trailing whitespace.
- Reject empty output.
- Do not log command output.
- Log only command executable and success/failure.
- Use a timeout, for example 10 seconds.
- Call the resolver at most once per process. Auth keys may be single-use, so
  do not retry repeatedly during the same run.

## macOS Keychain Option

Keychain is reasonable for bootstrap secrets and should be supported in the
design, not merely as an afterthought.

Preferred first-pass implementation:

- Use the existing `go-keyring` dependency for `keychain://service/account`.
- Keep this as one resolver behind the typed secret-ref abstraction.
- Preserve cross-platform behavior by making unsupported platforms return a
  clear error telling users to use `op://`, `env:`, or direct config/env.

Fallback implementation if native keyring support proves brittle:

- Users may configure the YAML-only command escape hatch with
  `security find-generic-password ... -w`.
- Keep it explicit through `allow_secret_command: true`.

Do not put full tsnet state into Keychain. Use file-backed `tsnet.Server.Dir`
for node state; a custom `ipn.StateStore` is out of scope unless the file
store becomes a concrete operational problem.

## 1Password Option

1Password is best used through typed `op://` refs:

```yaml
tsnet:
  auth_key_ref: "op://Private/dbrain/tsnet-auth-key"
```

Implementation should execute:

```sh
op read op://Private/dbrain/tsnet-auth-key
```

without invoking a shell.

Pros:

- Cross-platform.
- Easy for users already using 1Password CLI.
- No vendor SDK dependency.

Cons:

- Requires `op` installed and signed in.
- Headless setups need their own 1Password service-account story.

## Security Model

Default posture:

- Web UI and MCP can be exposed together from one tsnet node.
- Web UI is read/write when mounted remotely.
- MCP remains read-only.
- `stdio` remains the default transport.
- `http` binds only to localhost by default.
- `tsnet` exposes only to the tailnet.
- `tsnet` uses HTTPS by default.
- Plain HTTP over tsnet requires explicit opt-out.
- Avoid Tailscale Funnel for web or MCP unless a user explicitly chooses public
  internet exposure.

### Web UI Access

The web UI was originally built for localhost. Exposing it over the tailnet
means tailnet users allowed by Tailscale policy can reach it without a separate
dbrain login unless we add one. The current web UI is not read-only: it exposes
mutating endpoints such as `/api/tag` and `/api/links`, and future web
mutations should be treated the same way.

This is an accepted risk for the first implementation. Remote web is a trusted
tailnet administration surface, not a public or unauthenticated read-only
viewer. Do not build a stripped read-only web mux in the first pass.

The first implementation should treat Tailscale ACLs and node tags as the
primary access-control boundary:

- Document that remote web access is available to whichever tailnet users and
  devices the admin allows to reach the `dbrain` node or tag.
- Prefer `AdvertiseTags`, for example `tag:dbrain`, so admins can write stable
  ACLs independent of the user's machine name.
- Do not attempt to validate every tailnet's ACL policy inside the binary;
  Tailscale administrators will model access differently.

dbrain should still provide local guardrails:

- Print a startup warning when mounting the web UI remotely, including the
  hostname/tag and a reminder that Tailscale ACLs govern access.
- For direct tsnet requests, call `tsnet.Server.LocalClient().WhoIs` with the
  request context and remote address, then log the resolved caller identity.
- Add remote-only HTTP hardening headers, including HSTS when TLS is enabled,
  `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY` or equivalent
  CSP `frame-ancestors 'none'`, and `Referrer-Policy: no-referrer`.
- Add same-origin checks on the remote tsnet listener only. For v1, apply the
  check to every non-`GET` / non-`HEAD` request. If `Origin` is present, require
  it to match the request's effective origin, computed from the actual request
  scheme and host rather than a string reconstructed from `--tsnet-hostname`.
  Accept requests with no `Origin` so CLI/API clients continue to work.
  Tailscale ACLs decide who can connect, but browser-origin protections still
  matter once the web UI is reachable from a browser.

The v1 Origin rule assumes current write endpoints such as `/api/tag` and
`/api/links` are JSON POSTs without cookie auth or browser session state. If a
future endpoint accepts form posts, multipart uploads, cookies, or browser
sessions, revisit the guard.

Tailscale Serve and related proxy paths can include user identity headers, but
a direct `tsnet` server does not receive injected identity headers. Direct
tsnet identity logging should use `LocalClient.WhoIs`; if lookup fails, log the
remote tailnet address and the lookup error without blocking the request.

Recommended tsnet access controls:

- Use Tailscale ACLs to restrict which users/devices can reach the tsnet node.
- Prefer `AdvertiseTags` for a stable policy target, for example
  `tag:dbrain`.
- Log requested versus actual tags when available. `AdvertiseTags` is a request
  to the control plane, not proof that the node is actually tagged.
- Use request-level WhoIs logging for direct tsnet requests. Do not add
  dbrain-side allowlists unless the product direction changes toward broader
  multi-user authorization.

## Certificate Behavior

The safe default is to let tsnet/Tailscale manage HTTPS via `ListenTLS` while
preserving `Server.Dir`.

Design implications:

- A stable `Server.Dir` should prevent repeated fresh node setup after restarts.
- A stable hostname should prevent DNS/certificate identity churn.
- If state is deleted, the node may need re-authentication and may request new
  certificate material.
- Avoid creating many hostnames during tests.
- Do not run TLS/certificate tests by default in CI.
- Tailscale `.ts.net` certificates are logged to public Certificate
  Transparency logs, so the node FQDN is publicly discoverable even though the
  service is reachable only from the tailnet.

For local development, either use the normal HTTPS path with a stable dev
hostname and state dir, or explicitly disable TLS with `--tsnet-tls=false`.

### Custom Control Servers

`--tsnet-control-url` is experimental. It is the right tsnet field for alternate
control servers, but Tailscale SaaS DNS and `ListenTLS` certificate issuance
are not guaranteed to work the same way on custom control servers such as
Headscale.

First-pass behavior should be conservative:

- Document custom-control support as experimental.
- Do not show `.ts.net` URLs for custom-control mode unless tsnet status proves
  that is the actual reachable FQDN.
- Either reject `--tsnet-control-url` with `--tsnet-tls=true` until behavior is
  validated, or emit a clear experimental warning with the actual listener URL.
- Keep custom-control testing separate from the core Tailscale SaaS path.

The existing MCP HTTP handler is stateless and returns JSON responses. It does
not currently provide SSE streams for server-to-client notifications. If SSE is
added later, verify that the tsnet HTTP server does not disable HTTP/2 and that
SSE response flushing is not buffered.

## Authentication UX

First-run authentication must not look like a hang.

When no auth key is available and the node has not authenticated before:

- Wire `tsnet.Server.UserLogf` to stderr even when verbose logging is disabled.
- Tee all `UserLogf` output to stderr unconditionally.
- Detect URL-shaped strings in `UserLogf` output, not only
  `https://login.tailscale.com/...`, so custom-control auth URLs also surface.
- Dedupe repeated auth URLs.
- Re-emit detected URLs to stderr with clear text:

```text
Visit this URL to authenticate dbrain:
https://login.tailscale.com/...
```

If the process is non-interactive and no auth key/ref is configured, fail with
an actionable error instead of waiting indefinitely when possible.

`tsnet.Server.Logf` is backend debug logging and should remain behind
`--tsnet-verbose`. Do not depend on `Logf` for first-run authentication URLs.

## Operational Commands

Add these after the basic transport works:

```sh
dbrain tsnet status
dbrain tsnet reset --tsnet-hostname dbrain
```

`--hostname dbrain` is a default-hostname shortcut. The command still resolves
the final target through config, env, and flags, and acts on the resolved
state directory.

Do not add a separate `dbrain tsnet auth-url` command in v1. tsnet does not
expose the pending auth URL as a stable API; startup-time `UserLogf` capture
covers the real need without adding a log-scraping command.

Possible status output:

```text
hostname: dbrain
state_dir: /Users/alice/.local/share/dbrain/tsnet/dbrain
exists: true
locked: true
running: true
reachable: true
web_reachable: true
mcp_reachable: true
tailnet_ips: 100.x.y.z, fd7a:...
web_url: https://dbrain.example.ts.net/
mcp_url: https://dbrain.example.ts.net/mcp
tls: true
state: authenticated
cert_health: ok
needs_login: false
control_url:
```

Status reports resolved local state, lock status, and active health for the
configured node:

- Whether a dbrain process currently holds the state lock.
- Whether the node appears to be running, not only whether state files exist.
- If the node is running, probe both the web URL and MCP URL by default.
- Report a running-but-unreachable node as `state: down`.
- Check HTTPS certificate health with both a real HTTPS request and any local
  listener/certificate state that is practical to inspect.
- If Go DNS cannot resolve the MagicDNS name, use the local Tailscale daemon's
  peer status as a best-effort IP fallback and still validate HTTPS with the
  certificate hostname.
- Include machine-readable JSON fields for `running`, `reachable`,
  `web_reachable`, `mcp_reachable`, `cert_health`, and `needs_login`.
- Set `needs_login` when the tsnet node is not authenticated yet or startup
  would require an interactive Tailscale login URL.
- A clear distinction between `not configured`, `needs login`, `running but
  unreachable`, and `healthy`.

Reset should be explicit and guarded:

```text
This will delete tsnet state for dbrain:
/Users/alice/.local/share/dbrain/tsnet/dbrain
This requires re-authentication.
Proceed? [y/N]
```

`status` and `reset` must resolve config, flags, and env through the same
pipeline as `serve remote`. Use the resolved state directory as the primary
identity, not just `--hostname`, because users can set a custom `state_dir` or
run multiple roots. `reset` must refuse to run when the state-dir lock is held
by a running daemon.

## Implementation Plan

### Phase 1: Config And CLI

- Add tsnet config fields and env docs.
- Add `dbrain serve remote --web --mcp`.
- Add `--transport tsnet` to `dbrain serve mcp` as MCP-only compatibility.
- Add flags for hostname, state dir, listen address, TLS, direct auth key,
  auth-key ref, command escape hatch enablement, comma-separated tags, control
  URL, web enablement, MCP enablement, MCP path, and verbosity.
- Make `serve remote` tsnet-only in v1, while `serve mcp` keeps `stdio` as its
  default and accepts `--transport tsnet` for MCP-only remote compatibility.
- Fail fast if both web and MCP are disabled.
- Print a clear startup warning when remote web is enabled because the web UI is
  read/write.
- Mark `--tsnet-control-url` as experimental in CLI help and docs.
- Define strict `mcp_path` validation.
- Add docs and config sample entries.

### Phase 2: Secret Resolvers

- Add `internal/secrets` or similar with typed secret-ref resolution.
- Implement `env:` resolver.
- Implement `op://` resolver by running `op read <ref>` without a shell.
- Implement `keychain://` resolver using Go keyring where supported.
- Add the disabled-by-default YAML-only argv command escape hatch.
- Add tests for precedence, command timeout, no-shell execution, logging
  hygiene, and single-call behavior.
- Log a warning when a lower-precedence auth-key source is ignored because a
  higher-precedence source was set.

### Phase 3: Transport

- Add a shared remote server package or command wiring that can mount both web
  and MCP handlers on one HTTP mux.
- Add tsnet serving support that is not tied only to `internal/mcpserver`.
- Resolve hostname and default state dir from the resolved dbrain config, using
  `filepath.Join(cfg.DataDir, "tsnet", hostname)`.
- Create and validate the state directory before constructing `tsnet.Server`.
- Acquire `<state-dir>/dbrain.lock` and hold it for the process lifetime before
  starting tsnet.
- Resolve the auth key/ref once before starting tsnet.
- Create `tsnet.Server` with stable `Hostname`, `Dir`, optional `AuthKey`,
  optional `AdvertiseTags`, optional experimental `ControlURL`, always-on
  `UserLogf`, and verbose-only `Logf`. Set every field before any server method
  call.
- Call `Up(ctx)` with `tsnet.startup_timeout` before listening. Default to
  `45s`; expose `--tsnet-startup-timeout` for users who need a different
  timeout.
- Cache `LocalClient()` after startup for request identity logging when
  available. If `LocalClient()` fails, keep serving and log requests by remote
  address.
- Capture tsnet user logs through `UserLogf` and surface first-run
  authentication URLs clearly.
- Use `ListenTLS("tcp", listen)` by default.
- Use `Listen("tcp", listen)` only when TLS is explicitly disabled.
- Log the actual FQDN and URLs reported by tsnet/status where available; do not
  rely only on reconstructing strings from `--tsnet-hostname`.
- Add an outer dispatcher on the tsnet listener that sends `path == mcp_path`
  and `strings.HasPrefix(path, mcp_path + "/")` to MCP before the web handler
  is consulted. Subtree paths such as `/mcp/foo` should return MCP-shaped
  errors, not SPA HTML.
- Mount the existing web handler at `/` when web is enabled, after the MCP route
  so any SPA fallback cannot swallow `/mcp`.
- Emit a remote web startup warning that Tailscale ACLs govern access.
- Add remote-only security headers and concrete Origin checks for mutating web
  requests on the tsnet listener.
- For direct tsnet requests, use `tsnet.Server.LocalClient().WhoIs` against the
  request remote address for caller identity logging; fall back to remote
  address on lookup failure.
- If secret resolution, `Up(ctx)`, `ListenTLS`, `Listen`, or status lookup
  fails, close any partially constructed `tsnet.Server` and release the lock
  before returning.
- On context cancellation / SIGINT / SIGTERM, call `http.Server.Shutdown(ctx)`
  first, then `tsnet.Server.Close()`.
- Log final web and MCP URLs when available.

### Phase 4: Status And Reset

- Add `dbrain tsnet status`.
- Add guarded `dbrain tsnet reset`.
- Resolve state-dir identity through the same config/flag/env pipeline as
  `serve remote`.
- Refuse reset when the state-dir advisory lock is held.
- Add tests for state-dir validation and reset confirmation.

### Phase 5: Deferred Or Discarded

- Do not implement dbrain-side allowlists now. Tailscale ACLs/tags remain the
  access-control boundary for the intended setup.
- Do not evaluate custom `ipn.StateStore` now. File-backed `tsnet.Server.Dir`
  is the intended persistence mechanism.
- Do not add service-mode behavior now. A normal tsnet node is enough for the
  current product direction.

## Testing Strategy

Unit tests:

- Config/env resolution for tsnet fields.
- Thin internal tsnet interface or adapter around `tsnet.Server` and
  `LocalClient` so startup, logging, and shutdown behavior can be unit-tested
  without a real tailnet.
- Secret-ref resolution precedence.
- Lower-precedence auth-key source warning.
- Keychain resolver behavior behind platform guards.
- 1Password resolver argument construction.
- Command escape hatch disabled by default.
- Command timeout and single-call behavior.
- State-dir creation mode and unsafe-permission rejection.
- State-dir advisory lock acquisition and already-running failure behavior.
- iCloud/sync-root warning helper.
- `Up(ctx)` uses the configured startup timeout and fails clearly on timeout.
- First-run URL surfacing and dedupe from `UserLogf`; `Logf` stays
  verbose-only.
- Web handler and MCP handler reuse remains transport-neutral.
- Mux ordering: `/mcp`, `/mcp/`, and `/mcp/foo` must reach MCP or MCP-specific
  responses even when web is mounted at `/`.
- Strict `mcp_path` validation.
- `serve remote --web=false --mcp=false` fails fast.
- Remote web startup warning is emitted.
- Direct tsnet request logging uses `LocalClient.WhoIs`, not injected headers.
- `LocalClient` / `WhoIs` failures fall back to remote-address logging.
- Shutdown ordering is `http.Server.Shutdown(ctx)` before
  `tsnet.Server.Close()`.
- Startup failures unwind by closing tsnet and releasing the state-dir lock.
- Remote-only security headers are added.
- Mutating remote web requests reject mismatched `Origin` values and accept
  no-`Origin` CLI/API requests.
- Experimental `--tsnet-control-url` behavior is explicit.

Add a handler-level non-tsnet routing test that proves `/mcp`, `/mcp/`, and
`/mcp/foo` never fall through to SPA HTML.

Integration tests:

- Keep normal CI on stdio and localhost HTTP.
- Gate real tsnet tests behind an env var such as `DBRAIN_TEST_TSNET=1`.
- Use a test hostname prefix.
- Avoid certificate-churn tests by default.
- Before merging the transport, check the dependency and binary-size delta from
  adding `tailscale.com/tsnet`.

Manual test:

```sh
dbrain serve remote --web --mcp --tsnet-hostname dbrain-dev
curl https://dbrain-dev.<tailnet>.ts.net/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
open https://dbrain-dev.<tailnet>.ts.net/
```

Development-only HTTP manual test:

```sh
dbrain serve remote \
  --web \
  --mcp \
  --tsnet-hostname dbrain-dev \
  --tsnet-tls=false \
  --tsnet-listen :80
```

## Risks

- Certificate churn if hostname or state dir changes frequently.
- Node identity loss if the state dir is deleted or synced incorrectly.
- Users may put auth keys directly in config.
- 1Password or Keychain resolver failures can make startup look like a network
  problem unless errors are explicit.
- Multiple auth-key sources can confuse startup unless ignored lower-precedence
  sources are warned about without leaking secret values.
- First-run authentication can look like a hang if auth URLs are not surfaced.
- Adding `tailscale.com/tsnet` may materially increase binary size and
  dependency footprint.
- Some MCP clients may not support remote Streamable HTTP yet.
- Web UI remote exposure is read/write and has no separate dbrain login in the
  first pass; access relies on Tailscale ACLs/tags.
- Remote web mutating routes need browser-origin/CSRF guardrails because
  Tailscale ACLs do not protect against every browser-origin scenario.
- The concrete v1 CSRF guard only checks browser `Origin`; it is not a
  substitute for Tailscale ACLs or future dbrain-side authentication.
- The concrete v1 Origin guard assumes current write endpoints are JSON POSTs
  without cookie auth or browser sessions.
- Web UI remote exposure may need additional UI-side checks for absolute URLs,
  websocket/SSE assumptions if added later, and safe external-link behavior.
- Custom control servers may not support `.ts.net` DNS or `ListenTLS`
  certificate behavior; treat `--tsnet-control-url` as experimental.
- Tailscale `.ts.net` certificate issuance publishes the node FQDN to public
  Certificate Transparency logs.
- `ListenTLS` returning does not guarantee the first browser request will be
  instant; first-use certificate provisioning and device approval can still
  create visible latency.

## Remaining Work

No near-term open implementation questions remain for v1.

Questions about dbrain-side allowlists, custom `ipn.StateStore`, and
service-mode listeners are closed for now: they are not part of the current
roadmap.

## Recommended Decision

Implement the first pass with:

- `dbrain serve remote` as a tsnet-only command
- `dbrain serve remote --web --mcp` as the preferred combined command
- `dbrain serve mcp --transport tsnet` as MCP-only compatibility
- stable default hostname `dbrain`
- durable state under `~/.local/share/dbrain/tsnet/<hostname>`
- `0700` state-dir permissions
- exclusive `<state-dir>/dbrain.lock` held for the process lifetime
- OS-level advisory locking on the canonical resolved state-dir lock file
- no iCloud/sync path by default
- HTTPS by default through `ListenTLS`
- `45s` default `Up(ctx)` startup timeout with `--tsnet-startup-timeout`
- explicit `--tsnet-tls=false` for development-only HTTP over the tailnet
- typed `auth_key_ref` support for `env:`, `op://`, and `keychain://`
- Go keyring as the v1 `keychain://` backend
- warning when lower-precedence auth-key sources are ignored
- optional YAML-only command escape hatch disabled by default
- `--tsnet-control-url` as experimental custom-control support only
- clear first-run auth URL output through `UserLogf`
- explicit `Up(ctx)` startup before listening
- actual FQDN/URL logging from tsnet startup status where available
- `serve remote` guard when both web and MCP are disabled
- outer MCP dispatcher plus route tests proving `/mcp` paths are not swallowed
  by the web handler
- strict `mcp_path` validation
- `status` / `reset` target resolution through the same config pipeline as
  `serve remote`, using the resolved state dir as the primary identity
- `dbrain tsnet status` health checks for running/reachable/certificate state
- remote web startup warning plus best-effort `LocalClient.WhoIs` identity
  logging
- remote-only HTTP hardening headers and concrete Origin checks for mutating
  web requests
- graceful shutdown through `http.Server.Shutdown(ctx)` followed by
  `tsnet.Server.Close()`
- no custom `ipn.StateStore` initially

This gives users the operational simplicity of one binary while keeping remote
web and MCP access private to the tailnet by default and preserving tsnet's
node/certificate state in the location tsnet is designed to use.
