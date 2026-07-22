# Active Native Root Loader Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Open a verified active immutable root into tag-gated native segment readers without enabling semantic retrieval.

**Architecture:** A read-only loader receives cache/database/profile/generation facts from the store readiness path, reopens the root and every referenced content-addressed segment, verifies backend/provenance/dimensions, then imports each payload into a native USearch index. It returns a closeable collection of segment-local ordinal maps; candidate selection, SQLite validation, exact rerank, hybrid fusion, and serving remain separate layers.

**Constraints:** `usearch && cgo` only; no SQLite mutation, cache mutation, CLI, query routing, or implicit fallback. Any manifest/checksum/backend/import failure rejects the whole loader and remains lexical-only at later admission.

## Tasks

1. Add failing tagged tests for root/segment open, backend/dimension mismatch, tampered payload rejection, deterministic close, and no default-build dependency.
2. Implement closeable native root loader that imports every verified root segment and retains each manifest's ordinal-to-member mapping.
3. Add only an internal evaluator caller; defer semantic candidate fusion and serving until exact rerank and readiness contracts are implemented.
4. Run tagged adapter tests, CGO-free tests, full CI-like suite, and record the non-serving boundary.
