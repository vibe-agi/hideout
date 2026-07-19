# Implementation Plan: Shared Default VM Across Workspaces

<!-- markdownlint-disable MD013 MD060 -->

**Branch**: `035-shared-default-vm-cross-workspace` | **Date**: 2026-07-17 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from
`specs/035-shared-default-vm-cross-workspace/spec.md`

## Summary

Replace the automatic Lima environment's workspace-derived identity with one
stable profile-backed shared slot. Keep machine compatibility as an explicit
drift check rather than encoding it in the slot name. Move project authority
from `environment.Record` into an immutable daemon-owned workspace attachment
that binds a canonical host root, stable root identity, opaque workspace ID,
exact backend incarnation, session, host provider, and private guest view.

Delivery has a non-bypassable Phase R. It compares a live VZ multiple-share
control path with a dedicated high-performance Workspace Portal and jointly
tests the guest logical/physical path identity. Phase I may begin only after one
complete pair passes correctness, isolation, host-permission, lifecycle, and
fixed performance thresholds. The losing prototype is removed; if neither
passes, 035 stops without changing product behavior or claims.

The accepted implementation separates machine activation, workspace attach,
and session execution. It extends the 034 daemon-owned run service and registers
real provider/view topology in the 036 lifecycle catalog. Shared-mode Lima YAML
contains no project mount. `/workspace` is a session-private logical entry over
an opaque project-specific physical identity, while host-app and broker mapping
resolve only through the immutable attachment.

## Technical Context

**Language/Version**: Go 1.25; strict JSON schemas and bounded binary framing if the Portal candidate wins; platform-specific Darwin/VZ integration only if the VZ candidate wins

**Primary Dependencies**: Existing Manager, daemon, environment, session, lifecycle, backend/Lima, broker, host-capability, HostFS, network, audit, doctor, live-console, product-evidence, `golang.org/x/sys`, `github.com/hanwen/go-fuse/v2`, Lima 2.1.4, and its pinned `github.com/Code-Hex/vz/v3` v3.7.1; no module metadata is edited by hand

**Storage**: Clean machine-scoped environment records; private per-store workspace identity key; bounded workspace attachment discovery metadata; existing lifecycle journal, owner records, audit, and product-evidence roots; no raw host roots in public artifacts

**Testing**: Go unit/race/model/contract/schema tests, transport fixtures and benchmarks, malformed/overload probes, Manager/CLI/TUI/WebUI parity, Gate 0, real macOS arm64 Lima Gate evidence, installed-package verification, and evidence digest validation

**Target Platform**: First product claim is macOS arm64 with Lima/VZ and the supported runtime image; native and unsupported Lima platforms remain explicit workspace-bound modes

**Project Type**: Go CLI thin client, local single-user daemon, canonical Manager Core, Lima VM backend, and at most one selected workspace transport/helper

**Performance Goals**: in one VM/process, alternating paired Portal/static-virtiofs samples keep 10,000-entry Git status median at most 2 seconds and 2x the paired control; the 20,000-operation package fixture median is at most 3x the paired control; atomic-save visibility p95 is at most 250 ms; mounted-ready p95 is at most 1 second; warm first-target-byte p95 is at most the retained research baseline plus `max(500 ms, 15%)`

**Constraints**: No broad parent/home mount; no copy-back/sync/HostFS overlay substitute; no static selected workspace in shared Lima config; no CLI ownership fallback; no raw host path in guest/public surfaces; no hidden second automatic VM on drift; no guest-root containment claim; no Phase I before accepted Phase R evidence

**Scale/Scope**: One operator/store/daemon, one stable shared slot per canonical profile, up to 16 concurrent run sessions, bounded views/handles/in-flight bytes/enumeration, same/nested/disjoint roots, and one promoted transport on one platform

## Constitution Check

*GATE: Passed before research and re-checked after Phase 1 design.*

