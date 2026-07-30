# Hideout user guide

Hideout runs an unfamiliar AI or developer command inside a local VM and gives
you one place to inspect its boundary, activity, configuration, and cleanup.
The current supported release target is macOS on Apple silicon.

The selected project is writable. Direct networking does not hide your network
origin. Hideout reduces and explains the boundary; it does not make a harmful
command harmless.

## Start here

The ordinary first run is intentionally short:

```sh
hideout setup
hideout doctor
cd /path/to/project
hideout run -- git status --short
```

Replace `git status --short` with the tool you want to run only after the smoke
command succeeds:

```sh
hideout run -- claude
```

`setup` shows its intended write and defaults to No. It does not start a VM or
download a runtime. `doctor` reports the first failed prerequisite and a safe
next action.

While a command is running, open the terminal HUD in another terminal:

```sh
hideout tui
```

The main views are Overview, Activity, Config, Operations, and Help. Press `?`
for keys that apply to the current view. Press Enter on a row to inspect it.
Configuration and environment actions open a review dialog; they do not apply
on the first Enter.

For a browser presentation of the same Manager facts:

```sh
hideout ui
```

The browser URL contains a short-lived local token. Do not share it. The server
binds to loopback only.

## Finding a command

Primary help shows the supported journey, not every internal tool:

```sh
hideout help
hideout help run
hideout help search proxy
hideout help all
```

`hideout help --all` remains a compatibility alias for `hideout help all`.
Contextual help always states purpose, prerequisites, effects, safety,
recovery, and safe next commands. The complete catalog separates stable,
advanced, and unsupported lab commands.

## Proxy secrets

Do not put a proxy URL in a command argument or a long-lived environment
export. Store it through the running daemon, which prompts without echo:

```sh
hideout daemon start
hideout secret set local-proxy
hideout secret status local-proxy
hideout connect through local-proxy using 1.1.1.1
hideout show connection
```

`connect` first prints the canonical diff, live/next-attach effects, blockers,
and rollback. In a terminal it then asks for confirmation. Automation can
separate the steps with `hideout connect plan ...` followed by the exact
printed `hideout connect apply <operation-id> --yes`; an unconfirmed plan does
not change Desired state.

The value may be a URL such as `socks5://127.0.0.1:7890`, but the value belongs
in the hidden prompt—not in the command shown above. Only the reference
`local-proxy` appears in configuration, plans, output, and history.
On supported Macs, the daemon-managed secure store is your macOS Keychain.
Hideout does not copy the value into its profile or local data store.

### No daemon restart

A healthy `secret set` or `secret rotate` is accepted by the running daemon.
You do not need `hideout daemon stop`, and you do not need to recreate the VM.
Connection output tells you separately whether a change is live or waits for
the next eligible attach.

One-release compatibility can read `HIDEOUT_SECRET_<REF>` only from the
daemon's startup environment. It does not import that legacy value into the
Keychain automatically, and an export made after the daemon starts cannot
change it. Re-enter it once:

```sh
hideout secret set local-proxy
```

Then remove the old export from your shell setup. This also avoids exposing a
credential through process environments or shell history.

## Desired, effective, and pending

Configuration has four distinct facts:

- **Desired** is the profile value saved after a reviewed apply.
- **Effective** is what a currently attached workload is proved to use.
- **Transition** says whether activation is live, blocked, rolling back, or
  `pending-next-attach`.
- **Evidence** identifies the operation and proof supporting the claim.

For example:

```sh
hideout connect through local-proxy using 1.1.1.1
hideout show connection
```

The first command reviews and confirms one exact operation before Desired
changes. `--yes` is available only as an explicit non-interactive confirmation
of the plan printed by the same command or by `hideout connect plan`.

Changing Desired does not rewrite an already attached process. If that
environment has a proved live gateway, new connections can move online after
stage/probe/activate/prove while already accepted connections retain their old
route. Otherwise the next eligible attach uses the new Desired connection.
After daemon restart, stale durable route state is shown as `not-observed`
until a new gateway is proved. A failed privacy prerequisite fails closed;
Hideout does not silently fall back to direct networking.

In the TUI, choose Config with `3`, select a capability, and press Enter. The
dialog follows:

```text
Draft → Manager Plan → diff and impact → Confirm → Apply → terminal evidence
```

If another client changes the profile, the reviewed plan becomes stale and
cannot apply. Refresh and review a newly issued plan.

## Observation and retention

Hideout observes the command passed after `--` and its descendants inside the
run's workload boundary. It records metadata useful for deciding whether the
CLI behaved unexpectedly:

- command execution identity, parent/child relationship, time, and result;
- file open/read/write metadata: which process, which path, operation, count,
  and time;
- process-attributed network IP/port activity and DNS queries when the guest
  provider can prove attribution;
- explainable risk rules and their supporting event references.

Hideout does **not** record file contents, environment-variable values,
keystrokes, or a complete PTY transcript.

The Linux observer keeps every observed file mutation. To prevent loader and
library noise from overwhelming the bounded relay, it filters non-mutating
`open`/`read`/`mmap` activity under `/bin`, `/sbin`, `/usr`, `/lib`, `/lib64`,
`/proc`, `/sys`, and `/dev`, plus `/etc/ld.so.cache`. Reads of workspaces,
homes, profile/HostFS paths, temporary files, and other configuration paths
remain visible. File coverage therefore stays `Partial` with
`system-runtime-read-noise-filtered` evidence; this view is not a replacement
for a complete OS audit trail.

Use:

