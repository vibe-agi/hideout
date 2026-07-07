<!-- markdownlint-disable MD013 -->

# Implementation Plan: Export/Share Redaction Boundary

**Branch**: `005-export-share-redaction` | **Date**: 2026-07-07 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/005-export-share-redaction/spec.md`

## Summary

Add the constitutionally-named export/share boundary (constitution Principle IV;
`internal/audit/audit.go:20-21`): the single mediated way to take an evidence
artifact — an audit slice, a release-evidence bundle, or a Boundary Summary — off
the local machine. Every exported artifact passes two redaction stages: (1) the
Go-owned deterministic control-plane strip (`internal/audit` `RedactDetails`/
`RedactString`/`RedactValue`, already applied at audit write time in
`Writer.Emit`), re-asserted on export so artifacts assembled outside that path
(the shell-built release bundle, a Boundary Summary) are also guaranteed clean;
and (2) operator-selected user-data redaction driven by the existing
`audit.redact`/`redactAudit` Goja policy, but applied with export-specific
semantics distinct from the broker record path. The broker record path restores a
fixed metadata set after the script runs (`preserveBrokerAuditMetadata`,
`internal/broker/broker.go:947`); export instead treats that Core-owned set as
non-redactable evidentiary metadata and fails closed when a selection targets it,
rather than silently restoring it. Export fails closed on any redaction failure or
a missing user-data decision; it produces only a local artifact (no
network/transport authority) and emits a meta-audit event. Local full-fidelity
surfaces (`hideout audit show`, local JSONL, authenticated Manager/WebUI) are
unchanged.

## Technical Context

**Language/Version**: Go 1.25.0 plus the existing CLI and Manager control plane.

**Primary Dependencies**: Existing packages — `internal/audit`
(`RedactDetails`/`RedactString`/`RedactValue`/`RedactKey`, the deterministic
control-plane strip; `audit.Event` is the record shape), `internal/broker`
(`redactAuditDetails`/`preserveBrokerAuditMetadata`, the reference for the
restored evidentiary set), the `audit.redact`/`redactAudit` Goja policy via the
script evaluator (`RunAuditRedactScript`), `internal/manager`
(`AuditEvents` read+filter, `BoundarySummary`/`SummarizeRunBoundary`), the
`hideout audit` CLI dispatch (`internal/app/app.go:3730`), `internal/profile`
(`Store.Load`/`Store.ProfileDir`, to resolve the `audit.redact` `ScriptRefs`
offline the way the broker is wired at `internal/app/app.go:2424-2429`), and the
release-evidence bundle plus `schemas/release-dogfood.schema.json`. No new
redaction engine and no new production helper binary.

**Storage**: No new persistent store. Export reads existing evidence sources
(audit JSONL via `manager.AuditEvents`, the release-evidence bundle, a Boundary
Summary) and writes a single local artifact to an operator-chosen path. Local
audit and evidence are unchanged.

**Testing**: `go test ./...` (unit over the export-time two-stage redaction, the
evidentiary-set fail-closed, the missing-decision fail-closed, and the
acknowledge-covers-residual rule) and `scripts/test-gate0.sh` (static: an
exported artifact is provably clean; the export-artifact/provenance schema
validates). No real-Lima gate — this is a data-handling/redaction claim, not an
isolation claim; the native harness is acceptable for CLI/Manager wiring.

**Target Platform**: macOS/Linux host CLI plus the Manager control plane. No
backend isolation is claimed, so backend selection is not gated here.

**Project Type**: CLI plus Go Manager control plane (single project).

**Performance Goals**: Bounded one-shot redaction over a selected evidence slice;
no per-request hot path and no new steady-state cost.

**Constraints**: The control-plane strip is the Go-owned guaranteed floor and is
unconditional. The non-redactable evidentiary set is Core-owned and fixed
(`requestId`, `subject`, `command`, `route`, `requestedAction`, `status`,
`error` — from `preserveBrokerAuditMetadata`), not policy-configurable. The
user-data decision is dual-track (non-interactive flag/selection plus interactive
confirmation on a terminal) and fails closed absent a decision when user data is
present. Export owns no network/transport/destination authority. The redaction
contract applies to the meta-audit event too.

**Scale/Scope**: Single-operator prosumer. Three export surfaces (audit slice,
release-evidence bundle, Boundary Summary), one new CLI/Manager export path, one
export-artifact schema, and the doc/gate updates.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: PASS. The feature governs what evidence leaves the local
  trust zone. It fails closed when the control-plane strip cannot be applied, a
  required user-data decision is missing, the `audit.redact` policy errors, or a
  selection targets a non-redactable evidentiary field — never emitting a partial
  or unredacted artifact (FR-004, FR-010, FR-012). No new host, filesystem,
  network, or backend authority is acquired (FR-011).
- **Typed Authority**: PASS. The control-plane strip, the export-time application
  of the evidentiary set, artifact assembly, and provenance are Go-owned. The
  only flexible judgment — which user/application values to scrub — is the
  existing constrained `audit.redact` Goja decision point over Go primitives; Go
  independently enforces the Core-owned evidentiary floor the policy cannot widen
  or narrow. The export command is a Manager/CLI product path using the same
  Manager model, not UI-only.
- **Workspace And Policy**: PASS. No workspace mount, HostFS grant, passthrough
  mount, env policy, proxy secret, or profile-state change. User-data redaction
  reuses the existing `audit.redact` policy model, including deny precedence.
- **Generality And Provider Scope**: PASS. A generic export/share boundary over
  the existing evidence and redaction model. It hard-codes no bug tracker,
  transport, destination, or file-format quirk as Core semantics; the three
  evidence surfaces are existing artifact classes.
- **Evidence And Redaction**: PASS. Each export emits a local meta-audit summary
  (source, redaction stages applied, operator decision) that embeds no source
  evidence content and passes only the deterministic control-plane strip (not the
  export user-data stage); the artifact carries provenance so a recipient can tell
  what was
  scrubbed. No Hideout-minted control-plane secret can appear in an exported
  artifact.
- **Backend And Distribution**: PASS. No new backend capability, no real-Lima
  requirement (no isolation claim), and no new product helper binary. Native is a
  weak harness but acceptable here because nothing isolation-related is claimed.
- **Gates**: Gate 0 for the export-artifact/provenance schema, docs, and the
  static exported-artifact-cleanliness check; `go test ./...` for the two-stage
  redaction, evidentiary-set fail-closed, missing-decision fail-closed, and
  acknowledge-covers-residual behaviors.
- **Status And Docs**: `docs/STATUS.md` (promote the export/share redaction
  surface from design-ready to implemented), `docs/threat-model.md` (sharing
  under the boundary becomes a claim; raw hand-copy remains the unsafe path),
  `docs/privacy-run-design.md` (Audit and Explain: the export surface and its
  redaction contract), and `docs/privacy-run-test-plan.md` (the export gate).

**Pre-design result**: PASS. No constitution violation or complexity exception.

## Project Structure

### Documentation (this feature)

```text
specs/005-export-share-redaction/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── export-command.md
│   └── export-artifact.md
└── tasks.md        # generated by /speckit-tasks after this plan
```

### Source Code (repository root)

```text
internal/
├── export/             # NEW: the export/share boundary — two-stage redaction,
│                       # evidentiary-set enforcement, provenance, fail-closed
│                       # decision; Go-owned. Reuses internal/audit + the
│                       # audit.redact evaluator; does NOT reuse the broker's
│                       # preserveBrokerAuditMetadata restore.
├── audit/              # deterministic control-plane strip (reused; re-asserted
│                       # on export). May expose the evidentiary field set.
├── broker/             # source of the reference evidentiary set (unchanged)
├── manager/            # AuditEvents read+filter; BoundarySummary; add an
│                       # export plan/apply surface mirroring existing typed ops
└── app/                # hideout audit export subcommand (dispatch at app.go:3730)

