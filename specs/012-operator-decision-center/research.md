# Research: Operator Decision Center

<!-- markdownlint-disable MD013 -->

## Decision 1: Split Actionable Decisions From Informational Notices

**Decision**: Model actionable decisions and informational notices as separate
record classes under one operator center.

**Rationale**: HostFS write, adapter proposal, and share/export are pending
authority requests, so claim/lease/approve/deny/timeout semantics are coherent.
Privilege degraded and daemon background status are facts/status updates; an
operator cannot "deny" that a target can sudo or that a background operation is
running. Splitting the classes avoids false controls and keeps evidence honest.

**Alternatives considered**:

- One generic "decision" type for everything: rejected because notices would
  need meaningless default-deny and claim fields.
- Separate product-specific pages only: rejected because it preserves the 010
  fragmentation that 012 is meant to remove.

## Decision 2: Generic Decision Store With HostFS Compatibility Shims

**Decision**: Introduce a generic decision store and adapt existing 010 HostFS
write APIs to it, keeping `hostfs/write/*` as compatibility shims backed by the
same decision source of truth.

**Rationale**: Current HostFS write decisions already have id, state, claim,
timeout, preview, and audit semantics in `internal/manager/hostfs_write.go` and
`internal/hostfs/overlay/store.go`. A generic store lets new decision kinds
share list/watch/claim/resolve behavior while compatibility shims avoid
breaking existing CLI/API/smoke coverage.

**Alternatives considered**:

- Leave HostFS write on a separate store and mirror it into the generic queue:
  rejected because mirrored state can drift and create double-apply risk.
- Delete existing HostFS write APIs immediately: rejected because 010 shipped
  those routes and tests; compatibility is cheaper and safer.

## Decision 3: Provider Apply Remains Provider-Owned

**Decision**: The decision center owns lifecycle state and claim validation; the
underlying provider still owns apply/discard validation and side effects.

**Rationale**: The constitution requires Go Core and typed providers to own
authority. A generic decision resolver must not become a raw "execute this"
engine. HostFS write must revalidate snapshots, overlay grants, privilege, and
safe paths at apply time; export/share must re-run redaction and evidentiary
checks; adapter proposals remain non-applied unless a promoted provider exists.

**Alternatives considered**:

- Store generic provider commands and execute by reflection: rejected as raw
  authority execution.
- Treat decision approval as provider success: rejected because stale provider
  state can invalidate an approval.

## Decision 4: Adapter Proposals Are Actionable Only With Promoted Providers

**Decision**: Adapter capability proposals enter the decision center only when
the requested capability is declared by the adapter/profile and has a promoted
Go-owned provider. Otherwise the proposal records evidence and fails closed as
non-applied.

**Rationale**: 008/011 deliberately made `proposeCapability` non-applied.
012 can provide queue semantics for proposals, but cannot invent HostFS write,
privilege, endpoint, or profile mutation authority from JavaScript output.

**Alternatives considered**:

- Queue every proposal for manual approval: rejected because approval without a
  provider is theater and would imply authority that does not exist.
- Keep all proposals invisible: rejected because operators need evidence and
  future provider-ready proposals need a queue.

## Decision 5: Share/Leaving-Machine Export Uses Decisions, Local Export Does Not

**Decision**: Only export/share flows that leave the machine or prepare an
artifact for sharing create actionable decisions. Pure local export remains
synchronous under 005.

**Rationale**: 005 already made local export fail-closed and redacted. Forcing
every local export through claim/apply would harm the CLI workflow without
adding boundary value. Sharing is a different user intent and should use the
common decision path.

**Alternatives considered**:

- Route every export through the inbox: rejected as unnecessary UX friction.
- Keep share confirmation separate from the inbox: rejected because it would
  duplicate decision redaction, timeout, and audit behavior.

## Decision 6: Claim Identity Is Local Surface Identity, Not User Roles

**Decision**: Claim records identify local authenticated surfaces (`cli`, `tui`,
`webui`, `api`) and lease timing. They do not introduce users, roles, or
delegation.

**Rationale**: The constitution scopes Hideout to a professional individual
operator. Local token authentication is enough for v1; organization role models
are explicitly out of scope.

**Alternatives considered**:

- Add named users/roles: rejected as a product-scope violation.
- Make claims anonymous: rejected because stale/lost-claim diagnostics need a
  surface label and timestamps.

## Decision 7: Timeout Defaults Deny For Decisions Only

**Decision**: Actionable decisions use one global default timeout and optional
per-kind overrides; timeout always resolves to denial/discard/no-release.
Notices do not timeout into denial.

**Rationale**: Fail-closed is non-waivable for pending authority. Notices are
already facts, so denial has no semantic meaning.

**Alternatives considered**:

- Infinite pending decisions: rejected because unattended host mutation or share
  decisions would linger indefinitely.
- Auto-approve low-risk decisions: rejected because missing prompt must never be
  treated as approval.

## Decision 8: Event Stream Carries State Changes, Not Authority

**Decision**: Daemon/live-console events broadcast decision and notice changes.
They never carry claim tokens, raw provider handles, overlay object paths, or
implicit approval.

**Rationale**: 006/007 established local event transport and no daemon prompt
approval. 012 reuses the transport for freshness while Manager/Core remains the
authority path.

**Alternatives considered**:

- Resolve decisions directly from event messages: rejected because events are
  observation, not authority.
- Require polling only: rejected because 006/007 already delivered event-driven
  local surfaces and tests.

## Decision 9: Redaction Happens Before Persistence And Before Rendering

**Decision**: Decision/notice previews are redacted at construction and checked
again before API/watch/UI/export output.

**Rationale**: Multiple surfaces consume these records. Early redaction avoids
persisting Hideout control-plane material; output checks catch regressions and
provider-specific preview additions.

**Alternatives considered**:

- Redact only at UI rendering: rejected because API/watch/export could leak.
- Redact only at export: rejected because local UI/API are evidence surfaces
  and must not expose control-plane credentials.

## Decision 10: Gate 0 Is The Required Gate

**Decision**: 012 requires Gate 0, package tests, a decision-center smoke, and
targeted Manager/daemon/UI tests. It does not require real Lima.

**Rationale**: 012 changes local coordination and authority routing, not backend
prepare, DNS mediation, browser opener, or HostFS guest data-plane semantics.

**Alternatives considered**:

- Gate 2/3 by default: rejected because it would not prove this feature's local
  state/claim semantics.
- Unit tests only: rejected because CLI/API/daemon/UI coordination must be
  proven end to end locally.
