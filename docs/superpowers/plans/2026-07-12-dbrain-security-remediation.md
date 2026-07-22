# dbrain Verified Security Remediation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans`. Every production change below must follow `superpowers:test-driven-development`, and completion claims must follow `superpowers:verification-before-completion`.

**Goal:** Fix the two confirmed Medium vulnerabilities and the concrete, separately justified shared-seam hardening issues demonstrated during the checkout security review, without weakening dbrain's local-first workflows.

**Boundary:** `/Users/darron/src/dbrain`, branch `security-pass`, checkout/local synthetic tests only. Do not inspect production XDG state, installed binaries, launchd, live tsnet/Funnel, real Apple Notes/Safari/browser data, real archives/secrets, paid providers, or probe public product endpoints. Task 10 may perform read-only resolution of official upstream Git tags/releases solely to replace mutable workflow selectors; record the authoritative metadata used and make no other external requests.

**Evidence source:** `docs/security-reviews/2026-07-12-full-4269f53.md`. SEC-005 and SEC-011 are confirmed. SEC-001, SEC-002, SEC-003, SEC-007, SEC-008, SEC-009, and SEC-010 are separately justified hardening because their vulnerable seam or bounded impact was reproduced. SEC-014 is supply-chain defense in depth. SEC-004 is deferred until product limits are chosen; SEC-006, SEC-012, SEC-013, and SEC-015 remain gaps/refutations rather than remediation claims.

**Architecture:** Put policy at dominating sinks: `os.Root` for filesystem containment, `internal/store` for dbrain schema identity, `publicExternalURL` for share redaction, shared HTTP-origin and outbound-destination middleware, pre-node Funnel auth validation, a protocol-level MCP batch cap, and a stateful verifier only at the narrow service-auth consumer. Preserve authorized controls in every regression test.

**Standard gate:** After focused RED/GREEN work, run `task fmt`, `task lint`, `task test-ci`, and `task build`. Update `CHANGELOG.md`, relevant public docs, and the security ledger with retest evidence. Keep each finding independently reviewable and commit it separately when Git approval/signing is available.

---

## Task 1: SEC-005 — Reject Foreign SQLite Archives Before Replacement

**Files:**

- Create/modify `internal/store/schema_identity.go`
- Create `internal/store/schema_identity_test.go`
- Modify `internal/sqlitearchive/sqlite.go`
- Modify `internal/sqlitearchive/run_test.go`
- Modify `CHANGELOG.md`
- Modify `docs/security-reviews/2026-07-12-full-4269f53.md`

**Current interfaces:** `sqlitearchive.Restore(ctx, cfg, plan, opts)` calls `validateSQLite(ctx, restoredTemp)` before `moveExistingSQLiteFiles`; `store.currentSchemaVersion` and `store.schemaMigrations` own compatibility knowledge.

- [ ] **RED:** Add `TestRestoreRejectsForeignSQLiteBeforeMovingExistingFiles`. Create a real dbrain target plus DB/WAL/SHM sentinels and a gzip-compressed foreign SQLite containing only `test_values`. Assert a schema-identity error, unchanged target and sidecars, no `.pre-restore-*` files, and no install event. Run:

  ```sh
  go test ./internal/sqlitearchive -run '^TestRestoreRejectsForeignSQLiteBeforeMovingExistingFiles$' -count=1
  ```

  Expected RED: restore succeeds or moves the target.

- [ ] **RED controls:** Replace the old foreign-success fixture in `TestRestoreMovesExistingSQLiteFilesAndInstallsArchive` with a candidate created by `store.Open`. Add current-schema success, corrupt-input preservation, legacy-dbrain compatibility, future `user_version`, and migration-name-mismatch cases.

- [ ] **GREEN:** Add `store.ValidateRestorableDatabase(ctx, path) error`. Open read-only; require stable `items` and `sources` core-table/column fingerprints. For migration-aware databases, require `schema_migrations`, reject `PRAGMA user_version` above the compiled version, and require every recorded migration version/name to match either the current compiled name or an explicit accepted historical alias. Also accept the explicitly tested pre-2026-05-04 legacy fingerprint (`user_version=0`, no migration table, required core schema present) because SQLite archive/restore predates migration metadata; do not accept other metadata-free shapes. Inspect `internal/store/migrations_test.go` first, including the historical version-6 name case, and encode accepted aliases deliberately rather than weakening validation to arbitrary names. Do not call writable `store.Open` on the candidate. Keep `PRAGMA quick_check` in `sqlitearchive.validateSQLite`, then call the store validator before any backup or rename.

