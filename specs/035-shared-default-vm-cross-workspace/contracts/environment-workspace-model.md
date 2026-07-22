# Contract: Environment And Workspace Model

<!-- markdownlint-disable MD013 MD060 -->

## Selection

For every run, Core chooses exactly one mode before backend side effects:

| Request | Mode | Record identity | Workspace behavior |
|---------|------|-----------------|--------------------|
| No flag, promoted macOS arm64 Lima | `shared` | Stable profile slot | Dynamic exact session attachment |
| Explicit `--env <name>` | `dedicated` | Named record | Pinned exact project and distinct Lima instance |
| Native or unpromoted reusable platform | `workspace-bound` | Profile plus exact project | Existing exact static project mapping |
| `--ephemeral` | Same as the corresponding no-flag platform row | Same reusable record | Same workspace transport with session-local identity |
| `--rm` | none | No record | Disposable exact mapping |

There is no fallback between rows after selection. Shared transport failure does
not create a workspace-bound or second shared record. Named project mismatch is
drift, not an implicit rebind.

## Shared Slot

The slot key is a canonical function of profile name only. Its visible label is
presentation. A machine compatibility mismatch returns typed drift and an
executable recreate/dedicated recovery; it does not alter the slot key.

## Compatibility Canonicalization

One production descriptor owns the ordered fields and canonical encoding. Tests
must independently mutate each included field and each excluded session field.
Included drift must deny reuse; excluded changes must preserve compatibility.
Static-Lima workspace access/path presentation is included only when the
environment actually bakes that mount into machine configuration; shared
Portal and native host-process presentation remains session-scoped. Raw
network credentials never enter the descriptor.

## Record Invariants

- Shared records have no host or guest project field.
- Dedicated/workspace-bound records require their explicit project binding.
- Old alpha records lacking `mode` are unsupported and produce one
  remove/recreate command.
- Environment status is not backend observation and is not workspace authority.
- `LastSessionID` and `LastCommand` are history only; no last-workspace field is
  introduced.

## Machine Configuration

Shared Lima activation accepts no selected project input and emits no project
mount, project tag, dummy directory, raw host path, or environment-level guest
workspace. Profile/runtime/session-control mounts remain permitted machine or
session infrastructure.

Workspace attach begins only after Core binds the exact environment
incarnation, session, canonical root identity, and lifecycle registration.

## Drift And Recovery

Distinct stable codes are required for:

- old record shape;
- machine compatibility drift;
- dedicated project mismatch;
- preserve-mode profile in shared selection;
- unsupported shared platform/transport; and
- external absolute project metadata incompatible with alias mode.

Every next action must be a currently parsed command. Recovery must not suggest
deleting project content or silently changing the selected root.
