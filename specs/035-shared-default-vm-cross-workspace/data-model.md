# Data Model: Shared Default VM Across Workspaces

<!-- markdownlint-disable MD013 MD060 -->

## Environment Mode

Closed enum:

- `shared`: one stable automatic Lima slot for a profile; no project binding;
- `dedicated`: explicit named environment, one pinned project, separate Lima
  instance and boot identity; and
- `workspace-bound`: automatic environment whose backend/platform has not
  passed the shared-attachment gate, including native in 035.

Disposable `--rm` runs remain record-less and therefore have no environment
mode. `--ephemeral` is not an environment mode: it resolves the same mode and
record as the corresponding normal run while replacing only session identity
state.

## Shared Slot

| Field | Type | Rules |
|-------|------|-------|
| `id` | opaque string | Deterministic from canonical profile name and domain separator; not authority |
| `profile` | string | Canonical existing profile name |
| `displayLabel` | string | `default` for default profile or `default:<profile>`; presentation only |

The slot remains stable across project and profile edits. It selects a record;
it does not prove that record compatible.

## Machine Compatibility Identity

| Field | Type | Rules |
|-------|------|-------|
| `schema` | constant | Canonical machine-posture contract identifier |
| `machinePosture` | object | Only disk-genesis and isolation inputs |
| `backend` | string | `lima` for promoted shared mode |
| `backendConfig` | object | VM type, architecture and configuration facts |
| `runtime` | object | Exact image-content digest and guest architecture; catalog/distribution metadata is excluded |
| `targetIdentity` | object | Target OS user/UID and guest machine-id |
| `staticWorkspace` | optional object | Lima static-mount access/path presentation only; absent for shared Portal/native |
| `digest` | SHA-256 | Canonical encoding digest; no raw credential |

Excluded: shared project paths, distribution URL/catalog labels, runtime contract
metadata, command/argv, session/attachment IDs, terminal facts, HostFS
selectors/decisions, per-session secrets, host-app grants, lifecycle IDs.

## Environment Record

| Field | Type | Rules |
|-------|------|-------|
| `id` | environment ID | Stable store identity |
| `name` | string | Stable shared slot name or explicit/deterministic non-shared name |
| `mode` | Environment Mode | Required |
| `sharedSlot` | string? | Required only for `shared` |
| `machineIdentityId` | digest | Required for reusable records |
| `bootConfigurationId` | digest | Last proved environment-global boot presentation |
| `imageRef` / `runtime` | object | Exact machine image provenance |
| `profile` / `backend` | string | Required machine facts |
| `user` / `hostname` | string | Synthetic guest identity |
| `instanceName` | string | Exact backend instance |
| `dedicatedWorkspace` | Core-only root? | Required only for `dedicated` |
| `dedicatedGuestRoot` | guest path? | Required only for `dedicated` |
| `boundWorkspace` | Core-only root? | Required only for `workspace-bound` |
| `boundGuestRoot` | guest path? | Required only for `workspace-bound` |
| `status` | enum | `new`, `created`, `ready`, `running`, `stopped`, `error`, `unsupported-version` |
| timestamps/last command | existing fields | Operational display; never workspace authority in shared mode |

Validation:

- `shared` rejects every workspace binding field;
- `dedicated` and `workspace-bound` require exactly one binding;
- old records lacking `mode` fail with the typed alpha reset recovery;
- a stable slot with a different machine identity digest is drift, not a
  second automatic record; and
- an explicit named environment rejects a different selected project.

Environment network service state is a separate strict record. It binds the
current boot ID, desired service configuration ID, resolved secret-free
configuration fingerprint, gateway ID, mode, resolver, status, and timestamps.
Changing it is an online serialized transition, not machine drift.

Each owner/session record carries its immutable session snapshot ID. The
snapshot binds profile/identity lineage, canonical declarative config, policy
source content digests, and the private Git configuration captured for that
session; it carries no mutable profile-home content or secret value.

## Workspace Identity Key

| Field | Type | Rules |
|-------|------|-------|
| `version` | constant | Key-file format only, not a product schema generation |
| `key` | 256-bit random | Private `0600`, store rooted, never serialized to status/audit |
| `createdAt` | timestamp | Operator-local metadata |

Missing key with no attachment/evidence may be initialized atomically. Missing
or corrupt key while attachment state exists fails closed; it is not silently
rotated.

## Root File Identity

Platform-canonical identity captured from the opened root. On Darwin this must
include stable filesystem/device and directory identity sufficient to detect
replacement. The accepted transport records any additional open-handle or
provider identity needed to bind operations to the same object.

Rules:

- capture follows workspace safety/canonicalization;
- path replacement between capture and transport admission invalidates attach;
- filesystems without the required stable primitive receive dedicated-mode
  recovery; and
- the raw encoding is Core-only.

## Workspace Attachment

| Field | Type | Rules |
|-------|------|-------|
| `attachmentId` | random opaque ID | Unique per attach |
| `sessionId` | session ID | Owning 034 session |
| `environmentId` | environment ID | Owning shared machine |
| `incarnation` | lifecycle incarnation | Start generation, instance and boot identity |
| `workspaceId` | opaque ID | Stable keyed root identity; correlation only |
| `canonicalHostRoot` | path | Core-only |
| `rootFileIdentity` | Root File Identity | Core-only |
| `rootHandleIdentity` | opaque? | Required by selected transport when applicable; Core-only |
| `logicalGuestRoot` | path | Exactly `/workspace` |
| `physicalGuestRoot` | path | Opaque workspace-specific session path |
| `transport` | enum | Exactly the accepted Phase R implementation |
| `providerRef` | lifecycle reference | Required |
| `guestViewRef` | lifecycle reference | Required |
| `state` | Attachment State | Required |
| `createdAt` | timestamp | Required |
| `cleanupProof` | typed observation? | Required before release after side effects |

