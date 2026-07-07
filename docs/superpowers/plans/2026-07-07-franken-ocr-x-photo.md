# Franken OCR X Photo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `franken_ocr` / `focr` as a local X photo OCR provider that can be quality-tested before reuse for Apple Notes attachments.

**Architecture:** Keep the existing `internal/xphotoocr` provider boundary and add a `focr` provider prefix that shells out to the installed `focr` CLI. Store stdout text only as OCR evidence, keep stderr as diagnostic error context, and preserve existing OpenRouter, Ollama, and Tesseract behavior. Update preflight, compare tooling, CLI/docs strings, and changelog so the provider is discoverable without making it the default.

**Tech Stack:** Go, Cobra CLI, local `focr` binary, existing dbrain `xphotoocr` pipeline, existing Go tests.

---

### Task 1: Model Parsing And Provider Execution

**Files:**
- Modify: `internal/xphotoocr/types.go`
- Modify: `internal/xphotoocr/options.go`
- Modify: `internal/xphotoocr/providers.go`
- Modify: `internal/xphotoocr/photo.go`
- Modify: `internal/xphotoocr/util.go`
- Test: `internal/xphotoocr/run_test.go`

- [ ] **Step 1: Write failing provider tests**

Add tests proving `focr` writes OCR state, records `franken_ocr` provenance, ignores stderr on success, and errors when the local binary returns no text.

- [ ] **Step 2: Verify tests fail before implementation**

Run: `go test ./internal/xphotoocr -run 'TestRunFrankenOCR|TestOCRPhotoWithModelRejectsUnsupported' -count=1`

Expected: fail because `focr` model parsing/provider support does not exist.

- [ ] **Step 3: Implement minimal provider support**

Add model parsing for `focr`, `focr/<model>`, `focr:<model>`, `franken_ocr`, `franken_ocr/<model>`, and `franken_ocr:<model>`. Add an `Options.FOCRBinary` field defaulting to `focr`, execute `focr ocr <image>` with timeout, and store tool/version/model provenance as `franken_ocr`, `franken_ocr-v1`, and `focr/<resolved-model>`.

- [ ] **Step 4: Verify provider tests pass**

Run: `go test ./internal/xphotoocr -run 'TestRunFrankenOCR|TestOCRPhotoWithModelRejectsUnsupported' -count=1`

Expected: pass.

### Task 2: Compare Tool And Preflight Coverage

**Files:**
- Modify: `internal/xphotoocr/compare_types.go`
- Modify: `internal/xphotoocr/compare_run.go`
- Modify: `cmd/devtools/ocr_model_compare/main.go`
- Modify: `internal/app/preflight.go`
- Modify: `internal/app/preflight_test.go`
- Test: `internal/xphotoocr/compare_test.go`
- Test: `internal/app/preflight_test.go`

- [ ] **Step 1: Write failing compare/preflight tests**

Add tests proving `ocr_model_compare` can run a `focr` model through a fake binary and that OCR preflight does not require OpenRouter credentials for `focr/default`.

- [ ] **Step 2: Verify tests fail before implementation**

Run: `go test ./internal/xphotoocr ./internal/app -run 'TestCompareRunsFrankenOCR|TestPreflightOCRSkipsFrankenOCRModel' -count=1`

Expected: fail because compare options do not pass a `focr` binary and preflight has no explicit `focr` coverage.

- [ ] **Step 3: Implement compare/preflight wiring**

Add `FOCRBinary` to compare options and CLI flag `--focr-binary`, pass it into `Options`, and add a preflight regression for `focr`.

- [ ] **Step 4: Verify tests pass**

Run: `go test ./internal/xphotoocr ./internal/app -run 'TestCompareRunsFrankenOCR|TestPreflightOCRSkipsFrankenOCRModel' -count=1`

Expected: pass.

### Task 3: CLI Docs, Changelog, And Gates

**Files:**
- Modify: `internal/app/ocr.go`
- Modify: `internal/app/sync_flags.go`
- Modify: `internal/app/env_docs.go`
- Modify: `README.md`
- Modify: `config.yaml.sample`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Update user-facing strings and docs**

Document `focr/default` or `franken_ocr/default` as a local X photo OCR model option, keep OpenRouter/Gemini as default, and mention that `focr pull` must install models before use.

- [ ] **Step 2: Run focused tests**

Run: `go test ./internal/xphotoocr ./internal/app -count=1`

Expected: pass.

- [ ] **Step 3: Run standard gates**

Run:
- `task fmt`
- `task lint`
- `task test-ci`

Expected: all pass.
