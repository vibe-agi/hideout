# Implementation Plan: HostFS Write Overlay

<!-- markdownlint-disable MD013 -->

**Branch**: `[010-hostfs-write-overlay]` | **Date**: 2026-07-08 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/010-hostfs-write-overlay/spec.md`

## Summary

010 promotes HostFS from read-only portal to staged write overlay for workspace-external host paths. Guest write-class operations are accepted only after Hideout durably records an overlay operation; the host filesystem is not mutated until an authenticated local operator claims and applies a typed Manager decision. Apply revalidates policy, path identity, symlink state, conflicts, metadata constraints, and 009 guest privilege status, then performs a no-partial host mutation or fails closed.

The implementation extends the existing HostFS FUSE -> broker -> `hostfs.Service` path rather than creating backend writable mounts. JavaScript adapters from 008 can propose `host.fs.write.plan`, but Go Core owns staging, decision state, claim/lease, apply/discard, audit, cleanup, and export evidence. The daemon and live console broadcast pending/resolved decisions; they do not create authority or treat missing prompts as approval.

## Technical Context

**Language/Version**: Go 1.25.8, existing constrained Goja for script proposals only

**Primary Dependencies**: Existing `github.com/hanwen/go-fuse/v2` HostFS guest daemon, `golang.org/x/sys` for host metadata where needed, existing `internal/audit`, `internal/broker`, `internal/hostfs`, `internal/manager`, `internal/daemon`, `internal/liveconsole`, and `internal/export`

**Storage**: Session-scoped overlay store under Hideout session state, plus host-local audit JSONL and Manager/API JSON responses; no database

**Testing**: `go test ./...`, targeted hostfs/broker/manager/daemon/liveconsole/export/app tests, schema validation through Gate 0, `scripts/test-gate0.sh`, and real Lima Gate 2 HostFS smoke for write overlay

**Target Platform**: macOS host with Lima Linux guest for product HostFS proof; Linux host primitives covered by unit tests where portable; native backend remains a weak harness

**Project Type**: Single Go CLI/daemon/manager product with guest helper binary

**Performance Goals**: Staging and review are bounded local operations for single-operator sessions. Large file writes stream to overlay objects without loading complete files into UI state; previews are capped.

**Constraints**: No direct guest-to-host write pass-through; no workspace write blocking; no JavaScript authority; no implicit daemon prompt approval; timeout defaults to deny/discard; no broad DLP or guest-root containment claim.

**Scale/Scope**: Professional individual operator, one local store, one active decision claimant per staged operation. V1 applies one staged operation per decision; multi-operation transaction batches are later work.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Touches HostFS, broker RPC, guest FUSE, host filesystem mutation, daemon/live events, Manager API, audit/export, session cleanup, and 009 privilege status. Unsupported operations, missing grants, deny rules, reserved roots, unsafe symlinks, stale claims, conflicts, invalid metadata, failed overlay storage, expired decisions, and unsupported backends fail closed before host mutation.
- **Typed Authority**: New Manager/Core operations own `hostfs/write/plan`, `claim`, `apply`, `discard`, and status/review. Broker and hostfsd carry filesystem intent only. Go validators own policy, metadata, symlink, conflict, and apply checks. JavaScript may only return `host.fs.write.plan` proposals through 008 and cannot stage or resolve writes.
- **Workspace And Policy**: Workspace remains the shared read/write collaboration surface and is not blocked. HostFS write overlay is separate from read grants; read grants do not imply write. Deny rules and reserved roots win. Non-operator-authored write proposals require trust/review before becoming overlay grants.
- **Generality And Provider Scope**: Generic HostFS capability. Editors, agents, package managers, and command adapters are consumers/examples only.
- **Evidence And Redaction**: Audit records staged write, deny, pending decision, claim, apply, discard, timeout, conflict, cleanup, and privilege warning events. Manager/TUI/WebUI render pending/resolved state. Export/share includes the evidence through 005 redaction. Overlay object paths and control-plane material are never target/user-facing authority paths.
- **Backend And Distribution**: Uses existing `hideout-hostfsd` Linux guest helper, which must be updated and packaged if the RPC protocol changes. Native remains weak harness. Lima proof is required before claiming guest-visible writes.
- **Gates**: Gate 0 for schemas, docs, contracts, redaction, API, and unit tests. Gate 2 real Lima HostFS smoke for staged write, apply, conflict denial, and reserved-root denial. Gate 3 is not required unless network/bootstrap is changed; package/install smoke is required if hostfsd packaging changes.
- **Status And Docs**: Update `docs/STATUS.md`, `docs/privacy-run-design.md`, `docs/hostfs-overlay-design.md`, `docs/privacy-run-test-plan.md`, `docs/manager-control-plane.md`, `docs/tui-webui-experience.md`, `docs/script-extension-architecture.md`, `docs/threat-model.md`, and README/user docs if HostFS status or examples change.

**Pre-design result**: PASS. The feature introduces host filesystem authority, but only through typed Manager/Core plan/apply with fail-closed policy and real HostFS gate obligations.

## Project Structure

### Documentation (this feature)

```text
specs/010-hostfs-write-overlay/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── hostfs-write-api.md
│   ├── hostfs-overlay-store.md
│   ├── hostfs-broker-rpc.md
│   └── hostfs-write-evidence.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/hostfs/
├── hostfs.go              # op/scope validation, overlay grant model
├── service.go             # read + write overlay service entrypoints
├── overlay/               # staged operation store, snapshots, conflict checks
└── *_test.go

