# Contract: Migration Operator Experience

## Product language

Use these terms consistently:

- **Configuration only**: portable profile settings and environment definitions;
  no VM disk or profile application-state data.
- **Full VM state**: configuration, the selected profile's persistent
  `home`/`config`/`data`/`browser` state, and every selected persistent VM disk.
- **Safe Clone**: recommended import; keeps files but creates new guest identity.
- **Exact Guest Restore**: advanced import; keeps guest identity and may conflict
  if another copy runs.
- **Host folders are not copied**: workspace/HostFS mount contents remain on the
  source host and must be mapped on the destination.
- **Sealed bundle**: complete, encrypted, authenticated, and reusable.
- **Staged**: copied but not active or runnable.

Avoid unexplained terms such as adoption, binding, effect, revision, or claim in
the primary flow. They may appear in an expandable technical-details view.

## Command catalog

```text
hideout migrate
hideout migrate export
hideout migrate inspect BUNDLE
hideout migrate import BUNDLE
hideout migrate status [OPERATION]
hideout migrate resume OPERATION
hideout migrate cancel OPERATION
hideout migrate recover OPERATION
```

`hideout migrate` with an interactive terminal opens the migration wizard. Without
an interactive terminal it prints a short purpose statement, the three common
examples below, and exits without mutation.

### Top-level help

The first screen must answer what is copied, what is not copied, and the safe path:

```text
Move or copy Hideout environments to another computer.

Full VM state keeps files stored inside the VM and persistent profile application
state. Profile caches and host workspace folders are not copied; you map host
folders again when importing. Bundles are encrypted and reusable.

Common tasks:
  hideout migrate export --environment dev --mode full \
    --out dev.hideout-migration --ack-guest-content --preview
  hideout migrate inspect dev.hideout-migration
  hideout migrate import dev.hideout-migration

Run `hideout migrate <command> --help` for options and more examples.
```

The command catalog, CLI, TUI help overlay, and WebUI help drawer consume the same
structured help entries. Hideout continues to use its own command catalog and flag
sets; no Cobra dependency is introduced.

## Export CLI

Primary flags:

```text
--mode config|full          What to copy; defaults to full
--environment NAME         Environment to include; repeatable
--all                      Select every environment; mutually exclusive with --environment
--out PATH                 New bundle path; existing files are never overwritten
--include-secret REF       Include one exportable secret value; repeatable and opt-in
--ack-secret-transfer      Required with every explicit secret-value selection
--ack-guest-content        Required for full mode after sensitive-content review
--preview                  Validate and print the immutable plan without starting
--yes                      Apply the exact displayed, unblocked plan
--json                     Machine-readable, redacted output
```

Rules:

- `--all` means every current environment, not “all eligible environments.” In
  full mode, a running, unknown, or unsupported member blocks the plan and the
  output names it with a remediation. Nothing is silently omitted.
- Export never stops an environment automatically. The review gives the exact
  `hideout environment stop ...` command when stopping is required.
- With an interactive terminal, missing choices open prompts and every apply shows
  a review/confirmation screen.
- Without a terminal, missing selection, output, or an explicit `--preview`/`--yes`
  decision is an error with a copyable example. Apply always regenerates and
  validates the plan before accepting `--yes`.
- Passphrase and confirmation are read from a protected prompt, never a flag or
  environment variable.
- Secret values are excluded by default. Each `--include-secret` item receives a
  separate exportability result in review.

Successful start prints the bundle path, operation ID, source stop/claim behavior,
and the status command. Completion prints sealed bytes, logical bytes, compression
ratio, included/excluded classes, bundle ID, and a reminder that the source remains
unchanged.

## Inspect CLI

`hideout migrate inspect BUNDLE` prompts for the passphrase and performs no
mutation. Default output shows:

- sealed/incomplete and integrity result;
- source Hideout/backend versions and destination compatibility;
- environment names and whether configuration/full VM state is present;
- logical and encoded sizes;
- included profile application-state bytes and their credential-risk warning;
- included persistent disks and shared-disk relationships;
- excluded host folders, history/activity, runtime state, profile cache, and
  generated profile identity/configuration;
- secret references and whether selected values exist, never the values;
- guest identity facts as “will reset by default” or “can be preserved with
  advanced risk acknowledgement.”

`--json` has the same redacted fields. A wrong passphrase and authenticated-header
corruption share one message. Incomplete files show the owning export operation's
resume command only when local durable state proves ownership.

## Import CLI

Primary flags:

```text
--environment SOURCE       Import selected bundle environment; repeatable
--all                      Import every environment in the bundle
--name SOURCE=DEST         Destination environment name; repeatable
--workspace SOURCE=DEST    Destination host-folder mapping; repeatable
--secret SOURCE=DEST       Rebind to an existing destination secret ref; repeatable
--secret SOURCE=DEST       Rebind or import one included encrypted value
--policy SPEC              SOURCE=safe-clone|exact-guest-restore; repeatable
--approve PROPOSAL_ID      Approve one reviewed authority proposal; repeatable
--ack RISK_CODE            Accept one exact typed risk; repeatable
--ack-secret-transfer      Required when importing included secret values
--preview                  Inspect and build review only; never stage
--yes                      Apply the exact displayed, unblocked plan
--json                     Machine-readable, redacted output
```

Defaults:

- all imported authority proposals are disabled;
- every selected environment uses Safe Clone;
- source display names are only suggestions and must be conflict-free;
- no host path or secret reference is guessed;
- one bundle import creates a clone and never consumes the bundle.

