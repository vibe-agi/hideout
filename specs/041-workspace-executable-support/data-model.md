# Data Model: Workspace Executable Support

<!-- markdownlint-disable MD013 -->

## Execution Open Hint

A transient Linux FUSE input attached to an ordinary file-open request.

| Field | Type | Rules |
| ------- | ------ | ------- |
| `localFlags` | integer bitset | Kernel-provided; validated against an OS-specific closed allowlist |
| `executionHint` | boolean | Derived from `FMODE_EXEC`; ignored after validation and never sent over the Portal protocol |
| `semanticFlags` | typed bitset | Existing access mode plus append/create/exclusive/truncate/sync/no-follow only |

The execution hint grants no authority. Unknown local bits remain rejected with
`ENOTSUP`; invalid access modes remain rejected with `EINVAL`.

## Workspace Execution Capability

A product claim derived from the existing attachment and backend facts rather
than new persisted state.

| Field | Type | Rules |
| ------- | ------ | ------- |
| `backend` | enum | `lima` for the promoted claim |
| `host` | platform tuple | `darwin/arm64` for the promoted claim |
| `workspaceMechanism` | enum | `workspace-portal` for the promoted claim |
| `attachmentIdentity` | existing typed identity | Must match environment, provider, workspace root, incarnation, and session |
| `state` | derived enum | `supported` or `not-claimed`; no mutable capability record |

Static/dedicated virtiofs derives `not-claimed`. No environment schema or
configuration key is added.

## Workspace Execution Evidence

Retained, redacted proof for one exact candidate.

| Field | Type | Rules |
| ------- | ------ | ------- |
| `schema` | string | Closed 041 evidence schema identifier |
| `status` | enum | `passed` only when every required check is present and true |
| `commit` / `dirty` | source identity | Canonical 40-hex commit and `dirty=false` for promotion |
| `backend` | string | `lima` |
| `hostOS` / `hostArch` / `guestArch` | platform identity | `darwin`, `arm64`, `aarch64` |
| `workspaceMechanism` | string | `workspace-portal` |
| `checks` | closed boolean map | Direct script/binary/launcher, checkout effect, exact root, negative cases, no copy, no host fallback |
| `samples` | integer | At least 30 successful launcher executions |
| `warmFirstOutputP95Ms` | number | Positive and at most 2,000 ms |
| `nonClaims` | closed object | Static/dedicated virtiofs must equal `not-claimed` |

Evidence contains no workspace path, command arguments, output content, Portal
credential, endpoint, host home, guest temporary path, or file payload.

## Relationships And State Transitions

```text
session attachment (existing authority)
  -> FUSE OPEN with FMODE_EXEC + ordinary access mode
  -> local validation strips execution hint
  -> existing Portal OpenRequest with semantic flags
  -> existing exact-root host open
  -> kernel reads/interprets/maps file
  -> target executes in the guest
```

There is no new durable transition. Detach, provider loss, incarnation drift,
or credential mismatch continues to invalidate the existing attachment before
the host open succeeds.
