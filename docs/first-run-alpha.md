# First-Run Alpha Path

<!-- markdownlint-disable MD013 -->

This page is the canonical first 15 minutes for an external alpha user. It
assumes the public macOS arm64 package and a dedicated project workspace.
It does not create new security claims; current claims and non-claims remain in
[STATUS.md](STATUS.md), [threat-model.md](threat-model.md), and
[support-matrix.md](support-matrix.md).

## Preconditions

- Use macOS arm64 with Lima for the first-class alpha path.
- Use a dedicated project checkout, not `$HOME`, `~/.hideout`, browser profile
  directories, SSH credential roots, or the Hideout store.
- Provide a local proxy only when using the privacy path.
- Treat `--backend native` as a weak development harness, not isolation.

## Install And Verify

Install the signed package and Lima dependency from the official Homebrew tap,
then review the supported default configuration, check readiness, and run one
useful project command:

```bash
brew install vibe-agi/tap/hideout
hideout setup
hideout doctor
cd /path/to/project
hideout run -- git status --short
```

The formula validates the archive SHA-256, macOS signature, and package
manifest before installing into the Homebrew Cellar. Formula installation does
not start a VM, download the retained runtime, or write profile state under
`~/.hideout`. Interactive `hideout setup` reviews and writes the fixed
direct-network Lima configuration; it still does not start a VM or download
the runtime. Automation and advanced profiles continue to use explicit
`hideout init --no-input`. See [distribution-bootstrap.md](distribution-bootstrap.md) for the
inspectable standalone installer, manual download, custom prefixes, repair,
and uninstall.

The first run may download the separately retained runtime. Hideout names its
exact revision and declared size before a potentially long start and prints a
bounded heartbeat while waiting; it never invents byte or percentage progress.
To inspect package and binary identity explicitly:

```bash
hideout version
hideout package verify "$(brew --prefix hideout)"
```

For non-interactive automation, spell out the same fixed choices:

```bash
hideout init --template dev --profile default --backend lima \
  --network direct --runtime developer-standard --no-input
```

The default help stays on this first-result path:

```bash
hideout help
hideout help setup
hideout help doctor
hideout help privacy
```

Use `hideout help --all` only when you need the complete advanced and
developer command index. Asking for help is read-only: it does not create a
profile, start a VM, or download a runtime. The short help repeats the supported
macOS arm64 prerelease boundary and warns that direct networking does not hide
your network origin.

Homebrew users should repair a damaged keg through Homebrew:

```bash
brew reinstall vibe-agi/tap/hideout
```

For a standalone installation under `$HOME/.local`, inspect obsolete
package-owned leftovers before explicitly repairing them:

```bash
hideout package repair --prefix "$HOME/.local" --dry-run
hideout package repair --prefix "$HOME/.local"
```

The package owns and checksums its Linux `tun2socks` privacy helper. If package
verification reports that helper missing or damaged, reinstall the Homebrew
package or run the standalone package repair flow; do not satisfy the check
with an unrelated `tun2socks` found on `PATH`.

## Direct First Success

Start with the default Lima profile, which has the fewest external
prerequisites:

```bash
hideout doctor
```

The default view answers whether Hideout is ready and shows only actionable
findings. Use `hideout doctor --verbose` for every observed check or
`hideout doctor --format json` for automation and evidence.

To prepare one shareable diagnostic artifact without composing audit exports:

```bash
hideout support report --out ./hideout-support.json
```

This is a local-only collection; it does not upload anything. It records
bounded product/support/package/readiness/recovery facts, excludes raw audit
events and workspace contents, validates that protected secrets, proxy values,
tokens, machine IDs, and raw host-user paths are absent, then writes at most
1 MiB with mode `0600`. It refuses an existing file unless you explicitly add
`--overwrite`. Always inspect the JSON before sharing it in an issue.

Move into a dedicated workspace and run one command as the synthetic non-root
target:

```bash
cd /path/to/sanitized/project
hideout run -- pwd
hideout audit show --limit 20
```

