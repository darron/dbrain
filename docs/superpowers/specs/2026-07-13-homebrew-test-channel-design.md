# Homebrew Test Channel Design

**Date:** 2026-07-13

**Status:** Approved for implementation planning

**Repository boundary:** Checkout source and GitHub/Homebrew release automation
only. This design does not deploy a release, mutate the tap, install a formula,
or touch production dbrain configuration or data.

## Problem

The current release workflow is triggered by every Git tag matching `v*`. A
successful run publishes normal GitHub release assets and rewrites
`darron/homebrew-tap/Formula/dbrain.rb`. Consequently, a tag can immediately
change what every stable Homebrew user receives through `brew upgrade dbrain`.

The maintainer needs a separate path that builds an exact commit through the
real release and Homebrew packaging process, installs the normal `dbrain`
executable locally, and leaves the stable formula untouched.

## Goals

- Manually dispatch a GitHub Actions workflow with an exact commit SHA and an
  arbitrary human-readable test label.
- Run release-equivalent verification and build the existing platform matrix
  from that exact commit.
- Publish durable GitHub prerelease assets that Homebrew can download without
  authenticated Actions-artifact access.
- Maintain one moving `dbrain-test` formula in `darron/homebrew-tap`.
- Install the candidate as the normal `dbrain` command so launchd, shell paths,
  configuration lookup, and runtime behavior can be tested through the same
  executable path as stable.
- Keep `Formula/dbrain.rb` unchanged during test-channel publication.
- Make switching back to stable explicit, fast, and non-destructive.
- Ensure Homebrew uninstall or unlink operations cannot remove dbrain's XDG
  configuration, database, vault, cache, logs, launchd state, or other runtime
  data.
- Prevent prerelease-looking `v*` tags from entering the stable publication
  path.

## Non-Goals

- Automatically promote a test candidate to stable.
- Create a formula for every historical label or SHA.
- Run two linked `dbrain` binaries simultaneously under the same command name.
- Delete old GitHub prereleases automatically.
- Build from uncommitted local changes or a short SHA.
- Modify production dbrain state, launchd services, configuration, or data.
- Provide cryptographic provenance, signing, SBOMs, or artifact attestations in
  this change.
- Provide credential-level isolation from the production tap repository. A
  separate staging tap remains the stronger future option if that boundary is
  required.

## Existing Release Boundary

The current `.github/workflows/release.yaml`:

1. triggers for `v*` tags;
2. rebuilds the embedded web UI;
3. runs lint, tests, and a build;
4. builds archives for macOS amd64/arm64, Linux amd64/arm64, and Windows amd64;
5. publishes a normal GitHub release; and
6. updates `Formula/dbrain.rb` in `darron/homebrew-tap`.

The stable formula currently installs only the packaged `dbrain` binary into
its Homebrew keg. It has no uninstall, post-uninstall, or cleanup hook. The test
formula must retain that property.

## Chosen Architecture

### Separate manual workflow

Add `.github/workflows/homebrew-test.yaml` with `workflow_dispatch` inputs:

- `sha`: required, exactly 40 hexadecimal characters;
- `label`: required, free-form text whose trimmed value becomes the display
  label, at most 64 bytes after trimming, with control characters and line
  breaks rejected.

The workflow must run from the repository's default `main` branch. The
requested candidate SHA is an input, not the workflow ref. This keeps the
secret-bearing publication logic anchored to reviewed default-branch workflow
code while allowing the read-only build jobs to inspect and execute the exact
candidate commit.

### Candidate identity

The trimmed display label is retained in the GitHub prerelease title and notes.
The validated SHA is normalized to lowercase. A safe label slug is derived for
tags and filenames by:

1. converting ASCII letters to lowercase;
2. replacing runs outside `[a-z0-9._-]` with `-`;
3. trimming leading and trailing separators; and
4. limiting the result to 32 characters.

If normalization produces an empty slug, use `test`.

Each run has three separate identities:

- **Formula version:** `0.0.<GITHUB_RUN_NUMBER>.<GITHUB_RUN_ATTEMPT>`. This is
  numeric and monotonically increasing for predictable `brew upgrade
  dbrain-test` behavior, independent of arbitrary label ordering.
- **Binary release version:** `test/<slug>@<12-character-sha>`, injected through
  `DBRAIN_RELEASE_VERSION` before `task build`.
- **GitHub prerelease tag:**
  `homebrew-test-<run-number>-<run-attempt>-<slug>-<12-character-sha>`.

The full 40-character SHA remains visible in `dbrain version` through Go build
metadata and is recorded in the prerelease notes and generated formula test.

### Trusted metadata helper

