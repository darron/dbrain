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
contains. It does not run `task web-build` locally.

This keeps ordinary Go builds from requiring `npm`, but it means UI source
changes must be paired with refreshed dist assets before release.

The GitHub release workflow is stricter: it runs `npm ci` and `task web-build`
before every release `task build`, so published binaries embed web assets built
from the tagged UI source rather than relying only on the committed `dist`
directory.

Pull request CI also runs `task web-build` and then checks `web/ui/dist` for a
clean diff. If UI source changes were not committed with refreshed dist assets,
the PR fails before merge.

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
   task test-ci
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

For tag releases, GitHub Actions rebuilds `web/ui/dist` before compiling the Go
binary. The committed dist files still matter for local builds, review, and
source checkouts, but the release archives are protected from stale UI assets.
Pull request CI catches stale committed dist assets earlier by rebuilding and
requiring `git diff --exit-code -- web/ui/dist` to stay clean.

## Homebrew Tap Automation

The tag release workflow updates `darron/homebrew-tap` after the GitHub release
assets are published. `HOMEBREW_TAP_TOKEN` is required and must be able to
write contents to `darron/homebrew-tap`; a missing token fails the release
instead of silently leaving the tap stale.

The workflow downloads the built release archives, computes the four Homebrew
checksums for `darwin_amd64`, `darwin_arm64`, `linux_amd64`, and `linux_arm64`,
updates `Formula/dbrain.rb`, and pushes a commit named
`Update dbrain to <tag>`. A clean macOS arm64 runner then installs the updated
formula, runs `brew test`, checks that no external USearch dynamic library is
required, and exercises the installed binary's native semantic capability.

## Native semantic release boundary

The macOS arm64 archive is the first release artifact with native semantic ANN
support. It is built on the arm64 `macos-15` runner with CGO and the `usearch`
build tag. USearch v2.26.0 is compiled from checksum-pinned source into a static
archive with a macOS 12.0 deployment target. The release binary may dynamically
link Apple's system `libc++`, but it must not depend on an external
`libusearch_c.dylib`.

The Darwin amd64, Linux amd64/arm64, and Windows amd64 archives remain
`CGO_ENABLED=0`, carry no `usearch` build tag, and report semantic native state
as explicitly `unsupported`. Sync still succeeds on those artifacts and records
the `native_backend_unsupported` skip; lexical retrieval is unchanged.

Every release archive contains `THIRD_PARTY_NOTICES.md` and the upstream
USearch Apache-2.0 license as `LICENSE-USearch`. The stable and candidate
Homebrew formulae install both files under their package share directory.

## Homebrew Test Candidates

The **Homebrew Test Candidate** workflow at
`.github/workflows/homebrew-test.yaml` builds a committed SHA without publishing
it through the stable `dbrain` formula. The candidate commit must already be
pushed to and reachable in `darron/dbrain` on GitHub before dispatch. It does
not need to be on `main`; only the reviewed workflow ref must be `main`. Open
**Actions**, select **Homebrew Test Candidate**, choose the `main` branch, and
provide both required inputs:

- `sha`: the exact 40-character hexadecimal commit SHA to test, already
  reachable in the GitHub repository. A short SHA, branch name, tag, surrounding
  whitespace, local-only commit, or uncommitted work is rejected or cannot be
  checked out.
- `label`: an arbitrary human-readable label, such as `security-pass`. The
  trimmed label must be nonempty, at most 64 bytes, and contain no control
  characters or line breaks.

For example, first push the branch containing the candidate commit, then obtain
its full SHA and use that value as the workflow input:

```sh
git push origin HEAD
git rev-parse HEAD
```

The resulting workflow inputs look like:

```text
sha:   84b3cc07b1a4df8b2cdebe24f9982548fd60e805
label: security-pass
```

Only GitHub user `darron` may dispatch or rerun the reviewed workflow. The
workflow checks both the original actor and the rerun actor before checkout,
build, publication, or secret use. `darvisf` is a trusted write-capable bot
account: it cannot pass the normal dispatch/rerun gate, but its repository write
access means it is trusted not to replace the workflow or misuse repository
secrets. If another write-capable account is added and is not equally trusted,
this workflow is not a sufficient credential boundary.

Each successful run publishes a durable GitHub prerelease with an immutable,
run-specific tag and then advances one moving Homebrew formula,
`darron/tap/dbrain-test`. The prerelease remains available after the formula
moves to a newer candidate. The formula version is numeric
(`0.0.<run-number>.<run-attempt>`) so `brew upgrade` follows workflow order even
when labels do not sort naturally. The installed executable is still named
`dbrain`; there is no `dbrain-test` executable.

If dbrain is managed by launchd, restart it manually after every Homebrew
transition below. Homebrew relinking alone does not replace an already-running
dbrain process. The shell's `dbrain version` confirms which binary is linked,
but it does not confirm that an existing launchd process has restarted on that
binary.

