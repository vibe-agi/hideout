# Implementation Plan: Documentation Truth Gate

<!-- markdownlint-disable MD013 -->

**Branch**: `025-documentation-truth-gate` | **Date**: 2026-07-09 |
**Spec**: [spec.md](spec.md)

## Summary

025 adds a local documentation truth gate over existing docs and
product-hardening evidence. It creates a claim-boundary registry, a curated
command fixture, a docs truth smoke, 025 product-hardening proof ids, and Gate 0
wiring. It does not alter runtime behavior.

## Technical Context

**Language/Version**: Go 1.24+ repository code, POSIX shell, jq, Markdown docs.

**Primary Dependencies**: Existing docs, README files, `internal/productevidence`,
`scripts/test-gate0.sh`, and current 021-024 proof ids.

**Storage**: Docs truth reports, command check reports, product-hardening
evidence manifest, temporary stores for safe command execution.

**Testing**: `scripts/test-doc-truth-smoke.sh`, markdownlint, productevidence
tests, command fixture checks, and Gate 0.

**Target Platform**: macOS/Linux local operator machines. No Lima required.

**Constraints**: No new product authority, no automatic doc rewriting, no real
gate substitution, no `.tmp` scanning as current truth.

## Constitution Check

- **Privacy Boundary**: PASS. The feature only checks docs/evidence truth.
- **Typed Authority**: PASS. Command checks are parse-only or temp-store safe.
- **Workspace And Policy**: PASS. No workspace or policy change.
- **Generality And Provider Scope**: PASS. No provider behavior added.
- **Evidence And Redaction**: PASS. Product-hardening evidence is required.
- **Backend And Distribution**: PASS. Local docs truth does not replace real
  gates.
- **Gates**: PASS. Gate 0 gets docs truth smoke.
- **Status And Docs**: PASS. This is the status/docs alignment feature.

## Project Structure

```text
docs/
├── claim-boundaries.md
└── command-examples.json

scripts/
├── test-doc-truth-smoke.sh
└── test-gate0.sh

internal/productevidence/
├── claims.go
├── aggregate.go
└── docs_truth.go
```

## Complexity Tracking

No constitution violations. One docs smoke script and two docs registry files.

## Phase 0 Research Summary

See [research.md](research.md).

## Phase 1 Design Summary

See [data-model.md](data-model.md),
[contracts/docs-truth-gate.md](contracts/docs-truth-gate.md), and
[quickstart.md](quickstart.md).

## Post-Design Constitution Check

All checks remain PASS.
