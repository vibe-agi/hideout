# Research: Error Codes And Recovery Hints

<!-- markdownlint-disable MD013 -->

## Decision 1: Add A Small `internal/recovery` Registry

**Decision**: Public recovery codes live in a Go-owned registry with code,
subsystem, severity, reason, hint, next actions, and docs refs.

**Rationale**: Doctor findings already support structured `NextActions` and
`EvidenceRefs` (`internal/doctor/report.go:63-73`) but have no stable code.
Docs truth must validate against Go-owned truth rather than another shell list.

**Rejected Alternatives**:

- Put codes only in docs: repeats drift risk.
- Generate codes from prose: unstable and untestable.

## Decision 2: Doctor Is The Primary Structured Surface

**Decision**: Add optional code/reason/hint fields to doctor findings and
schema first.

**Rationale**: Doctor reports are already schema-validated and redacted. Human
rendering is centralized in `internal/doctor/render.go:16-40`, so parity is
testable.

## Decision 3: CLI Wiring Is Selected, Not Universal

**Decision**: Wire selected package, init, and release-readiness cases to codes.
Do not migrate every internal error.

**Rationale**: Some errors originate in guest shell/bootstrap paths or generic
Go errors. Forcing all of them into public codes would create unstable API.

## Decision 4: Hints Are Guidance, Not Mutation

**Decision**: Recovery hints may contain copyable commands or docs refs, but
they must not trigger repair, approval, or retry.

**Rationale**: Hideout's fail-closed behavior is a product property. Codes
explain it; they do not change it.

## Decision 5: Docs Truth Validates References

**Decision**: User-facing docs may reference public codes. Docs truth reads the
Go registry JSON/helper output and rejects missing references.

**Rationale**: 025/026 established that docs truth should consume Go-owned
truth sources instead of maintaining parallel lists.
