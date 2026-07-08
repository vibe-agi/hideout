# Research: Release Hardening And Compatibility Matrix

<!-- markdownlint-disable MD013 -->

## Decision 1: Go Core Owns The Support Matrix

**Decision**: Implement the authoritative matrix in `internal/releasecompat` and
render it to CLI/doctor/docs tests. Markdown and shell must verify against this
matrix rather than own parallel truth tables.

**Rationale**: Previous slices repeatedly found stale text after implementation.
A Go-owned table lets tests compare docs, schemas, doctor, and version output
against one source.

**Alternatives considered**:

- Markdown-only matrix: easy to read but hard to enforce.
- JSON-only checked into docs: machine-readable but awkward for CLI/doctor
  without generated code. A Go source with JSON rendering is simpler here.

## Decision 2: Local-Fast And Release-Candidate Are Separate Modes

**Decision**: `scripts/test-release-readiness.sh --local-fast` may produce a
readiness artifact, but it must mark `releaseReady=false` and
`evidenceClass=local-fast`. `--release-candidate` requires real Gate 2/Gate 3
evidence paths or a successful existing release-dogfood run.

**Rationale**: DNS and HostFS privacy claims depend on real Lima gates. Local
schema/test success is valuable but cannot replace those gates.

**Alternatives considered**:

- Always run real gates: correct for release candidates, too slow/fragile for
  ordinary local development.
- Treat skipped real gates as warnings: would repeat the 004 failure mode.

## Decision 3: Readiness Artifact Is A Redacted Summary, Not Raw Logs

**Decision**: Store command names, statuses, matrix version, platform, commit,
gate evidence references, and non-claims. Do not inline full logs or local
secret-bearing environment values.

**Rationale**: 005 established export/share redaction. Release evidence should
be safe to include in dogfood artifacts and issue reports.

## Decision 4: Doctor Gets Matrix Findings Without Changing Exit Semantics

**Decision**: Add a `support-matrix` doctor finding. Supported/first-class
entries pass, degraded entries warn, unsupported entries error only when the
operator requested that unsupported backend/platform as an asserted run target.

**Rationale**: Doctor is diagnostic. Native is expected to remain degraded; that
must be visible without turning every local dev check into a hard failure.

## Decision 5: Version Output Carries A Compact Matrix Summary

**Decision**: Extend `hideout version` with matrix schema/version and current
host support summary. Keep existing version lines stable.

**Rationale**: Existing scripts grep exact version lines. Appending support
lines gives operators the matrix without breaking package smoke checks.

## Decision 6: Compatibility Fixtures Are Focused Smoke Fixtures

**Decision**: For every schema/ABI family in FR-011, keep at least one accepted
fixture and one unknown-version fixture in tests. The fixture test may be local
and synthetic as long as it exercises the same schema/version parser or validator
used by production.

**Rationale**: 016 is alpha hardening, not long-term migration tooling. The key
contract is fail-closed on unknown major versions before mutation.

## Decision 7: Existing Release Dogfood Remains The Heavy Gate

**Decision**: Do not replace `scripts/test-release-dogfood.sh`. The new
readiness wrapper records matrix support and redacted readiness while composing
existing package, doctor, Gate0, and release-dogfood paths.

**Rationale**: The dogfood script already builds a release-like artifact and
invokes `scripts/test-phase1.sh --release-candidate`; duplicating it would
create another source of drift.

## Decision 8: Documentation Drift Check Is Pattern-Based And Conservative

**Decision**: Gate0 smoke checks required positive statements and required
non-claims. It does not try to parse every historical spec trail; historical
directories remain allowed when they clearly describe superseded decisions.

**Rationale**: A brittle global grep would punish legitimate history. The guard
should catch current shipped docs and README/status overclaims.

## Decision 9: Support Levels Are Closed Vocabulary

**Decision**: Support levels are exactly `first-class`, `supported`,
`degraded`, `unsupported`, and `gate-required`.

**Rationale**: Closed vocabulary keeps doctor/docs/readiness comparison stable
and makes unsupported/degraded states explicit.

## Decision 10: Linux Is Supported But Narrower Than macOS

**Decision**: Matrix labels Linux amd64/aarch64 as supported with narrower smoke
coverage. It must not inherit macOS real-gate wording unless a Linux gate is
actually provided.

**Rationale**: The user can provide Linux environments, but the current primary
dev host and most real-gate evidence are macOS/Lima oriented.
