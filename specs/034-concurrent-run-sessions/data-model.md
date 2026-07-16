# Data Model: Concurrent Run Sessions

<!-- markdownlint-disable MD013 MD060 -->

## Reusable Environment

Existing environment identity remains authoritative and unchanged.

| Field | Meaning | Rule |
|---|---|---|
| `id` | Stable environment ID | Existing validation |
| `workspace` | Pinned canonical host workspace | Same for every 034 session |
| `guestWorkspace` | Existing guest path | Static mount declaration unchanged |
| `instanceName` | Existing Lima identity | One instance for all owners |
| `status` | `new`, `created`, `ready`, `running`, `stopped`, `error`, or `unsupported-version` | `new` is a projected row before persistence; `running` iff a live owner or transition exists; last owner derives terminal state |
| `lastSessionId` | Most recently attached session | Informational, never liveness proof |
| `runtimeRoot` | Private environment transport root | Host-local implementation field, never public |

The environment record does not store an authoritative session count. Counts
are derived from owner locks.

## Session Owner Record

Strict schema: `hideout.active-session/v1`.

| Field | Type | Public | Rule |
|---|---|---|---|
| `schema` | string | yes | Exact version |
| `sessionId` | string | yes | Valid generated session ID |
| `environmentId` | string | yes | Existing environment ID |
| `profile` | string | yes | Existing profile name |
| `backend` | string | yes | Existing backend name |
| `workspaceId` | string | yes | Stable digest/identity, not raw host path |
| `state` | enum | yes | `preparing`, `running`, `cleaning`, `failed` |
| `terminalMode` | enum | yes | `none` or `pty` |
| `startedAt` | timestamp | yes | UTC |
| `updatedAt` | timestamp | yes | UTC |
| `commandClass` | string | yes | Bounded executable basename, not argv |
| `cleanupError` | string | host-local | Deterministically redacted, bounded |
| `ownerLock` | open file | no | OS lifetime proof, never serialized as path |

The record lives beneath the private environment directory. The open exclusive
flock, not the JSON, proves liveness.

### Observed Owner State

| Observation | Meaning | Active count |
|---|---|---|
| Owner file is exclusively locked | `live` | yes |
| Record exists and lock can be acquired | `stale` | no |
| Record or lock cannot be safely read | `unprovable` | no; lifecycle mutation fails closed |
| No record | `absent` | no |

## Session Runtime Child

Host layout beneath the existing environment mount:

```text
runtime/sessions/<session-id>/
├── bootstrap/
├── network/
├── shims/
└── tmp/
```

Inside the target mount namespace this child is `/hideout/session`. The target
cannot walk to the parent transport root through `..` or `/proc` because the
bind mount is the namespace root for that path and `/proc` is private.

The durable host session layout remains separate and owns audit, decisions,
broker state, HostFS read-owner state, and exported evidence. Cleanup of one
runtime child never deletes a sibling child or `runtime/services`.

## Session View

| Field | Meaning | Validation |
|---|---|---|
| `sessionId` | Selected runtime child | Generated ID; no arbitrary path |
| `targetUser` | Existing profile user | Non-root and observed in guest |
| `guestWork` | Existing pinned workspace path | Must equal environment identity |
| `mountNamespace` | Private mount-table identity | Must differ from sibling/host setup namespace |
| `pidNamespace` | Private process namespace | Target observes only own tree/helper children |
| `procMount` | Private `/proc` | Must report the session PID namespace |
| `hostfsEnabled` | Whether private HostFS FUSE is mounted | Mount exists only in this namespace |
| `initialTTY` | Initial rows/columns and terminal mode | No dynamic-resize claim |

## Environment Service State

Strict schema: `hideout.environment-service/v1`.

| Field | Type | Public | Rule |
|---|---|---|---|
| `schema` | string | yes | Exact version |
| `environmentId` | string | yes | Existing environment |
| `kind` | enum | yes | V1: `network` |
| `status` | enum | yes | `starting`, `ready`, `cleaning`, `failed` |
| `configurationFingerprint` | SHA-256 | host-local | Canonical non-secret config plus resolved-secret digest |
| `mode` | string | yes | `direct` or `tun2socks` |
| `bootId` | guest boot UUID | host-local | Required for `ready`/`cleaning`; stale across a VM reboot and never trusted without runtime health verification |
| `ownerCount` | integer | derived only | Never persisted as authority |
| `startedAt` | timestamp | yes | UTC |
| `lastError` | string | host-local | Bounded/redacted |

`direct` does not create a long-lived service. For `tun2socks`, active owner
locks are the reference count. A backing secret mismatch changes the
fingerprint and denies the attach without exposing either secret.

## Environment Activation Receipt

An ephemeral strict receipt binds the currently live instance to the first
owner's successful full checks.

| Field | Meaning |
|---|---|
| `environmentId` | Existing environment |
| `instanceName` | Existing backend instance |
| `backendConfigVersion` | Existing pinned config version |
| `runtimeIdentityDigest` | Existing runtime instance expectation digest |
| `namespaceProbe` | Required tools and actual root-control probe passed |
| `ownerSessionId` | First live owner that established receipt |
| `observedAt` | UTC |

The receipt is usable only while its owner is proved live. With zero live
owners, the next run performs full runtime observation again.

## Lifecycle State Machine

```text
attach requested
  -> transition lock
  -> reconcile stale owners
  -> validate environment and service fingerprint
  -> create owner(preparing) + acquire owner lock
  -> start/verify VM or validate live activation receipt
  -> activate environment service if first owner
  -> release transition lock
  -> create session namespace
  -> owner(running)
  -> target exits/cancels
  -> transition lock
  -> owner(cleaning)
  -> clean own namespace/data plane/runtime child
  -> clean shared service only when no sibling owner is live
  -> owner close/remove
  -> environment running if siblings remain, otherwise ready/error
  -> transition unlock
```

Any failure before owner creation leaves no owner. Any failure after owner
creation follows the same cleanup path. If cleanup cannot be proved complete,
the owner record becomes `failed`, the environment becomes `error` when no
sibling remains, and status exposes typed recovery without claiming success.

## Lock Ordering

To prevent deadlocks, all code uses this order:

1. environment transition lock;
2. environment service state lock;
3. session owner lock (created/held, never waited on while probing);
4. existing decision/HostFS provider locks.

Status probes never take the transition lock. They open owner files
non-blockingly and report `unprovable` rather than waiting.

## Public Session Summary

The Manager/CLI summary is derived from owner records and existing audit:

```json
{
  "id": "ses_...",
  "environmentId": "env_...",
  "profile": "default",
  "backend": "lima",
  "state": "running",
  "ownerStatus": "live",
  "terminalMode": "pty",
  "startedAt": "2026-07-16T12:00:00Z",
  "commandClass": "bash"
}
```

It omits host paths, owner-lock paths, PIDs, broker endpoints, tokens, proxy
references/material, HostFS claim tokens, and raw argv.
