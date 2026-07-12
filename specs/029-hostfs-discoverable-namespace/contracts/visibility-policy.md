# Contract: HostFS Visibility Policy

<!-- markdownlint-disable MD013 -->

## Public Selector Grammar

```text
selector     = exact | one-level | tree
exact        = "see:" absolute-path
one-level    = "see-dir:" absolute-path
tree         = "see-tree:" absolute-path
absolute-path = "/" path-component *("/" path-component)
```

V1 rejects `*`, `?`, and `[` glob syntax in every `see*` selector. Existing
`read:` and `stat:` glob behavior is unchanged.

| Selector | Stored op | Stored scope | Visibility |
| --- | --- | --- | --- |
| `see:/a/file` | `discover` | `exact-file` | Exact node plus synthetic ancestors |
| `see-dir:/a/dir` | `discover` | `dir` | Root and all non-hidden immediate children |
| `see-tree:/a/dir` | `discover` | `recursive-dir` | Lazy descendants through depth 32 |

`see:` may name a file, directory, symlink, or other node. If it names a
directory, lookup succeeds but readdir returns
`hostfs.directory.not-enumerable`/`EACCES`.

## Capability Separation

`discover` grants only:

- clean/canonical containment checks;
- no-follow node-kind classification;
- enumeration when selector scope permits it;
- synthetic ancestor construction.

It does not grant ordinary stat metadata, content read, execute, write,
overlay, symlink target, ownership, ACL, xattr, or search authority.

## Evaluation Order

For each path and requested operation, Core evaluates in this order:

1. Hideout reserved root: hidden for every operation, no proposal.
2. Matching operation-specific deny: denied operation cannot create a proposal.
3. Existing exact operation/staged-node authority: preserve the exact lookup
   and operation behavior needed to use that authority outside reserved roots.
4. Matching explicit or Core-injected sensitive-root discover deny: suppress
   discover allows, broad enumeration, and read proposals for remaining
   discovery-only access.
5. Matching discover allow: coarse visibility only.
6. No match: hidden.

The evaluator returns whether the path lies in an explicit discover domain.
New EACCES distinctions apply only when that value is true. Grant-implied-only
legacy visibility preserves its previous unauthorized-operation collapse.
An operator-authored exact content grant can therefore still be used when a
broad preset exclusion hides the same path from parent enumeration; the deny
does not broaden that exact visibility or create a proposal.

Core compiles the categorized
`hostpathrisk.BroadDiscoveryHiddenRoots` set into every effective policy that
contains a discover grant. This applies equally to presets, profile rules,
environment rules, and run-scoped manual selectors. Template-provided deny
rules are reviewable configuration, not the enforcement boundary.

## Directory Completeness

A successful list is complete relative to its declared visible domain.

- `see-dir:` includes every non-hidden immediate child.
- `see-tree:` applies the same rule lazily to each traversed directory.
- Force-hidden entries are outside the declared visible domain and are omitted.
- Synthetic ancestor lists are complete relative to represented explicit
  descendants.
- More than 4096 entries returns `hostfs.directory.incomplete`/`EOVERFLOW`.
- Traversal beyond 32 relative components returns the same incomplete error.
- A no-follow child classification failure or observed inconsistent directory
  result returns incomplete; it is not skipped.
- No sentinel filename or silent truncation is allowed.
- At most four enumeration calls run concurrently for one session; excess work
  returns a bounded fail-closed result and does not queue without limit.

## Coarse Result Shape

```json
{
  "name": "report.pdf",
  "kind": "file",
  "locked": true,
  "caps": ["discover"]
}
```

Allowed kinds are `file`, `dir`, `symlink`, and `other`. A locked result omits
size, mode, owner/group, timestamps, inode/device identity, extended
attributes, content, and symlink target.

## CLI And Manager Plan/Apply

Ordinary add/deny use existing product paths:

```text
hideout profile fs <profile> add \
  --fs see-dir:/absolute/path --reason <reason>

hideout profile fs <profile> deny \
  --no-fs see-tree:/absolute/path --reason <reason>
```

The Manager API uses existing authenticated routes:

```text
POST /api/v1/profile/hostfs/plan
POST /api/v1/profile/hostfs/apply
```

Request shape remains the existing profile HostFS request with the new selector
grammar. Apply rebuilds and validates the effective profile under the profile
mutation lock.

## Legacy `list:` Migration

New `list:` parsing fails with a message that explains its old grant-derived
subset/full-metadata semantics and names the migration command.

```text
hideout profile fs <profile> migrate-list \
  --map <rule-id>=see-dir \
  --map <rule-id>=see-tree \
  --reason <reason>
```

Manager operation shape:

```json
{
  "profile": "default",
  "operation": "migrate-list",
  "migrations": [
    {"ruleId": "hfs_abc", "selector": "see-dir"},
    {"ruleId": "hfs_def", "selector": "see-tree"}
  ],
  "reason": "reviewed legacy visibility migration"
}
```

Planning must:

- strict-decode the profile without granting runtime use;
- identify every legacy list-only rule;
- require exactly one mapping for every such rule and no unrelated rule;
- show old and proposed path, breadth, and metadata disclosure;
- set `requiresConfirmation=true`;
- fully validate the transformed profile before returning the plan.

Apply re-reads under the profile mutation lock, rejects drift, transforms every
mapped rule in memory, validates the complete current profile, and performs one
atomic save. No partial migration or compatibility alias is allowed.

## Presets

| Selection | Expansion | Confirmation |
| --- | --- | --- |
| `none` | No workspace-external discover rule | None beyond profile apply |
| `landmarks` | Confirmed `see-dir:` roots for selected Desktop/Documents/Downloads | Existing final interactive init confirmation |
| `home-tree` | Current home `see-tree:` plus categorized broad-discovery denies | Explicit name-disclosure acknowledgement plus plan/apply confirmation |

Privacy and omitted noninteractive selection normalize to `none`. Preset
expansion never scans the selected roots. The plan states that names are user
data and may enter target/model context.

## Compatibility

- Profiles with no `see*` rules expose no additional real entries.
- Existing read/dir/tree/stat/overlay and deny behavior remains operation
  specific.
- Workspace remains a separate direct mount and is not evaluated by discover.
- Reserved roots always win.
- Explicit operator-authored exact content grants outside reserved roots keep
  their existing content authority and only their necessary exact visibility.
