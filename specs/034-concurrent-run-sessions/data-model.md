# Data Model: Daemon-Owned Concurrent Run Sessions

<!-- markdownlint-disable MD013 MD060 -->

## Daemon Runtime

One live process owns one Hideout store.

| Field | Meaning |
|-------|---------|
| `instanceId` | Random non-secret identity for one daemon lifetime |
| `storeRoot` | Operator-private store governed by the existing placement check |
| `apiSocket` | Existing 0600 Manager/status/events Unix socket |
| `sessionSocket` | New 0600 full-duplex run-session Unix socket |
| `startedAt` | Daemon lifetime start |
| `state` | `starting`, `serving`, `stopping`, or `stopped` |
| `credentialGeneration` | Monotonic in-memory generation, never a token value |
| `sessions` | Live session worker registry keyed by session ID |

Only one Daemon Runtime may hold the existing per-store daemon lock. The
runtime owns one Manager Core instance. A daemon process role may be entered by
the `hideout` executable; executable filename is not part of the contract.

## Operator Credential State

| Field | Meaning |
|-------|---------|
| `currentHash` | In-memory hash of the current operator token |
| `previousHash` | Prior hash accepted only during rotation grace |
| `generation` | Rotation generation |
| `issuedAt` | Current token issue time |
| `rotateAt` | Time a new token must be atomically written |
| `previousUntil` | End of bounded prior-token grace |

Raw tokens exist only in the 0600 token file, request authentication data, and
short-lived client memory. Status, events, audit, and session summaries expose
none of these fields except the non-secret generation where useful.

State transition:

```text
absent -> current(generation N)
current(N) -> current(N+1) + previous(N, grace)
previous(N, grace) -> expired
daemon stop/crash -> all generations invalid for a new daemon instance
```

## Run Session Request

The strict host-local request contains the complete existing CLI run intent.

| Field group | Fields |
|-------------|--------|
| Selection | profile, backend, network, proxy secret reference, workspace, guest workspace, environment name, ephemeral/remove flags |
| Target | argv array, public run environment map |
| HostFS | run grants, run denies, disable-profile-grants flag |
| Evidence | audit selection, verbose result preference |
| Host integration | preview targets represented as typed endpoint requests after Manager resolution |
| Confirmation | plan digest/version and explicit accepted boolean when required |
| Terminal | mode, rows, columns, bounded validated TERM |

The request carries secret *references*, never resolved proxy material. Manager
reconstructs and validates all policy from store state. Unknown fields fail.

## Terminal Descriptor

| Field | Rule |
|-------|------|
| `mode` | `none` or `pty` after resolving client `auto/always/never` |
| `rows` | 1 through 65535 for PTY; absent for none |
| `columns` | 1 through 65535 for PTY; absent for none |
| `term` | Bounded portable terminal name; defaults to `xterm-256color` |

No host path, username, hostname, shell, locale, theme, arbitrary environment,
or file descriptor identity belongs in this entity.

## Session Connection

One authenticated Unix connection owns at most one run.

| Field | Meaning |
|-------|---------|
| `connectionId` | Random non-secret daemon-local identifier |
| `credentialGeneration` | Generation accepted at authentication |
| `leaseDeadline` | Latest time a valid renewal must arrive |
| `request` | Validated Run Session Request |
| `state` | Connection state below |
| `lastRenewedAt` | Last successful operator credential proof |

```text
accepted -> authenticated -> planning -> awaiting-confirmation
authenticated/planning -> starting -> streaming -> completing -> closed
any nonterminal state -> cancelling -> closed
any malformed/auth-expired state -> refused/closed
```

Connection EOF, lease expiry, protocol error, daemon stop, or bounded write
failure transitions only its worker to `cancelling`. There is no detached state.

## Session Worker

The daemon-owned active object.

| Field | Meaning |
|-------|---------|
| `sessionId` | Existing canonical session identity |
| `connectionId` | Owning Session Connection |
| `environmentId` | Selected reusable environment |
| `workspaceId` | Hash of the pinned workspace identity |
| `profile` / `backend` | Immutable run facts |
| `terminalMode` | `none` or `pty` |
| `state` | `preparing`, `running`, `cleaning`, `completed`, or `failed` |
| `cancel` | Daemon-only cancellation function |
| `done` | Completion signal used by shutdown and tests |
| `owner` | Existing OS-backed session owner lease |
| `providers` | Per-run broker/HostFS/network/host-capability state |
| `supervisor` | One Guest Supervisor Connection |
| `completion` | Final Run Completion after target and cleanup |

