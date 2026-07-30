# Phase 1 Data Model: Operator Observability Console

## Conventions

- IDs are opaque, validated, prefixed strings (`op_`, `ses_`, `exec_`,
  `seg_`, `risk_`).
- Times persisted on the host are UTC RFC 3339 with nanoseconds. Guest
  observations also carry boot-relative monotonic nanoseconds.
- Digests are SHA-256 over versioned canonical JSON and include a domain
  separator.
- Enums reject unknown values on mutation paths. Read paths may preserve a
  future value as unsupported and degrade the projection.
- Secret bytes, file contents, environment values, terminal input/full output,
  and packet payloads are not entities and must never enter these models.
- Every retained activity entity includes an exact `ActivityOwner`.

## Configuration domain

### ProfileProjection

The authoritative, non-secret profile state visible to every client.

| Field | Type | Meaning |
| --- | --- | --- |
| `schema` | string | `hideout.profile-projection.v1` |
| `profile` | string | Validated profile name |
| `revision` | uint64 | Monotonic commit revision |
| `contentDigest` | digest | Canonical desired profile plus secret refs |
| `desired` | ProfileDesired | Persisted desired values |
| `effective` | ProfileEffective | Runtime-observed values and snapshots |
| `transition` | Transition? | Active or last failed transition |
| `updatedAt` | time | Last committed desired mutation |

Legacy profiles receive revision `1` when first loaded by the transaction
service. An out-of-band profile file edit changes the canonical content digest;
the service atomically advances the revision before serving a plan, so a plan
cannot hide external drift.

### ClientDraft

Ephemeral client-local proposed changes. It is never authoritative and is not
persisted by the daemon.

| Field | Type | Meaning |
| --- | --- | --- |
| `profile` | string | Target profile |
| `baseRevision` | uint64 | Projection seen when editing began |
| `changes` | list of TypedChange | Closed discriminated union |
| `clientNonce` | opaque string | Correlates UI state, not authority |

`TypedChange.kind` is one of the registered Go handlers, initially
`network.posture`, `network.proxyRef`, `network.dns`,
`profile.environment`, `profile.hostfs`, `profile.commandProxy`,
`profile.commandAdapter`, or `activity.retention`.

### ConfigurationPlan

Canonical review returned by Manager.

| Field | Type | Meaning |
| --- | --- | --- |
| `schema` | string | `hideout.configuration-plan.v1` |
| `operationId` | OperationID | Stable server-issued retry identity |
| `profile` | string | Target profile |
| `baseRevision` | uint64 | Exact CAS revision |
| `baseDigest` | digest | Detects out-of-band changes |
| `canonicalChanges` | list of TypedChange | Normalized proposal |
| `diff` | list of ReviewDiff | Before/after with secret-safe values |
| `effects` | list of PlannedEffect | Typed live/deferred/restart effects |
| `blockers` | list of Blocker | Active session/capability/policy blockers |
| `warnings` | list of Warning | Explicit but non-blocking risk |
| `rollback` | RollbackPlan | Expected recovery path |
| `planDigest` | digest | Binds the entire review |
| `expiresAt` | time | Bounded review lifetime |

A plan with blockers can be displayed but cannot be applied. Secret diffs use
availability/generation labels, never values or value hashes.

### PlannedEffect

| Field | Type | Meaning |
| --- | --- | --- |
| `effectId` | string | Stable within the plan |
| `kind` | enum | Persist/stage/activate/drain/restart/cleanup |
| `scope` | enum | Profile/environment/new sessions/active connections |
| `provider` | string | Go-owned provider identifier |
| `live` | bool | Eligible without daemon/VM restart |
| `summary` | string | Secret-safe operator text |
| `proofRequired` | list of code | Required evidence before success |

### Operation

The durable idempotency and user-visible terminal record.

| Field | Type | Meaning |
| --- | --- | --- |
| `schema` | string | `hideout.operation.v1` |
| `id` | OperationID | Server-issued during plan |
| `kind` | string | Registered operation kind |
| `owner` | OwnerRef | Profile/environment/session |
| `planDigest` | digest | Reviewed plan |
| `baseRevision` | uint64 | CAS input |
| `phase` | OperationPhase | State machine below |
| `effects` | list of EffectResult | Per-effect progress/evidence |
| `result` | OperationResult? | Terminal result |
| `recovery` | Recovery | Safe next action |
| `createdAt`, `updatedAt` | time | Ordering |

