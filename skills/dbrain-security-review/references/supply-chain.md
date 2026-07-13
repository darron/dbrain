# Supply-Chain Review

## Inventory

Record every dependency and publication boundary with source, version selector, integrity mechanism, permissions, secret/token authority, generated output, producer, consumer, and verification evidence. Scanner or advisory presence is a candidate until current reachability and impact are traced.

## GitHub Actions

- Require immutable commit-SHA pinning for third-party actions where practical; distinguish mutable tags from digests/SHAs and review updater policy.
- Review top-level and job-level `permissions`, `GITHUB_TOKEN`, OIDC `id-token`, environments, approvals, artifacts/caches, `pull_request` versus `pull_request_target`, workflow reuse, forks, untrusted checkout/code execution, script interpolation, and secret availability.
- Identify runtime installs (`@latest`, curl-pipe-shell, package-manager downloads, browser/tool downloads, rule-pack updates), whether integrity/version is fixed, and whether network is required during a trusted job.
- Treat a mutable action or broad permission as defense in depth or a candidate until a realistic event, attacker-controlled input, token authority, and impact path is demonstrated.

## Dependencies And Embedded UI

- Review `go.mod`/`go.sum`, npm manifests and lockfiles, replace directives, indirect dependencies, build tags, native helpers, vendored/generated code, and licenses/notices.
- For advisories, verify the vulnerable package/version, symbol or behavior reachability, platform/build conditions, attacker input, compensating guard, and fixed-version feasibility.
- Verify npm lock integrity, lifecycle scripts, registry/source fields, git/URL dependencies, bundled dependencies, and reproducibility without rewriting the lockfile.
- Compare embedded `web/ui/dist` with a clean authorized frontend build when dependencies are already available. Record source commit/tool versions and whether stale or untracked output can ship.

## Helpers And Models

- Resolve helper binary/model source, version or digest, checksum/signature, download/update path, cache permissions, execution identity, and model/template provenance.
- Review optional OCR, transcription, browser, ffmpeg, whisper, tesseract, Ollama, and provider tooling without installing or contacting providers.
- Distinguish a user-selected local executable/model from code downloaded or mutated by automated setup.

## Releases And Homebrew

- Review trigger/ref selection, clean checkout, build matrix, CGO/platform differences, archive contents, checksums, signing, attestations, provenance, SBOM generation/publication, artifact upload/download pinning, and release-token permissions.
- Absence of SBOMs, attestations, or signing is not automatically a vulnerability; record the threat and provenance gap.
- Trace Homebrew tap mutation, formula URL/checksum/version update, commit/push authority, cross-repository token boundary, branch protection assumptions, and whether untrusted release input reaches commits or shell.
- Verify archives exclude secrets, private data, dev config, debug artifacts, browser profiles, and unintended generated files.

## Browser Extensions

- Review Chrome/Safari source and generated-package ownership, manifests, permissions/host permissions, externally connectable/origin rules, content-script/page boundaries, message validation, native/local service calls, update URLs, versioning, deterministic packaging, signing/notarization/conversion boundaries, and publication credentials.
- Trace extension-supplied URLs/content as untrusted at dbrain mutation routes; an extension origin string alone is not identity proof.

## Third-Party Notices

- Compare shipped Go, npm, helper, model, font/icon, and extension contents with third-party notices and license obligations.
- Record missing or stale notices separately from exploitable security findings unless they create a demonstrated distribution/integrity impact.

## Skill Publishing

- Review repository-owned skill source, generated metadata, marketplace/cachebuster or packaging flow, destination ownership, action pinning, artifact integrity, and published-versus-checkout drift.
- For OIDC publishing, verify workflow/event restrictions, `id-token: write` scope, subject/audience binding, environment protections, package identity, trusted publisher configuration, and that pull-request code cannot mint or misuse publication credentials.
- Never inspect real tokens or publish during a checkout-only review.

## Evidence Record

For each lane record exact files, workflow/event, permissions, immutable/mutable selectors, secrets or OIDC authority by name only, generated artifacts, commands run, tool versions, network limitations, reachability triage, verdict, and residual uninspected publication state.