Before testing, inspect the installed launchd plist. A plist that uses a
Cellar-specific or custom `--bin` path must be reinstalled with the same
configuration and label so its binary points at the normal Homebrew link
(`$(brew --prefix)/bin/dbrain`). Otherwise a restart can continue to use a
different keg, and version testing is not meaningful.

The restart blocks below show both forms; run exactly one of the two restart
commands shown after each transition. The command without `--label` restarts
the default `com.darron.dbrain` service. For a non-default service, replace
`com.example.dbrain-dev` with the exact label installed in its plist; the
example value is literal shell text, not angle-bracket placeholder syntax.

### Install a candidate

The stable and test formulae conflict because both provide the `dbrain`
executable. They may remain installed in separate Homebrew kegs, but only one
may be linked at a time:

```sh
brew unlink dbrain
brew install darron/tap/dbrain-test
dbrain version
# If dbrain is managed by launchd, run exactly one of these:
# Default service label (com.darron.dbrain):
dbrain launchd restart --check-full-disk-access=false
# Non-default service; replace com.example.dbrain-dev with its installed label:
dbrain launchd restart --label com.example.dbrain-dev --check-full-disk-access=false
```

Check `dbrain version` against the workflow summary. It reports both the exact
candidate commit and the test release identity derived from the label.

### Upgrade to the next candidate

After another candidate run advances the moving formula:

```sh
brew update
brew upgrade dbrain-test
dbrain version
# If dbrain is managed by launchd, run exactly one of these:
# Default service label (com.darron.dbrain):
dbrain launchd restart --check-full-disk-access=false
# Non-default service; replace com.example.dbrain-dev with its installed label:
dbrain launchd restart --label com.example.dbrain-dev --check-full-disk-access=false
```

### Return to stable

If the stable formula remains installed, switch links without deleting either
keg:

```sh
brew unlink dbrain-test
brew link dbrain
dbrain version
# If dbrain is managed by launchd, run exactly one of these:
# Default service label (com.darron.dbrain):
dbrain launchd restart --check-full-disk-access=false
# Non-default service; replace com.example.dbrain-dev with its installed label:
dbrain launchd restart --label com.example.dbrain-dev --check-full-disk-access=false
```

### Remove the test formula

Remove only the test formula's Homebrew-managed link and Cellar keg, then
restore the stable link:

```sh
brew uninstall dbrain-test
brew link dbrain
dbrain version
# If dbrain is managed by launchd, run exactly one of these:
# Default service label (com.darron.dbrain):
dbrain launchd restart --check-full-disk-access=false
# Non-default service; replace com.example.dbrain-dev with its installed label:
dbrain launchd restart --label com.example.dbrain-dev --check-full-disk-access=false
```

`brew unlink` and `brew uninstall dbrain-test` affect only Homebrew-managed
links and the selected formula's keg. They do **not** remove or modify dbrain
configuration, configured XDG roots, the SQLite database, vault, media, cache,
logs, temporary files, launchd plists or service state, external helper tools,
or downloaded models. In particular, they do not delete `~/.config/dbrain`,
`~/.local/share/dbrain`, or data stored under custom XDG paths. The test formula
has no uninstall, post-uninstall, cleanup, zap, migration, or runtime-data hook.

### Ordering and failure recovery

Stable and test tap-update jobs share the non-cancelling
`dbrain-homebrew-tap-update` queue. The test formula generator also refuses to
replace an existing formula with an equal or lower run/attempt version. This
keeps stable and candidate pushes from racing and prevents an older queued
candidate from moving `dbrain-test` backward. Such a rejected older run does
not change the current formula.

If the GitHub prerelease succeeds but the tap update reports failure, the
prerelease and its tag remain durable. First inspect both the failed run and the
current `Formula/dbrain-test.rb` in `darron/homebrew-tap`, including its version
and release URL. A push result can be ambiguous: if the tap already points to
the reported candidate, do not start another run solely because the push step
reported failure.

Repair the diagnosed cause. Use a pull request and merge the correction to
`main` for a workflow or trusted-helper defect. Repair the token, credential,
repository, or transient GitHub state directly when that is the cause. If the
tap did not advance, start a new **Homebrew Test Candidate** run after the
repair; the new run gets a new tag and higher formula version. Never move,
replace, or retarget the old candidate tag.

The stable `.github/workflows/release.yaml` remains tag-driven, but it publishes
only tags that match exact final versions of the form `vX.Y.Z`. Labels such as
`v1.2.3-rc.1`, `v1.2.3-test`, and other `v*` variants fail before checkout,
release publication, or tap mutation. A Homebrew test candidate is never
promoted automatically; stable release creation is a separate action.
