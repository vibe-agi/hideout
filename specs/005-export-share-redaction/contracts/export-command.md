<!-- markdownlint-disable MD013 -->

# Contract: Export Command Surface

Contract for the operator-facing export/share surface. It is a product path: the
same Manager model backs CLI and Manager/WebUI (constitution: Product Path).

## CLI

- The command is `hideout audit export`, dispatched beside `hideout audit show`
  (`internal/app/app.go:3730`). It reuses the `audit show` filter options so the
  operator exports the same slice they can view.
- Selectors:
  - `--source audit|bundle|boundary-summary` (default `audit`).
  - For `audit`: `--session`, `--profile`, `--action`, `--decision`, `--limit`
    (same as `audit show`).
  - For `bundle`: `--bundle <path>`.
  - For `boundary-summary`: `--from <run/audit path>`.
- Redaction/decision inputs:
  - `--redact <selector>` — a user-data redaction selector (a detail field key or
    dotted path). MAY be repeated. Go-parsed; a selector naming a Non-Redactable
    Evidentiary Set field is rejected at parse time (fail closed, no script run).
    The parsed set is passed to the policy as `ctx.Extra["exportRedaction"]`.
  - `--policy-profile <name>` — the profile whose `audit.redact` policy applies.
    For `--source audit` it defaults to per-event resolution by the event's
    `Profile`; for `bundle`/`boundary-summary` it MUST be given to run any policy.
  - `--acknowledge-full-fidelity` — the operator accepts that residual user data
    (what the policy did not scrub) ships verbatim. It never bypasses a configured
    policy (FR-013).
  - When a terminal is present and neither a selection nor the acknowledge flag
    resolves residual user data, the command MUST print the pre-export review and
    ask for an interactive confirmation.
- Output:
  - `--out <path>` — the local artifact path (required; local path only, no URL).
- Behavior:
  - MUST read the source, apply stage 1 (control-plane strip) then stage 2
    (`audit.redact` with export semantics), enforce the Non-Redactable
    Evidentiary Set, write the artifact, and emit the meta-audit event.
  - MUST fail closed (non-zero exit, no artifact) when: the control-plane strip
    errors; user data is present and no decision is made; the `audit.redact`
    policy errors; or a selection targets a non-redactable evidentiary field.
  - MUST NOT own or perform any send/upload; the artifact is produced locally
    only.
  - An empty/filtered-to-nothing selection MUST produce a valid zero-record
    artifact and print a "0 records matched" notice (not an error).

## Policy Resolution And Application

- The `audit.redact` policy is resolved offline (no broker server): a profile's
  `Policy.ScriptRefs` filtered to the `redactAudit` entrypoint, resolved against
  `store.ProfileDir(name)`, run via `policy.Evaluator.RunAuditRedactScript`
  (`internal/policy/policy.go:427`) — the same inputs the broker uses
  (`internal/app/app.go:2424-2429`, `internal/broker/broker.go:906-926`).
- For `--source audit`, each event is redacted by its own profile's policy
  (`audit.Event.Profile`); a cross-profile slice does not apply one profile's
  policy to another's events. For `bundle`/`boundary-summary`, only
  `--policy-profile` selects a policy; absent one, no policy runs and residual
  user data is governed solely by the decision.
- Go owns the evidentiary floor independently of the script: a `--redact` selector
  naming an evidentiary field is refused before any script runs, and after the
  script Go compares the returned `details` against the input for the seven
  evidentiary keys and fails closed on any change (SC-007). The script cannot
  widen or narrow the evidentiary set.
- The applied policies' `id` and `sha256(source)`
  (`internal/broker/broker.go:924-925` pattern) are recorded in the artifact
  provenance `redactionStages`.

## Manager (typed plan/apply)

- A typed `evidence.export/plan` + `evidence.export/apply` op mirrors the existing
  Manager plan/apply surfaces (as in run/init/command-proxy/profile ops). Plan
  returns the pre-export review (what will be included vs. redacted, the decision
  required); apply performs the export and returns the artifact path + meta-audit
  reference.
- The Manager op MUST use the same Go export application as the CLI; neither is a
  raw redaction bypass, and neither owns transport.
- Plan MUST surface the required decision so a WebUI/TUI can render the conscious
  review; apply MUST fail closed identically to the CLI.

## Pre-Export Review (FR-008)

- Before an artifact is written, the operator MUST be able to see: the source and
  record count, the redaction stages that will apply, which user-data selection
  is in effect, and the residual that would ship if full-fidelity is acknowledged.
- The review is derived from authoritative export facts (not recomputed), and is
  itself redacted so the review cannot leak.

## Redaction Contract Reference

- Stage 1 and the artifact envelope are specified in
  [export-artifact.md](export-artifact.md).
- Every field printed, planned, or written passes the deterministic control-plane
  strip; the meta-audit event passes `RedactDetails` like any other audit event.
