# Backend Lifecycle Observation Contract

## Purpose

Provide one backend fact source for stop, attach, status, and daemon restart.
This contract does not verify packages, images, tools, or capability policy.

## Observation

```json
{
  "state": "running",
  "instanceName": "hideout-default-env-example",
  "bootId": "01234567-89ab-cdef-0123-456789abcdef",
  "observedAt": "2026-07-16T05:00:00Z"
}
```

Allowed states:

- `running`: expected instance is running and the current guest boot ID is
  valid and observed.
- `stopped`: expected instance exists and backend inventory reports stopped.
- `absent`: expected instance does not exist; accepted as terminal only for an
  operation whose contract allows absence.
- `unknown`: observation failed, was ambiguous, malformed, or contradicted the
  expected identity. `reasonCode` is required.

## Provider Rules

1. The observer obtains facts from backend inventory and the guest kernel, not
   from target output or `environment.Record.Status`.
2. A running Lima observation requires the instance name and `/proc/.../boot_id`
   identity already used by activation verification.
3. A changed boot ID supersedes the old incarnation. It is not silently
   accepted as the old generation.
4. Stop command success is never converted to `stopped` without observation.
5. Inventory errors, duplicate instances, malformed state, missing running boot
   ID, and timeout return `unknown`.
6. Native reports lifecycle not applicable and cannot satisfy VM-stop proof.

## Stop Transaction

Before invocation, observe `running` and require identity equality with the
stop attempt. For Lima the complete invocation plus follow-up observation is
bounded to 35 seconds: at most 30 seconds for the existing stop command and at
most 5 seconds for independent observation. Poll until:

- the same instance is `stopped`;
- allowed `absent` is observed; or
- the window ends/another result occurs, producing `unknown`.

An unknown result leaves activity `stopping-unknown` and rejects attachment
until reconciliation obtains a definitive current observation.

## Redaction

Public output may contain lifecycle state, bounded reason code, stable
environment ID, start generation, and non-secret instance label. It does not
contain command output, SSH/control paths, descriptors, PIDs, credentials,
proxy values, or arbitrary backend stderr.
