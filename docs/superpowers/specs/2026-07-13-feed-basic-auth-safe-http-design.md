# Feed Basic Auth Through Safe HTTP Design

**Date:** 2026-07-13

**Status:** Approved for implementation planning

**Repository boundary:** Isolated checkout behavior and synthetic tests only.
This change does not inspect or mutate the production feed database, real
credentials, Converge, or the installed Homebrew binary.

## Problem

Feed subscriptions intentionally support HTTP Basic Auth embedded in an HTTP or
HTTPS URL. Feed normalization preserves the URL userinfo, and CLI/JSON output
redacts its password. The security hardening merge moved feed requests onto the
shared `safehttp` client, which correctly rejects every request URL containing
userinfo before network access. Existing authenticated subscriptions therefore
enter backoff without contacting their feed server.

## Goals

- Preserve existing authenticated feed subscriptions without a database or
  configuration migration.
- Remove URL userinfo before the request reaches `safehttp`.
- Express the same credentials as an HTTP Basic Authorization header.
- Retain credentials only across redirects to the exact original origin: the
  same scheme, hostname, and effective port.
- Strip credentials before a cross-origin redirect while preserving all shared
  SSRF, DNS-resolution, private-network, scheme, and redirect-count controls.
- Keep request/result/error URLs free of userinfo after the fetch begins.
- Cover the authorized and rejection paths with synthetic regression tests.

## Non-Goals

- Relax `safehttp` userinfo rejection for other callers.
- Add new feed credential fields, secret references, schema migrations, or CLI
  flags.
- Change how feed subscription URLs are normalized, keyed, stored, or redacted.
- Probe the production Converge feed or use its real credentials.
- Change feed scheduling, backoff, parsing, or entry-import semantics.

## Chosen Architecture

`internal/feedimport.HTTPFetcher.Fetch` remains the only compatibility boundary
for credential-bearing feed URLs. `ResolvedURL` remains the navigation target.
The fetcher parses that target, captures any username and optional password,
copies the URL with `User` cleared, and constructs the request from the
sanitized URL. A successful fetch persists that sanitized final URL as the next
`ResolvedURL`; when a later fetch therefore finds no target userinfo, it checks
`NormalizedURL` and then `URL` for stored credentials and recovers them only
from a successfully parsed candidate with the same normalized HTTP origin as
the navigation target. Cross-origin resolved targets never inherit stored
credentials. When credentials are available, the fetcher calls
`Request.SetBasicAuth` so neither `safehttp` nor the transport sees URL
userinfo, and it does not rewrite the stored subscription URL fields.

For each fetch, clone the configured HTTP client and wrap its existing
`CheckRedirect` callback. The wrapper compares each redirect target with the
sanitized initial URL using normalized HTTP origins, including default ports.
It deletes `Authorization` before any redirect whose origin differs, then calls
the original callback so the shared safe-HTTP redirect policy remains
authoritative. Same-origin redirects retain Basic Auth. Redirect URLs that
themselves contain userinfo remain rejected by `safehttp`.

The fetch result records the sanitized initial URL and the sanitized final
request URL. Existing subscription storage continues to retain the original
credential-bearing URL for backward compatibility, while the existing output
redaction remains defense in depth.

## Alternatives Rejected

### Permit userinfo in `safehttp`

Rejected because it weakens a global security boundary for every outbound HTTP
caller and would make credential forwarding behavior implicit.

### Add separate credential storage now

Rejected for this regression fix because it requires schema, secret-resolution,
CLI, migration, and operator changes. It may be revisited as a separate design.

## Error Handling

Malformed targets continue to fail during request creation. A URL without
userinfo behaves exactly as before. Safe-HTTP policy failures remain terminal
fetch errors and retain their existing classification/backoff behavior. HTTP
`401` responses remain normal feed HTTP errors, making invalid credentials
distinguishable from local policy rejection. Structured and nested URL errors
are sanitized by a bounded single-pass scanner with a pre-grown output builder.
It scans each detected HTTP(S) or network-path authority once, removes through
the last userinfo `@`, preserves malformed-port diagnostics, and retains the
original error chain for `errors.Is` and `errors.As`.

## Verification

Add focused tests in `internal/feedimport/http_test.go` proving:

1. a credential-bearing URL reaches the original server with the expected
   Basic Authorization header and no request URL userinfo;
2. a same-origin redirect retains authorization;
3. a cross-origin redirect receives no authorization while the original server
   does;
4. result request/final URLs contain no userinfo;
5. redirects containing userinfo and unsafe/private destinations remain subject
   to the shared policy; and
6. a sanitized stored `ResolvedURL` recovers credentials only from a
   same-origin normalized/original URL on later polls, including percent-escaped
   credentials;
7. a cross-origin stored `ResolvedURL` never receives recovered credentials;
8. malformed URL sanitation has bounded allocation growth across many
   credential-bearing authority fragments; and
9. unauthenticated feed fetching remains unchanged.

After focused tests, run `task fmt`, `task lint`, `task test-ci`, and `task
build`, then request an independent security-focused review of the actual diff.

## Residual Risk

The original credential-bearing subscription URL remains in the local database
because removing it requires a separate credential-storage migration. Existing
CLI and JSON redaction must therefore remain in place.