Operation phases:

```text
planned -> claimed -> staging -> activating -> proving -> succeeded
                    \          \             \-> rolling-back
                     \          \----------------> failed
                      \--------------------------> cancelled
rolling-back -> rolled-back | rollback-unproved
```

Terminal retry behavior:

- same operation ID and same plan digest: return the stored result;
- same ID and a different digest/owner: reject `operation-mismatch`;
- expired unclaimed plan: reject `plan-expired`;
- crash in a nonterminal phase: reconcile provider evidence, then continue,
  roll back, or mark `recovery-required`; never blindly replay an effect.

### Transition

Separates desired, effective, and in-flight state.

| Field | Type | Meaning |
| --- | --- | --- |
| `operationId` | OperationID | Owner |
| `kind` | string | e.g. `network.route` |
| `from` | EffectiveRef | Observed starting state |
| `desired` | DesiredRef | Reviewed target |
| `phase` | string | Stage/activate/drain/prove/rollback |
| `blockers` | list | Exact active blockers |
| `evidence` | list | Non-secret observations |
| `startedAt` | time | Transition start |

At most one conflicting transition owns a profile. Non-conflicting, explicitly
commutative operations may proceed only when their handlers declare separate
keys.

## Secret domain

### SecretReference

| Field | Type | Meaning |
| --- | --- | --- |
| `ref` | string | Validated non-secret name, e.g. `local-proxy` |
| `provider` | string | `macos-keychain` or typed unsupported provider |
| `availability` | enum | `available`, `missing`, `locked`, `unavailable` |
| `generation` | uint64 | Monotonic version, not a hash of the value |
| `updatedAt` | time? | Last confirmed store mutation |
| `reason` | code? | Stable failure reason |

There is deliberately no read-value operation in the public Manager contract.
Only an internal runtime resolver can obtain bytes, and it returns a
short-lived buffer that callers clear after configuring the provider.

### KeychainEnvelope

Opaque Keychain item payload, never returned from the store boundary:

| Field | Type | Meaning |
| --- | --- | --- |
| `schema` | string | `hideout.keychain-secret.v1` |
| `generation` | uint64 | Recovery-visible version |
| `operationId` | OperationID | Crash/idempotency evidence |
| `value` | bytes | Secret value |

The envelope permits reconciliation if the daemon crashes after the Keychain
write but before the operation terminal record is flushed. Public output does
not expose the envelope or a digest of `value`.

## Workload ownership domain

### ActivityOwner

Closed union:

```text
ReusableOwner {
  environmentId,
  backend,
  backendIncarnationId,
  guestBootId
}

DisposableOwner {
  sessionId,
  guestBootId
}
```

Display names, reusable slot names, and profile names are labels only. They
cannot authorize query, retention, or deletion.

### WorkloadBoundary

| Field | Type | Meaning |
| --- | --- | --- |
| `owner` | ActivityOwner | Exact retention owner |
| `sessionId` | SessionID | Run |
| `cgroupPath` | string | Hideout-owned guest leaf |
| `cgroupId` | uint64 | Kernel cgroup identity for filtering |
| `targetUser` | string | Validated non-root target |
| `state` | enum | Boundary lifecycle state |
| `observerGeneration` | uint64 | Guest observer incarnation |
| `createdAtMono` | uint64 | Guest monotonic time |

The supervisor and observer are explicitly excluded. The target is placed
atomically in the leaf; descendants inherit it. A cgroup cannot be reused
between sessions.

Boundary states are `creating`, `observing`, `ready`, `draining`, `empty`,
`removed`, and `unproved`.

### Execution

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | ExecutionID | Stable across PID reuse |
| `sessionId` | SessionID | Workload |
| `parentExecutionId` | ExecutionID? | Attributed parent |
| `pid`, `tid` | uint32 | Guest identifiers |
| `execSeq` | uint64 | Per-boundary monotonic identity |
| `startedAtMono` | uint64 | Guest ordering |
| `startedAt` | time | Host-correlated display time |
| `executable` | string | Redacted, bounded path |
| `argv` | list of string | Redacted, bounded arguments |
| `cwd` | string? | Local-visible path or unresolved reason |
| `exit` | ExitObservation? | Code/signal/time or unknown reason |

