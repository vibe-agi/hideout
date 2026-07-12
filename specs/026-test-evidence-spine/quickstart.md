# Quickstart: Test And Evidence Spine

<!-- markdownlint-disable MD013 -->

## Goal

Validate that 026 makes proof requirements centrally registered and evaluable
without breaking existing 021-025 evidence.

## Local Validation

Run product evidence tests:

```bash
go test ./internal/productevidence
```

Expected:

- registry validation passes;
- missing proof fixtures report feature ID and proof ID;
- stale-by-commit and stale-by-package are evaluator results;
- missing artifact and digest mismatch fixtures fail evaluation;
- existing 021-025 manifest fixtures still pass.

## Registry View

Generate or inspect the Go-owned registry JSON view through the implementation
entrypoint chosen in tasks.

Expected:

- schema is `hideout.proof-registry/v1`;
- 021-025 required proof IDs appear exactly once;
- rows include `featureId`, `proofId`, `layer`, `requiredFor`,
  `freshnessPolicy`, `claimIds`, and `artifactPolicy`;
- output is deterministic across repeated runs.

## Docs Truth Compatibility

Run the docs truth smoke:

```bash
scripts/test-doc-truth-smoke.sh --out /tmp/hideout-doc-truth-026
```

Expected:

- docs truth consumes registry JSON or a Go helper derived from it;
- no separate shell list is used for 021-025 required proof IDs;
- 025 evidence remains schema-valid and redacted.

## Release Readiness Boundary

Run the release readiness smoke used by Gate 0:

```bash
scripts/test-release-hardening-smoke.sh
```

Expected:

- product-hardening evidence is reported as local/supporting evidence;
- real Gate 2/Gate 3 requirements remain distinct;
- local proof does not set release-ready by itself.

## Full Gate

Run Gate 0:

```bash
scripts/test-gate0.sh
```

Expected:

- 026 registry/evaluator validation runs;
- existing 021-025 product-hardening lanes remain compatible;
- Gate 0 exits 0.
