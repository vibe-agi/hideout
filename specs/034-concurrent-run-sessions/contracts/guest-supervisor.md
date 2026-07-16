# Contract: Guest Session Supervisor

<!-- markdownlint-disable MD013 MD060 -->

## Trusted Identity

- `hideout-session-supervisor` is a fixed Go helper built for the supported
  Linux guest architecture.
- Its binary and helper manifest are packaged and checksum-verified like other
  guest helpers.
- The daemon resolves/materializes it through Core-owned helper lookup. Client,
  profile, adapter, workspace, and target data cannot choose its path or digest.
- The authenticated root-control SSH command is fixed by the Lima provider and
  does not request an SSH PTY.

## Session View

- The launcher verifies current boot identity, creates a private mount and PID
  namespace, mounts private `/proc`, and binds only the session runtime child to
  `/hideout/session`.
- Existing environment workspace transport remains directly mounted and shared
  by sessions in that pinned workspace.
- HostFS, network bootstrap, shims, and control state come only from the owning
  session child.
- The target runs as the validated non-root profile user.
- Ordinary sibling targets cannot enumerate or consume another session's
  process/control view. Guest root remains an explicit non-claim.

## Start Payload

- The first supervisor frame is strict, bounded, and versioned.
- It may carry session ID, target user, clean guest workdir, argv, explicit
  target environment, expected boot ID, and terminal descriptor.
- It cannot carry a privileged executable, launcher script, namespace flags,
  mount source outside the exact session child, setup identity, or arbitrary
  root command.
- Command existence is checked inside the session view. No host or sibling
  fallback is attempted.

## PTY And Process Ownership

- PTY mode allocates the guest PTY inside the helper, applies initial size before
  exec, sets the target process group/session correctly, and forwards PTY bytes.
- Non-PTY mode keeps stdout and stderr separate.
- Resize acts on the owned PTY only.
- Signal/cancel acts on the owned target process group only.
- The supervisor reaps the target and descendants before emitting completion.
- No target-controlled bytes are decoded as supervisor frames.

## Liveness And Cleanup

- Daemon-to-supervisor heartbeat and transport ownership are session scoped.
- EOF, heartbeat loss, cancel, or protocol error terminates the target process
  group and descendants within a bound.
- Normal completion emits one typed result, then the trusted launcher runs its
  fixed HostFS/network/session-view cleanup.
- Manager separately proves the exact session process tree absent before
  marking isolation cleanup complete.
- A daemon crash cannot leave a target intentionally detached. Restart does not
  infer liveness from stale helper or owner metadata.

## Non-Claims

- The helper does not provide guest-root containment in a shared VM.
- It does not implement cross-workspace dynamic mounts.
- It does not expose a generic root shell or remote execution service.
- It does not guarantee every terminal emulator's theme/OSC behavior; that is
  the 037 compatibility slice.
