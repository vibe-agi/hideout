# Data Model: Community Host-App Recipes

<!-- markdownlint-disable MD013 MD060 -->

## Host-App Pack Manifest

Strict package-authored data read from an immutable installed snapshot.

| Field | Type | Rules |
|-------|------|-------|
| `schemaVersion` | string | `hideout.host-app-pack/v1` |
| `id` | string | Stable qualified package slug; cannot use built-in namespace |
| `version` | string | Informational bounded version; not authority |
| `description` | string | Untrusted annotation, bounded |
| `apps` | array | 1-16 unique app declarations |
| `bindings` | array | 1-32 unique binding declarations |
| `tests` | array | Optional deterministic quality vectors; never a security badge |
| `installHint` | object | Optional untrusted copy-only text/URL; never executed |

Unknown fields, scripts, hooks, capability-provider declarations, and arbitrary
result channels fail validation.

## App Declaration

Package expectation for one app. It does not authenticate the app.

| Field | Type | Rules |
|-------|------|-------|
| `id` | string | Pack-local slug |
| `platforms` | array | V1 contains only `darwin` |
| `bundleNames` | array | Bounded basenames only; no separators, `$`, or absolute path |
| `executableRelativePath` | string | Clean relative regular executable inside bundle |
| `expectedBundleId` | string | Optional expectation that can only narrow Core observation |
| `expectedTeamId` | string | Optional expectation that can only narrow Core observation |
| `launch` | object | Bounded elevated launch syntax; never raw invocation argv |
| `requestedSafetyProfile` | string | Optional Core-owned profile id; package cannot define it |

## Binding Declaration

Maps familiar guest command symbols to one existing capability and app.

| Field | Type | Rules |
|-------|------|-------|
| `id` | string | Pack-local stable slug |
| `commands` | array | 1-16 simple unique names; reserved/conflicting names fail enable |
| `appId` | string | References one app in the same pack |
| `capabilityId` | string | V1 fixed to `host.app.open-resource` |
| `grammar` | object | V1 strict declarative `open-resource-v1` |
| `resourceKinds` | array | `workspace`, `hostfs-portal`; at least one |
| `resultPolicy` | string | V1 fixed to `none` |
| `requestedAccess` | string | `safe` or `ask-each-run`; final access is Core/operator result |

Package and binding IDs form a qualified app identity:

```text
<pack-id>/<app-id>
```

The guest never supplies this identity.

## Source Lock

Core-observed origin of one acquired candidate.

| Field | Type | Rules |
|-------|------|-------|
| `kind` | enum | `local`, `git` |
| `localPath` | string | Operator data; intake location only, never runtime source |
| `url` | string | Exact repository source; may be user data in local audit |
| `commit` | string | Required full lowercase 40-hex for git |
| `acquiredAt` | time | Informational UTC timestamp |

Git acquisition disables system/global config, hooks, filters, prompting, and
submodule recursion. Local and git acquisition copy only bounded regular files
without symlinks or special nodes.

## Pack Revision

Immutable installed snapshot identity.

| Field | Type | Rules |
|-------|------|-------|
| `revisionId` | string | Derived from source digest |
| `packId` | string | Matches manifest id |
| `source` | Source Lock | Exact acquired source |
| `sourceDigest` | string | SHA-256 over path/content/mode canonical tree |
| `manifestDigest` | string | SHA-256 over strict manifest bytes |
| `basePermissionFingerprint` | string | SHA-256 over package-declared authority-bearing fields |
| `validationStatus` | enum | `passed`, `failed` |
| `testStatus` | enum | `not-run`, `passed`, `failed`; quality only |
| `installedAt` | time | UTC |
| `state` | enum | `installed`, `revoked` |

Snapshots live under a private Core-owned root. Runtime rechecks source digest
before compiling a binding. Source mutation outside that root has no effect.

## Permission Fingerprint Inputs

The immutable revision owns a base fingerprint over canonical sorted package
data:

```text
pack id
binding ids and command aliases
qualified app ids
bundle basenames and executable relative path
identity expectations
launch syntax
requested safety-profile id
resource kinds
grammar fields
capability and result policy
requested access policy
host-data return declaration
```

