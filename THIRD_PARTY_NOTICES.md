# Third-Party Notices

Date: 2026-05-04

This file lists third-party dependencies used by `dbrain`. It is an engineering
notice inventory for open-source publication and release review, not legal
advice.

## Scope

Included:

- Go runtime modules reachable from `./cmd/dbrain`.
- Frontend packages recorded in `web/ui/package-lock.json`.

Not included:

- The `dbrain` project license itself. See `LICENSE`.
- Go modules that appear only in historical checksums, local module caches,
  linting tools, or test-only tooling outside the compiled `./cmd/dbrain`
  runtime graph.
- System tools installed outside this repository, such as `yt-dlp`, `ffmpeg`,
  `ollama`, `tesseract`, `golangci-lint`, Homebrew packages, browser binaries,
  or model files.

If a source or binary release bundles third-party source files, npm packages,
Go module source trees, or generated web assets, include the corresponding
upstream license files from those distributions alongside this notice file.

## Native Darwin arm64 Dependency

The `usearch`-tagged Darwin arm64 release contains a statically linked USearch
`v2.26.0` from upstream commit
`cc23bbaf21ef52313c5a495adbc40cbd733cdcfb`. USearch is licensed under
Apache-2.0; its exact upstream license is shipped as `LICENSE-USearch` in
release archives and installed package-share notices. The repository source is
`third_party/usearch/LICENSE`. The archive is linked into the executable as
`libusearch_c.a`; the Apple system C++ runtime remains dynamically linked.

## Go Runtime Dependencies

Generated from:

```sh
GOCACHE=.gocache GOMODCACHE=.gomodcache go list -deps -test=false -f '{{with .Module}}{{.Path}} {{.Version}}{{end}}' ./cmd/dbrain
```

