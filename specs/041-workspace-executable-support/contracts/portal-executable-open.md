# Contract: Portal Executable Open

<!-- markdownlint-disable MD013 -->

## Scope

This contract applies when a Linux target executes a file through the existing
session-scoped Workspace Portal FUSE mount.

## Input Rules

1. The client validates the full kernel-provided open flag set against a closed,
   OS-specific allowlist.
2. Linux `FMODE_EXEC` is accepted as a non-semantic execution hint.
3. The arm64 kernel large-file bit and existing close-on-exec/nonblocking bits
   remain accepted local-only flags.
4. Unknown bits fail with `ENOTSUP`; invalid access modes fail with `EINVAL`.

## Wire Rules

1. `FMODE_EXEC` MUST NOT add or modify any Portal protocol bit.
2. The wire request encodes only read/write access mode and the existing
   append/create/exclusive/truncate/sync/no-follow semantics.
3. The host server continues to decode and validate its own closed wire flag
   set before opening the exact-root-resolved path.

## Boundary Invariants

- Accepting the hint grants no write, create, traversal, HostFS, host process,
  or outside-workspace authority.
- No copied executable or host-native fallback may be introduced.
- Symlink, attachment, environment, provider, session, incarnation, admission,
  and lifecycle validation remain unchanged.
- Permission, format, architecture, and interpreter failures remain ordinary
  guest execution failures.

## Required Tests

- OS-neutral encoding test: the accepted hint leaves a read-only wire request
  unchanged and fails when omitted from the supplied allowlist.
- Linux contract test: the real go-fuse `FMODE_EXEC` value plus kernel-local
  ignored flags encodes as read-only.
- Mutation proof: removing `FMODE_EXEC` from the Linux allowlist makes direct
  Portal execution fail with the observed unsupported-operation error.
- Real positive proof: an executable script and Linux arm64 binary both run
  directly from the shared Workspace Portal.