- [ ] **Marker decision:** Recheck migration versions on current branch, `main`, and recent local branches. If adding a fixed `PRAGMA application_id`, allocate the next unused append-only migration and retain the tested legacy fingerprint so existing dbrain archives remain restorable. Treat the marker as identity, not cryptographic authenticity.

- [ ] **GREEN verification:** Run:

  ```sh
  go test ./internal/store -run 'TestValidateRestorableDatabase|TestOpen.*Schema' -count=1
  go test ./internal/sqlitearchive -run '^TestRestore' -count=1
  ```

- [ ] **Retest:** Foreign-valid and future/mismatched candidates fail before replacement; current and accepted legacy dbrain archives restore; corrupt input preserves the current target.

**Commit:** `fix: validate dbrain schema before sqlite restore`

---

## Task 2: SEC-011 — Redact Sensitive URLs Before Public Sharing

**Files:**

- Modify `web/chat_shares.go`
- Modify `web/chat_shares_test.go`
- Modify `docs/research-harness.md`
- Modify `CHANGELOG.md`
- Modify the security ledger

**Dominating interface:** `publicExternalURL(raw string) (string, bool)` is used by source-key mapping, content URL extraction, metadata generation, legacy-row rendering, and anonymous page rendering.

- [ ] **RED:** Add `TestPublicExternalURLRejectsUserinfoAndStripsSensitiveQuery`. Reject all URL userinfo. Remove query keys case-insensitively for `token`, `key`, `signature`, `access_token`, `api_key`, `apikey`, `secret`, `sig`, and every `x-amz-*`; preserve ordinary query keys such as `section`, `x`, and `utm_source`.

- [ ] **RED end-to-end:** Add `TestPublicShareRedactsSensitiveURLs` that builds a turn, checks `PublicChatShareInput.OriginalURLs` and `MetadataJSON`, saves/reloads the share, renders anonymous `/share/{slug}`, and proves no userinfo or secret sentinel survives while an ordinary query URL, CSP, and raw-HTML omission controls remain. Add `TestPublicShareRedactsLegacySensitiveURLs` by inserting sensitive stored URLs directly and asserting render-time sanitization.

  ```sh
  go test ./web -run 'Test(PublicExternalURLRejectsUserinfoAndStripsSensitiveQuery|PublicShareRedactsSensitiveURLs|PublicShareRedactsLegacySensitiveURLs)$' -count=1
  ```

  Expected RED: sentinels remain in input/metadata/HTML.

- [ ] **GREEN:** In `publicExternalURL`, reject non-nil `u.User`; parse `u.Query()`, delete sensitive keys using lower-cased names/prefix matching, re-encode remaining values, and keep the existing scheme/host/private/protected-path/punctuation rules. Do not migrate local rows: the renderer must sanitize legacy rows at disclosure time.

- [ ] **Retest:** Run the focused tests and all `go test ./web -count=1`. Document that signed URLs become intentionally nonfunctional when shared and that public shares use a conservative query policy.

**Commit:** `fix: redact credentials from public share urls`

---

## Task 3: SEC-001 — Contain DB-Derived Vault Paths At Filesystem Sinks

**Files:**

- Create `internal/vaultfs/root.go` and `root_test.go`
- Modify `internal/mediaarchive/prune.go`, `s3.go`, and tests
- Modify `internal/vault/item.go`, `source.go`, `entity_write.go`, and tests
- Modify `internal/youtubeimport/cleanup.go` and tests
- Audit/convert sibling DB-path readers in `internal/xphotoocr`, `internal/xmediatranscribe`, `internal/itemcategorize`, `internal/mediadownload`, `internal/app/get.go`, `internal/mcpserver/get.go`, `internal/mcpserver/resource_readers.go`, `internal/ask/evidence.go`, and `internal/brainresearch/exact_tags.go`
- Modify `CHANGELOG.md` and the security ledger

- [ ] **RED primitive:** Test traversal, absolute names, escaping parent/leaf symlinks, contained relative symlinks, and authorized read/write/stat/remove behavior.

- [ ] **RED exploit:** Add `TestPruneLocalPathRejectsTraversalAndSymlinkEscape` with archive-ready DB rows and outside sentinels, plus contained prune control. Add equivalent upload-open, item/source/entity write, and YouTube DB-derived note deletion cases. Add MCP `get`/resource and research/evidence regressions proving a persisted escaping `NotePath` cannot disclose an outside sentinel while normal contained notes remain readable.

  ```sh
  go test ./internal/vaultfs -run '^TestRoot' -count=1
  go test ./internal/mediaarchive -run 'Test(PruneLocalPath|RunMarksArchived|RunDefersPrune)' -count=1
  go test ./internal/vault ./internal/youtubeimport -run 'Test.*(VaultEscape|Prune.*Note)' -count=1
  go test ./internal/mcpserver ./internal/ask ./internal/brainresearch -run 'Test.*(VaultEscape|NotePathContainment)' -count=1
  ```

  Expected RED: traversal/symlink cases reach files outside the vault.

