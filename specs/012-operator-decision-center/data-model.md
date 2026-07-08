# Data Model: Operator Decision Center

<!-- markdownlint-disable MD013 -->

## Entity: Operator Center

Local collection of actionable decisions and informational notices for one
Hideout store.

Fields:

- `version`: center schema version.
- `decisions`: action records visible to authenticated local surfaces.
- `notices`: informational records visible to authenticated local surfaces.
- `generatedAt`: time the center snapshot was produced.

Validation:

- Must never include claim tokens, overlay object paths, broker tokens, UI
  tokens, proxy values, generated machine IDs, or hidden implementation paths.
- Decisions and notices must use separate arrays and separate record versions.

## Entity: Actionable Decision

Pending or terminal authority request requiring local operator resolution.

Fields:

- `version`: decision record version.
- `id`: stable id.
- `kind`: one of `hostfs.write`, `adapter.proposal`, `evidence.share`.
- `source`: profile/session/backend/surface facts when known.
- `state`: `pending`, `claimed`, `approved`, `applied`, `denied`, `discarded`,
  `timed-out`, `failed`, or `stale`.
- `risk`: structured risk facts.
- `proposedAction`: provider-specific action summary.
- `preview`: redacted preview.
- `allowedActions`: allowed terminal actions for current state.
- `defaultOutcome`: denial/discard/no-release outcome used on timeout.
- `timeoutAt`: deadline for pending authority.
- `claim`: current claim metadata without token value/hash.
- `providerRef`: opaque provider reference used only by Manager/Core.
- `auditRef`: local evidence reference.
- `createdAt`, `updatedAt`: timestamps.

State transitions:

```text
pending -> claimed
pending -> timed-out
pending -> denied/discarded
claimed -> approved/applied
claimed -> denied/discarded
claimed -> timed-out
claimed -> failed
claimed -> stale
terminal states -> no further authority transition
```

Validation:

- `kind`, `id`, `state`, `defaultOutcome`, `timeoutAt`, `preview`, and
  `auditRef` are required.
- `claim` is present only in `claimed` state.
- Terminal states cannot be claimed or applied again.
- Provider apply must revalidate provider state at resolution time.
- Unknown kind fails closed.

## Entity: Informational Notice

Local fact/status update requiring observation or acknowledgement, not approval.

Fields:

- `version`: notice record version.
- `id`: stable id.
- `kind`: one of `privilege.status`, `background.status`, or future promoted
  notice kinds.
- `source`: profile/session/backend/surface facts when known.
- `severity`: `info`, `warning`, or `error`.
- `status`: current fact or status value.
- `payload`: structured fact payload.
- `preview`: redacted preview.
- `acknowledged`: boolean acknowledgement state.
- `acknowledgedBy`: optional local surface label.
- `acknowledgedAt`: optional timestamp.
- `auditRef`: local evidence reference.
- `createdAt`, `updatedAt`: timestamps.

Validation:

- Must not contain claim, approve, deny, discard, lease, timeout, providerRef,
  or defaultOutcome fields.
- Acknowledgement must not mutate provider state.
- Repeated notices with same stable id update status instead of creating
  unbounded duplicates, unless the kind defines event-per-occurrence semantics.

## Entity: Decision Claim

Exclusive time-bounded ownership metadata for one actionable decision.

Fields:

- `surface`: `cli`, `tui`, `webui`, `api`, or `manager-client`.
- `claimedAt`: timestamp.
- `expiresAt`: lease expiry timestamp.
- `token`: returned only in the direct claim response, never in list/status,
  watch events, audit, UI, or export.

Validation:

- A claim token is required to resolve a claimed decision.
- Expired claim cannot resolve authority.
- A second claim wins only after the previous lease has expired and state is
  still otherwise resolvable.

## Entity: Decision Resolution

Terminal result for an actionable decision.

Fields:

- `decisionId`: decision id.
- `kind`: decision kind.
- `status`: terminal status.
- `decision`: `allow`, `deny`, `discard`, `timeout`, or provider-specific
  terminal outcome.
- `reason`: redacted reason.
- `providerResult`: redacted provider result when available.
- `auditRef`: evidence reference.

Validation:

- Apply/approve requires a valid claim unless the kind explicitly supports a
  no-claim deny path.
- Timeout is recorded as a denial/discard/no-release outcome.
- Provider failure must not be converted to approval.

## Entity: Notice Acknowledgement

Operator acknowledgement for one notice.

Fields:

- `noticeId`: notice id.
- `surface`: local surface label.
- `acknowledgedAt`: timestamp.
- `auditRef`: evidence reference.

Validation:

- Requires an existing notice.
- Must not create a decision claim or provider action.

## Entity: Decision Preview

Redacted summary shown before resolution.

Fields:

- `summary`: short operator-readable text.
- `facts`: structured risk/action facts.
- `diff`: optional diff/preview fields for HostFS or export/share.
- `userDataPresent`: whether user/application data remains.

Validation:

- Must pass deterministic control-plane redaction.
- Must not include full overlay object paths, claim tokens, raw hidden store
  paths, or backend handles.

## Entity: Compatibility Route

Existing product route backed by the generic decision store.

Fields:

- `route`: existing route name such as `hostfs/write/claim`.
- `decisionId`: generic decision id.
- `compatVersion`: compatibility response version.
- `sourceOfTruth`: must identify generic decision center.

Validation:

- Compatibility route must not write independent decision state.
- Compatibility response must not expose fields hidden by generic records.