Per-run credentials live inside `providers` and guest session files. They are
not fields of Daemon Runtime, Session Connection, status, or events.

## Session Wire Frame

| Field | Rule |
|-------|------|
| `type` | One byte from the closed direction-specific catalog |
| `length` | Unsigned bounded payload length |
| `payload` | Raw bytes or strict JSON defined by the frame type |

Client to daemon:

- `hello`, `run-request`, `confirm`, `stdin`, `stdin-eof`, `resize`, `signal`,
  `cancel`, `renew`.

Daemon to client:

- `hello-accepted`, `review`, `started`, `terminal`, `stdout`, `stderr`,
  `notice`, `error`, `completion`.

Daemon to supervisor:

- `supervisor-start`, `stdin`, `stdin-eof`, `resize`, `signal`, `cancel`,
  `heartbeat`.

Supervisor to daemon:

- `supervisor-ready`, `terminal`, `stdout`, `stderr`, `supervisor-error`,
  `completion`.

Frame types `0x01` through `0x7f` are mandatory closed-catalog types. Types with
the high bit set (`0x80` through `0xff`) are optional extension frames and may be
ignored only after their bounded payload is consumed. Unknown mandatory types,
invalid direction, duplicate start/completion, or oversized payload fail
closed.

## Guest Supervisor Start

| Field | Rule |
|-------|------|
| `protocol` | Exact supported supervisor protocol |
| `sessionId` | Valid canonical session ID |
| `targetUser` | Existing validated non-root profile user |
| `guestWork` | Clean absolute pinned guest workspace |
| `argv` | Non-empty NUL-free argument array |
| `env` | Validated explicit target environment assignments |
| `terminal` | Terminal Descriptor |
| `expectedBootId` | Activation receipt boot identity |
| `sessionSource` | Fixed `/hideout/runtime/sessions/<sessionId>` source |

The helper path, privileged setup executable, namespace flags, mount targets,
and cleanup commands are not request fields. They are fixed by trusted code.

## Guest Supervisor Connection

| Field | Meaning |
|-------|---------|
| `sshTransport` | Daemon-owned authenticated root-control SSH connection |
| `sessionChannel` | Non-PTY SSH exec channel running the fixed launcher |
| `state` | `starting`, `ready`, `running`, `reaping`, `completed`, or `lost` |
| `targetPid` | Guest-local process-group identity, never public status |
| `lastHeartbeat` | Daemon liveness observation |

EOF or heartbeat loss kills the target process group and descendants before
the supervisor exits. A completion is authoritative only after target reaping;
Manager cleanup proof remains separately authoritative for session-view
absence.

## Run Completion

| Field | Meaning |
|-------|---------|
| `kind` | `exit`, `signal`, `cancelled`, `protocol-error`, or `cleanup-error` |
| `exitCode` | Exact process-style client result (0-255) |
| `signal` | Portable signal name when applicable |
| `targetCompleted` | Whether supervisor proved target reaped |
| `cleanupCompleted` | Whether Manager proved ordered cleanup |
| `sessionId` | Non-secret session identity |
| `summary` | Redacted bounded operator guidance |

A target exit can be successful while cleanup fails; the final client result
must report the cleanup failure rather than false success. Raw credentials,
control paths, and target-controlled error bytes are excluded.

## Durable Owner Record

The existing strict owner record remains crash evidence. Its liveness is the
open kernel lock, not the JSON file. The daemon is the only normal process that
acquires it after 034.

```text
preparing -> running -> cleaning -> completed
preparing/running/cleaning -> failed
live lock lost + nonterminal record -> stale/unproved recovery state
```

On daemon restart, nonterminal records are inspected and reported. They are not
silently adopted or removed.

## Lock And Ownership Order

1. Daemon singleton lock.
2. Environment transition lock.
3. Session worker registry insertion/removal.
4. Session owner lock.
5. Environment shared-service state.
6. Per-run provider ownership.
7. Guest supervisor transport.

The environment transition lock is released before the target lifetime. Stop
uses the same order and refuses while any live worker/owner exists.
