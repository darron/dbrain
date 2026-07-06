# dbrain Tailscale Remote Access

This guide covers remote web and MCP access through Tailscale `tsnet`, Tailscale
Serve, and Tailscale Funnel.

Use this when you need to run `dbrain` from another device, expose MCP to a
remote agent, or temporarily publish the web UI through Funnel.

## Quick Choice

- Local machine only: use `dbrain serve web` and local stdio MCP.
- Private tailnet access: use `dbrain serve remote`.
- Public internet access through Tailscale: use `dbrain serve remote` with
  Funnel, GitHub OAuth for web, and MCP bearer auth for MCP.
- MCP-only private tailnet access: use `dbrain serve mcp --transport tsnet`.
- Existing local HTTP service behind Tailscale: use `dbrain serve mcp
  --transport http` plus `tailscale serve`.

## Private Tailnet Remote

`dbrain serve remote` starts one built-in Tailscale `tsnet` node and serves:

- the read/write web UI at `https://<hostname>.<tailnet>.ts.net/`
- read-only MCP at `https://<hostname>.<tailnet>.ts.net/mcp`

```sh
dbrain serve remote --web --mcp --tsnet-hostname dbrain-dev
```

The default state directory is `<data_dir>/tsnet/<hostname>`. Keep it stable
across restarts and out of iCloud, Dropbox, OneDrive, and similar sync folders.
It contains Tailscale node and certificate state.

Common shape:

```yaml
tsnet:
  hostname: dbrain-dev
```

Remote web is the same trusted read/write administration UI as
`dbrain serve web`. Tailscale ACLs, device trust, node tags, and optional
dbrain GitHub OAuth decide who can reach it. Enable web auth even on the
tailnet when more than one person or device class can reach the node.

## Long-Running Remote

`serve remote` is also the intended long-running process for launchd-backed
installs and scheduled sync work. Keep one stable hostname and state directory,
then configure the scheduler separately:

```yaml
scheduler:
  sync_all:
    enabled: true
    interval: 1h
    run_on_start: false
```

The scheduler runs inside the already-running `serve remote` process and skips
overlapping `sync all` runs. Use the scheduler-specific config keys documented
by `dbrain config env` for per-stage limits, models, browser profile, optional
Apple Notes/Safari tabs, and skip flags.

Launchd helpers generate and manage a macOS user service around the same
command/config boundary:

```sh
dbrain launchd plist --label com.darron.dbrain --stdout-log ~/.local/share/dbrain/logs/launchd.out.log --stderr-log ~/.local/share/dbrain/logs/launchd.err.log
dbrain launchd install --label com.darron.dbrain --stdout-log ~/.local/share/dbrain/logs/launchd.out.log --stderr-log ~/.local/share/dbrain/logs/launchd.err.log
dbrain launchd restart --label com.darron.dbrain
```

Check scheduler state through the web surface:

```sh
curl -s https://dbrain-dev.<tailnet>.ts.net/api/scheduler/sync-all
```

## Public Funnel Remote

Funnel is public internet exposure on the same `tsnet` node identity, hostname,
state directory, and Tailscale auth credentials. It is a listener mode, not a
second dbrain feature set.

Any of these enable Funnel:

```sh
dbrain serve remote --web --mcp --tsnet-funnel
```

```sh
DBRAIN_TSNET_FUNNEL=true dbrain serve remote --web --mcp
```

```yaml
tsnet:
  funnel: true
```

Funnel requires:

- Tailscale Funnel allowed in tailnet policy.
- MagicDNS enabled.
- HTTPS certificates enabled for the tailnet.
- `tsnet.tls=true` / `--tsnet-tls=true`.
- listener port `:443`, `:8443`, or `:10000`.

dbrain rejects Funnel with plain HTTP or unsupported ports.

## Funnel Policy

Tailnet policy must allow the node owner or tag to publish Funnel. To allow only
one tailnet user to start Funnel on their own nodes:

```jsonc
"nodeAttrs": [
  {
    "target": ["you@example.com"],
    "attr": ["funnel"],
  },
],
```

