# HostFS Overlay Design

<!-- markdownlint-disable MD013 -->

## Contract

HostFS Overlay is a future HostFS V2 capability that gives the guest a writable
view of selected host paths without changing the host files until the user
explicitly applies a diff.

This document follows [architecture-principles.md](architecture-principles.md)
and extends the HostFS V1 read-only model in [privacy-run-design.md](privacy-run-design.md).

## Product Goal

Allow tools and agents to behave as if they can edit selected host files while
Hideout captures all changes in an isolated overlay.

User experience:

```text
read host file -> lower host content
write host file -> overlay content
read again -> overlay content
host file -> unchanged
review diff -> apply or discard
```

This supports safe agent editing, dry-run workflows, and review-before-apply.

## Non-Goals

HostFS Overlay is not:

- direct host write access;
- a general full-disk overlay;
- a replacement for workspace write behavior;
- a synchronization engine;
- a conflict-free merge system;
- a way to bypass HostFS grants, deny rules, or reserved Hideout control-plane
  invariants.

## Layer Model

```text
Guest view
  merged HostFS view

Upper layer
  Hideout overlay store

Lower layer
  read-only host filesystem through HostFS grants
```

The host filesystem remains read-only from Hideout's perspective until an
explicit apply operation.

## Authority Model

V1 authorities:

```text
read
stat
list
dir
tree
glob read/stat
deny
```

V2 overlay authorities should be explicit:

```text
overlay:/absolute/file
overlay-dir:/absolute/directory
overlay-tree:/absolute/directory
overlay:/absolute/dir/*.txt
```

The exact CLI grammar may change, but the user-facing word should communicate
that writes go to an overlay, not the host.

Avoid naming this `write:` in the product CLI because users may interpret it as
real host mutation.

## File Semantics

### Read

If an overlay version exists, read the overlay. Otherwise read the host lower
layer.

### Write Existing File

First write copies or materializes an overlay version, then writes to the
overlay only.

### Create

Allowed only when the overlay grant includes a directory or tree authority that
covers the new path.

### Delete

Creates a whiteout record. Guest view hides the path. Host file remains.

### Rename

V2 should initially support rename within the same overlay authority. Cross-root
rename is later.

### chmod/chown/xattr

Default V2 should fail unsupported or store overlay-only metadata. It must not
modify host metadata.

### Symlink

Symlink writes are high-risk. Initial V2 should deny symlink creation and deny
writes through symlinks that escape the overlay authority.

## Overlay Store

Session-scoped overlay:

```text
~/.hideout/sessions/<session-id>/hostfs-overlay/
  objects/
  metadata.json
  whiteouts.json
  base-index.json
```

Environment-scoped overlay may be added later for persistent sandboxes:

```text
~/.hideout/environments/<environment-id>/hostfs-overlay/
```

Default should be session-scoped to avoid surprising persistent mutations.

## Base Index

The overlay must record base metadata for conflict detection:

```text
host path
size
mtime
mode
content hash, when cheap enough or required before apply
rule id
source
first read/write time
```

If the host file changes after the base index is captured, apply must detect the
conflict.

## Diff Model

Overlay diff should classify:

```text
created
modified
deleted
renamed
binary-changed
metadata-changed
conflicted
```

Commands:

```bash
hideout diff <session-id>
hideout review <session-id>
hideout apply <session-id>
hideout discard <session-id>
```

Manager API should expose the same diff model for TUI and WebUI.

## Apply Semantics

Default apply is conservative:

- fail if base changed;
- fail if target path is no longer covered by policy;
- fail if a deny rule or reserved Hideout control-plane invariant now applies;
- write via temporary file and atomic rename when possible;
- record apply audit events;
- keep a backup or rollback record when practical.

Conflict choices may be added later:

```text
skip
overwrite
manual merge
apply selected hunks
```

## Audit

Overlay write events include:

```text
action=host.fs.overlay.write
path=<requested host path>
decision
ruleId
source
hostChanged=false
overlayObject=<opaque id>
bytes
```

Apply events include:

```text
action=host.fs.overlay.apply
path=<host path>
decision
baseChanged=true|false
hostChanged=true|false
result
```

Audit must not expose overlay object paths as authority-bearing filesystem
paths to the target.

## Failure Behavior

Fail closed when:

- no overlay grant covers the path;
- a deny rule matches;
- a reserved Hideout control-plane invariant matches;
- symlink resolution escapes policy;
- overlay store cannot be created safely;
- conflict is detected during apply;
- requested write operation is unsupported.

## Implementation Shape

HostFS Overlay should extend the existing broker + guest FUSE path:

```text
guest FUSE write/create/delete/rename
  -> hostfsd
  -> Host Broker
  -> HostFS policy
  -> Overlay Store
  -> audit
```

The backend should not mount a writable host directory.

## Phase Plan

### V2a: Exact File Overlay

- overlay existing exact files;
- read own writes;
- diff modified files;
- discard session overlay;
- apply with conflict detection.

### V2b: Directory Overlay

- create files under `overlay-dir`;
- delete through whiteouts;
- filtered list merges lower and upper;
- diff created/deleted files.

### V2c: Tree And Glob Overlay

- recursive directory overlay;
- glob overlay selectors;
- rename support;
- richer review UI.

### Later

- real host write grants;
- cross-session persistent overlays;
- automatic merge;
- hunk-level apply;
- filesystem watcher integration.

## Open Questions

- Should exact-file overlay require an initial host file to exist?
- Should overlay state be tied to session or environment by default?
- How should binary diffs be represented in TUI/WebUI?
- Which editor workflow should `hideout review` support first?