```sh
hideout activity summary
hideout activity events --session <id>
hideout activity executions --session <id>
hideout activity risks --session <id>
hideout activity coverage --session <id>
```

`--session` always means that one run, including when several runs share a
reusable environment. Use `--environment <id> --incarnation <id>` only when you
intentionally want the retained history for the whole exact VM incarnation.

Coverage is a result, not decoration:

- `Available` means the requested subsystem and window are supported and no
  known loss invalidates the claim.
- `Partial` names loss, truncation, provider gaps, or an attribution limit.
- `Unavailable` means absence of events must not be interpreted as absence of
  behavior.

Local activity belongs to an exact environment/VM incarnation. It is stored
privately for the current user and lasts for that environment lifecycle.
Cleaning or recreating the environment removes its retained observations.
Capacity limits are bounded; truncation changes coverage to Partial rather than
pretending the record is complete.

The default is 256 MiB per exact owner with no wall-clock TTL
(`owner-lifecycle`), plus a read-only 1 GiB store safety ceiling and a bounded
active-segment allowance. A configured TTL prunes sealed segments, not
individual records. Retention changes apply to future owners; an already
attached reusable environment keeps its effective bound policy until it is
cleaned or recreated. `hideout doctor --feature activity` shows current
coverage, owner/global headroom, TTL posture, and any pruning or corruption.

Only the launched command and descendants in the run workload boundary are in
scope. An unrelated host process or another VM workload must not be attributed
to that command.

## Environments and cleanup

Inspect exact identities before lifecycle changes:

```sh
hideout env list
hideout env inspect <name>
hideout stop --dry-run <environment-id>
hideout stop <environment-id>
hideout clean --dry-run <environment-id>
hideout clean <environment-id>
```

Stop retains environment data. Clean deletes the selected environment runtime
and its incarnation-bound retained observation data. Clean is not reversible.
Active sessions, active workspace views, unproved ownership, or a stale plan
block apply.

The TUI Overview key `e` opens the same exact-target stop/clean plan. Clean
requires typing the exact environment ID; it is not a broad one-key delete.

## Reviewing and sharing data

Local paths are useful evidence and are visible locally. Hideout cannot guess
whether every project path is personally sensitive. Sharing is therefore a
separate review boundary:

```sh
hideout support report --out ./hideout-support.json
hideout audit export --source boundary-summary --out ./boundary.json
```

Exports deterministically remove known Hideout secrets, URI user information,
authentication fields, sensitive arguments/query values, and control-plane
tokens. Review the resulting file before sending it. Full-fidelity export
requires a separate explicit acknowledgement.

The shareable support report contains no activity record, activity path,
command argv, domain, IP, or exact activity owner ID. A boundary summary carries
only the categorical observation/privacy/retention contract. It does not prove
that an empty activity query means no behavior; that conclusion still requires
`Available` coverage for the relevant subsystem and whole time window.

## Update and uninstall

Homebrew installations should be managed by Homebrew:

```sh
brew upgrade vibe-agi/tap/hideout
brew reinstall vibe-agi/tap/hideout
brew uninstall vibe-agi/tap/hideout
```

Do not repair or remove files inside the Cellar manually. A normal upgrade,
reinstall, or uninstall preserves durable Hideout state.

For a verified standalone package:

```sh
hideout package verify <prefix>
hideout package repair --prefix <prefix> --dry-run
hideout package uninstall --prefix <prefix> --dry-run
```

To delete durable state as well, first preview purge and then repeat the exact
store path as confirmation:

```sh
hideout package uninstall --prefix <prefix> --purge --dry-run
hideout package uninstall --prefix <prefix> --purge --confirm-purge <exact-store>
```

Purge is not recoverable.

## Troubleshooting

### `secret ref local-proxy is not set`

The selected connection names a reference the running daemon cannot resolve:

```sh
hideout secret set local-proxy
hideout secret status local-proxy
hideout run -- <command>
```

Use the hidden prompt. Do not fix this with a new environment export.

### Configuration plan is stale

Another client changed the profile after review. No stale plan was applied:

```sh
hideout tui
hideout show connection
```

Refresh, review the new diff, and confirm the newly issued operation.

### Capability is unsupported

There is no proved provider for that action in the current daemon:

```sh
hideout support matrix
hideout version
```

Update Hideout or choose an advertised provider. Do not use weak-isolation or
lab flags as an accidental workaround.

### TUI says STALE or disconnected

STALE preserves the last facts but disables mutations:

```sh
hideout daemon status --human
hideout daemon start
hideout tui
```

Only a new authoritative snapshot restores mutation controls.

### Activity is empty

First inspect coverage and the exact workload owner:

```sh
hideout session list
hideout activity coverage --session <id>
hideout activity summary --session <id>
```

Partial or Unavailable coverage cannot support a “nothing happened” claim.

### Clean is blocked

Inspect the exact environment and its sessions:

```sh
hideout env inspect <name>
hideout session list
hideout stop --dry-run <environment-id>
```

Do not bypass active-session, workspace-view, or ownership blockers.

### Direct networking warning

Direct mode is the setup default and does not hide your network origin. Review
the privacy connection before launching a network-sensitive command:

```sh
hideout help connect
hideout doctor --network tun2socks --proxy-secret <ref> --mediated-resolver <ip>
```

## Developer and automation path

Start with the ordinary path, then use the advanced catalog:

```sh
hideout help all
hideout explain --profile <name> --backend lima -- <command>
hideout doctor --format json
hideout activity summary --json
hideout version --json
```

Machine-readable output is explicit; the TUI is for humans. Lab commands are
unsupported and require explicit opt-in.