| Go module | Version | License |
|---|---:|---|
| `al.essio.dev/pkg/shellescape` | `v1.5.1` | MIT |
| `filippo.io/edwards25519` | `v1.2.0` | BSD-3-Clause |
| `github.com/aws/aws-sdk-go-v2` | `v1.41.6` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream` | `v1.7.9` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/config` | `v1.29.5` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/credentials` | `v1.19.15` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/feature/ec2/imds` | `v1.18.22` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/internal/configsources` | `v1.4.22` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/internal/endpoints/v2` | `v2.7.22` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/internal/ini` | `v1.8.2` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/internal/v4a` | `v1.4.23` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding` | `v1.13.8` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/service/internal/checksum` | `v1.9.14` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/service/internal/presigned-url` | `v1.13.22` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/service/internal/s3shared` | `v1.19.22` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/service/s3` | `v1.100.0` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/service/sso` | `v1.30.16` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/service/ssooidc` | `v1.35.20` | Apache-2.0 |
| `github.com/aws/aws-sdk-go-v2/service/sts` | `v1.42.0` | Apache-2.0 |
| `github.com/aws/smithy-go` | `v1.25.0` | Apache-2.0 |
| `github.com/aymanbagabas/go-osc52/v2` | `v2.0.1` | MIT |
| `github.com/charmbracelet/bubbles` | `v1.0.0` | MIT |
| `github.com/charmbracelet/bubbletea` | `v1.3.10` | MIT |
| `github.com/charmbracelet/colorprofile` | `v0.4.1` | MIT |
| `github.com/charmbracelet/harmonica` | `v0.2.0` | MIT |
| `github.com/charmbracelet/lipgloss` | `v1.1.0` | MIT |
| `github.com/charmbracelet/x/ansi` | `v0.11.6` | MIT |
| `github.com/charmbracelet/x/cellbuf` | `v0.0.15` | MIT |
| `github.com/charmbracelet/x/term` | `v0.2.2` | MIT |
| `github.com/clipperhouse/displaywidth` | `v0.9.0` | MIT |
| `github.com/clipperhouse/stringish` | `v0.1.1` | MIT |
| `github.com/clipperhouse/uax29/v2` | `v2.5.0` | MIT |
| `github.com/coder/websocket` | `v1.8.12` | ISC |
| `github.com/creachadair/msync` | `v0.7.1` | BSD-3-Clause |
| `github.com/dustin/go-humanize` | `v1.0.1` | MIT |
| `github.com/fxamacker/cbor/v2` | `v2.9.0` | MIT |
| `github.com/gaissmai/bart` | `v0.26.1` | MIT |
| `github.com/go-ini/ini` | `v1.67.0` | Apache-2.0 |
| `github.com/go-json-experiment/json` | `v0.0.0-20250813024750-ebf49471dced` | BSD-3-Clause |
| `github.com/golang/groupcache` | `v0.0.0-20241129210726-2c02b8208cf8` | Apache-2.0 |
| `github.com/google/btree` | `v1.1.3` | Apache-2.0 |
| `github.com/google/uuid` | `v1.6.0` | BSD-3-Clause |
| `github.com/hdevalence/ed25519consensus` | `v0.2.0` | BSD-3-Clause |
| `github.com/huin/goupnp` | `v1.3.0` | BSD-2-Clause |
| `github.com/klauspost/compress` | `v1.18.2` | Apache-2.0 |
| `github.com/ledongthuc/pdf` | `v0.0.0-20250511090121-5959a4027728` | BSD-3-Clause |
| `github.com/lucasb-eyer/go-colorful` | `v1.3.0` | MIT |
| `github.com/mattn/go-isatty` | `v0.0.20` | MIT |
| `github.com/mattn/go-runewidth` | `v0.0.19` | MIT |
| `github.com/mitchellh/go-ps` | `v1.0.0` | MIT |
| `github.com/muesli/ansi` | `v0.0.0-20230316100256-276c6243b2f6` | MIT |
| `github.com/muesli/cancelreader` | `v0.2.2` | MIT |
| `github.com/muesli/termenv` | `v0.16.0` | MIT |
| `github.com/ncruces/go-strftime` | `v1.0.0` | MIT |
| `github.com/pires/go-proxyproto` | `v0.8.1` | Apache-2.0 |
| `github.com/prometheus-community/pro-bing` | `v0.4.0` | MIT |
| `github.com/remyoudompheng/bigfft` | `v0.0.0-20230129092748-24d4a6f8daec` | BSD-3-Clause |
| `github.com/rivo/uniseg` | `v0.4.7` | MIT |
| `github.com/spf13/cobra` | `v1.10.2` | Apache-2.0 |
| `github.com/spf13/pflag` | `v1.0.9` | BSD-3-Clause |
| `github.com/steipete/sweetcookie` | `v0.0.0-20260102214724-68ec5a0bced4` | MIT |
| `github.com/tailscale/hujson` | `v0.0.0-20221223112325-20486734a56a` | BSD-3-Clause |
| `github.com/tailscale/peercred` | `v0.0.0-20250107143737-35a0c7bd7edc` | BSD-3-Clause |
| `github.com/tailscale/web-client-prebuilt` | `v0.0.0-20250124233751-d4cd19a26976` | BSD-3-Clause |
| `github.com/tailscale/wireguard-go` | `v0.0.0-20250716170648-1d0488a3d7da` | MIT |
| `github.com/x448/float16` | `v0.8.4` | MIT |
| `github.com/xo/terminfo` | `v0.0.0-20220910002029-abceb7e1c41e` | MIT |
| `github.com/zalando/go-keyring` | `v0.2.6` | MIT |
| `go4.org/mem` | `v0.0.0-20240501181205-ae6ca9944745` | Apache-2.0 |
| `go4.org/netipx` | `v0.0.0-20231129151722-fdeea329fbba` | BSD-3-Clause |
| `golang.org/x/crypto` | `v0.46.0` | BSD-3-Clause |
| `golang.org/x/exp` | `v0.0.0-20250620022241-b7579e27df2b` | BSD-3-Clause |
| `golang.org/x/net` | `v0.48.0` | BSD-3-Clause |
| `golang.org/x/oauth2` | `v0.33.0` | BSD-3-Clause |
| `golang.org/x/sync` | `v0.20.0` | BSD-3-Clause |
| `golang.org/x/sys` | `v0.42.0` | BSD-3-Clause |
| `golang.org/x/term` | `v0.38.0` | BSD-3-Clause |
| `golang.org/x/text` | `v0.32.0` | BSD-3-Clause |
| `golang.org/x/time` | `v0.12.0` | BSD-3-Clause |
| `gopkg.in/yaml.v3` | `v3.0.1` | Apache-2.0 |
| `gvisor.dev/gvisor` | `v0.0.0-20260224225140-573d5e7127a8` | Apache-2.0 |
| `modernc.org/libc` | `v1.72.0` | BSD-3-Clause |
| `modernc.org/mathutil` | `v1.7.1` | BSD-3-Clause |
| `modernc.org/memory` | `v1.11.0` | BSD-3-Clause |
| `modernc.org/sqlite` | `v1.49.1` | BSD-3-Clause |
| `tailscale.com` | `v1.96.5` | BSD-3-Clause |

## Frontend Dependencies

Generated from `web/ui/package-lock.json`.

| Package | Version | License |
|---|---:|---|
| `@esbuild/*` platform packages | `0.27.7` | MIT |
| `@jridgewell/gen-mapping` | `0.3.13` | MIT |
| `@jridgewell/remapping` | `2.3.5` | MIT |
| `@jridgewell/resolve-uri` | `3.1.2` | MIT |
| `@jridgewell/sourcemap-codec` | `1.5.5` | MIT |
| `@jridgewell/trace-mapping` | `0.3.31` | MIT |
| `@rollup/rollup-*` platform packages | `4.60.2` | MIT |
| `@sveltejs/acorn-typescript` | `1.0.9` | MIT |
| `@sveltejs/vite-plugin-svelte` | `6.2.4` | MIT |
| `@sveltejs/vite-plugin-svelte-inspector` | `5.0.2` | MIT |
| `@types/estree` | `1.0.8` | MIT |
| `@types/trusted-types` | `2.0.7` | MIT |
| `acorn` | `8.16.0` | MIT |
| `aria-query` | `5.3.1` | Apache-2.0 |
| `axobject-query` | `4.1.0` | Apache-2.0 |
| `clsx` | `2.1.1` | MIT |
| `deepmerge` | `4.3.1` | MIT |
| `devalue` | `5.7.1` | MIT |
| `dompurify` | `3.4.1` | MPL-2.0 OR Apache-2.0 |
| `esbuild` | `0.27.7` | MIT |
| `esm-env` | `1.2.2` | MIT |
| `esrap` | `2.2.5` | MIT |
| `fdir` | `6.5.0` | MIT |
| `fsevents` | `2.3.3` | MIT |
| `is-reference` | `3.0.3` | MIT |
| `locate-character` | `3.0.0` | MIT |
| `magic-string` | `0.30.21` | MIT |
| `marked` | `18.0.2` | MIT |
| `nanoid` | `3.3.11` | MIT |
| `obug` | `2.1.1` | MIT |
| `picocolors` | `1.1.1` | ISC |
| `picomatch` | `4.0.4` | MIT |
| `postcss` | `8.5.10` | MIT |
| `rollup` | `4.60.2` | MIT |
| `source-map-js` | `1.2.1` | BSD-3-Clause |
| `svelte` | `5.55.4` | MIT |
| `tinyglobby` | `0.2.16` | MIT |
| `vite` | `7.3.2` | MIT |
| `vitefu` | `1.1.3` | MIT |
| `zimmerframe` | `1.1.4` | MIT |

## Release Checks

Before publishing a release archive:

- Regenerate this inventory from a clean checkout.
- Confirm no GPL, AGPL, SSPL, Elastic License, or Business Source License
  dependency appears in the runtime or shipped frontend dependency set.
- Include exact upstream license files for any third-party source, package
  directory, generated frontend asset, or vendored dependency copied into the
  release archive.
