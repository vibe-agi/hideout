# Contract: Lima Guest Session View

<!-- markdownlint-disable MD013 MD060 -->

## Preconditions

The Lima provider MUST prove through the authenticated root-control SSH path:

- setup identity is root and separate from the target;
- `bash`, `unshare`, `mount`, `setpriv`, and private `/proc` work in the running
  guest;
- target user is the existing non-root profile identity;
- the selected runtime child is exactly
  `/hideout/runtime/sessions/<validated-session-id>` before the bind;
- the workspace and environment instance match the pinned record.

Missing or failed primitives deny activation. Hideout does not install them at
run time and does not fall back to a non-isolated Lima target.

Existing environment records remain inspectable and recreatable when this
probe fails; only target activation is denied.

## Namespace Construction

Trusted Go constructs a fixed quoted command that:

1. creates mount and PID namespaces and mounts a private `/proc`;
2. marks mount propagation private;
3. bind-mounts the validated session child onto `/hideout/session`;
4. runs the per-session bootstrap;
5. mounts HostFS at `/hideout/hostfs` inside this namespace when enabled;
6. checks the exact target command in the clean target `PATH`;
7. drops to the profile user and executes the original argv without reparsing.

There is no generic root command input. Session ID, target user, workdir,
environment entries, and argv are independently validated and shell-quoted by
Go.

## Transport-Liveness Guardian

Each target has one additional Core-owned session on the same authenticated
SSH transport. It receives no target stdin and no capability material. The
host sends a fixed `ping` heartbeat while the target is live and writes exactly
`done` after normal target completion. Missing heartbeat or abrupt transport
EOF invokes cleanup. Before starting `unshare`, a fixed launcher writes its PID
and Linux process start time into a root-owned guest-ephemeral record. Cleanup
validates both values and the exact session source argument in
`/proc/<pid>/cmdline`, then kills that namespace parent so
`--kill-child=KILL` terminates its descendants. It MUST NOT use a mutable target
environment, process-name, workspace-wide, user-wide, or environment-wide
selector.

The guardian is a cleanup primitive, not an owner lease or authority token.
Host owner liveness still derives only from the OS-backed owner flock. A
guardian failure is visible and fail-closed; cleanup does not remove ownership
proof until the exact session is absent from guest `/proc`.

After normal target and guardian completion, the owning authenticated SSH
transport opens one final root-control channel to prove the exact session is
absent from guest `/proc`. A successful proof is retained only on the in-memory
backend session so the later cleanup phase does not create another SSH
connection. Cancellation, transport failure, or a failed proof retains no such
state and the cleanup phase performs the original fail-closed proof.

## Visibility Contract

For ordinary target code, a sibling session's following surfaces MUST be
absent:

- `/hideout/session` runtime child and control files;
- broker credential and endpoint metadata;
- HostFS mount, grant state, staged writes, and decision artifacts;
- proxy secret and network bootstrap files;
- process IDs, command lines, descriptors, and environment through `/proc`;
- terminal input/output and signals.

The existing workspace and persistent profile state are intentionally shared.
Workspace file changes and ordinary same-user workspace conflicts are not
session-isolation failures.

## HostFS

Each enabled HostFS daemon and FUSE mount starts after the private mount
namespace exists and is destroyed with that namespace. Sibling sessions may
have different HostFS authority. Existing profile rules, decisions, read
grants, staged writes, reserved roots, and apply semantics remain unchanged.

## Terminal

- Non-TTY runs receive independent stdin/stdout/stderr and exact exit status.
- TTY runs receive one SSH PTY with the initial host rows and columns.
- Host terminal mode is restored on normal return, cancellation, and setup
  error.
- Abrupt host termination closes only the owning transport guardian; sibling
  SSH channels and targets remain live.
- SIGWINCH forwarding, full OSC/CSI fidelity, and system appearance changes
  are not 034 claims.

## Non-Claims

- Guest root may inspect or join sibling namespaces.
- 034 does not provide a VM wall between sessions.
- 034 does not dynamically attach another workspace.
- 034 does not make workspace contents private between sessions.
