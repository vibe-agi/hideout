<!-- markdownlint-disable MD013 -->

# Research: Export/Share Redaction Boundary

All decisions are grounded in the current code; file:line references are from the
codebase survey performed for this plan.

## Decision: A new `internal/export` package owns the boundary; export-time redaction application is distinct from the broker record path

**Rationale**: The broker already runs the `audit.redact` policy when recording
broker audit details (`redactAuditDetails`, `internal/broker/broker.go:901-945`),
but immediately after the script it calls `preserveBrokerAuditMetadata`
(`broker.go:947-953`), which **restores** `requestId`, `subject`, `command`,
`route`, `requestedAction`, `status`, and `error` from the pre-redaction copy.
That is correct for the local record (those fields are evidentiary and must
survive local redaction), but if export naively reused this path, an operator who
selects `command` or `route` for redaction would find it silently restored —
violating SC-006. Export therefore needs its own application of the same policy:
run the script, then enforce the Core-owned evidentiary set as **non-redactable**
(fail closed when a selection targets it) rather than **restored**. Isolating this
in `internal/export` keeps the two semantics from entangling.

**Alternatives considered**:

- Reuse `redactAuditDetails` directly on export: rejected — its restore step
  re-introduces the SC-006 conflict and hides it inside broker record semantics.
- Add a flag to `redactAuditDetails` to switch restore vs. fail-closed: rejected
  — overloads a broker record function with export-boundary policy; harder to
  test in isolation and blurs ownership.

## Decision: The non-redactable evidentiary set is Core-owned and fixed, sourced from `preserveBrokerAuditMetadata`

**Rationale**: The set `{requestId, subject, command, route, requestedAction,
status, error}` is exactly what the broker preserves as evidentiary metadata
(`broker.go:948`). Making Core own this set (not the `audit.redact` policy) means
a policy cannot widen it (to dodge user-data redaction by declaring more fields
"evidentiary") or narrow it (to strip evidence). This matches constitution
Principle II ("redaction ... remain Go-owned always"). The set should be defined
once in Go and shared by the broker (restore) and export (fail-closed) so the two
never drift.

**Alternatives considered**:

- Policy-configurable evidentiary set: rejected in clarification — a policy could
  weaken the evidentiary guarantee in either direction.
- Duplicating the literal list in `internal/export`: acceptable as a fallback but
  a shared Go constant is preferred so broker and export stay in lockstep.

## Decision: Re-assert the deterministic control-plane strip on export, even though `Writer.Emit` already applies it

**Rationale**: `audit.Writer.Emit` applies `RedactDetails` at write time
(`internal/audit/audit.go:78`), so the local audit JSONL is already
control-plane-clean. But two of the three export surfaces are assembled **outside**
that path: the release-evidence bundle is written by shell
(`scripts/test-release-dogfood.sh`) and a Boundary Summary is derived by
`SummarizeRunBoundary` (`internal/manager/boundary_summary.go:36`) and carries an
`auditPath` field (a local path). Export re-asserts the strip
(`RedactDetails`/`RedactString`/`RedactValue`) over every field of every artifact
so the guarantee holds uniformly regardless of source, and so a future change to a
source path cannot silently leak. Re-asserting an already-clean audit slice is
idempotent.

**Alternatives considered**:

- Trust the emit-time strip and skip re-assertion: rejected — the bundle and
  Boundary Summary are not produced by `Writer.Emit`, and FR-002 requires the
  guarantee on every exported artifact.

## Decision: Reuse `manager.AuditEvents` for the audit-slice read/filter; expose export as `hideout audit export`

**Rationale**: `auditShow` already reads a filtered slice via
`manager.New(store).AuditEvents(AuditEventFilter{Session, Profile, Action,
Decision, Limit})` (`internal/app/app.go:3739-3757`). The export command reuses
that exact read+filter so the operator selects the same slice they can `audit
show`, then applies export redaction and writes the artifact. Dispatch is a new
`case "export"` beside `case "show"` (`app.go:3730-3736`). A matching Manager
typed plan/apply op keeps the path from being CLI-only (constitution: Product Path
uses the same Manager model from CLI/TUI/WebUI).

**Alternatives considered**:

- A standalone top-level `hideout export`: rejected — export is an evidence
  operation and `hideout audit` already owns the evidence-read surface; grouping
  keeps discoverability and reuses the filter options.

## Decision: Resolve the `audit.redact` policy offline from a named profile; apply each event's own profile policy for a cross-profile slice

**Rationale**: The broker builds its `redactAudit` inputs from a live profile —
`ProfileDir: profileDir, ScriptRefs: p.Policy.ScriptRefs`
(`internal/app/app.go:2424-2429`), resolving script paths against `ProfileDir`
(`internal/broker/broker.go:911-912`). Offline export has no broker server, but
the same inputs are available from the store: `store.Load(name).Policy.ScriptRefs`
filtered to the `redactAudit` entrypoint, resolved against `store.ProfileDir(name)`
(`internal/profile/profile.go:282`). Export builds a `policy.Evaluator` and calls
`RunAuditRedactScript(source, "redactAudit", ctx)`
(`internal/policy/policy.go:427`) — the same call the broker makes
(`internal/broker/broker.go:926`). An audit slice can span profiles
(`audit.Event.Profile`, `internal/audit/audit.go:48`), so export applies **each
event's own profile's** `redactAudit` policy to that event rather than guessing
one policy for all. For `bundle`/`boundary-summary` (no per-record profile), the
operator names the policy with `--policy-profile`; absent one, no policy scrubbing
runs and user data is governed only by the decision. If an event's profile cannot
be resolved and the event carries residual user data with no decision, export
fails closed. The applied policies' `id` + `sha256(source)`
(`internal/broker/broker.go:924-925` pattern) are recorded in the artifact
provenance.

