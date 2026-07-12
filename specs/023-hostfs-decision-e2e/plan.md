# Implementation Plan: HostFS And Decision E2E

<!-- markdownlint-disable MD013 -->

**Branch**: `023-hostfs-decision-e2e` | **Date**: 2026-07-09 |
**Spec**: [spec.md](spec.md)

**Input**: Feature specification from
`/specs/023-hostfs-decision-e2e/spec.md`

## Summary

023 turns the existing HostFS write overlay and operator decision center into
executable product-hardening evidence. Local-fast mode proves decision state,
claim/resolve/timeout semantics, visibility, redaction, and broader operation
coverage without claiming guest data-plane behavior. Real Gate 2 mode upgrades
the current HostFS write smoke into evidence that proves the core overlay
contract: the guest reads staged content before apply, the host lower file is
unchanged before apply, and apply mutates only the planned host path.

## Technical Context

**Language/Version**: Go 1.24+ repository code, POSIX shell, jq assertions, and
existing Gate 2 Lima shell harness.

**Primary Dependencies**: Existing `internal/hostfs/overlay`,
`internal/decision`, `internal/manager`, `internal/liveconsole`,
`internal/productevidence`, `scripts/test-gate2-lima.sh`, and
`scripts/test-hostfs-write-overlay-smoke.sh`.

**Storage**: Temporary HostFS fixture roots, overlay object/operation/decision
store files, generic decision store files, audit logs, product-hardening
evidence JSON, and temporary Gate 2 artifacts.

**Testing**: `go test ./...`, `scripts/test-gate0.sh`, enhanced
`scripts/test-hostfs-write-overlay-smoke.sh`, new
`scripts/test-hostfs-decision-e2e.sh`, optional real Gate 2 mode, schema
validation, markdownlint, and redaction scans.

**Target Platform**: macOS/Linux local operator machines. Local-fast mode works
without Lima. Real HostFS data-plane mode uses the existing Gate 2 Lima path and
may be `not-run` when prerequisites are missing.

**Project Type**: Go CLI/daemon/local WebUI/TUI project with shell E2E gates and
JSON evidence artifacts.

**Performance Goals**: Local-fast proof should remain suitable for Gate 0 smoke
runtime. Real Gate 2 proof may be slower and remains explicit or release-style.

**Constraints**: No new HostFS operation, approval surface, network authority,
script authority, browser/device workflow, workspace blocking, or guest-root
containment claim. Local-fast cannot satisfy real HostFS/FUSE guest semantics.
Evidence must name covered and uncovered write classes.

**Scale/Scope**: One script, one local-fast evidence manifest, optional real
Gate 2 evidence, a deterministic fixture store, representative real write
operations, and local decision-center coverage for claim/resolve/timeout.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Touches existing HostFS write overlay and decision
  authority only. It fails closed on missing support, stale snapshots,
  conflicting claims, timeout, redaction failure, schema failure, and missing
  real Gate 2 prerequisites.
- **Typed Authority**: Host mutation remains behind existing Manager decision
  plan/apply and Go-owned providers. HostFS writes route through
  `internal/hostfs/overlay.Store` and Manager `ClaimDecision`/`ApproveDecision`
  branches; scripts only orchestrate tests and collect evidence.
- **Workspace And Policy**: Does not alter workspace mounts, HostFS grants,
  env policy, proxy secrets, or profile state. It uses explicit temp fixtures
  and existing HostFS overlay grants. Deny/reserved-root behavior remains owned
  by lower HostFS policy.
- **Generality And Provider Scope**: The script fixture and real Gate 2 lane are
  evidence scopes, not new Core semantics. No provider-specific tool or agent
  behavior becomes product behavior.
- **Evidence And Redaction**: Evidence comes from overlay decisions, decision
  records, HostFS status/apply results, audit refs, liveconsole model state, and
  product-hardening evidence. Claim tokens, provider refs, private overlay
  object ids, broker/UI tokens, `HIDEOUT_SECRET_*`, machine-id material, and
  control-plane field names must be absent from public artifacts.
- **Backend And Distribution**: Local-fast is a weak local proof. Real guest
  HostFS staging requires the existing Gate 2 Lima path and packaged/compiled
  Linux HostFS daemon helper. No new helper is introduced.
- **Gates**: Gate 0 includes local-fast proof and schema/static/docs checks.
  Real Gate 2 mode is explicit and prerequisite-gated; release readiness must
  continue to require the relevant real gate evidence.
- **Status And Docs**: Update `docs/STATUS.md`,
  `docs/privacy-run-test-plan.md`, HostFS docs if needed, and 023 quickstart.
  Claims/non-claims remain unchanged.

## Project Structure

### Documentation (this feature)

```text
specs/023-hostfs-decision-e2e/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── hostfs-decision-e2e.md
│   └── product-hardening-evidence-023.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/
├── productevidence/      # 023 proof ids, required-proof aggregation, tests
├── hostfs/overlay/       # existing staged operation/view/apply semantics
├── decision/             # existing generic claim/resolve/timeout store
├── manager/              # existing decision and HostFS write routes
└── liveconsole/          # existing HostFS/decision model visibility

scripts/
├── test-hostfs-decision-e2e.sh
├── test-hostfs-write-overlay-smoke.sh
├── test-gate2-lima.sh
└── test-gate0.sh

docs/
├── privacy-run-test-plan.md
├── STATUS.md
└── hostfs-overlay-design.md

schemas/
└── product-hardening-evidence.schema.json
```

**Structure Decision**: Keep 023 as evidence and smoke plumbing over existing
HostFS/decision product paths. Do not create a new HostFS subsystem, approval
surface, or UI flow.

## Complexity Tracking

No constitution violations. The only new script is a proof runner over existing
authority and evidence mechanisms.

## Phase 0 Research Summary

Research output is in [research.md](research.md). Key grounded decisions:

- Local-fast covers generic decision semantics and redaction; real Gate 2 owns
  guest HostFS staging claims.
- CLI/API claim/apply remains the deterministic mutation surface; UI/TUI are
  visibility surfaces for 023.
- Evidence must list covered and uncovered HostFS write operations to avoid a
  "full matrix" overclaim.
- Existing Gate 2 replace smoke already proves the most important staged read
  and host-lower-before-apply contract; 023 turns that into stable evidence and
  adds a directory operation when real prerequisites are available.

## Phase 1 Design Summary

Design artifacts:

- [data-model.md](data-model.md)
- [contracts/hostfs-decision-e2e.md](contracts/hostfs-decision-e2e.md)
- [contracts/product-hardening-evidence-023.md](contracts/product-hardening-evidence-023.md)
- [quickstart.md](quickstart.md)

## Post-Design Constitution Check

- **Privacy Boundary**: PASS. No authority expands; all pass claims are tied to
  explicit local-fast or real Gate 2 proof classes.
- **Typed Authority**: PASS. Host mutation remains Go-owned through existing
  Manager/HostFS providers.
- **Workspace And Policy**: PASS. The feature uses temp fixtures and does not
  change workspace or profile policy semantics.
- **Generality And Provider Scope**: PASS. Fixtures are evidence scopes only.
- **Evidence And Redaction**: PASS. Product-hardening evidence and redaction
  scans are first-class requirements.
- **Backend And Distribution**: PASS. Local-fast remains weak; real HostFS
  proof stays Gate 2.
- **Gates**: PASS. Gate 0 gets local-fast proof; real Gate 2 remains explicit.
- **Status And Docs**: PASS. Status/test-plan updates are required tasks.