internal/broker/
├── hostfs.go              # HostFS RPC envelope, audit, broker dispatch
└── *_test.go

cmd/hideout-hostfsd/
└── main_linux.go          # FUSE write/create/delete/rename/chmod/chown bridge

internal/manager/
├── hostfs_write.go        # plan/claim/apply/discard/status Core operations
├── api.go                 # /api/v1/hostfs/write/* routes
├── server.go              # WebUI review controls
└── *_test.go

internal/daemon/
├── events.go              # pending/resolved decision events
└── *_test.go

internal/liveconsole/
├── events.go
├── reducer.go
└── *_test.go

internal/export/
└── *_test.go              # write evidence export/redaction fixtures

schemas/
├── hostfs-write-decision.schema.json
├── hostfs-write-event.schema.json
└── manager-api.schema.json

scripts/
├── test-hostfs-write-overlay-smoke.sh
└── test-gate2-lima.sh     # invoke the real Lima HostFS write slice

docs/
└── status/design/test-plan updates listed above
```

**Structure Decision**: Keep the authority split in existing package boundaries. `internal/hostfs` owns policy and filesystem semantics; `internal/broker` owns broker RPC and audit envelope; `cmd/hideout-hostfsd` owns Linux guest FUSE translation; `internal/manager` owns operator decision state and host apply; daemon/liveconsole/WebUI/TUI only observe or request Manager operations.

## Phase 0: Research

Research output: [research.md](research.md)

Resolved decisions:

- guest syscall success means durable staging success, not host apply;
- v1 supports one staged operation per decision/apply;
- overlay write grants are explicit and separate from read grants;
- special file/symlink/hardlink/xattr/ACL mutation is denied in v1;
- conflict detection uses identity tuple plus content hash where applicable;
- apply uses operation-specific no-partial strategy;
- timeout defaults to deny/discard;
- degraded/unknown 009 status is surfaced as risk evidence, not a block by default.

## Phase 1: Design And Contracts

Design outputs:

- [data-model.md](data-model.md)
- [contracts/hostfs-write-api.md](contracts/hostfs-write-api.md)
- [contracts/hostfs-overlay-store.md](contracts/hostfs-overlay-store.md)
- [contracts/hostfs-broker-rpc.md](contracts/hostfs-broker-rpc.md)
- [contracts/hostfs-write-evidence.md](contracts/hostfs-write-evidence.md)
- [quickstart.md](quickstart.md)

**Post-design Constitution Check**: PASS. The design keeps host mutation in Go-owned Manager/Core apply, uses explicit claims and revalidation, treats daemon/UI as observation/control clients, preserves workspace and 009 non-claims, and requires Gate 2 real Lima proof for HostFS write behavior.

## Complexity Tracking

No constitution violations.
