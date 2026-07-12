# HostFS Overlay Design

<!-- markdownlint-disable MD013 -->

## Contract

HostFS Overlay is the HostFS write path that gives the guest a writable view of
selected host paths without changing the host files until the operator
explicitly applies a Manager decision.

This document follows [architecture-principles.md](architecture-principles.md)
and extends the HostFS V1 read-only model in [privacy-run-design.md](privacy-run-design.md).

Status note: 010 implements the staged overlay path for `create`, `replace`,
`append`, `truncate`, `mkdir`, `delete`, `rename`, `chmod`, and constrained
`chown` through explicit Manager plan/claim/apply. The core rule is unchanged:
guest writes stage overlay intent first, and host mutation happens only after
Go-owned apply. Workspace writes remain outside HostFS and are not blocked by
this feature.

## Discoverable Namespace And Read Approval

029 adds name visibility without adding implicit content authority. The policy
uses ordinary per-root HostFS rules:

```text
see:/absolute/path             exact node only; a directory is not enumerable
see-dir:/absolute/directory    complete immediate child names
see-tree:/absolute/directory   complete names at each lazily traversed depth
```

These selectors expose only name, coarse kind, locked state, and the generic
`discover` capability. They expose no real size, mode, owner, timestamps,
content, or symlink target. V1 rejects discover globs. Successful directory
listings are complete relative to the declared visible domain; more than 4096
entries, depth beyond 32, protected-directory failure, or child-inspection
failure returns a typed error rather than a partial list.

Target-visible errors are scoped. Outside an explicit discover domain and for
force-hidden paths, lookup remains `ENOENT`. A visible locked read returns
prompt `EACCES`; exact-directory readdir and unauthorized write do not create a
read decision. Host prerequisite failure returns typed `EIO`, and incomplete
enumeration returns `EOVERFLOW`. The Linux helper maps the validated typed error
record, never human stderr, to errno.

An eligible locked-file read creates or coalesces one five-minute
`hostfs.read` decision and returns immediately. Manager Core owns deduplication,
the eight-pending and eight-new-per-minute limits, terminal memory, claim,
approve/deny, and authenticated reopen. Target-provided reason text is labeled
untrusted, bounded, and control-plane redacted. V1 never blocks a FUSE syscall
while waiting for a person and never approves a directory read through this
flow.

Approval writes one exact canonical-file grant into private session state only
after policy, claim, canonical path, and live owner lock are revalidated. The
same running guest sees content authority on its next retry; no watcher or
polling loop is used. Positive FUSE entry and attr caches are bounded to one
second and negative caching is disabled, so ordinary granted metadata converges
within one second. Authorization is checked on every content operation and
never depends on cached mode or attr state. Symlink retarget, expiry, malformed
state, policy drift, owner loss, and cleanup all fail closed. Read-only opening
of missing provider state cannot recreate session authority.

Visibility presets expand to these same rules. `none` grants no external name
visibility; `landmarks` creates explicit one-level roots; `home-tree` requires
an acknowledgement that names may enter target or model context and adds the
shared categorized sensitive-root exclusions for review. Independently of
presets, Core compiles that single categorized exclusion source into every
effective discover policy, so a manually authored broad rule cannot bypass it.
An exact content grant remains directly usable but cannot make a
discover-denied name reappear in parent enumeration. Legacy `list:` rules are
not aliased: ordinary profile loading returns a guided migration error and an
operator must use `hideout profile fs <profile> migrate-list` to map every
legacy rule to `see-dir` or `see-tree` in one reviewed, atomic migration.

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

Overlay authorities are explicit:

```text
overlay:/absolute/file
overlay-dir:/absolute/directory
overlay-tree:/absolute/directory
overlay:/absolute/dir/*.txt
```

The user-facing word communicates that writes go to an overlay, not directly to
the host.

Avoid naming this `write:` in the product CLI because users may interpret it as
real host mutation.

Overlay follows the Go-primitive-plus-JS-decision split from
[architecture-principles.md](architecture-principles.md): a constrained JS
policy decision point may decide the overlay write disposition (allow, ask,
audit-only), while copy-up, whiteout, diff computation, apply execution, and
conflict detection always run in Go.

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

010 supports staged rename when both source and destination are covered by the
effective overlay authority. Apply revalidates source identity,
destination absence or replace semantics, symlink resolution, reserved roots, and
conflict state before any host mutation.

### chmod/chown/xattr

010 supports staged `chmod` and constrained `chown` through explicit Manager
apply. `chown` applies only when the requested owner/group does not require
additional host privilege; privilege-requiring ownership changes fail closed.
xattr and ACL mutation remain out of scope.

### Symlink

Symlink writes are high-risk. Initial V2 should deny symlink creation and deny
writes through symlinks that escape the overlay authority.

## Overlay Store

Session-scoped overlay:

```text
~/.hideout/sessions/<session-id>/hostfs-overlay/
  objects/
  objects/
  operations/
  decisions/
```

Overlay state is session-scoped to avoid surprising persistent mutations.

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

## Audit

Overlay write events include:

```text
action=host.fs.overlay.stage
path=<requested host path>
decision
ruleId
source
hostChanged=false
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
paths to the target or exported artifacts. Terminal decisions clean staged
content objects while preserving operation/decision records and audit evidence.

## Evidence

023 adds product-hardening evidence for this contract without changing HostFS
authority. `scripts/test-hostfs-decision-e2e.sh --local-fast` proves local
decision semantics, one-winner claim behavior, deny and timeout outcomes,
conflict fail-closed behavior, live-console model visibility, operation
coverage-matrix honesty, and public artifact redaction. Local-fast evidence is
not guest HostFS data-plane proof.

Real guest staging claims remain owned by Gate 2. When
`scripts/test-hostfs-decision-e2e.sh --real-gate2` runs with prerequisites, it
wraps the Lima HostFS path and proves representative file replace and directory
creation behavior: the guest sees staged content or directory state before
apply, the host lower state is unchanged before apply, and apply mutates only
the planned host path. Without prerequisites the real lane records `not-run`;
native/local-fast evidence must not satisfy real Gate 2 HostFS claims.

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

### Implemented In 010

- exact, directory, and tree overlay grants;
- content operations: create, replace, append, truncate;
- path/metadata operations: mkdir, delete, rename, chmod, constrained chown;
- same-session read/list overlay view;
- local CLI/WebUI/TUI/Manager decision surfaces;
- claim-token one-winner resolution, timeout default deny, and conflict
  detection at apply.

### Later

- real host write grants;
- cross-session persistent overlays;
- richer review, merge, and hunk-level apply workflows.

## Open Questions

- Should exact-file overlay require an initial host file to exist?
- Should overlay state ever become environment-scoped, or stay session-only?
