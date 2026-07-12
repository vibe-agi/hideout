# Data Model: Operator Console MVP

<!-- markdownlint-disable MD013 -->

## Console Model

Aggregated state used by WebUI and TUI.

Fields:

- `overview`: existing Manager overview.
- `decisions`: redacted actionable decision rows.
- `notices`: redacted informational notice rows.
- `doctorStatus`: cached or explicitly generated local doctor status.
- `packageStatus`: read-only package/install/prerequisite summary.
- `supportStatus`: read-only support matrix summary.
- `background`: daemon background operations.
- `hostfsWrites`: HostFS write decision summaries.
- `streamHealth`: live/stale/disconnected/credential-expired/fallback state.
- `errors`: visible panel-specific errors.

Validation:

- Derived from existing Manager/daemon facts.
- Contains no claim token or provider-private ref.
- Contains no raw staged HostFS content object data.

## Action Required Item

Decision or notice requiring operator attention.

Fields:

- `id`
- `kind`
- `type`: `decision` or `notice`.
- `status`
- `severity`
- `summary`
- `allowedActions`
- `defaultOutcome`
- `timeoutAt` or stale indicator when available.
- `nextActions`

Validation:

- Decisions expose claim/approve/deny through existing Manager routes.
- Notices expose acknowledge through existing notice route only.
- Provider-private refs and claim tokens are never rendered.

## Console Panel

Display unit in WebUI/TUI.

Panel states:

- `loading`
- `empty`
- `populated`
- `warning`
- `error`
- `denied`
- `stale-token`
- `timeout`
- `credential-expired`

Validation:

- Every required panel has an empty state.
- Errors are visible and do not trigger hidden retries with ambient authority.

## Console Action

Operator-triggered operation.

Allowed v1 actions:

- `decision.claim`
- `decision.resolve.apply`
- `decision.resolve.discard`
- `notice.ack`
- `console.refresh`
- `doctor.run-light`

Validation:

- Action names map to existing Manager/daemon routes.
- `doctor.run-light` is explicit and local.
- Package repair, environment cleanup, HostFS mutation, and backend operations are not new console actions.

## Stream Health State

Represents event refresh status.

States:

- `live`
- `idle-live`
- `stale`
- `disconnected`
- `credential-expired`
- `daemon-less`
- `fallback`

Validation:

- Healthy stream state forbids hidden interval polling.
- Fallback is visible when stream is absent or closed.