**Alternatives considered**:

- One `--policy-profile` applied to an entire cross-profile slice: rejected —
  silently applies one profile's redaction to another profile's events.
- Guess a "current" profile: rejected — offline export has no ambient profile.

## Decision: `--redact` selection grammar and ABI into `audit.redact`; Go owns evidentiary detection

**Rationale**: `AuditContext` exposes `Extra map[string]interface{}`
(`internal/policy/policy.go:70`) as the extension carrier, and the script signals
what it scrubbed by returning a modified `AuditRedaction.Details`
(`internal/policy/policy.go`), not a side channel. So the ABI is: (1) `--redact
<selector>` is a Go-parsed list of detail-field selectors (detail keys / dotted
paths), repeatable. (2) A selector naming a Core-owned evidentiary field is
rejected at parse time → fail closed (SC-007), before any script runs. (3) The
selection enters the script as `ctx.Extra["exportRedaction"]`; the `redactAudit`
script scrubs those fields and returns `details`. (4) After the script, Go
compares the returned `details` against the input for the seven evidentiary keys;
any change fails closed — defense-in-depth so a policy that scrubs an evidentiary
field on its own is also refused. (5) Residual user data = `details` keys minus
control-plane (already stripped) minus the evidentiary set; residual present with
no decision → fail closed, with `acknowledge-full-fidelity` → verbatim.

**Alternatives considered**:

- A script-declared "handled" flag: rejected — the returned `details` already is
  the signal; a separate flag can drift from what was actually scrubbed.
- Trusting the script for the evidentiary floor: rejected — Core must own it
  (constitution II); the Go post-script compare is the enforcement.

## Decision: Dual-track user-data decision (non-interactive flag/selection plus interactive confirmation); fail closed absent a decision

**Rationale**: Evidence is produced both interactively and in scripts/CI (the
release-evidence bundle is produced by `scripts/test-release-dogfood.sh`). A
purely interactive prompt would break scripted export; a purely flag-based path
loses the pre-export review (FR-008) for interactive operators. The command
therefore accepts a non-interactive decision (a redaction selection and/or an
explicit `acknowledge-full-fidelity` flag) and, on a terminal with neither
supplied, prints the pre-export review and asks for confirmation. When user data
is present and neither a non-interactive decision nor an interactive confirmation
resolves it, the export fails closed (FR-012).

**Alternatives considered**:

- Interactive-only: rejected — unusable for CI/scripted evidence.
- Flag-only: rejected — drops the conscious-review property for the common
  interactive case.

## Decision: A configured `audit.redact` policy always applies on export; `acknowledge-full-fidelity` covers only residual

**Rationale**: Keeping the two stages composable means the policy is not a
bypassable step. `acknowledge-full-fidelity` is the operator's decision about the
**residual** user data the policy did not scrub, not a switch that disables the
policy (FR-013, SC-008). This prevents a foot-gun where acknowledging inclusion
silently ships data the operator had already configured to redact.

**Alternatives considered**:

- Acknowledgment bypasses the policy: rejected in clarification — silently drops
  configured scrubbing.

## Decision: Export produces only a local artifact; no network/transport authority

**Rationale**: Pulling transport into 005 would add network/destination authority
to a redaction feature and widen the boundary (constitution: no unnecessary
authority). The operator performs the actual send/upload with their own tools
(FR-011). The artifact is a single local file at an operator-chosen path.

**Alternatives considered**:

- Built-in upload to a destination: rejected — adds transport authority and a
  provider surface out of scope for a redaction boundary.

## Decision: The exported artifact is a provenance envelope wrapping redacted records; referenced content is resolved-and-redacted or the export refuses

**Rationale**: FR-009 requires provenance (source, commit, redaction stages) and
FR-006 forbids emitting a reference back to un-exported local data. The artifact
is an envelope: a header carrying provenance and the applied redaction-stage
record, plus the redacted evidence body. When a source field references other
local content (a Boundary Summary's `auditPath`, an isolation-gate `auditPath` in
the release bundle), export either inlines the resolved-and-redacted content or
refuses; it never emits a dangling local path. A JSON schema
(`schemas/export-artifact.schema.json`) fixes the envelope so Gate 0 can validate
it.

**Alternatives considered**:

- Sidecar provenance file: rejected — two files are easy to separate; a single
  self-describing artifact travels better and keeps provenance attached.
- Preserve references as local paths: rejected — violates FR-006 and produces
  artifacts a recipient cannot open.

## Decision: Gate 0 plus unit tests; no real-Lima gate

**Rationale**: This is a data-handling/redaction claim, not an isolation claim, so
no backend isolation gate applies (constitution: native is a weak harness but
acceptable when no isolation is claimed). Gate 0 validates the export-artifact
schema and runs a static exported-artifact-cleanliness check (seed known
control-plane material and user data into a source, export, assert the artifact
is clean and the selection took effect). `go test ./...` covers the two-stage
redaction, the evidentiary-set fail-closed, the missing-decision fail-closed, and
acknowledge-covers-residual.

**Alternatives considered**:

- A real-Lima gate: rejected — nothing isolation-related is exercised; it would
  add cost without evidence value.