schemas/
└── export-artifact.schema.json   # NEW: exported-artifact envelope + provenance

scripts/
├── test-gate0.sh                  # add the exported-artifact-cleanliness static check
└── test-export-redaction-smoke.sh # NEW: seed secrets/user data, export, assert clean

docs/
├── STATUS.md
├── threat-model.md
├── privacy-run-design.md
└── privacy-run-test-plan.md
```

**Structure Decision**: A new `internal/export` package owns the boundary so the
export-time redaction application stays distinct from the broker record path
(the two must not share `preserveBrokerAuditMetadata`, which would reintroduce
the SC-006 conflict). It reuses `internal/audit` for the control-plane strip and
the existing `audit.redact` evaluator for user-data selection. The CLI surface is
a new `hideout audit export` subcommand alongside `audit show`
(`internal/app/app.go:3730`), with a matching Manager typed plan/apply op so the
path is not CLI-only.

## Phase 0: Research

See [research.md](research.md).

## Phase 1: Design

See [data-model.md](data-model.md),
[contracts/export-command.md](contracts/export-command.md),
[contracts/export-artifact.md](contracts/export-artifact.md), and
[quickstart.md](quickstart.md).

## Post-Design Constitution Check

- **Privacy Boundary**: PASS. Two-stage redaction with fail-closed on every
  failure mode; the exported artifact is provably clean or absent.
- **Typed Authority**: PASS. Go owns the strip, the evidentiary floor, assembly,
  and provenance; `audit.redact` remains the only flexible decision point.
- **Workspace And Policy**: PASS. No authority broadening.
- **Generality And Provider Scope**: PASS. Generic boundary; three existing
  evidence surfaces; no destination/transport in scope.
- **Evidence And Redaction**: PASS. Meta-audit event and provenance covered by
  contracts and success criteria; redaction contract applies to every field.
- **Backend And Distribution**: PASS. No backend capability, no real-Lima, no new
  helper binary.
- **Gates**: PASS. Quickstart maps each requirement to unit or Gate 0 evidence.
- **Status And Docs**: PASS. Doc updates enumerated above and carried to tasks.

## Complexity Tracking

No constitution violations or exceptional complexity are required. The one new
package (`internal/export`) is justified: the export-time redaction application
MUST differ from the broker record path (`preserveBrokerAuditMetadata` restores
`command`/`route`/etc., which on export would silently un-redact a field the
operator selected — the SC-006 conflict). Keeping export application in its own
Go-owned package makes the evidentiary-set fail-closed rule explicit and testable
rather than entangled with broker record semantics.