Workspace ID derivation:

```text
"wrk_" + hex(HMAC-SHA256(
  storeWorkspaceIdentityKey,
  "hideout.workspace.identity" NUL canonicalHostRoot NUL rootFileIdentity
))
```

The full digest is used unless the final contract records a collision-bound
truncation. No presentation path independently truncates or hashes it.

## Attachment State

```text
planned
  -> provider-starting
  -> provider-ready
  -> view-mounting
  -> ready
  -> draining
  -> released
```

Failure transitions:

- before a concrete effect: `planned -> released` with typed failure;
- after an effect with proved rollback: current state -> `released` with
  failure evidence; and
- after an effect with ambiguous cleanup: current state -> `unproved`, which
  blocks attach/reuse/automatic stop for the exact incarnation.

Only the authenticated backend supervisor-ready barrier may transition the
attachment and its lifecycle resources to active/ready. Entering a method that
will start the supervisor is not proof.

## Root Relation

| Value | Meaning | Product statement |
|-------|---------|-------------------|
| `same` | Same captured root identity | Sessions intentionally collaborate; concrete provider may be reference-counted |
| `nested` | One canonical root contains the other | Ancestor has asymmetric authority; descendant cannot escape upward |
| `disjoint` | Neither root contains the other | Ordinary non-root sibling-unavailable claim applies |

Relations are informational. They do not authorize, reject, merge, or widen an
attachment.

## Workspace Provider

Transport-neutral contract:

| Field | Type | Rules |
|-------|------|-------|
| `providerId` | opaque ID | No path/control material |
| `attachment` | reference | Immutable authority input |
| `implementation` | string | Must equal accepted research artifact |
| `limits` | Limit Set | Enforced before/through operation |
| `credentialAudience` | string? | Transport-specific and session/incarnation bound |
| `state` | lifecycle state | Planned before side effect |
| `observation` | typed result | Proves ready/absent/unproved |

If a same-root share or provider is deduplicated, its owner is manager or
environment scoped and each session owns a separate binding. One binding cannot
remove the concrete provider while another remains.

## Guest Workspace View

| Field | Type | Rules |
|-------|------|-------|
| `viewId` | opaque ID | Session unique |
| `sessionId` | session ID | Required |
| `providerRef` | lifecycle reference | Required drain dependency |
| `environmentRef` | lifecycle reference | Required backend-incarnation drain dependency |
| `sessionRef` | lifecycle reference | Required run-session drain dependency |
| `logicalRoot` | path | `/workspace` |
| `physicalRoot` | path | Workspace-specific, session-private |
| `state` | lifecycle state | Planned/starting/active/draining/released/unproved |
| `absenceProof` | typed observation? | Required for independent view cleanup |

The logical entry, physical mount, and any staging root exist only in the
session namespace. A VZ multi-share staging mount is removed before target
readiness.

## Limit Set

At minimum:

- concurrent views per environment and per session;
- open files and directories per session/provider;
- in-flight requests per session and globally;
- queued bytes per session and globally;
- frame/request size;
- directory entries and page size; and
- cleanup-reserved capacity so saturation cannot block teardown.

Limit failures are typed and truthful. They do not create an approval decision
or silently fall back to another transport.

## Research Decision Artifact

| Field | Type | Rules |
|-------|------|-------|
| `schema` | constant | `hideout.workspace-research-decision/v1` |
| `feature` | constant | `035` |
| `result` | enum | `accepted` or `rejected` |
| `selectedCandidate` | string? | Exactly one complete pair when accepted |
| `candidateResults` | array | Both candidates with every gate outcome |
| `pathIdentity` | object | Logical/physical mechanism and tool observations |
| `operationMatrix` | array | Supported/unsupported plus errno/durability behavior |
| `limits` | Limit Set | Required when accepted |
| `performance` | object | Paired same-VM static-control/candidate raw samples, retained research first-byte reference, and thresholds |
| `topology` | object | Host/guest processes, control and data paths, lifecycle shape |
| `provenance` | object | Commit, dirty, host, macOS, Lima, runtime, fixtures and tool versions |
| `artifacts` | array | Relative path plus SHA-256 |
| `decisionAt` | timestamp | Required |

`accepted` requires every mandatory result passed and a clean promotable
dependency posture. It names one candidate only. `rejected` blocks Phase I and
does not alter current product behavior.

## Public Summaries

### Environment Summary

Machine-scoped fields only: environment ID, mode, shared-slot label,
compatibility ID, instance/lifecycle state, active session count, active view
count, optional transport service state, and non-secret blockers. Workspace
bindings appear only for dedicated/workspace-bound records.

### Session Summary

Environment ID, session ID, workspace ID, non-authoritative display label,
logical guest root, transport, view state, overlap notices, and non-secret
blockers. Public/shared output excludes canonical host root and control fields.

Operator-local inspect and audit may show user paths under the existing local
data and export boundary; guest, adapters, ordinary events, and shared evidence
may not.