Environment values and terminal bytes are never fields.

## Observation and activity domain

### ObservationEnvelope

Ephemeral authenticated guest-to-host message:

| Field | Type | Meaning |
| --- | --- | --- |
| `schema` | string | Wire schema |
| `owner`, `sessionId`, `cgroupId` | identity | Routing and validation |
| `observerGeneration` | uint64 | Detects restarts |
| `cpu`, `sequence` | uint64 | Ordering/loss detection |
| `monotonicNs` | uint64 | Guest time |
| `kind` | enum | Process/file/network/DNS/coverage/control |
| `payload` | closed union | Bounded metadata |

Daemon ingestion verifies the active boundary, sequence, schema, and bounds
before redaction. An invalid envelope cannot be reattributed to another owner.

### ActivityRecord

The redacted, aggregated unit presented to users.

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | string | Owner-local stable ID |
| `owner`, `sessionId` | identity | Exact scope |
| `executionId` | ExecutionID? | Actor, or explicit mediated/unknown actor |
| `kind` | enum | `process`, `file`, `connection`, `dns`, `risk`, `coverage` |
| `operation` | string | Exec/open/read/write/rename/connect/query/etc. |
| `subject` | closed union | Path/file identity, endpoint, or domain |
| `count` | uint64 | Aggregated operation count |
| `bytes` | uint64? | Kernel-observed bytes when supported |
| `firstAt`, `lastAt` | time | Aggregation interval |
| `firstSeq`, `lastSeq` | uint64 | Evidence ordering |
| `attribution` | enum | `exact`, `inferred`, `mediated`, `unknown` |
| `truncation` | list of code | Explicit bounded-field loss |
| `coverageRef` | string | Applicable interval |

Aggregation keys include owner, session, execution, operation, authoritative
file identity/normalized path or endpoint/domain, attribution, and coverage
interval. Events from different executions or coverage intervals never merge.

### FileSubject

| Field | Type | Meaning |
| --- | --- | --- |
| `path` | string? | Best local-visible resolved guest path |
| `device`, `inode`, `mountId` | integers? | Authoritative file identity |
| `pathState` | enum | `resolved`, `aliased`, `raced`, `truncated`, `unknown` |
| `fileType` | enum? | Regular/directory/symlink/socket/etc. |
| `destructive` | bool | Named rule result |

No file content is retained.

### NetworkSubject

| Field | Type | Meaning |
| --- | --- | --- |
| `transport` | enum | TCP/UDP/other |
| `destinationIp` | IP | Kernel-observed destination |
| `destinationPort` | uint16 | Kernel-observed port |
| `route` | enum | direct/proxy/mediated/unknown |
| `domain` | string? | Normalized DNS/proxy name |
| `domainAttribution` | enum | `exact`, `inferred`, `unknown` |
| `correlationReason` | code | Why the grade is valid |

Packet payload is not retained.

### CoverageInterval

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | string | Stable interval ID |
| `owner`, `sessionId` | identity | Scope |
| `subsystem` | enum | `process`, `file`, `network`, `dns` |
| `state` | enum | `Available`, `Partial`, `Unavailable` |
| `reason` | code | Stable reason registry |
| `evidence` | list of code/value | Hook/probe/drop/sequence facts |
| `startSeq`, `endSeq` | uint64? | Affected event range |
| `startedAt`, `endedAt` | time? | Affected time |

Intervals are append-only. A state change closes the previous interval before
opening the next. Missing/gap evidence may retroactively close the last
`Available` interval at the last proven sequence, but may not erase it.

Initial reason registry includes:

`observer-ready`, `observer-starting`, `observer-restarted`,
`unsupported-backend`, `cgroup-unproved`, `hook-unavailable`, `btf-missing`,
`bpf-lsm-disabled`, `fanotify-fallback`, `ring-overflow`, `sequence-gap`,
`schema-mismatch`, `path-unresolved`, `actor-unresolved`,
`encrypted-dns`, `shared-mediator`, `retention-pruned`, `redaction-dropped`,
`daemon-disconnected`, and `cleanup-unproved`.