- [ ] **GREEN:** Implement a small `internal/vaultfs.Root` wrapper around Go 1.26 `os.OpenRoot`/`os.Root`. Reject blank logical paths; convert stored slash form at the boundary; expose only required `Open`, `ReadFile`, `WriteFile`, `MkdirAll`, `Remove`, and `Stat` operations. Perform the security decision through `os.Root`, not `filepath.Rel`/`EvalSymlinks` prechecks.

- [ ] **GREEN callers:** Open the configured vault root and use root-relative operations immediately before every destructive, upload, and persisted-note write sink. Preserve content-hash-generated media paths and normal relative note paths. Return actionable errors naming the logical path, not file contents.

- [ ] **Sibling audit:** Search every `VaultDir` + persisted path join and either convert it or document why its path is generated rather than DB-controlled. This must include MCP `get` and resource readers plus ask/research exact-tag evidence, not only worker/CLI paths. Run package controls for OCR/transcription/categorization/media download, MCP, ask, and research.

- [ ] **Retest:** Repeat traversal and symlink exploits from clean `t.TempDir` fixtures at least twice; outside sentinels remain, contained operations succeed. Document residual same-user filesystem authority.

**Commit:** `fix: contain persisted paths within the vault`

---

## Task 4: SEC-002 — Contain Apple Notes Attachments Within The Notes Container

**Files:**

- Modify `internal/applenotes/attachment_copy.go`
- Modify `internal/applenotes/attachments.go`
- Modify `internal/applenotes/attachments_test.go`
- Modify `docs/apple-notes-ingestion.md`, `CHANGELOG.md`, and the ledger

- [ ] **RED:** Add `TestEnrichAttachmentFilesRejectsContainerEscapes`: absolute outside path, `../` traversal, escaping leaf symlink, and escaping parent symlink must be blocked without copying sentinel content. Add `TestEnrichAttachmentFilesExtractsContainedPaths` for both relative and absolute paths inside the synthetic Notes container.

  ```sh
  go test ./internal/applenotes -run '^TestEnrichAttachmentFiles(RejectsContainerEscapes|ExtractsContainedPaths)$' -count=1
  ```

  Expected RED: all outside variants are extracted.

- [ ] **GREEN:** Derive the allowed container as `filepath.Dir(sourceDBPath)`, open it with `os.OpenRoot`, translate absolute candidates to relative logical names, and perform `Stat`/`Open` through `os.Root`. Pass the opened root through one enrichment operation. Preserve regular-file and byte-plus-one limits. Classify rejection as `outside_notes_container`.

- [ ] **Compatibility:** Existing arbitrary-absolute-path tests must use a realistic synthetic Notes container. If a legitimate fixture proves another Notes-owned root is required, add an explicit allowlist entry and tests; never restore unrestricted absolute-path reads.

- [ ] **Retest:** Run all `go test ./internal/applenotes -count=1`; contained absolute and relative attachments still extract, outside and symlink escapes block terminally.

**Commit:** `fix: contain apple notes attachment reads`

---

## Task 5: SEC-007 — Fail Closed Before Public Funnel Startup

**Files:**

- Modify `internal/remote/server.go` and `server_test.go`
- Modify `web/auth_config.go` and tests to expose an explicit public-auth requirement; reuse `internal/mcpserver.AuthEnabled`
- Modify `README.md`, `docs/tsnet-transport.md`, `docs/web-route-capabilities.md`, `internal/app/env_docs.go`, `CHANGELOG.md`, and the ledger

- [ ] **RED:** Add pre-node tests for Funnel web without enabled auth, Funnel MCP without enabled bearer auth, and combined surfaces with only one auth system. Assert failure before state-dir, lock, node creation, or `Up`. Add authenticated web/MCP and non-Funnel unauthenticated controls.

  ```sh
  go test ./internal/remote ./web -run 'Test(ServeWithDeps.*Funnel.*Auth|ValidatePublicAuthConfig)' -count=1
  ```