If the dbrain tsnet node is tagged, target the tag instead:

```jsonc
"nodeAttrs": [
  {
    "target": ["tag:dbrain"],
    "attr": ["funnel"],
  },
],
```

This authorizes who may publish a Funnel. It does not restrict who on the public
internet can visit the Funnel URL. Avoid `autogroup:member` unless every
tailnet member should be allowed to publish public Funnel services.

## Public Auth

Funnel makes the selected web and MCP surfaces publicly reachable at the network
layer. dbrain auth still controls application access.

For the web UI:

```yaml
auth:
  enabled: true
  providers: ["github"]
  base_url: "https://dbrain-dev.<tailnet>.ts.net"
  session_key: "env:DBRAIN_AUTH_SESSION_KEY"
  github:
    client_id: "..."
    client_secret: "env:DBRAIN_AUTH_GITHUB_CLIENT_SECRET"
```

The GitHub OAuth callback URL is:

```text
<auth.base_url>/auth/github/callback
```

Approve users in the local DB:

```sh
dbrain auth github approve your-github-login
dbrain auth github list
```

For MCP:

```sh
dbrain auth mcp token add agent-name
```

Enable enforcement:

```yaml
mcp:
  auth:
    enabled: true
```

or:

```sh
DBRAIN_MCP_AUTH_ENABLED=true dbrain serve remote --tsnet-funnel
```

MCP clients must send:

```text
Authorization: Bearer <token>
```

## Smoke Tests

Check dbrain's resolved view of the tsnet node:

```sh
dbrain tsnet status --tsnet-hostname dbrain-dev
dbrain tsnet status --tsnet-hostname dbrain-dev --tsnet-funnel
dbrain tsnet status --tsnet-hostname dbrain-dev --json
```

`tsnet status` resolves the same hostname, state directory, listener, TLS,
Funnel, web, and MCP settings as `serve remote`. When possible, it also reports
configured web/MCP URLs, whether each surface is reachable, certificate health,
and whether the node still needs login.

Smoke-test MCP:

```sh
curl -s https://dbrain-dev.<tailnet>.ts.net/mcp \
  -H "Authorization: Bearer $DBRAIN_MCP_TOKEN" \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

If bearer auth is intentionally disabled, omit the Authorization header.

## Troubleshooting

`no such host` for `https://<hostname>.<tailnet>.ts.net/` usually means DNS has
not propagated yet, MagicDNS/HTTPS is not enabled, or the Funnel DNS record has
not been published. Public DNS can lag behind a successful tsnet startup.

If `tsnet status` shows `needs_login=true`, start `dbrain serve remote` and
complete the Tailscale login URL, or configure `tsnet.auth_key_ref`.

If tailnet access works but public access does not, check:

- Funnel policy includes the right user or tag.
- `DBRAIN_TSNET_FUNNEL=true`, `tsnet.funnel: true`, or `--tsnet-funnel` is set.
- TLS is enabled.
- The listener port is `:443`, `:8443`, or `:10000`.
- Public DNS has had time to propagate.

Funnel request logs can show a Tailscale transport identity such as a tag or an
IPv6 `fd7a:115c:a1e0::/48` address. For public Funnel web traffic, the useful
user identity is the app-layer log line:

```text
web request ... auth="github" identity="..."
```

For MCP with bearer auth, the app-layer log line includes token metadata without
the raw secret:

```text
mcp request ... auth="bearer" token_name="..." token_fingerprint="..."
```

## Unattended Startup

Prefer a typed secret ref for Tailscale auth keys:

```yaml
tsnet:
  auth_key_ref: "op://Private/dbrain/tsnet-auth-key"
```

Keychain and environment refs are also supported:

```yaml
tsnet:
  auth_key_ref: "keychain://dbrain/tsnet-auth-key"
```

```yaml
tsnet:
  auth_key_ref: "env:TS_AUTHKEY"
```

`tsnet.auth_key_command` exists for custom secret managers, but it is
config-file-only and requires `tsnet.allow_secret_command: true`.