Keep validation, normalization, candidate identity generation, and formula
rendering in repository-owned scripts rather than embedding untestable shell and
Ruby fragments throughout workflow YAML.

The scripts execute from the reviewed workflow ref, not the requested candidate
checkout, in any job that holds write authority. Build jobs may use the computed
metadata but may not provide executable scripts or formula text to later
secret-bearing jobs.

### Job and credential separation

The workflow is divided into four authority levels:

1. **Prepare:** validate the workflow ref, full SHA, and label; derive immutable
   candidate metadata. Repository permission is read-only.
2. **Verify and build:** check out the exact requested SHA with
   `persist-credentials: false`, rebuild the UI, run release-equivalent gates,
   and build archives. These jobs receive no tap token and have only
   `contents: read` permission.
3. **Publish prerelease:** download archives, compute checksums, create the
   immutable candidate tag at the requested SHA, and publish a GitHub release
   marked `prerelease`. This job does not check out or execute candidate code.
4. **Update tap:** check out only the trusted workflow source and
   `darron/homebrew-tap`, render `Formula/dbrain-test.rb`, update the narrow
   prerelease audit exception when required, verify the path allowlist, and push
   the tap commit. It never executes the candidate binary or candidate scripts.

The test workflow must fail when `HOMEBREW_TAP_TOKEN` is missing. A successful
binary build without an installable formula does not satisfy this workflow's
contract.

### Durable prerelease assets

GitHub Actions artifacts are an internal transport between jobs only. The
Homebrew formula points at assets attached to the candidate's GitHub prerelease.
The release is explicitly marked prerelease and uses a tag unique to the run,
attempt, label slug, and requested SHA.

Archives retain the existing target matrix and package contents. Filenames
include the immutable candidate tag so formula URLs cannot collide with stable
release assets.

### Moving Homebrew formula

The tap gains `Formula/dbrain-test.rb`, defining `class DbrainTest < Formula`.
It must:

- use the monotonically increasing formula version;
- point to the four macOS/Linux candidate archives and exact checksums;
- declare `conflicts_with "dbrain", because: "both install the dbrain binary"`;
- install the packaged executable as `dbrain`, not `dbrain-test`;
- test both the requested full SHA and the injected test release version using
  `dbrain version`; and
- contain no uninstall, post-uninstall, cleanup, zap, migration, or runtime-data
  hook.

The workflow may update only:

- `Formula/dbrain-test.rb`; and
- `audit_exceptions/github_prerelease_allowlist.json` when Homebrew requires a
  version-specific prerelease exception.

Before committing, it must assert that `Formula/dbrain.rb` is byte-for-byte
unchanged and that `git diff --name-only` contains no path outside that exact
allowlist. The commit message is:

```text
Update dbrain test to <label> (<short-sha>)
```

### Stable release guard

Keep the stable workflow's `v*` event filter for GitHub compatibility, but add a
first-step validation requiring the complete tag to match:

```text
^v[0-9]+\.[0-9]+\.[0-9]+$
```

Tags such as `v0.6.0-rc.1`, `v0.6.0-test`, or `version-1` must fail before
checkout, build, release publication, or tap mutation. Stable release semantics
otherwise remain unchanged.

The stable and test tap-update jobs share a non-cancelling concurrency group,
`dbrain-homebrew-tap-update`, so simultaneous stable and candidate runs cannot
race while pushing different tap files.

## Operator Workflow

### Publish a candidate

From GitHub Actions, run **Homebrew Test Candidate** from `main` and provide:

```text
sha:   84b3cc07b1a4df8b2cdebe24f9982548fd60e805
label: security-pass
```

The workflow summary must report:

- trimmed display label;
- normalized slug;
- full SHA;
- binary release version;
- formula version;
- prerelease URL; and
- exact install, upgrade, rollback, and removal commands.

### Switch from stable to test

```sh
brew unlink dbrain
brew install darron/tap/dbrain-test
dbrain version
```

Both formulae may remain installed in separate Cellar kegs. Only one can link
the `dbrain` executable at a time.

### Upgrade to the next candidate

```sh
brew update
brew upgrade dbrain-test
dbrain version
```

### Switch back to stable

```sh
brew unlink dbrain-test
brew link dbrain
dbrain version
```

### Remove the test formula

```sh
brew uninstall dbrain-test
brew link dbrain
```

Unlink and uninstall operate only on Homebrew-managed links and the test
formula's Cellar keg. They do not remove:

- `~/.config/dbrain`;
- `~/.local/share/dbrain`;
- any configured XDG config/data/cache roots;
- the SQLite database, vault, media, logs, cache, or temporary files;
- launchd plists or service state; or
- externally installed helper tools or models.