This is the first compatibility proof: it demonstrates the packaged VM,
workspace, identity, and retained runtime. Direct networking does not hide the
network origin and is not a privacy-network claim.

### Shared machine and separate-VM escape hatches

The 035 contract changes the automatic macOS arm64 Lima shape from one VM per
project to one compatible machine per canonical profile. Each run still gets an
immutable exact-project attachment and sees its selected project at
`/workspace`; a display label or previous project is never attachment
authority. Clean real behavior and performance evidence backs this supported
source-tree behavior.

Different projects in the same automatic machine share one guest
kernel. Ordinary non-root sessions receive separate process/mount views and
cannot select a sibling attachment, but guest root is not contained. Use a
dedicated named environment when projects require a VM wall:

```bash
hideout env create isolated --workspace "$PWD" --profile default --backend lima
hideout run --env isolated -- bash
```

Use a cloned profile plus a dedicated environment when projects must also have
separate guest home, identity, cache, and environment-service network posture:

```bash
hideout profile clone default isolated
hideout env create isolated --workspace "$PWD" --profile isolated --backend lima
hideout run --env isolated -- bash
```

The Workspace Portal is the live workspace transport, not HostFS overlay or a
copy/sync layer. Workspace writes change the selected host project immediately.
Paths outside that project still use explicit HostFS discover/read/write
authority.

The retained runtime is a separate, approximately 1 GB first-use download; it
is not embedded in the smaller Hideout host package. If Lima startup takes
longer than one second, Hideout prints a concise status line and periodic
heartbeat; use `--verbose` only when raw backend details are useful. Inspect its
exact revision, digest, size, source, inventory, and SBOM status before first
boot:

```bash
hideout runtime inspect developer-standard
```

