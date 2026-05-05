# Open-Source License Review

Date: 2026-05-04

This is an engineering license scan for open-source readiness, not legal
advice. Its purpose is to flag dependencies and repository artifacts that need
human review before publishing `dbrain`.

## Scope

Reviewed:

- Repository-level license and notice files.
- Go module graph after `go mod download all`.
- Runtime package graph for `./cmd/dbrain`.
- Frontend dependencies in `web/ui/package-lock.json`.
- Downloaded module license files in the repo-local `.gomodcache`.

Not reviewed:

- License obligations for generated binaries or packaged release archives.
- Licenses for system tools installed outside the repo, such as `yt-dlp`,
  `ffmpeg`, `ollama`, `tesseract`, `golangci-lint`, or Homebrew packages.
- A legal review of whether release archives need to include exact upstream
  license files beyond `THIRD_PARTY_NOTICES.md`.

## Summary

- The repository project license is MIT. The root `LICENSE` file was added after
  this dependency scan.
- `THIRD_PARTY_NOTICES.md` now lists `./cmd/dbrain` runtime dependencies and
  frontend lockfile dependencies.
- The current targeted scan does not show a runtime dependency that obviously
  forces `dbrain` itself to use a copyleft license. Most dependency obligations
  appear to be notice/source-distribution obligations, not project-license
  selection constraints.
- No GPL, AGPL, SSPL, Elastic License, or Business Source License dependency was
  found in the compiled `./cmd/dbrain` runtime graph by the targeted scan.
- The downloaded Go module cache and `go.sum` include GPL-3.0 lint/tooling
  modules. The checked `./cmd/dbrain` runtime graph does not import them, but
  they are still a cleanup concern for the repository's public dependency story.
- `modernc.org/libc` is in the `./cmd/dbrain` runtime graph and is licensed
  BSD-style at the module root. A GPL file exists under that module's
  `testdata/nsz.repo.hu/.../crlibm/COPYING`; it was not in the runtime import
  directories. Do not vendor or ship module-cache testdata in release archives
  without a deeper source-distribution review.
- Frontend lockfile licenses are permissive or dual-permissive for the current
  dependencies. `dompurify` is listed as `(MPL-2.0 OR Apache-2.0)`, so the
  Apache-2.0 option should be used in notices if that matches the package's
  published terms.

## Go Findings

### Runtime graph

The suspicious-license runtime scan for `./cmd/dbrain` only matched:

- `modernc.org/sqlite v1.49.1`
- `modernc.org/libc v1.72.0`

`modernc.org/libc` has a BSD-style module root license. The GPL hit is inside
module testdata and was not part of the compiled package directories returned by
`go list -deps -test=false ./cmd/dbrain`.

### Tooling and cache graph

The local module cache contains GPL-3.0 license files for modules such as:

- `github.com/golangci/golangci-lint`
- `github.com/OpenPeeDeeP/depguard/v2`
- `github.com/denis-tingaikin/go-header`
- `github.com/firefart/nonamedreturns`
- `github.com/golangci/plugin-module-register`
- `github.com/leonklingele/grouper`
- `github.com/xen0n/gosmopolitan`

`go mod why -m` did not show these as needed by the main module, and the
`./cmd/dbrain` runtime graph excluded them. They appear to be lint/tooling or
historical checksum/cache artifacts rather than linked application dependencies.

Recommendation: keep lint tools installed as external developer prerequisites
rather than checked-in tool dependencies, or isolate them in a separate tooling
module if reproducible lint tooling becomes important. Before publishing, rerun
the audit from a clean checkout and confirm these modules are absent from the
runtime graph and not vendored.

These tooling findings should not by themselves decide the `dbrain` project
license if the modules remain external tools and are not linked, vendored, or
shipped as part of `dbrain` releases. They do matter for how clean the public
repository's dependency story looks.

### MPL and dual-license notes

MPL-2.0 or dual-license modules were found in the downloaded module cache, mostly
outside the `./cmd/dbrain` runtime graph. They are not automatically blockers,
but they require notice/source-obligation review if they are shipped in source or
binary distributions.

Examples to classify in a full notice pass:

- `github.com/cyphar/filepath-securejoin`
- HashiCorp modules such as `github.com/hashicorp/golang-lru/v2`
- `github.com/golang/freetype`, which has a dual FTL/GPL license

## Frontend Findings

`web/ui/package-lock.json` currently lists mostly MIT dependencies. Notable
non-MIT entries:

- `aria-query`: Apache-2.0
- `axobject-query`: Apache-2.0
- `dompurify`: `(MPL-2.0 OR Apache-2.0)`
- `picocolors`: ISC
- `source-map-js`: BSD-3-Clause

The frontend suspicious-license scan did not find GPL, AGPL, SSPL, Elastic
License, or Business Source License entries in the lockfile or installed package
metadata.

## Repository Artifacts

- Root project license: MIT in `LICENSE`.
- Root third-party notice file: `THIRD_PARTY_NOTICES.md`.
- `.gitignore` excludes `.gomodcache/`, `.gocache/`, `web/ui/node_modules/`,
  `/data/`, `/vault/`, `/tmp/`, and `/bin/`.
- `web/ui/dist` is intentionally tracked for Go embedding; include frontend
  dependency notices if release packages include or are built from the bundled
  web assets.
- `docs/apple-notes-ingestion.md` mentions a GPL-3.0 Apple Notes exporter as a
  reference point only. It is not vendored or used as a dependency.

## Recommended Cleanup Before Publishing

1. Run the license audit from a clean checkout, not a warmed developer module
   cache, and save the generated dependency inventory.
2. Keep `.gomodcache`, `.gocache`, `node_modules`, local data, vaults, and temp
   directories out of source and release archives.
3. Regenerate `THIRD_PARTY_NOTICES.md` before each release and include exact
   upstream license files for any third-party source or generated asset copied
   into release archives.
4. Decide whether lint tooling should stay as external prerequisites or move
   into a separate tooling module with its own license review.
5. Add a CI license scan that fails on GPL, AGPL, SSPL, Elastic License, and
   Business Source License in runtime or shipped frontend dependencies.

## Reproduction Commands

Useful commands from this pass:

```sh
go mod download all
go list -m -json all
go list -deps -test=false -f '{{with .Module}}{{.Path}} {{.Version}}{{end}}' ./cmd/dbrain
go mod why -m <module>
rg -n --ignore-case 'gnu general public license|affero|lesser general public|gpl|agpl|lgpl|mozilla public license|mpl-2|server side public|sspl|elastic license|business source|bsl' .gomodcache --glob 'LICENSE*' --glob 'COPYING*' --glob 'NOTICE*'
rg -n --ignore-case 'gpl|agpl|lgpl|sspl|elastic license|business source|bsl|mpl' web/ui/package-lock.json web/ui/node_modules/**/package.json
```
