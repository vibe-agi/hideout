<!-- markdownlint-disable MD013 -->

# Data Model: Export/Share Redaction Boundary

## Export Request (operator input)

The inputs that select and govern one export. Not persisted — it drives a single
export and is reflected in the meta-audit event.

- `source`: which evidence surface — `audit` (a filtered audit slice), `bundle`
  (a release-evidence bundle directory/manifest), or `boundary-summary`.
- `selector`: for `audit`, the same filter `hideout audit show` accepts
  (`session`, `profile`, `action`, `decision`, `limit`); for `bundle`, the bundle
  path; for `boundary-summary`, the source run/audit path.
- `redactionSelection`: optional, repeatable list of detail-field selectors
  (detail keys / dotted paths) the operator wants scrubbed. Go-parsed. A selector
  naming a Non-Redactable Evidentiary Set field is rejected at parse time (fail
  closed). Passed to the policy as `ctx.Extra["exportRedaction"]`.
- `policyProfile`: optional profile name whose `audit.redact` policy applies. For
  an `audit` source it defaults to per-event resolution by `audit.Event.Profile`;
  for `bundle`/`boundary-summary` it must be given explicitly to run any policy.
- `decision`: the user-data decision — `redact` (a selection resolves it),
  `acknowledge-full-fidelity` (residual user data ships verbatim), or unset.
- `out`: the local output path for the artifact.

Rules:

- If the source carries residual user/application data and `decision` is unset
  with no non-interactive selection and no interactive confirmation, the export
  fails closed (FR-004, FR-012).
- `out` is a local path only; no destination/URL is accepted (FR-011).

## Evidence Source (read-only)

A local, full-fidelity artifact eligible for export, unchanged by the export.

- `audit slice`: `[]audit.Event` from `manager.AuditEvents(filter)`
  (`internal/app/app.go:3739`); already control-plane-stripped at write time
  (`audit.Writer.Emit`, `internal/audit/audit.go:78`), user-data verbatim.
- `release-evidence bundle`: the `hideout.release-dogfood.v1` manifest and its
  referenced logs (`schemas/release-dogfood.schema.json`); assembled by shell, so
  NOT guaranteed control-plane-stripped until export re-asserts it.
- `boundary-summary`: `manager.BoundarySummary`
  (`internal/manager/boundary_summary.go:15`), already lossy; carries an
  `auditPath` local reference that export must resolve/redact or refuse (FR-006).

## Two-Stage Redaction (the boundary's core)

Applied to every field of every exported artifact, in order.

- `stage 1 — control-plane strip`: Go-owned and unconditional. Reuses
  `audit.RedactDetails`/`RedactString`/`RedactValue`/`RedactKey`. Strips the
  `HIDEOUT_SECRET_*` namespace, `cap_`/`ui_` token values, control-plane detail
  field names (`capabilitytoken`/`brokertoken`/`uitoken`/`managertoken`), and
  generated machine-id material. Re-asserted on export regardless of source.
- `stage 2 — user-data redaction`: the `audit.redact`/`redactAudit` Goja policy
  via `RunAuditRedactScript` (`internal/policy/policy.go:427`), applied with
  export-specific semantics. Policy source is resolved offline from a profile:
  `store.Load(name).Policy.ScriptRefs` (entrypoint `redactAudit`) against
  `store.ProfileDir(name)` — for `audit`, per-event by `audit.Event.Profile`; for
  `bundle`/`boundary-summary`, from `policyProfile`. The selection enters via
  `ctx.Extra["exportRedaction"]`; the script returns redacted `details`
  (`AuditRedaction.Details`). Always runs when configured; an
  `acknowledge-full-fidelity` decision covers only residual data the policy did not
  scrub (FR-013).

Rules:

