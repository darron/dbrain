# Evidence Gates

## Contents

- Candidate states
- Provenance values
- Candidate ledger
- Attack-path gate
- Refutation gate
- Retest gate
- Coverage accounting

## Candidate States

Use exactly one current state per candidate:

1. **Hypothesis** — plausible abuse case, not yet fully source traced.
2. **Source traced** — current entry, transformations, expected guards, and sink are identified.
3. **Behavior observed** — a tool or isolated runtime reached the relevant path and observed acceptance or rejection.
4. **Impact demonstrated** — the security-relevant side effect or bounded proof of impact was observed.
5. **Refutation survived** — the falsification condition and strongest plausible dominating guards were tested or ruled out.
6. **Confirmed** — evidence supports a confirmed-vulnerability verdict with severity and confidence.
7. **Retested after remediation** — the fix rejects the exploit path and preserves the authorized path.

State progression is not automatic. A candidate may stop at any state with a probable, gap, defense-in-depth, or refuted verdict. Do not use **Confirmed** merely because source tracing is persuasive.

## Provenance Values

Label each material evidence item with one of:

```text
Model asserted
Context cited
Source traced
Tool observed
Runtime reproduced
Retest verified
```

- **Model asserted:** unverified reviewer reasoning; orientation only.
- **Context cited:** documentation, history, comments, or user-provided context.
- **Source traced:** current checkout path traced through callers, guards, and sinks.
- **Tool observed:** exact command or scanner output observed and manually interpreted.
- **Runtime reproduced:** isolated real-seam or runtime behavior reproduced with synthetic data.
- **Retest verified:** post-remediation exploit/rejection and authorized paths both rerun successfully.

Attach boundary identity to provenance, such as `Source traced — checkout 4269f53` or `Runtime reproduced — checkout-built binary, synthetic root`. Evidence from one boundary does not silently transfer to another.

## Candidate Ledger

For every candidate record:

- candidate ID, title, classification/verdict, severity, confidence, and current state;
- affected boundary and exact artifact provenance;
- prerequisites and attacker/input class;
- edge-by-edge attack path;
- evidence items with file/line or exact command/outcome and provenance;
- falsification statement and refutation attempt;
- observed rejection/acceptance and concrete impact;
- inspected and uninspected scope;
- required next evidence, remediation, regression test, and residual risk.

Missing evidence is a named gap, not an invitation to fill the record with inference.

## Attack-Path Gate

Write:

```text
attacker and prerequisite
  -> entry point
  -> parser/transformation
  -> expected guard
  -> sink or side effect
  -> concrete impact
```

Identify each crossing where data changes representation or authority: config precedence, decoding, normalization, path joining, redirect, database load, template/Markdown/HTML rendering, model prompt/output, argv construction, archive extraction, or generated packaging. Cite the actual caller and callee at each material edge.

## Refutation Gate

Before confirmation, write a falsification statement: “This candidate is false if …”. Then actively search for the strongest dominating guard at:

- route middleware and identity/capability checks;
- shared URL, redirect, host, path, symlink, and size validators;
- storage query predicates, schema constraints, and transactions;
- final sink wrappers, output encoding, and content security controls;
- build tags, platform conditions, caller restrictions, feature defaults, and generated artifacts;
- focused tests that exercise the actual rejection seam.

Record search terms, files inspected, tests run, and why the guard does or does not dominate every claimed path. “No guard was visible” is not a refutation attempt.

When refuted, retain a compact record: original hypothesis, falsification statement, dominating guard or corrected premise, evidence/provenance, inspected scope, and residual untested boundary.

## Retest Gate

For a remediation, require both:

1. **Exploit/rejection-path retest:** the original synthetic attack reaches the intended control and is rejected without the prohibited side effect.
2. **Authorized-path retest:** the legitimate operation still succeeds through the same shared seam and relevant sibling callers.

Use **Retest verified** only after both are observed from the final checkout. A passing new test without a verified pre-fix failure is not proof of the original defect.

## Coverage Accounting

Maintain explicit lists of:

- files and generated artifacts inspected;
- exact commands and outcomes;
- runtime seams reproduced;
- integrations mocked or faked;
- checks attempted but blocked;
- tools unavailable or intentionally skipped;
- included surfaces not inspected;
- excluded boundaries not claimed.

Distinguish “command proposed” from “command run,” “process alive” from “work advanced,” and “request returned success” from “security control exercised.”