Enablement owns an effective fingerprint over the complete base input plus the
selected access posture and, for `safe`, the exact Core safety-profile id and
version. `ask-each-run` has no selected safety profile. A Core safety-profile
version change therefore suspends prior acceptance and appears in the same
bounded permission diff even when package bytes did not change.

Descriptions, tests, and installation hints affect source digest but not the
permission fingerprint. Any authority-bearing change suspends prior acceptance.

## Observed App Identity

Core-derived host facts, never package self-attestation.

| Field | Type | Rules |
|-------|------|-------|
| `qualifiedAppRef` | string | Exact pack/app identity |
| `platform` | string | V1 `darwin` |
| `rootClass` | string | One fixed allowed application root; no raw path in public view |
| `canonicalPathDigest` | string | Host-local identity only; not guest/public path |
| `bundleId` | string | Core-observed signing fact when signed |
| `teamId` | string | Core-observed signing fact when signed |
| `codeIdentity` | string | Bounded Core-observed signing identity |
| `contentDigest` | string | Core-computed canonical bundle-tree digest required for explicit unverified trust and drift check |
| `verification` | enum | `verified`, `unverified`, `absent`, `drifted`, `unsupported` |
| `ownerClass` | string | `root`, `operator`; other owners rejected |
| `safeProfileCompatibility` | array | Core-derived compatible profile ids/versions |
| `observedAt` | time | UTC |

Raw canonical bundle and executable paths remain host-local implementation
facts and are not part of guest/public models.

For an unsigned app, `contentDigest` is a versioned Merkle-style digest over
normalized relative path, entry type, permission bits, regular-file bytes, and
contained symlink target text. Core traverses from an already validated bundle
descriptor, never follows a link outside that bundle, rejects devices, sockets,
FIFOs, unsupported links, and configured count/byte limits, and verifies entry
identity before and after reading. A concurrent mutation, limit failure, or
unprovable entry yields `unsupported` or `drifted`; no trust or launch occurs.

## Core Safety Profile

Package-owned reviewed Core data, not community manifest data.

| Field | Type | Rules |
|-------|------|-------|
| `id` | string | Stable profile id |
| `version` | string | Changes invalidate prior safe compatibility |
| `identityMatchers` | array | Matches Core-observed signed family facts |
| `requiredArgv` | array | Core-supplied safe flags |
| `forbiddenArgv` | array | Effect-floor deny list |
| `isolatedState` | object | Qualified app/run directory layout |
| `requiredSettings` | map | Core-owned exact keys/values |
| `forbiddenSettings` | map | Dangerous equivalent effects denied |
| `verification` | array | Pre-launch checks over combined argv/settings/state |

Unknown or unreviewed app identity has no compatible safety profile.

## Profile App Enablement

Operator authority for future runs of one profile.

| Field | Type | Rules |
|-------|------|-------|
| `schema` | string | `hideout.host-app-enablement/v1` |
| `profile` | string | Existing profile owner |
| `packId` | string | Installed non-revoked pack |
| `revisionId` | string | Exact immutable revision |
| `bindingIds` | array | Explicit enabled binding set |
| `sourceDigest` | string | Must match revision at compile time |
| `basePermissionFingerprint` | string | Must match the immutable revision |
| `permissionFingerprint` | string | Exact accepted effective permissions, including Core safety version |
| `access` | enum | `safe`, `ask-each-run` |
| `observedIdentityDigest` | string | Exact accepted Core observation |
| `conflictReplacements` | map | Explicit old-owner to new-owner choices |
| `enabledAt` | time | UTC |
| `state` | enum | `enabled`, `suspended`, `disabled`, `revoked` |
| `reason` | string | Bounded Core-derived state reason |

### Enablement State Transitions

```text
candidate reviewed
  -> enabled

enabled
  -> suspended  (source/permission/identity/app drift)
  -> disabled   (profile operator action)
  -> revoked    (store-wide incident action or pack removal)

suspended/disabled
  -> enabled    (fresh exact review and apply)

revoked
  -> terminal for that revision
```