### RiskFinding

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | RiskID | Stable owner-local identity |
| `ruleId`, `ruleVersion` | string | Named deterministic rule |
| `severity` | enum | info/low/medium/high/critical |
| `title`, `explanation` | string | Human-readable reason |
| `evidenceRefs` | list | Activity IDs only |
| `confidence` | enum | exact/inferred/limited |
| `policyStatus` | enum | allowed/denied/not-evaluated |
| `firstAt`, `lastAt`, `count` | aggregate | Lifecycle |
| `nextAction` | CommandAction? | Catalog-backed safe action |

Observed risk and policy violation are separate dimensions.

### ActivitySegment

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | SegmentID | Unique within owner |
| `owner` | ActivityOwner | Directory and deletion authority |
| `schema` | string | Framing/record version |
| `firstSeq`, `lastSeq` | uint64 | Contents |
| `firstAt`, `lastAt` | time | Query bounds |
| `bytes`, `records` | uint64 | Quota |
| `state` | enum | active/sealed/pruning/deleted/corrupt |
| `sha256` | digest? | Sealed segment integrity |
| `indexDigest` | digest? | Sealed index integrity |

An active segment can be repaired only by truncating after the last valid
CRC-framed record and opening a `Partial` coverage interval. A corrupt sealed
segment is quarantined and never silently skipped.

## Console projection domain

### OperatorSnapshot

One authoritative seed for CLI, TUI, and WebUI:

| Group | Contents |
| --- | --- |
| connection | daemon stream health and instance/generation |
| profiles | desired/effective/transition/revision |
| sessions | active command, boundary, terminal state |
| activity | recent aggregates and query cursor |
| coverage | current and recent affected intervals |
| risks | sorted active findings |
| operations | active/recent terminal operations |
| capabilities | supported provider and reason |
| nextActions | command-catalog action references |

The snapshot has `instanceId`, `schema`, and `sequence`. Event application is
valid only for the same instance and exactly increasing sequence. A gap or
disconnect changes the client projection to `STALE` and disables mutation
until a new snapshot is loaded.

### OperatorEvent

Closed event union carrying only Manager projections:

`profile.changed`, `transition.changed`, `operation.changed`,
`session.changed`, `activity.appended`, `coverage.changed`, `risk.changed`,
`capability.changed`, and `lifecycle.changed`.

UI-local selection, filters, modal drafts, and scroll position are not Manager
events.

## Lifecycle and deletion

### CleanupProof

| Field | Type | Meaning |
| --- | --- | --- |
| `owner` | ActivityOwner | Exact deletion target |
| `operationId` | OperationID | Lifecycle authority |
| `beforeSegments` | uint64 | Expected scope |
| `deletedSegments` | uint64 | Observed effects |
| `status` | enum | absent/unproved/failed |
| `observedAt` | time | Host proof |
| `reason` | code? | Failure |

Environment clean/delete/recreate and disposable session cleanup include the
activity owner as a typed lifecycle effect. A cleanup operation cannot report
success until the exact owner directory and manifest are absent. It must never
select by prefix, display name, or “current environment.”

## Cross-entity invariants

1. A configuration commit occurs only when both profile revision and content
   digest still match the reviewed plan.
2. One operation ID binds to one owner and one plan digest forever.
3. A completed retry cannot execute a provider effect again.
4. No public entity contains a secret value or secret-derived digest.
5. Existing sessions retain the snapshot captured at start; profile commits
   affect only declared live effects and future snapshots.
6. Every activity record belongs to one exact owner and at most one session.
7. An execution belongs to the session cgroup at its exec observation; PID
   alone never establishes identity.
8. `Available` is impossible across an unaccounted sequence gap or required
   missing hook.
9. Persisted activity has passed deterministic redaction; redaction failure
   drops the record and degrades coverage.
10. Retention or corruption loss is visible as a coverage interval.
11. Lifecycle success implies exact-owner activity absence.
12. A stale console cannot create, confirm, or apply an operation.
