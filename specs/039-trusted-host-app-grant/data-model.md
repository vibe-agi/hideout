# Data Model: Trusted Host-App Workspace Grant

<!-- markdownlint-disable MD013 -->

## Entity: Trusted-Host-App workspace grant

Durable operator policy authorizing a trusted (native) host-app open for one
profile + workspace + app binding. Stored on the guest-unreachable control plane.

### Storage

- Location: `profiles/<profile>/host-app-trust-grants.json` (beside the existing
  `host-app-mode.json`), under the reserved store the guest cannot reach.
- File mode `0600`; written atomically (temp + rename), like other profile
  policy files.
- A manifest holds a list of grants so one profile can trust more than one
  workspace.

### Fields (per grant entry)

| Field | Meaning | Source | Constraints |
| --- | --- | --- | --- |
| `workspaceId` | Core-derived stable workspace identity | `DeriveWorkspaceID` at grant time; equals the run's value | non-empty; opaque `wrk_…`; never a host path |
| `qualifiedAppRef` | host-app binding app reference | built-in VS Code binding | non-empty; `builtin.<pack>/<rev>/<app>` shape |
| `bindingDigest` | digest of the app binding | binding compilation | non-empty; `sha256:<hex>` |
| `grantedAt` | when the operator granted it | grant command | RFC3339 UTC |

Manifest also carries a schema version and the profile name for validation
symmetry with other profile-store manifests.

### What is deliberately absent

- No host path, host username, capability token, machine id, or raw guest argv.
- No per-run identifiers (`sessionId`, `runId`) — the whole point is that the
  grant is not run-scoped.
- No expiry timestamp — validity is by equality-match + explicit revoke (drift
  and revoke are the only invalidation paths; time-boxed expiry is out of scope).

### Validation rules

- Version must equal the current grant-manifest schema version.
- Each entry's `workspaceId`, `qualifiedAppRef`, `bindingDigest` must be non-
  empty and well-formed; a malformed manifest fails closed (treated as "no
  grants"), never as an implicit allow.
- Duplicate `(workspaceId, qualifiedAppRef, bindingDigest)` entries collapse to
  one.

### Match semantics (open time)

A run's projected open is trusted-authorized iff a stored grant entry equals the
run's `(workspaceId, qualifiedAppRef, bindingDigest)` AND the profile host-app mode is
`trusted`. Any inequality → no match → fail closed. Safe mode ignores
grants entirely.

### Lifecycle / state transitions

```text
(no grant / no hint)
   │  trusted open → fail closed + record request hint
   ▼
REQUESTED
   │  operator: hideout allow host-app code
   ▼
GRANTED ────────────────────────────────────────────────┐
   │  match at open time → trusted native launch         │
   │  (reuse in any later run)                            │
   │                                                      │
   │  workspace identity or bindingDigest drifts          │
   ▼                                                      │
NO MATCH → fail closed + record current request hint      │
                                                          │
   │  operator: hideout deny host-app code                │
   │  OR: profile host-app-mode <p> safe (drops all)      │
   ▼                                                      │
(no grant) ◄──────────────────────────────────────────────┘
```

## Entity: Trusted host-app request hint

A request hint is non-authoritative control-plane state written after a trusted
open is refused. It captures the exact Core-derived binding observed by that run
so an explicit host-side grant command can promote it without independently
recomputing a run-time app identity. Possessing or triggering a hint never
authorizes a host launch.

### Storage and key

- Location: `profiles/<profile>/host-app-trust-requests/<key>.json` under the
  guest-unreachable control-plane store.
- `<key>` is a deterministic digest of `(workspaceId, command)`, allowing
  concurrent projects and commands to coexist without a shared mutable slot.
- Each file is written atomically with mode `0600` and decoded strictly;
  absence, unknown fields, trailing data, version drift, or key mismatch fails
  closed as “no matching request”.

### Fields and lifecycle

| Field | Meaning | Source |
| --- | --- | --- |
| `version` | request/grant format generation | Core constant |
| `profile` | owning profile | selected profile |
| `command` | projected command name | immutable binding request |
| `workspaceId` | current workspace identity | Core run derivation |
| `qualifiedAppRef` | observed app binding reference | Core projection binding |
| `bindingDigest` | observed app binding digest | Core projection binding |
| `recordedAt` | observation time | control plane clock |

The grant command accepts only the hint whose profile, workspace, and command
match its independently derived inputs. Switching the profile to safe mode
removes all hints so a later trusted epoch requires a fresh run observation.
Hints are internal coordination records, not durable policy, public evidence,
or an additional authorization path.

## Relationship to existing entities

- **Profile host-app mode (`host-app-mode.json`)**: unchanged; still gates whether trusted
  is even eligible. The grant is the second, per-workspace half. Safe mode makes
  grants inert and (on switch to safe) deletes them.
- **Host-app binding (`OpenResourceBinding`)**: supplies `qualifiedAppRef` and
  `bindingDigest`; its `Access` (compiled from host-app-mode) still selects the
  ask-each-run path. The grant is what that path now consults first.
- **`GrantScope`**: already carries `WorkspaceID`, `QualifiedAppRef`,
  `BindingDigest`, `Profile` at the check point — the grant match reads these.
- **Request hint**: captures the same Core-derived binding after a refused
  trusted open but has no authority until an explicit host-side grant promotes
  an exact `(profile, workspace, command)` match.
- **Per-run projection decision**: for trusted host-app, superseded by the persistent
  grant. Other decision kinds (HostFS read/write, ask-each-run community packs)
  are unchanged.