- **Privacy Boundary - PASS**: This feature changes direct workspace and backend
  attachment authority. Unsupported transport, unstable root identity,
  canonicalization ambiguity, provider overload, host permission denial,
  credential expiry, cleanup uncertainty, incarnation drift, and preserve-mode
  incompatibility all fail before new authority or block reuse/stop. There is
  no broad-mount or copy fallback.
- **Typed Authority - PASS**: Manager Core validates one immutable attachment;
  the selected Go-owned backend/provider performs attach and cleanup. The
  daemon owns lifecycle and transport connections but cannot invent a root or
  grant a workspace. JavaScript receives only typed relative intent and an
  opaque workspace ID.
- **Workspace And Policy - PASS**: The selected project remains the intentional
  live read/write collaboration root. HostFS remains hidden/staged and separate.
  Workspace safety and reserved-root checks run before root capture; unsafe
  override cannot authorize broad shared-slot mounts.
- **Generality And Provider Scope - PASS**: Git, Node, Python, Go, editors,
  Claude, and Codex are fixtures for filesystem/path behavior, not Core product
  semantics. VZ and Portal are backend candidates behind one generic attachment
  contract.
- **Evidence And Redaction - PASS**: One workspace ID and lifecycle graph feed
  audit, status, events, doctor, Manager API, TUI/WebUI, and evidence. Canonical
  host roots, identity key, provider token, and control endpoints stay out of
  guest/public output; operator-local paths retain the existing export boundary.
- **Backend And Distribution - PASS**: Phase R inventories every process that
  opens or watches a root. A winning helper/driver must be package-owned,
  checksummed, repaired, diagnosed, signed where required, and disabled from
  source-tree overrides in the promotion gate. Native is mechanics only.
- **Gates - PASS**: Gate 0 proves model, drift, root safety, lifecycle, schemas,
  redaction, mode matrix, and candidate protocol properties. Real Lima evidence
  proves one boot serving two projects, live I/O, isolation, performance,
  sibling survival, path identity, host projection, and exact-incarnation stop.
- **Status And Docs - PASS**: Promotion updates `docs/STATUS.md`,
  `docs/privacy-run-design.md`, `docs/threat-model.md`,
  `docs/claim-boundaries.md`, `docs/privacy-run-test-plan.md`, support/first-run,
  lifecycle UI, and host-capability projection docs. Before clean evidence they
  continue to describe workspace-bound automatic environments.

Post-design re-check: all items remain PASS. The private workspace attachment
is direct collaboration authority already selected by the operator; lifecycle
metadata does not become permission. The new package centralizes an identity
and relation currently recomputed inconsistently and introduces no script-owned
authority. No constitutional waiver is required.

## Phase Boundaries

### Phase R - Existence Research

Phase R may add production-shaped probes, fixed fixtures, benchmarks, and one
strict research-decision schema. It may not change environment selection,
records, defaults, public claims, or retain a hidden fallback. It ends with
exactly one of:

1. `accepted` with one transport/path pair, exact commit and dependencies,
   operation/limit matrix, raw samples, and all thresholds passed; or
2. `rejected` with both candidates failed and Phase I blocked.

An SDK type, local patch, mount demo, or microbenchmark cannot produce
`accepted`. Any selected helper/driver must also have a viable package and
support posture.

### Phase I - Product Implementation

Phase I consumes the immutable accepted artifact, implements only its named
transport, removes the losing candidate, replaces the environment model
cleanly, connects attachment/lifecycle/surfaces, and produces clean release-
shaped evidence. Tasks after the Phase R barrier remain incomplete if the
artifact is absent, dirty for promotion, stale, or rejected.

## Project Structure

### Documentation (this feature)