No formula hook or workflow step may add cleanup behavior for those paths.

## Failure Handling

The workflow fails without publication or tap mutation when:

- it is dispatched from a ref other than `main`;
- the SHA is not exactly 40 hexadecimal characters;
- the requested commit cannot be checked out from the repository;
- the label is empty, longer than 64 bytes, or contains control characters;
- UI rebuild, lint, tests, or build fail;
- any expected archive is missing;
- the candidate tag already exists at a different commit;
- release creation or upload fails;
- `HOMEBREW_TAP_TOKEN` is absent;
- formula generation cannot account for all four Homebrew targets;
- the stable formula changes;
- a tap path outside the allowlist changes; or
- the tap push loses a race even after concurrency serialization.

Failures after GitHub prerelease publication but before the tap push leave a
visible prerelease that is not selected by the moving formula. Rerunning creates
a distinct run-attempt identity and may publish a new candidate; it must never
retarget an existing candidate tag to another SHA.

## Security Properties

- Candidate code executes without the tap secret and without repository write
  permission.
- Secret-bearing jobs do not execute candidate binaries or scripts.
- Candidate tags are immutable with respect to their requested SHA.
- Stable and test formulas are separate files with an explicit diff allowlist.
- Stable publication accepts only final numeric semantic-version tags.
- Workflow inputs reach shell commands through environment variables or
  positional arguments, not direct expression interpolation into executable
  shell source.
- External actions use immutable commit SHAs in the new workflow.
- The test workflow does not automatically trigger production deployment,
  launchd restart, stable formula mutation, or stable release promotion.

This design prevents accidental stable promotion and limits candidate-code
authority. It does not prevent a malicious or compromised tap credential from
editing the stable tap repository; a separate staging tap and token would be
required for that stronger boundary.

## Test Strategy

Implementation follows test-first development. The local tests must prove:

1. full 40-character SHA validation accepts uppercase/lowercase hexadecimal and
   rejects short, malformed, empty, and whitespace-padded values;
2. labels normalize deterministically, preserve their trimmed display value,
   reject control characters and oversize input, and use `test` for an empty
   normalized slug;
3. run number and attempt produce a monotonically ordered numeric formula
   version;
4. candidate tags and binary release versions contain the expected slug and SHA
   prefix without shell metacharacter execution;
5. formula generation includes all four Homebrew targets, exact URLs,
   checksums, version, conflict declaration, normal `dbrain` installation, and
   SHA/release-version tests;
6. generated formulae contain no uninstall or cleanup hooks and no dbrain XDG
   paths;
7. the tap diff allowlist rejects `Formula/dbrain.rb` and every unrelated path;
8. the manual workflow has only `workflow_dispatch`, exact input declarations,
   read-only build permissions, persisted-credential disabling, prerelease
   publication, a non-executing secret boundary, and the shared tap concurrency
   group;
9. the stable workflow rejects non-final `v*` tags before publication and uses
   the same concurrency group; and
10. documented switch, upgrade, rollback, and uninstall commands match the
    generated formula name and executable behavior.

After focused tests pass, run the repository gates:

```sh
task fmt
task lint
task test-ci
task build
```

Also parse both workflow YAML files, run `actionlint` when installed, run the
release-automation test target, render a formula from synthetic archives, and
run `git diff --check`.

No production tag, GitHub prerelease, tap commit, or Homebrew installation is
authorized as part of local implementation verification. An actual manual
workflow dispatch is a separate external publication action requiring explicit
approval after the code is merged.

## Documentation And Changelog

Update `docs/release-build.md` with:

- the stable-versus-test channel distinction;
- dispatch inputs and identity scheme;
- install, upgrade, rollback, and removal commands;
- the guarantee that Homebrew removal does not touch dbrain runtime data; and
- failure recovery when prerelease publication succeeds but tap update fails.

Add a concise `CHANGELOG.md` entry for the new maintainer-facing test release
channel and stable-tag guard.

## Acceptance Criteria

The change is complete when:

- a maintainer can identify an exact committed SHA and arbitrary label as a
  candidate without creating a stable tag;
- the workflow definition builds that SHA through release-equivalent gates;
- published candidate assets are durable GitHub prerelease assets;
- the moving formula installs the normal `dbrain` executable and reports the
  requested SHA plus test label identity;
- stable `Formula/dbrain.rb` is protected by tested path and byte-diff checks;
- stable release tags are limited to exact `vX.Y.Z` forms;
- switching and rollback commands are documented and do not delete runtime
  state; and
- focused automation tests and all standard repository gates pass.
