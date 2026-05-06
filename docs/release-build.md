# Release And Web Build Notes

Date: 2026-05-05

This project ships as a Go binary with an embedded Svelte/Vite web UI.

## Embedded Web Assets

The built web UI under `web/ui/dist` is intentionally tracked. The Go web
server embeds it from `web/server.go`:

```go
//go:embed all:ui/dist
var embeddedUI embed.FS
```

`task build` compiles the Go binary and embeds whatever `web/ui/dist` currently
contains. It does not run `task web-build`.

This keeps ordinary Go builds from requiring `npm`, but it means UI source
changes must be paired with refreshed dist assets before release.

`task build` injects release metadata from `DBRAIN_RELEASE_VERSION`, a GitHub
Actions tag ref, or an exact checked-out git tag. Untagged local builds report
`release_version: unknown` and rely on the build short commit for identity.

## When To Run `task web-build`

Run `task web-install` after a fresh checkout if `web/ui/node_modules` is
missing, or after `web/ui/package-lock.json` changes.

Run `task web-build` whenever a change can affect the bundled UI:

- `web/ui/src/**`
- `web/ui/package.json`
- `web/ui/package-lock.json`
- `web/ui/vite.config.js`
- `web/ui/svelte.config.js`
- any future UI asset or build-config file

Commit the resulting `web/ui/dist/**` changes with the source UI change.

Go-only, CLI-only, docs-only, and backend-only changes do not need
`task web-build` unless they change an API contract or static data expected by
the UI.

## Release Checklist

Before tagging or packaging a release that includes UI changes:

1. Run `task web-install` if dependencies are missing or changed.
2. Run `task web-build`.
3. Inspect `git status --short web/ui/dist web/ui/src web/ui/package*.json`.
4. Commit refreshed `web/ui/dist/**` assets with the UI source changes.
5. Run the normal gates:

   ```sh
   task fmt
   task lint
   task test
   task build
   ```

6. Optionally spot-check the embedded UI:

   ```sh
   bin/dbrain serve web
   ```

## Avoiding Stale Embedded Assets

Do not use the Vite dev server as release evidence. It serves source assets
directly and does not prove that `web/ui/dist` is current.

If UI behavior looks different between development and `bin/dbrain serve web`,
rebuild `web/ui/dist` and check the diff:

```sh
task web-build
git diff -- web/ui/dist
```

If `task build` succeeds but the served UI looks old, the binary likely embedded
stale tracked dist assets. Rebuild the UI, then rebuild the Go binary.