The fastest way to feel the boundary after the first run succeeds is the host
editor bridge: `hideout run -- code .` opens the selected project in your host
VS Code through a typed, audited host-app permission — the guest has no `code`
binary at all. See [Open In A Host Editor](#open-in-a-host-editor).

## Privacy Follow-Up

Create a separate privacy profile only when its prerequisites are available:

```bash
export HIDEOUT_SECRET_PROXY_URL=socks5://127.0.0.1:7890
hideout init \
  --template privacy \
  --profile privacy \
  --backend lima \
  --network tun2socks \
  --proxy-secret proxy-url \
  --mediated-resolver 1.1.1.1 \
  --runtime developer-standard \
  --no-input
hideout doctor --profile privacy --backend lima --level deep
```

The installed package supplies `tun2socks`; the user still supplies a proxy
secret reference and a mediated resolver. If those are unavailable, `doctor`
reports observed facts and next actions. Real network privacy proof still
requires Gate 3 evidence; local doctor output is not a replacement for that gate.

Hideout never changes a requested privacy profile to direct networking when a
proxy, mediated resolver, or other privacy prerequisite is missing.

To change an existing profile instead of creating a separate one, persist only
the host-secret reference and resolver. If an already-running daemon did not
inherit the referenced host secret, stop it before active work and let the next
run start it with that environment. The next attach switches the reusable
environment's egress and DNS service without recreating or restarting the VM.
A direct/proxy posture change requires all earlier target sessions in that VM
to have exited; this prevents one session from silently changing a sibling's
network boundary:

```bash
export HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:7890
hideout connect through default-proxy using 1.1.1.1
hideout show connection
hideout run -- true
```

The proxy URL remains in the host environment; the profile stores only
`default-proxy`. A proxy running on this Mac should use its host-loopback
address (`127.0.0.1`), because Hideout's host gateway—not the guest—consumes
the URL. A remote operator proxy keeps its normal remote hostname. Changing
the upstream behind an existing proxy generation is
online: accepted TCP connections may finish on the previous route while new
connections use the new route. Mediated DNS can also switch online. Switching
between direct and proxy posture is environment-wide and waits for exclusive
session ownership, but still uses the same guest boot and disk.

Switch back without forgetting the saved proxy and resolver:

```bash
hideout connect directly
hideout connect through default-proxy
```

Existing target sessions keep their accepted route. The next eligible attach
applies the selected posture. Advanced automation can use the existing
`profile network` command or the typed Manager `profile/network/plan` and
`profile/network/apply` routes; all three paths use the same Manager planner
and validator.

## Install The Tested Agent CLI

Installing any tool in the guest follows three rules, and the commands below
are just this shape applied to one agent:

1. the guest home (`/hideout/profile/home`) persists across sessions, so a
   tool installed once stays installed;
2. install into `$HOME/.local` — its `bin` directory is already on the guest
   `PATH`, and no `sudo` or system prefix is needed or available;
3. pin exact versions so later runs stay reproducible.

Install the pinned evidence package into the durable target-owned prefix. This
runs inside the selected guest and requires neither `sudo` nor a host-global npm
prefix:

```bash
hideout run --profile default -- sh -eu -c '
  rm -rf "$HOME/.npm" "$HOME/.local/lib/node_modules/@openai/codex" "$HOME/.local/bin/codex"
  npm install --global --prefix "$HOME/.local" @openai/codex@0.144.1
  "$HOME/.local/bin/codex" --version
'
```

The privacy evidence lane proves the registry DNS and HTTPS request crosses the
existing DoH/tun2socks path and that proxy credentials stay out of target and
public evidence. Package installation does not log in. Interactive login,
browser callbacks, and durable agent authentication are explicitly outside
031.

If the command needs a host file outside the workspace, grant read access
explicitly:

```bash
hideout run --profile default --fs read:/absolute/file -- <cli>
```

If the CLI first needs to navigate names without reading file contents, use an
explicit discover rule:

```bash
hideout run --profile default --fs see-dir:/absolute/directory -- <cli>
```

The CLI sees complete immediate names and coarse kinds, but locked reads return
`EACCES`. An eligible exact-file request appears in `hideout decision list
--kind hostfs.read`; after a local operator claims and approves it, the same
running CLI retries the read. Visible names are user data and may enter model
context. `see*` does not grant content, writes, execution, or symlink targets.

Onboarding keeps visibility at `none` unless selected explicitly. For example,
`--hostfs-visibility landmarks` creates reviewed one-level roots. Broad
`--hostfs-visibility home-tree` additionally requires
`--acknowledge-name-disclosure`; do not enable it merely to avoid individual
grants.

macOS protected directories can show a TCC prompt when HostFS first enumerates
the selected root. Hideout cannot perform a truthful silent preflight. To probe
one root deliberately, use a command that warns before touching it:

```bash
hideout doctor --feature hostfs --probe-hostfs-root /absolute/directory
```

If the command needs to write through HostFS, stage the write with an overlay
grant and then apply the local decision explicitly:

```bash
hideout run --profile default --fs overlay-dir:/absolute/directory -- <cli>
hideout hostfs write status
hideout decision list
```

HostFS write apply changes host lower files only after an authenticated local
operator claims and applies the decision. Workspace writes are intentionally
visible to the target and are audited rather than blocked.

## Open In A Host Editor

With a supported, code-signed Visual Studio Code installation, a CLI running
inside the Lima guest can open the mapped host workspace without learning its
host path:

```bash
hideout doctor --profile default --feature projection --level deep
hideout run --profile default --backend lima -- code .
hideout run --profile default --backend lima -- code -g src/main.go:12:3
```

The default safe mode uses run-scoped VS Code state, disables extensions and
automatic workspace tasks, and returns no host data to the guest. It needs no
approval and opens immediately, and it prints a one-line notice naming the safe
posture and how to upgrade. Safe mode is the recommended and fully working
default; keep it unless you specifically need your own editor profile and
extensions.

Requesting your full, native host app is trusted mode. Turn it on for a profile
with `hideout profile host-app-mode default trusted`, then grant the specific
command for the current project by running, on the host from inside the project
directory:

```bash
hideout allow host-app code
```

The grant is a durable per-profile, per-workspace policy stored in
guest-unreachable control-plane state and keyed by a Core-derived workspace
identity, so a later one-shot `hideout run -- code .` reuses it and opens
natively with no live approval window. It is separate from the running command's
authority: without the grant a trusted launch fails closed and names the exact
command to allow. Revoke at any time with `hideout deny host-app code`; the next
open falls back to the safe isolated window. Changing the workspace or the app
identity re-closes the grant until you re-affirm it. Hideout never passes through
raw guest argv, resolves `code` from ambient `PATH`, or falls back to generic
host execution.

## Operator Console

Start the local daemon when you want a live local console:

```bash
hideout daemon start
hideout tui --profile default
hideout ui --no-open --print-url
```

The TUI and WebUI organize existing Manager state: action-required counts,
HostFS write decisions, generic decisions, notices, background work, stream
health, and explicit doctor/package/support commands. They do not add authority
or auto-run doctor.

## Fast Development Harness

For local development of Hideout itself, `native` can be useful:

```bash
hideout init \
  --template dev \
  --profile dev \
  --backend native \
  --network direct \
  --no-input
hideout run --profile dev --backend native -- pwd
```

This is a weak harness only. Do not use native output as isolation, DNS, HostFS,
or privilege-separation evidence.

## When Stuck

Use doctor first:

```bash
hideout doctor --level deep
hideout doctor --feature packaging --level deep
hideout doctor --feature dns --level deep
hideout doctor --feature hostfs --level deep
hideout doctor --feature privilege --level deep
hideout doctor --fix --dry-run
```

Doctor reports local observed facts, candidate causes, next actions, and
gate-required markers. It should not claim release readiness or replace real
Gate 2/Gate 3 proof.

Common failure hints:

- missing helper: run `hideout package verify "$HOME/.local"` and inspect the
  named helper.
- missing or damaged `tun2socks` (`package.prerequisite.missing`): reinstall
  the Homebrew package, or run `hideout package repair --prefix <dir>` for a
  standalone installation, then rerun `doctor --feature packaging`.
- native backend warning: switch to Lima for isolation evidence.
- hardened native refusal: use a non-native backend with enforced-capable
  privilege separation, or create an explicit degraded fallback profile.
- missing proxy secret (`init.proxy-secret.missing`) or resolver
  (`init.mediated-resolver.missing`): set `HIDEOUT_SECRET_PROXY_URL` and rerun
  `hideout init` or `doctor --feature dns`.
- degraded privilege posture (`privilege.status.degraded`): recreate with an
  enforced-capable image or keep the profile explicitly degraded.
- stale package install (`package.obsolete-leftover`): run
  `hideout package repair --prefix "$HOME/.local" --dry-run` before applying
  repair.
- missing release gate evidence (`release.gate-evidence.missing`) or stale
  supporting evidence (`release.evidence.stale`): rerun the named gate or
  product-hardening proof on the exact candidate commit.

The registry behind these stable codes is inspectable with
`hideout support recovery-codes --json`.

## First-Run E2E Proof Modes

The local Gate 0 proof uses the packaged installer and installed binary, but it
does not claim Lima/privacy isolation:

```bash
scripts/test-first-run-e2e.sh --local-fast --out /tmp/hideout-first-run
```

This local-fast lane installs with `--skip-init`, verifies the package,
initializes a weak/dev native profile once, runs one installed-binary command,
and writes `hideout.product-hardening-evidence/v1` with audit and Boundary
references.

Real backend first-run proof is explicit and prerequisite-gated:

```bash
scripts/test-first-run-e2e.sh --real-backend --out /tmp/hideout-first-run-real
scripts/test-first-run-e2e.sh --real-backend --require-real --out /tmp/hideout-first-run-real
```

When Lima/privacy prerequisites are missing, the first command records
`not-run` evidence; `--require-real` turns that into a non-zero result. A pass
in real-backend mode must use the Lima/privacy path and must not fall back to
native.

## Alpha Readiness Reminder

Completing this page does not by itself make a release candidate. Release
readiness requires the support/readiness gate and real Gate 2/Gate 3 evidence
for the exact release commit and artifact.