No transition mutates a running session's binding set.

## Immutable Run Binding

Compiled at run start from built-in and enabled exact revisions. A default-safe
binding is path-free and may be identity-deferred: its package, command,
grammar, permissions, expected app family, and exact Core safety-profile
version are immutable at run start, while the path-bearing observed app
identity is attached inside Core on first command use and revalidated at every
launch. An ask-each-run binding observes eagerly because its decision identity
includes the exact observed application.

| Field | Type | Rules |
|-------|------|-------|
| `bindingDigest` | string | Canonical digest over all fields |
| `packId` / `revisionId` | string | Exact source identity |
| `bindingId` | string | Exact binding |
| `command` | string | One owned guest command |
| `capabilityId` | string | V1 open-resource only |
| `qualifiedAppRef` | string | Derived internally |
| `grammar` | object | Immutable strict grammar |
| `resourceKinds` | array | Accepted kinds |
| `access` | enum | Safe or ask-each-run |
| `safetyProfile` | string | Exact id/version when safe |
| `identityDeferred` | boolean | Safe only; no host app observation during unrelated run startup |
| `expectedIdentitySetDigest` | string | Exact enabled community identity set when deferred |
| `profile` / `sessionId` / `environmentId` | string | Current run identity |

The shim carries command/action/binding identity; broker verifies all values
against this object. Guest intent contains only unbound resource/location/window
data.

## Unbound Open-Resource Intent

Strict guest request produced by the declarative grammar.

| Field | Type | Rules |
|-------|------|-------|
| `resources` | array | V1 exactly one unbound `{guestPath}` reference |
| `location` | object | Optional positive line/column |
| `windowMode` | enum | `reuse`, `new` |

It has no resource kind, relative path, portal ref, app, pack, binding,
capability, result policy, host path, executable, mode authority, or raw argv
field. Unknown fields fail decode. Core derives the richer Resource Reference
only after live session resolution and checks the derived kind against the
immutable binding's allowed set.

## Resource Reference

| Field | Type | Rules |
|-------|------|-------|
| `kind` | enum | `workspace`, `hostfs-portal` |
| `guestPath` | string | Canonical absolute guest path |
| `relativePath` | string | Optional bounded audit-friendly path |
| `portalRef` | string | Core-generated for HostFS; never provider token |

Workspace resolves through the session workspace mapping. HostFS portal
resolution requires current owner/session/profile plus existing content
authority, then canonical revalidation immediately before launch.

## Run-Scoped App Decision

Extends the existing host-app decision with exact app identity.

| Field | Type | Rules |
|-------|------|-------|
| `kind` | string | Generic host-app open-resource decision kind |
| `capabilityId` | string | Exact capability |
| `qualifiedAppRef` | string | Exact app |
| `packId` / `revisionId` / `bindingId` | string | Exact recipe identity |
| `command` | string | Validated command subject |
| `sessionId` / `profile` | string | Live owner |
| `workspaceId` / `environmentId` / `identityId` | string | Exact run identity |
| `resource` | object | Host-path-free class and relative target |
| `timeoutAt` | time | Existing default-deny lifecycle |
| `state` | enum | Existing pending/claimed/approved/denied/expired/revoked states |

Approval is unusable after owner loss, session end, profile/workspace/
environment drift, package update, binding disable, identity change, or HostFS
authority loss.

## Inspection View

Shared CLI/Manager/doctor read model.

| Field group | Contents |
|-------------|----------|
| Product summary | command, app display, profile, access, readiness, next action |
| Package | id, revision, source kind, source digest, test status |
| Permissions | fingerprint, accepted/current status, bounded diff |
| App identity | verified/unverified/absent/drifted/unsupported, observed IDs, root class |
| Binding | commands, resource classes, capability, grammar, result policy, shadow status |
| Safety | requested and compatible Core profile, safe/elevated posture |
| Runtime | active only in new runs, grant state, last outcome/audit ref |
| Hint | optional package-provided text clearly marked untrusted/copy-only |

Public inspection omits raw host paths, executable paths, tokens, raw argv, and
repository credentials.