- [ ] **GREEN:** Add `web.RequirePublicAuthConfig(ctx, cfg) error` (or an equivalent exported predicate plus validator) whose contract rejects `auth.enabled=false` and otherwise applies the existing public HTTPS/OAuth validation. Add `validateFunnelSurfaceAuth(ctx, cfg, opts)` and call it immediately after `opts.Validate()`. Funnel+Web uses the new fail-closed web contract; Funnel+MCP requires `mcp.auth.enabled=true`; combined exposure requires both. Preserve the existing permissive `ValidatePublicAuthConfig` contract if other callers depend on disabled-auth returning nil. Do not require a current token row at preflight; enabled bearer middleware fails closed without a valid token.

- [ ] **Retest:** Auth-disabled Funnel never reaches the fake node; private-tailnet and localhost auth-disabled modes remain supported. CLI errors explain how to enable the appropriate auth or disable Funnel/surface.

**Commit:** `fix: require authentication for funnel surfaces`

---

## Task 6: SEC-008 — Apply One Origin Guard To Local And Remote Mutations

**Files:**

- Create `internal/httpsecurity/origin.go` and tests
- Modify `web/server.go` and web integration tests
- Modify `internal/remote/handler.go` and retain its tests
- Modify `README.md`, `docs/web-route-capabilities.md`, `CHANGELOG.md`, and the ledger

- [ ] **RED:** Reproduce a hostile-Origin `text/plain` JSON `/api/tag` request through the local `web.NewHandler` and assert 403 plus unchanged store state. Add same-origin, missing-Origin CLI, browser-extension `/api/links`, and extension `/api/tag` controls.

  ```sh
  go test ./internal/httpsecurity ./internal/remote ./web -run 'Test.*Origin' -count=1
  ```

- [ ] **GREEN:** Move the existing remote policy into `httpsecurity.OriginGuard(next)`: GET/HEAD allowed; missing Origin allowed; exact request origin allowed; Chrome/Safari extension origin allowed only for `/api/links`; all other state-changing requests rejected before dispatch. Wrap the full local mux and the remote combined handler. Do not trust forwarded headers by default and do not add unrelated global content-type policy.

- [ ] **Retest:** The hostile request cannot mutate; existing remote tests remain unchanged and pass; no-Origin CLI clients and authorized extension link adds still work.

**Commit:** `fix: guard local web mutations by origin`

---

## Task 7: SEC-010 — Bound MCP JSON-RPC Batches Before Dispatch

**Files:**

- Modify `internal/mcpserver/protocol.go`
- Modify `internal/mcpserver/server_test.go` and stdio tests
- Modify MCP docs/README, `CHANGELOG.md`, and the ledger

- [ ] **Policy:** Set the fixed initial protocol limit to `maxBatchRequests = 16`. This is a documented hardening choice, not a measured operational threshold.

- [ ] **RED:** Exactly 16 ping requests yield 16 responses. A 17-entry batch returns one `-32600` error and dispatches none. Mixed request/notification batches at the limit preserve response filtering. Cover HTTP and stdio.

  ```sh
  go test ./internal/mcpserver -run 'Test.*Batch' -count=1
  ```

- [ ] **GREEN:** After JSON unmarshal and empty-batch rejection in `Server.processPayload`, reject over-limit batches before the loop, with a stable error telling clients to split requests. Do not partially execute.

- [ ] **Retest:** Existing two-entry batches work; max+1 does no per-request work on HTTP or stdio.

**Commit:** `fix: cap mcp json-rpc batches`

---

## Task 8: SEC-009 — Reject In-Process Service-Auth Replays

**Files:**

- Modify `internal/serviceauth/serviceauth.go` and tests
- Modify `web/auth.go` and auth tests
- Verify `internal/app/doctor.go` tests
- Modify `CHANGELOG.md` and the ledger

- [ ] **RED:** Add `ReplayVerifier` tests: first use succeeds; identical second use fails; distinct nonce succeeds; stale/wrong-route failures do not consume entries; concurrent identical calls produce exactly one success. Add web integration where the first doctor probe succeeds and replay returns 401.

  ```sh
  go test ./internal/serviceauth ./web ./internal/app -run 'Test(ServiceAuth|FetchServiceFullDiskAccess)' -count=1
  ```

- [ ] **GREEN:** Keep pure `VerifyHeader`. Add a mutex-protected `ReplayVerifier.VerifyAndConsume` that validates signature/time first, removes expired entries, and atomically records valid nonces until the skew window expires. Store one verifier on `authManager` and use it only for the narrow doctor service route.

- [ ] **Retest:** The CLI generates a fresh header per request; exact-header retries fail. Document residual replay across process restart/multiple processes as accepted for this Low, local diagnostic authority.

**Commit:** `fix: reject service auth nonce replays`

---

## Task 9: SEC-003 — Enforce Public-Destination Policy On Imported URLs

