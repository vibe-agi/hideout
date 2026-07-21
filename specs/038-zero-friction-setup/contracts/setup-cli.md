# Contract: Setup CLI

## Grammar

Accepted:

```text
hideout setup
```

Rejected:

```text
hideout setup <anything>
hideout setup --yes
hideout setup --force
hideout setup --no-input
```

Automation uses explicit `hideout init ... --no-input`.

## Fixed Projection

```text
profile       default
template      dev
backend       lima
runtime       developer-standard, exact catalog revision
workspace     future selected project, read/write at /workspace
network       direct
other files   hidden unless separately granted
audit         always on
```

Setup grants no HostFS visibility, trusted host-app, host-app recipe, endpoint,
adapter-pack, decision, proxy, or agent credential authority.

## Fresh Review

The initiating terminal renders one concise review before profile mutation.
It must include:

- Lima isolation;
- exact runtime revision, preview status, and declared size;
- the future project selected by `run`, not the setup working directory;
- read/write `/workspace` and hidden outside files;
- direct network and the fact that it does not hide network origin;
- audit as always on;
- setup writes configuration only; and
- setup starts no VM and downloads no runtime.

It must not include raw host paths, host usernames, store paths, internal IDs,
image URLs, daemon tokens, capability tokens, proxy values, machine IDs, or raw
task inventory.

## Confirmation

Prompt exactly once through the initiating local terminal with default `No`.
Only an affirmative response creates a confirmation. Negative input, empty
input, EOF, Ctrl-C, control-byte input, or non-TTY fails/cancels with no durable
setup state.

The daemon may be auto-started before confirmation. Its bounded socket, token,
lock, and audit runtime state is the only allowed cancellation side effect.

## Ready Output

Fresh success says configuration is ready, not that isolation has been proved.
It includes runnable commands for:

```text
hideout doctor
cd /path/to/project && hideout run -- git status --short
hideout run -- sh -lc '<exact tested agent install command>'
hideout run -- codex --version
<explicit privacy follow-up using natural connection commands>
```

The exact agent version and integrity come from the package-owned compatibility
fixture. Setup neither installs nor authenticates the agent.

## Repeated Setup

Valid existing state renders `Already set up`, effective current posture, and
next commands. It does not ask for confirmation or send apply. Customized valid
state is not compared unfavorably to setup defaults.

Partial, malformed, unsafe, or unprovable state returns typed guidance for an
explicit recovery surface and changes nothing.

## First-Run Wait

The first `hideout run` after setup owns possible runtime transfer and VM
startup. Before a long wait, it prints exact runtime family/revision, declared
size, and possible first-use download. While Lima starts, it emits bounded
elapsed-time heartbeat. It never reports estimated bytes or percentages it
cannot observe.

## Non-Claims

Setup does not prove:

- guest-root containment;
- private or mediated networking;
- workspace write filtering;
- every agent or package-manager compatibility;
- VM isolation before a real run; or
- authenticated agent state.
