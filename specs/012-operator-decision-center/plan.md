# Implementation Plan: Operator Decision Center

<!-- markdownlint-disable MD013 -->

**Branch**: `[012-operator-decision-center]` | **Date**: 2026-07-08 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/012-operator-decision-center/spec.md`

## Summary

Introduce a local Manager-owned operator center with two explicit record classes:
actionable decisions and informational notices. Actionable decisions get
claim/lease/timeout/default-deny semantics and provider apply hooks; notices get
broadcast/ack/evidence semantics only. 012 will add a small generic decision
core, migrate 010 HostFS write decisions behind compatibility shims, admit
adapter capability proposals as non-applied decisions unless a promoted provider
exists, and gate share/leaving-machine export through the same queue while
leaving pure local export synchronous.

## Technical Context

**Language/Version**: Go 1.25 module (`go.mod` declares `go 1.25.0`).

**Primary Dependencies**: Standard library plus existing project packages:
`internal/hostfs/overlay`, `internal/manager`, `internal/daemon`,
`internal/liveconsole`, `internal/export`, `internal/cmdadapter`, and
`internal/audit`.

**Storage**: Store-rooted JSON records under the Hideout store. Existing HostFS
write records live under session-local `hostfs-overlay` directories
(`internal/hostfs/overlay/store.go:388-448`). 012 adds a generic decision
store while keeping HostFS compatibility backed by the same source of truth.

**Testing**: `go test ./...`, package-level manager/daemon/export tests,
`scripts/test-gate0.sh`, `scripts/test-adapter-pack-smoke.sh` unchanged, plus a
new decision-center smoke wired into Gate 0.

**Target Platform**: macOS/Linux local operator machine. Native backend remains
a weak harness; no real-Lima claim is introduced by 012.

**Project Type**: Single Go CLI/daemon/local WebUI application with local
Manager API and terminal/browser surfaces.

**Performance Goals**: Listing and watching ordinary local queues should be
instant for an operator-scale store. Tests should cover at least 100 decisions
and notices without user-visible delay.

**Constraints**: Fail closed on missing provider, stale claim, expired lease,
timeout, redaction failure, persistence failure, or audit failure. Notices must
never expose claim/approve/deny fields. Daemon/websocket-like behavior must stay
within existing local daemon/event transport; no remote approval service.

**Scale/Scope**: Professional individual operator; single local store; v1 kinds
are HostFS write, adapter proposal, share/export decision, privilege notice, and
background status notice.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Touches HostFS write apply/discard, adapter capability
  proposals, export/share release, privilege/background evidence, local UI/API
  eventing, and audit. Unsupported or ambiguous authority denies before provider
  side effects; notices cannot grant authority.
- **Typed Authority**: Manager Core owns decision plan/apply and notice ack.
  Go-owned providers execute HostFS write apply/discard and export/share release
  after claim validation. JavaScript adapters may only produce proposal facts;
  Go validates known capability/provider availability before a decision can
  become actionable.
- **Workspace And Policy**: Does not alter workspace mounts or broad HostFS
  grants. HostFS write still requires existing overlay grant and apply
  validation. Export/share still obeys 005 redaction and evidentiary rules.
- **Generality And Provider Scope**: Generic local decision and notice model.
  HostFS, adapter proposal, export/share, privilege, and background are current
  kinds; no package manager, agent, browser, or org workflow becomes Core
  semantics.
- **Evidence And Redaction**: Audit for create/claim/apply/deny/timeout/stale
  and notice create/ack. Manager API/TUI/WebUI render redacted preview only.
  Export includes decision/notice evidence without claim tokens, overlay object
  paths, broker tokens, UI tokens, generated machine IDs, or hidden store paths.
- **Backend And Distribution**: No helper binary or backend capability change.
  Native/local tests are sufficient for product wiring; backend isolation gates
  are unchanged.
- **Gates**: Gate 0, `go test ./...`, new decision-center smoke, markdownlint,
  schema checks, and diff-check. No Gate 2/3/4 unless implementation touches
  backend/network/browser paths beyond local UI surfaces.
- **Status And Docs**: Update `docs/STATUS.md`, `docs/privacy-run-design.md`,
  `docs/threat-model.md` if claim/non-claim wording changes,
  `docs/privacy-run-test-plan.md`, `docs/manager-control-plane.md`, and
  `docs/tui-webui-experience.md`.

**Post-Design Recheck**: PASS. The design keeps provider execution in Go Core,
keeps JS proposal-only, separates notices from decisions, preserves HostFS and
export provider validation, and adds Gate 0/local smoke evidence only.

## Project Structure

### Documentation (this feature)

```text
specs/012-operator-decision-center/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── decision-center-api.md
│   ├── decision-record.md
│   ├── notice-record.md
│   └── compatibility.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/decision/
├── doc.go
├── types.go
├── store.go
├── redaction.go
└── evidence.go

internal/manager/
├── decisions.go              # Core plan/list/claim/resolve/ack/watch helpers
├── hostfs_write.go           # compatibility shims backed by decision center
├── api.go                    # /api/v1/decisions/* and /api/v1/notices/*
├── manager.go                # overview summaries
└── server.go                 # WebUI decision/notice panels

internal/daemon/
├── events.go                 # decision/notice event kinds
└── server.go                 # daemon event fan-out for decision center

internal/app/
└── app.go                    # hideout decision ... CLI and hostfs compat

schemas/
├── decision-record.schema.json
└── notice-record.schema.json

scripts/
└── test-decision-center-smoke.sh
```

**Structure Decision**: Add one focused `internal/decision` package for the
generic records, storage, redaction, and evidence helpers. Manager Core remains
the only authority-owning orchestrator and adapts existing providers into that
package. CLI/API/UI/daemon are consumers of Manager operations, not separate
decision stores.

## Complexity Tracking

No constitution violations.
