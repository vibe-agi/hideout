# Research: HostFS And Decision E2E

<!-- markdownlint-disable MD013 -->

## Decision 1: Split Local-Fast And Real Gate 2 Proofs

**Decision**: Local-fast proves decision state, generic claim/resolve/timeout,
redaction, and UI/TUI model visibility. Real Gate 2 proves guest HostFS staging
semantics.

**Rationale**: The native/local path cannot prove Linux guest FUSE data-plane
behavior. Existing Gate 2 already exercises the real HostFS daemon and guest
write path (`scripts/test-gate2-lima.sh` hostfs write smoke). Keeping the split
prevents native output from satisfying real HostFS claims.

**Alternatives considered**:

- Let local-fast satisfy all 023 claims. Rejected: repeats prior native
  overclaim pattern.
- Require real Gate 2 for every 023 run. Rejected: too slow and prerequisite
  heavy for Gate 0.

## Decision 2: Use CLI/API For Deterministic Apply

**Decision**: 023 uses existing CLI/API/Manager decision paths for claim/apply.
WebUI/TUI are visibility surfaces, not the mutation driver for this feature.

**Rationale**: 021 already owns browser/PTY click proof. 023's safety claim is
about host mutation correctness, stale/conflict handling, and decision status.
CLI/API gives deterministic artifacts while still using the same Go-owned
Manager authority.

**Grounding**: Manager `ClaimDecision` delegates HostFS decisions to
`ClaimHostFSWrite` and generic decisions to the decision store
(`internal/manager/decisions.go:122-168`). `ApproveDecision` delegates HostFS
apply to `ApplyHostFSWrite` (`internal/manager/decisions.go:171-241`).

**Alternatives considered**:

- Require WebUI click apply in 023. Rejected: duplicates 021 and makes mutation
  proof depend on browser availability.
- Add a new approval endpoint. Rejected: violates non-expansion.

## Decision 3: Require Guest-Read And Host-Lower Assertions For Real Claims

**Decision**: Any real HostFS pass claim must assert both that guest reads show
staged overlay state before apply and host lower state is unchanged before
apply.

**Rationale**: This is the core product promise for write overlay. Proving only
pending decision creation is insufficient.

**Grounding**: Overlay `View` reads live staged operations before host lower
files (`internal/hostfs/overlay/store.go:469-490`), while real Gate 2 currently
asserts guest-visible `hostfs_overlay_guest=hostfs-after` and host lower remains
`hostfs-before` before apply (`scripts/test-gate2-lima.sh:324-388`).

**Alternatives considered**:

- Assert only decision pending before apply. Rejected: misses guest-visible
  staged state.
- Assert only final apply. Rejected: misses pre-apply non-mutation.

## Decision 4: Evidence Must Declare Operation Coverage

**Decision**: Each proof declares covered and uncovered HostFS write classes.
If real Gate 2 covers only replace plus one directory operation, evidence says
representative and lists unsupported/uncovered operations.

**Rationale**: 010 supports multiple write classes. 023 must not imply full
operation coverage unless it actually tests the full set.

**Grounding**: Overlay apply supports `create`, `replace`, `append`,
`truncate`, `mkdir`, `delete`, `rename`, `chmod`, and constrained `chown`
(`internal/hostfs/overlay/store.go:583-610`).

**Alternatives considered**:

- Only state "HostFS write works". Rejected: too broad.
- Force full real matrix in v1. Rejected: expensive and brittle; representative
  real proof plus local-fast matrix is enough for this hardening slice.

## Decision 5: Reuse Product-Hardening Evidence

**Decision**: 023 extends `hideout.product-hardening-evidence/v1` with stable
proof ids and required local-fast completeness.

**Rationale**: 021 and 022 already created the product-hardening proof layer.
Reusing it keeps 025 documentation truth mapping simple and avoids ad hoc text
logs.

**Grounding**: `productevidence.WriteFile` validates and writes sanitized
manifests (`internal/productevidence/writer.go:44-52`).

**Alternatives considered**:

- Create a HostFS-specific evidence schema. Rejected: unnecessary fragmentation.

## Decision 6: Use Existing Decision Store Concurrency Semantics

**Decision**: Claim race proof targets the existing decision store file lock and
Manager decision route behavior rather than adding new synchronization.

**Rationale**: 012 fixed cross-instance claim races with file locking. 023
should prove the existing contract holds in the HostFS/decision E2E context.

**Grounding**: `decision.Store.ClaimDecision` obtains a lock file before
reading/updating state (`internal/decision/store.go:132-188`), and
`ResolveDecision` validates claim tokens under the same locking model
(`internal/decision/store.go:190-234`).

**Alternatives considered**:

- Add a separate HostFS-only claim lock. Rejected: duplicates decision center
  authority.

## Decision 7: Redaction Scan Covers Public Artifacts Only

**Decision**: Redaction proof scans public evidence, CLI/API/UX model output,
and audit/export artifacts. Private overlay store files may contain internal
object ids but must not be surfaced as public proof.

**Rationale**: The product promise is that shareable/local public evidence
does not leak control-plane material. Private store internals are not exported
directly.

**Grounding**: Overlay `Claim.Token` is JSON-omitted while `TokenHash` may be
stored (`internal/hostfs/overlay/types.go:103-110`), and generic decisions are
redacted before public reads (`internal/decision/store.go:74-82`,
`internal/decision/store.go:102-129`).

**Alternatives considered**:

- Scan all temp store internals. Rejected: would conflate private store state
  with public evidence and create false positives.
