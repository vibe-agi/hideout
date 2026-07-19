# Data Model: Zero-Friction Setup

<!-- markdownlint-disable MD013 -->

## Overview

038 adds no persisted product schema. It introduces wire and in-memory types
that bind an operator-visible init review to Manager-owned effects, plus
registry entries in the existing product-evidence model.

## Init Service Request

Represents normalized intent sent to Manager for either fixed setup or advanced
init.

| Field | Type | Rule |
| --- | --- | --- |
| `version` | string | Exact current init-service request version |
| `mode` | enum | `setup` or `init`; setup fixes all choices |
| `profile` | string | Valid profile name; setup uses `default` |
| `template` | string | Existing template ID; setup uses `dev` |
| `backend` | string | Existing backend name; setup uses `lima` |
| `network` | string | Existing mode; setup uses `direct` |
| `runtimeFamily` | string | Existing catalog family; setup uses `developer-standard` |
| `visibility` | object | Existing HostFS visibility inputs; setup grants none |
| `privilege` | object | Existing template privilege observation inputs |
| `toolSupply` | object | Existing explicit init supply inputs |
| `dryRun` | bool | Advanced init only; setup rejects flags |

The request contains references, not secret backing values. Unknown fields or
unsupported combinations fail closed.

## Setup State Classification

Manager-owned pure observation of the requested profile and required local
state.

| State | Meaning | Apply allowed |
| --- | --- | --- |
| `fresh` | Profile absent and prerequisites are plan-able | Yes, after confirmation |
| `ready` | Existing profile is valid; setup is terminally idempotent | No |
| `repairable` | Existing state is valid enough to identify bounded missing work | No; explicit recovery only |
| `blocked` | Malformed, unsafe, conflicting, or unprovable state | No |

`ready`, `repairable`, and `blocked` observations must not call
`LoadOrInit`, save profile metadata, materialize identity, write evidence, or
change directory mtimes.

## Init Review

Public, redacted, versioned review returned to a thin client.

| Field | Type | Purpose |
| --- | --- | --- |
| `version` | string | Review contract identity |
| `planVersion` | string | Existing InitTask plan version |
| `planDigest` | 64-hex string | Canonical semantic binding |
| `mode` | enum | `setup` or `init` |
| `state` | setup state | Fresh/ready/repairable/blocked |
| `requiresConfirmation` | bool | True only for an applicable fresh plan |
| `profile` | string | Effective profile name |
| `template` | string | Effective template |
| `backend` | string | Effective backend |
| `network` | string | Effective network mode |
| `runtime` | object | Family, revision, digest, preview status, declared size |
| `workspace` | object | Future run path `/workspace` and read/write posture |
| `otherFiles` | string | Existing HostFS visibility summary |
| `audit` | string | `always-on` |
| `notices` | list | Bounded typed disclosures and recovery guidance |

The review contains no daemon token, capability token, proxy value, machine ID,
raw host path, image URL, store path, or internal task inventory.

## Prepared Init

Manager-owned plan package transported back for apply.

| Field | Type | Purpose |
| --- | --- | --- |
| `request` | Init Service Request | Normalized semantic intent |
| `review` | Init Review | Public binding and presentation facts |
| `plan` | existing `inittask.Plan` | Exact typed effect plan |
| `observation` | object | Profile existence/state and prerequisite identity |

The client may transport but cannot manufacture or broaden this object. Apply
recomputes its canonical digest and compares it with a fresh Manager
observation under the profile mutation lock.

## Canonical Plan Projection

Stable JSON projection hashed with SHA-256.

Included:

- request and review contract versions;
- setup/init mode and setup state;
- profile, template, backend, network, workspace, and runtime provenance;
- each task's stable ID, kind, status, risk, target scope, capability boundary,
  normalized inputs, normalized outputs, and confirmation requirement;
- profile absence/presence and prerequisite identities that affect apply; and
- effect-relevant notices.

Excluded:

- display-only wording and whitespace;
- audit time and generated timestamps without authority significance;
- daemon/session/request IDs;
- absolute store paths and host usernames;
- secret backing values; and
- evidence output paths.

If a generated value affects authority, the exact reviewed value is carried in
Prepared Init and used at apply; it is not regenerated for comparison.

## Init Confirmation

Local client acknowledgment submitted with apply.

| Field | Type | Rule |
| --- | --- | --- |
| `reviewVersion` | string | Must equal prepared review version |
| `planDigest` | string | Must equal prepared semantic digest |
| `confirmed` | bool | Must be true; absence is denial |

No reusable approval token is created. EOF, non-TTY, Ctrl-C, empty input, or a
non-affirmative response produces no confirmation object.

## Apply Result

Reuses the existing InitTask result and adds a bounded presentation status.

| Status | Meaning |
| --- | --- |
| `configured` | Fresh confirmed plan applied successfully |
| `ready` | Existing valid profile observed; no apply occurred |
| `failed` | Apply or post-effect observation failed; result lists observed effects honestly |

The result distinguishes configuration readiness from real Lima execution.
Next steps are runnable product commands, not internal identifiers.

## Setup Evidence Requirement

Existing `productevidence.Requirement` entries identify feature `038` and map
stable proof IDs to FR/SC claims, layer, target, freshness, and artifact policy.
No manifest schema change is needed.

Required proof groups:

- Gate 0 intent/parity, cancellation/read-only/drift, and daemon recovery;
- packaged local PTY setup;
- real Gate 2 first-run and agent install/run;
- real Gate 2 `not-run`; and
- docs truth.

Local and `not-run` entries never satisfy real setup/Lima claims.

## State Transitions

```text
absent --prepare--> fresh --confirm/apply--> configured
   |                    |                         |
   |                    +--cancel--------------> absent
   |                    +--drift---------------> stale (no mutation)
   |
existing valid --pure prepare--> ready (terminal, no apply)
existing partial --pure prepare--> repairable (explicit recovery)
unsafe/unprovable --pure prepare--> blocked (fail closed)
```

Daemon auto-start is outside this product-state graph. It may remain running
after cancel, but conveys no setup success or target authority.