- Stage 1 is never skipped and never depends on operator input.
- Stage 2 operates only within the user-redactable field space. Go enforces the
  Non-Redactable Evidentiary Set independently of the script: a selector naming an
  evidentiary field is rejected before the script runs, and after the script Go
  compares the returned `details` against the input for the evidentiary keys and
  fails closed on any change (SC-007).
- Residual user data = returned `details` keys minus control-plane (already
  stripped) minus the evidentiary set; residual with no decision fails closed,
  with `acknowledge-full-fidelity` ships verbatim.

## Non-Redactable Evidentiary Set (Core-owned, fixed)

The fixed set of evidentiary metadata fields a redaction selection cannot delete.

- `fields`: `requestId`, `subject`, `command`, `route`, `requestedAction`,
  `status`, `error` — the set the broker preserves at
  `preserveBrokerAuditMetadata` (`internal/broker/broker.go:948`).
- `ownership`: defined once in Go and shared by the broker (restore semantics) and
  export (fail-closed semantics); the `audit.redact` policy cannot expand or
  shrink it.

Rules:

- On export, a selection targeting one of these fields fails closed — the field is
  neither silently restored (the broker's local behavior) nor emitted un-redacted
  (SC-007). The broker's local restore behavior is unchanged.

## Exported Artifact (provenance envelope)

The redacted, shareable copy produced by the boundary; conforms to
`schemas/export-artifact.schema.json`.

- `version`: artifact schema version.
- `provenance`: `source` kind, `commit` (when applicable), `createdAt`, the
  `redactionStages` applied (control-plane always; user-data policy id/sha256 when
  run), and the operator `decision` (`redact` | `acknowledge-full-fidelity`).
- `body`: the redacted evidence — redacted `[]audit.Event`, redacted bundle
  manifest+inlined logs, or redacted Boundary Summary; referenced content is
  inlined-and-redacted, never a dangling local path.
- `recordCount`: number of evidence records; `0` is valid (with a "0 records
  matched" notice), not an error (Edge Cases).

Rules:

- No Hideout-minted control-plane secret appears anywhere in the artifact
  (SC-001).
- Every provenance and body field passes stage 1.
- The artifact is a single local file; nothing moves it off-box (FR-011).

## Export Decision (recorded)

The operator's conscious choice governing user data for one export.

- `mode`: `redact` (a selection resolves the user data) or
  `acknowledge-full-fidelity` (residual ships verbatim).
- `channel`: `flag` (non-interactive) or `interactive` (terminal confirmation).
- `absent`: when user data is present and no decision is made on either channel,
  the export fails closed.

Rules:

- `acknowledge-full-fidelity` never bypasses a configured `audit.redact` policy;
  it covers only residual (FR-013, SC-008).

## Export Meta-Audit Event (evidence of the export itself)

An `audit.Event` emitted locally for each export. It is a summary of the export,
not a copy of the source evidence.

- `action`: an export action name (for example `evidence.export`).
- `decision`: the export outcome (`allow` on success, `deny` on fail-closed).
- `details`: `source` kind, `recordCount`, `redactionStages` (the policies
  applied: id/sha), the operator `decision` mode/channel, the failure reason on a
  refusal, and the `out` path. These are summary fields; the event does not embed
  any source evidence values.

Rules:

- The meta-audit event is a local audit event and follows local audit rules: the
  deterministic control-plane strip is applied at `audit.Writer.Emit`
  (`RedactDetails`), and its summary fields — including the operator-chosen `out`
  path — are recorded verbatim locally, exactly like other user/application data
  in local audit. User-data selection (export stage 2) does NOT run at local emit.
- Because it is a summary, the event embeds no source evidence content, so there
  is no source user/application data in it to leak.
- If the meta-audit event is itself later exported, it passes export stage 2 like
  any other audit event; the export boundary governs it then, not at local emit.
- A fail-closed export still emits a meta-audit event recording the refusal and
  reason (SC-005 counts every export attempt's outcome).
- No Hideout-minted control-plane secret appears in the event (via
  `RedactDetails`).