Exact Guest Restore requires both the explicit policy and its exact current
`--ack` code. A generic `--yes` is insufficient. The review says:

```text
This keeps the guest machine ID and SSH host keys. Hideout cannot verify that the
source computer or another imported copy is offline. Running copies together may
confuse SSH, service discovery, licenses, or software that treats this identity
as unique.
```

Before confirmation, output groups decisions as:

1. **Will copy** — environment definitions, profile application state, and
   persistent VM disks.
2. **Will create new** — Hideout/control/backend/profile identities and generated
   profile configuration; guest identity under Safe Clone.
3. **Will keep** — guest identity only where Exact Guest Restore was explicitly
   chosen; opaque application identity may remain inside disks.
4. **Needs your choice** — names, host folders, secret mappings, unsupported facts.
5. **Disabled unless approved** — mounts, endpoints, network/proxy, host apps,
   command adapters, scripts, and packs.

Completion prints new environment names/IDs, guest identity outcome, disabled
proposals, cleanup result, and a safe first-run command. Imported environments are
not automatically started.

## Status, resume, cancel, and recover

`hideout migrate status` shows a compact table. With an operation ID it shows:

- plain-language phase and whether the source must remain stopped;
- bytes completed/total for both logical and encoded work;
- components completed/total and current component name;
- elapsed time and throughput based only on migration work;
- last durable checkpoint time;
- whether cancellation is currently waiting for a safe boundary;
- exact next action, if any;
- retained partial/staged bytes after failure.

Do not show CPU percentages, opaque counters, hashes, operation phases, or backend
IDs in the default view. Put them under `--technical`/details.

`resume` never asks the user to recreate a plan. It explains why a passphrase or
provider action is needed and continues the same operation. `cancel` previews
whether partial data will be retained and asks separately before deletion.
`recover` offers only actions currently advertised by the Manager, each with its
effect on staged data. No command guesses a cleanup target.

## TUI layout

The migration screen is an operator HUD, not a dashboard of unexplained metrics:

<!-- markdownlint-disable MD013 -->

```text
┌ Hideout Migration ─ Import ─ dev.hideout-migration ─ 00:12:43 ───────────────┐
│ Scope                 │ Plan / Progress              │ Details & next action │
│ ✓ dev → dev-clone     │ Copying root disk            │ 38.2 / 100.0 GiB      │
│ ! workspace mapping   │ ████████░░░░░░ 38%           │ 126 MiB/s             │
│ ○ 2 disabled grants   │ 1 of 3 components complete   │ Enter: inspect/edit    │
│                       │ checkpoint 4 seconds ago      │ Source may run now     │
├───────────────────────┴──────────────────────────────┴───────────────────────┤
│ ↑↓ select  Enter inspect/edit  Space toggle  r resume  c cancel  ? help     │
└──────────────────────────────────────────────────────────────────────────────┘
```

<!-- markdownlint-enable MD013 -->

- Top line: task, bundle/operation, elapsed time, and terminal state.
- Left: concrete objects and unresolved choices.
- Center: current work over time with bytes/components, not decorative telemetry.
- Right: explanation, risk, error remediation, and exact next action.
- Footer: only keys valid in the current state.

Pressing Enter on a configurable row opens a centered terminal modal. Path rows
support validated destination entry; identity rows choose Safe Clone or advanced
Exact Guest Restore; authority rows show source fact, destination value, risk, and
approve/disable controls. Escape discards unconfirmed edits. Confirmation shows
the immutable plan digest and all high-risk acknowledgements.

The TUI remains useful at narrow widths: it collapses to one pane with tabs, never
hides blockers, and exposes a non-color symbol/text status. Screen-reader/plain
mode emits the same ordered review as CLI.

## WebUI layout

WebUI uses the same five review groups and Manager projection. It may add searchable
tables, disk graph visualization, file picker, and richer history, but it may not
add authority or defaults unavailable in CLI/TUI. Dialogs submit typed values and
proposal IDs only. No imported HTML/script is rendered as markup or executed.

The page survives reload by reading durable operation state. Passphrase fields and
one-shot handles are never placed in URL, local storage, session storage, analytics,
or serialized application state.

## Failure messages

Every failure has:

1. What Hideout could not prove or complete.
2. What remains unchanged/safe.
3. Whether partial/staged data remains and its size.
4. One recommended next command/action.
5. An optional stable technical code.

Example:

```text
Import paused: Hideout could not prove that the staged VM shut down after identity
reset. No environment was activated. The encrypted bundle is unchanged; 42 GiB of
staged data remains. Close workloads that use Lima, then run:

  hideout migrate resume mig_01...

Technical code: migration.adoption.stop_unproved
```

Raw provider stderr, credentials, secret values, and unredacted URLs never appear
in default failure text.

## Acceptance criteria

- A new user can identify the correct export/import commands from top-level help
  and explain what happens to VM files and host folders.
- A user can complete Safe Clone without learning backend or cryptographic terms.
- A professional user can obtain complete JSON plan/status/evidence and perform
  two-step noninteractive confirmation without bypassing risk checks.
- CLI, TUI, and WebUI produce the same plan digest for identical choices.
- Every editable TUI/WebUI field is reachable from keyboard and has an equivalent
  CLI flag or plan document field.
- No surface claims success until the Manager operation reaches `Complete`.