**Files:**

- Create `internal/safehttp/` and tests
- Refactor `internal/feedimport/http.go` to use it
- Modify source-enrichment clients in `reader.go`, `sucuri.go`, `wordpress.go`, `wayback.go`, `makerworld.go`, and `failure_network.go`
- Modify `internal/mediadownload/run.go`, `download.go`, tests, and status mapping
- Verify `internal/xapi` callers
- Modify network/config docs, `CHANGELOG.md`, and the ledger

- [ ] **RED shared policy:** Direct loopback, redirect to private, mixed public/private DNS, IPv4-mapped private, CGNAT/Tailscale, link-local/metadata, userinfo, non-HTTP schemes, and environment-proxy bypass are rejected. Exact configured private origin and same-origin redirect controls succeed. Use injected resolver/dial functions; no external network.

- [ ] **GREEN primitive:** Implement `safehttp.NewClient(Policy)` with dial-time resolution and IP classification, validated numeric-IP dialing while preserving Host/TLS ServerName, redirect revalidation, explicit redirect cap, and proxy disabled for imported URLs. Exact configured origins may authorize private destinations; never share that authorization with imported URL clients.

- [ ] **Client roles:** Public-only clients for imported source/media/MakerWorld/redirect URLs; exact-origin clients for configured reader and Wayback availability services; public policy again for fallback/source-derived URLs. Preserve feed import's explicit `AllowPrivateNetwork` test/compatibility escape hatch.

- [ ] **RED integrations:** Source and media direct/redirect loopback rejected; configured local reader/Wayback controls succeed; WordPress/Wayback response-derived URLs cannot pivot private; media policy rejection creates no file and becomes terminal `blocked`; public synthetic downloads succeed.

  ```sh
  go test ./internal/safehttp ./internal/feedimport ./internal/sourceenrich ./internal/mediadownload ./internal/xapi -count=1
  ```

- [ ] **Retest:** Repeat direct and redirect cases, including IPv6 and exact-origin configured services. Confirm policy errors do not hot-loop as retryable work.

**Commit:** `fix: block private destinations for imported urls`

---

## Task 10: SEC-014 — Pin Release And CI Tooling Immutably

**Files:**

- Modify `.github/workflows/pr-ci.yaml` and `.github/workflows/release.yaml`
- Add a focused workflow-policy test/script using the repo's existing test conventions
- Modify `CHANGELOG.md` and the ledger

- [ ] **Resolve, do not invent:** From official upstream Git refs/releases, resolve reviewed 40-hex commits for `actions/setup-node@v4` and `go-task/setup-task@v2`, and choose an exact `golangci-lint/v2` release. Record upstream tag and commit in comments/evidence. Keep exact setup-task tool version rather than `3.x` if supported.

- [ ] **RED:** Add a check that fails for non-local `uses:` selectors not ending in 40 hex characters and `go install ...@latest`. Run it against current workflows and capture failure.

- [ ] **GREEN:** Replace mutable selectors in PR/release workflows with reviewed SHAs and version comments; replace `golangci-lint@latest` with the exact version. Preserve already pinned actions and permissions.

- [ ] **Verification:** Parse both workflows, run the policy test, and run the relevant local Task lint/test/build commands. Do not claim a compromised action or artifact; classify this as defense in depth.

**Commit:** `ci: pin security-sensitive workflow tooling`

---

## Task 11: Final Retest, Documentation, And Adversarial Review

**Files:**

- Modify `docs/security-reviews/2026-07-12-full-4269f53.md`
- Finalize `CHANGELOG.md` and affected docs
- Update `.superpowers/sdd/progress.md`

- [x] Run every finding-specific exploit regression and authorized control from clean fixtures, with `-count=2` for SEC-001, SEC-005, SEC-011, Origin, Funnel, MCP batch, service replay, and outbound-destination tests.
- [x] Run:

  ```sh
  task fmt
  task lint
  task test-ci
  task build
  git diff --check
  git status --short
  ```

- [x] Re-run the skill validator and ensure the skill still describes the implemented controls accurately.
- [x] Dispatch independent read-only reviewers for: local/filesystem/restore, web/auth/MCP/public shares, and outbound-network/supply chain. Treat reviewer claims as hypotheses and correct only verified issues.
- [x] Update each ledger record to **Retested** only when the original exploit is rejected and the authorized path still passes. Keep residual risks and excluded production/runtime coverage explicit.
- [x] Inspect all diffs for unrelated changes, temporary harnesses, secrets, generated drift, and placeholders.

**Commit:** `docs: finalize dbrain security remediation evidence`
