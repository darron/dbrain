# Secrets, Configuration, And Logging

## Runtime Precedence

Trace effective values through defaults, config file, environment, CLI flags, explicit config selection, generated service/plist environment, and command-specific overrides. Resolve the target first: checkout source, repo-local runtime, installed artifact, and production state are distinct evidence boundaries.

Document which precedence layer supplied a sensitive or authority-bearing value without recording the value. Verify that a lower-trust layer cannot unexpectedly override listener, auth, endpoint, filesystem-root, helper-path, archive, or provider controls.

## Secret References And Escape Hatches

- Trace plaintext values, environment references, `op://` references, `keychain://` references, resolution timing, error handling, caching, and redaction.
- Identify explicit command escape hatches that accept plaintext for bootstrap, testing, migration, or repair. Verify they are opt-in, visibly diagnosed, excluded from routine output, and do not silently persist.
- Treat secret names, vault/account/item paths, endpoint usernames, and credential presence as potentially sensitive metadata.
- Do not invoke real secret managers or inspect real secret values in a checkout-only review.

## Subprocesses

- Prefer direct argv execution without a shell. Trace executable selection, argument boundaries, working directory, inherited/scrubbed environment, stdin, stdout/stderr, timeouts, cancellation, output limits, temp files, and exit errors.
- Inspect direct secret CLI flags because they leak through process listings, logs, shell history, crash reports, or diagnostics; prefer environment, stdin, file descriptor, or provider-native secure mechanisms when supported.
- Verify hostile filenames, URLs, prompts, transcript/OCR text, helper output, and config values remain data rather than executable arguments or shell syntax.
- Bound captured output and redact command/error rendering without hiding actionable non-secret diagnostics.

## Files And Modes

Review creation and replacement modes plus parent-directory modes for config, databases/WAL/SHM, vault notes, raw extracts, transcripts, OCR, media, OKF, traces, metrics, logs, cache, temp files, backups, archive staging, tsnet state, OAuth/session material, and generated plists. Account for umask, existing-file mode preservation, atomic rename, symlinks, and cleanup.

A `0644` file is not automatically exploitable: record the local-attacker model and effective ancestor permissions. Conversely, a restrictive file mode does not compensate for a world-searchable parent, unsafe symlink resolution, public rendering, or backup leakage.

## Logging And Export Surfaces

Inspect:

- access logs: URLs, query strings, headers, cookies, bearer/service tokens, client identity, public-share identifiers, and remote addresses;
- metrics: source keys, titles, paths, provider/model identifiers, failures, sizes, timings, and labels with unbounded cardinality;
- research traces: prompts, retrieval packs, raw extracts, citations, model responses, tokens, paths, and retention/visibility;
- errors/debug output: config/env dumps, resolved secret values, argv, subprocess output, HTTP bodies, database rows, filesystem paths, and stack traces;
- public shares/media: protected/internal URLs, raw Markdown/HTML, active content, private source labels, archived objects, traces, and authentication decisions;
- OKF and rendered Markdown: raw versus derived evidence, canonical URLs, embedded active content, private paths/identifiers, and output-root containment;
- backups/archives: database, WAL/SHM, config, logs, traces, temp files, credentials, manifests, object metadata, and retention/deletion.

## Redaction Contract

- Redact at the serialization/logging seam, not only at a UI layer.
- Cover headers, URLs, nested JSON/maps, config structs, argv, errors, provider responses, and multiline output.
- Preserve enough stable context to diagnose the boundary without emitting secret values or private corpus text.
- Test representative secret schemes and encodings with synthetic sentinels; assert absence from stdout, stderr, logs, metrics, traces, public shares, OKF, and backups as applicable.
- Record false-negative limits: redaction does not replace data minimization, restrictive modes, authentication, retention limits, or output containment.

## Evidence Questions

For each secret/config/logging candidate record the source and precedence layer, reader identity, propagation path, output/sink, effective permissions or audience, retention, redaction/containment guard, synthetic rejection/leak test, authorized behavior, and any production-only fact left unverified.
