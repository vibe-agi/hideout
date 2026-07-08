# Contract: HostFS Overlay Store

<!-- markdownlint-disable MD013 -->

## Scope

The overlay store is session-scoped Hideout control-plane state. It records staged write operations and content objects before guest success is returned.

## Layout

Conceptual layout:

```text
<session>/hostfs-overlay/
├── objects/
├── operations/
├── decisions/
└── locks/
```

Exact filenames are implementation details. No overlay store path is exposed to the target or treated as user-facing authority.

## Durability Rule

A guest write-class operation may return success only after:

1. policy and path checks pass;
2. staged content or metadata is fully written;
3. the operation record is written;
4. the linked decision record is written;
5. required directory/file sync or equivalent durability step succeeds.

If any step fails, the guest operation fails and no host mutation occurs.

## Operation Record

Required fields:

- `version`
- `id`
- `sessionId`
- `profile`
- `backend`
- `operation`
- `requestedPath`
- `canonicalPath`
- `destinationPath`
- `grantId`
- `grantSource`
- `baseSnapshot`
- `newSnapshot`
- `contentObject`
- `preview`
- `requestedMode`
- `requestedOwner`
- `requestedGroup`
- `privilegeStatus`
- `decisionId`
- `status`
- `createdAt`
- `updatedAt`

Unknown required fields fail schema validation.

## Base Snapshot

Required fields depend on operation:

- all operations: existence, kind, mode, high-resolution mtime when available;
- regular-file content operations: size and content hash;
- metadata operations: uid/gid/mode when available;
- delete/rename: identity tuple for source and destination absence/presence;
- symlink path: link facts and canonical target facts if safely resolvable.

## Cleanup

Content objects are removed on terminal HostFS write outcomes:

- timeout;
- explicit discard;
- conflict;
- failed apply;
- successful apply.

Session termination and daemon restart do not approve pending decisions and do
not grant filesystem authority. They may preserve pending review material so an
authenticated operator can apply or deny it later. Overdue preserved decisions
default-deny on the next status or timeout-worker pass.
