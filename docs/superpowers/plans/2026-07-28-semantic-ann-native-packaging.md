# Native Semantic ANN Release Packaging Plan

**Goal:** Ship a statically linked USearch `2.26.0` backend in the macOS arm64
release and Homebrew candidate artifacts while every other release target
remains CGO-free and reports an explicit successful unsupported capability.

**Source pin:** Fetch
`https://proxy.golang.org/github.com/unum-cloud/usearch/@v/v2.26.0+incompatible.zip`,
require raw ZIP SHA-256
`54f280b044757a391a6905b0850ad96789b6b2ce7c20ec9511b3b4628e27b0bd`,
and record upstream commit
`cc23bbaf21ef52313c5a495adbc40cbd733cdcfb`.

## Task 1: Test and implement reproducible native preparation

**Files:**

- Create `internal/releaseautomation/usearch_packaging_test.go`
- Modify `Taskfile.yml`
- Create `internal/semanticindex/usearch_link_darwin.go`
- Add the upstream USearch license and update `THIRD_PARTY_NOTICES.md`

Start with a failing policy test for the exact version, commit, archive URL,
checksum, deployment target, CMake static target, tagged build, and Darwin C++
runtime link. Add Taskfile targets that download to a temporary file, verify
the checksum before extraction, build `usearch_static_c`, stage it as
`libusearch_c.a` with `usearch.h`, and set the macOS deployment target to 12.0
for both CMake and CGO.

The tagged Darwin link shim supplies `-lc++`. Normal builds do not compile it.

## Task 2: Prove the native artifact

Add Taskfile targets for tagged race tests, build, and artifact verification.
Verification must prove:

- Mach-O arm64;
- `CGO_ENABLED=1` and `-tags=usearch`;
- minimum macOS version 12.0;
- no external USearch dylib dependency;
- only expected system dylibs/frameworks;
- `semantic status --json` reports `supported_ready`, `usearch`, `2.26.0`.

Package the upstream license and third-party notice with the binary.

## Task 3: Split stable release build lanes

**Files:**

- Modify `.github/workflows/release.yaml`
- Modify `internal/releaseautomation/workflows_test.go`

Keep Darwin amd64, Linux amd64/arm64, and Windows amd64 in the Ubuntu
`CGO_ENABLED=0` matrix. Build Darwin arm64 in a dedicated pinned `macos-15`
job using the native Taskfile targets. Require publishing to depend on both
lanes. Preserve the canonical archive filename.

Make the Homebrew tap update and a clean macOS arm64 `brew install` plus
`brew test` smoke part of the successful release path. The installed binary
must repeat the no-external-USearch and capability checks.

## Task 4: Split Homebrew candidate lanes and strengthen formula tests

**Files:**

- Modify `.github/workflows/homebrew-test.yaml`
- Modify `internal/releaseautomation/formula.go`
- Modify `internal/releaseautomation/formula_test.go`
- Modify `internal/releaseautomation/workflows_test.go`

Preserve exact-candidate and owner/default-branch guards. Add the dedicated
native Darwin arm64 build, publish dependency, and clean installed-formula
smoke. The generated formula test must require native capability on macOS
arm64 and explicit unsupported capability elsewhere.

## Task 5: Document and verify the release boundary

**Files:**

- Modify `CHANGELOG.md`
- Modify `docs/release-build.md`
- Modify `docs/semantic-retrieval.md`
- Modify the accepted automatic-sync design implementation status

Document that Homebrew macOS arm64 contains statically linked USearch, still
uses the system C++ runtime, and has no external USearch dylib. Other artifacts
remain CGO-free and explicitly skip native semantic refresh successfully.

Run:

```text
task fmt
task lint
task test-ci
task build
task usearch-static-darwin-arm64
task test-usearch-darwin-arm64
task build-usearch-darwin-arm64
task verify-usearch-darwin-arm64
```

Also cross-compile the CGO-free release targets and inspect their Go build
metadata to prove they contain neither CGO nor the `usearch` tag. Finally run a
clean local archive/install smoke and an independent review before pushing the
stacked draft PR.
