# Research: Doctor And Package Recovery E2E

<!-- markdownlint-disable MD013 -->

## Decision 1: Reuse Existing Smoke Paths

**Decision**: 024 calls or upgrades `scripts/test-package-smoke.sh` and
`scripts/test-doctor-smoke.sh` instead of implementing independent package
repair and doctor recovery logic.

**Rationale**: The existing smokes already exercise the product paths used in
Gate 0. Duplicating the repair flow would create drift and repeat the "parallel
theater" risk from earlier reviews.

**Alternatives considered**:

- Build a new bespoke package-repair fixture. Rejected because it could pass
  while the canonical smoke fails.
- Only trust existing smoke output. Rejected because 024 needs stable proof ids
  and artifact references for documentation truth mapping.

## Decision 2: Product-Hardening Evidence Is The Proof Surface

**Decision**: 024 extends `hideout.product-hardening-evidence/v1` with stable
proof ids for package repair, doctor repair, guidance, and redaction.

**Rationale**: 021-023 already use the manifest, and 025 will map docs claims to
proof ids. Reusing the schema avoids one-off logs.

**Alternatives considered**:

- Extend release-readiness directly. Rejected for local-fast proof because
  release readiness requires real Gate 2/Gate 3 evidence.

## Decision 3: Guidance Findings Are Not Repairs

**Decision**: Doctor findings for missing Lima, DNS/HostFS gate-required state,
privilege degraded, or missing external prerequisites remain guidance entries.
They may carry next actions and gate-required markers, but they are not counted
as fixed by doctor.

**Rationale**: Local doctor cannot prove or repair real backend prerequisites.
Treating those findings as fixed would overclaim readiness.

**Alternatives considered**:

- Let doctor mark warnings as repaired after showing guidance. Rejected because
  it weakens the recovery contract.

## Decision 4: Packaged Path Preferred, Source Fallback Labeled

**Decision**: Package repair proof uses a local package artifact and installed
`hideout` by default. Source-tree fallback may exist only for developer-local
diagnostics and must be labeled.

**Rationale**: The external alpha path is packaged. Proving source `go run`
alone would not prove install/verify/repair usability.

**Alternatives considered**:

- Source-only recovery proof. Rejected as too weak for external alpha.

## Decision 5: Redaction Scans Public Artifacts

**Decision**: Scan package/doctor logs, doctor JSON, exported doctor report,
and product-hardening evidence. Do not scan private temporary internals unless
they are referenced as public artifacts.

**Rationale**: The promise is shareable local evidence cleanliness, not that
private stores contain no internal state.

**Alternatives considered**:

- Scan every temp file. Rejected because package internals and private state may
  contain implementation identifiers that are not public evidence.