```text
specs/035-shared-default-vm-cross-workspace/
|-- spec.md
|-- plan.md
|-- research.md
|-- data-model.md
|-- quickstart.md
|-- contracts/
|   |-- environment-workspace-model.md
|   |-- workspace-attachment.md
|   |-- transport-research-decision.md
|   `-- lifecycle-and-surfaces.md
|-- checklists/
|   `-- requirements.md
`-- tasks.md
```

### Source Code (repository root)

```text
internal/
|-- workspaceattach/
|   |-- model.go             # attachment, root identity, overlap, state
|   |-- identity.go          # private key and one workspace-ID derivation
|   |-- contract.go          # machine/attach/execute split and transport API
|   |-- limits.go            # bounded admission and fairness contract
|   `-- status.go            # redacted operator/public projections
|-- environment/
|   `-- environment.go       # clean shared/dedicated/workspace-bound record
|-- manager/
|   |-- run_environment.go   # stable slot and compatibility drift
|   |-- run_workspace.go     # canonical attach plan/apply/cleanup
|   |-- run_apply.go         # ready barrier and attachment binding
|   |-- run_session.go       # session facts only
|   `-- manager.go           # machine/view summaries
|-- backend/
|   |-- backend.go           # machine, workspace, and execution contracts
|   `-- lima/
|       |-- lima.go          # shared machine config without project mount
|       |-- workspace.go     # selected attachment provider
|       `-- session_view.go  # private physical/logical workspace view
|-- lifecycle/
|   `-- catalog.go           # provider/view/(optional service) closed kinds
|-- daemon/
|   |-- daemon.go            # selected provider composition
|   |-- sessions.go          # sole attachment owner
|   `-- status.go            # machine/view inventory
|-- hostcap/                 # immutable attachment-based projection
|-- doctor/                  # transport/root/TCC/drift recovery
|-- liveconsole/             # machine and workspace-view state
`-- productevidence/         # stable 035 proof registry entries

cmd/
`-- hideout-workspace-probe/ # Phase R only; removed or package-owned if selected

schemas/
|-- workspace-attachment.schema.json
|-- workspace-research-decision.schema.json
|-- environment-summary.schema.json
`-- daemon-status.schema.json

scripts/
|-- test-workspace-transport-research.sh
|-- test-shared-workspace-smoke.sh
|-- test-shared-workspace-lima-e2e.sh
|-- test-gate0.sh
`-- lib/
    |-- gate2-shared-workspace.sh
    `-- gate2-shared-workspace-performance.sh

docs/
|-- architecture-principles.md
|-- privacy-run-design.md
|-- privacy-run-test-plan.md
|-- threat-model.md
|-- claim-boundaries.md
|-- support-matrix.md
|-- first-run-alpha.md
|-- host-capability-projection.md
|-- tui-webui-experience.md
`-- STATUS.md
```

**Structure Decision**: Add one `workspaceattach` domain because root identity,
attachment authority, overlap, transport limits, redacted status, and lifecycle
binding must be shared by Manager, daemon, backend, host projection, evidence,
and tests. Keep machine execution in backend/Lima and stop decisions in the
existing lifecycle coordinator. Candidate probe code is quarantined and the
loser is deleted; the product does not ship two selectable workspace paths.

## Complexity Tracking

| Addition | Why Required | Simpler Alternative Rejected Because |
|----------|--------------|---------------------------------------|
| `workspaceattach` domain package | One environment now has multiple immutable root authorities and every subsystem must share one ID/relation/state model | Leaving attachment facts in Manager structs preserves duplicate path hashes and environment-level fallbacks |
| Binary Phase R with two bounded candidates | Current Lima/VZ stack lacks a supported live-share control path, while a user-space portal has unproved hot-path cost | Selecting either from API inspection would turn an unproved primitive into a product claim |
| Logical plus opaque physical guest path | Shared profile tools can key trust/history/cache by cwd even when each namespace displays `/workspace` | A fixed bind at `/workspace` makes distinct projects observationally identical to path-keyed tools |

These are implementation complexities, not constitutional violations. Each is
required to remove an existing ambiguity or to prove the feature is feasible
before changing product behavior.
