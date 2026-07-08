# Hideout

[简体中文](README.zh-CN.md)

Hideout runs untrusted developer tools and agentic CLIs inside an isolated
backend boundary (currently a reusable Lima VM), mediates every host access
through typed, audited, fail-closed gates, and records evidence you can
inspect. Privacy hardening is one of the benefits, not the definition.

Current status: private alpha; run supervised. Gates and release evidence are
defined in [docs/privacy-run-test-plan.md](docs/privacy-run-test-plan.md).

## What Hideout Protects

Hideout replaces ambient host authority with explicit capabilities:

- the target gets an isolated home, XDG paths, machine identity, and git config;
- the project workspace is mounted read/write for normal development;
- host files outside the workspace require explicit HostFS grants; write-class
  HostFS operations require separate overlay grants and local operator apply
  before the host lower files change;
- environment variables follow profile env policy: explicit public values,
  allowlisted inherits, and deny patterns (`profile.env.public`,
  `profile.env.inherit`, `profile.env.deny`);
- host escapes such as `open` and `preview.open` go through typed brokered
  routes;
- proxy credentials can be used by Hideout without appearing in target env;
- Lima target commands run as a non-root target user when Hideout can prove
  target `sudo -n` and `/usr/bin/sudo -n` are unavailable; privileged Hideout
  setup such as network bootstrap and HostFS daemon startup uses a separate
  root-control setup identity and is audited;
- every run writes audit and boundary summary evidence.

Important non-claims:

- secrets already inside the mounted workspace are visible to the target;
- `direct` network mode does not hide network identity;
- `tun2socks` hides the network origin path, but it is not a data-loss
  prevention system;
- Hideout does not claim protection after a target has obtained guest root, and
  weak/pre-009 images that still allow target passwordless sudo are surfaced as
  degraded with recreate/base-image guidance;
- `--backend native` is a development harness, not isolation.

## Install Requirements

For a release-like tarball on macOS:

- Lima (`limactl`);
- Google Chrome or another supported Chromium-compatible browser for real
  browser host-open checks;
- an optional local proxy for `tun2socks` mode.

The tarball path does not require Go. It contains the host binaries, Linux guest
helpers, manifest schemas, and the package installer. The package installer uses
the packaged `hideout` binary to verify `package-manifest.json` checksums before
copying the prebuilt artifacts from the extracted package.

For local source-tree development, Go is also required.

For the alpha package path, extract the tarball and run the package installer
from the package root. The package installer follows the same default init path
and does not require Go:

```bash
tar -xzf hideout-<platform>.tar.gz
cd hideout
./install.sh
export PATH="$HOME/.local/bin:$PATH"
hideout version
hideout package verify "$HOME/.local"
hideout doctor
```

Installing a newer package to the same prefix upgrades package-owned files and
preserves profiles, audit, evidence, adapter packs, decisions, and notices.
Package uninstall removes only package-owned files by default; durable user
state is removed only with an explicit purge:

```bash
hideout package uninstall --prefix "$HOME/.local" --dry-run
hideout package uninstall --prefix "$HOME/.local"
hideout package uninstall --prefix "$HOME/.local" --purge
```

For local source-tree development, Go is required. The source-tree installer
builds:

- `hideout`;
- the host command shim;
- the Linux guest shim;
- the Linux HostFS daemon;
- the guest-local DNS-over-HTTPS stub used by privacy-mode DNS mediation.

```bash
scripts/install-local.sh
export PATH="$HOME/.local/bin:$PATH"
hideout version
hideout doctor
```

## Quickstart

```bash
export HIDEOUT_SECRET_PROXY_URL=socks5://host.lima.internal:7890
hideout init \
  --template privacy \
  --profile default \
  --backend lima \
  --network tun2socks \
  --proxy-secret proxy-url \
  --mediated-resolver 1.1.1.1 \
  --no-input
hideout run -- <cli>
hideout run --fs read:/absolute/file -- <cli>
hideout run --fs overlay-dir:/absolute/directory -- <cli>
hideout hostfs write status
hideout decision list
hideout notice list
hideout explain -- <cli>
hideout audit show --limit 20
hideout audit export --share --source audit --out /tmp/share.json --acknowledge-full-fidelity
```

## First Run

Use a dedicated project checkout. Do not run Hideout from `$HOME`, `~/.hideout`,
or a directory containing host credentials. The workspace is intentionally
mounted into the guest.

```bash
cd /path/to/sanitized/project
hideout run --profile smoke --backend lima --network direct -- pwd
```

`hideout init` and `hideout doctor --fix` print copyable next-step commands
for `doctor`, a smoke run, and any configured generic CLI tool.

For a local weak-isolation development harness, create a separately labeled dev
profile instead of calling it privacy:

```bash
hideout init \
  --template dev \
  --profile local-dev \
  --backend native \
  --network direct \
  --no-input
```

This first run should only verify the backend, workspace mount, and isolated
identity. Use a dedicated `smoke` profile so existing `default` profile policy
cannot trigger extra guest setup during the first check.

Every reusable environment is named: runs without `--env` use a deterministic
auto-named environment per profile and workspace, and `hideout env create`
makes one explicitly with a pinned base image declaration. Changing an
environment's identity inputs fails closed with a recreate hint instead of
silently switching guests.

```bash
hideout env create work --image 'template:_images/ubuntu-lts'
hideout run --env work -- <command>
hideout env list
hideout env inspect work
hideout env recreate work
hideout stop work
hideout clean --stopped work
hideout env remove work
```

Use `--rm` for a disposable environment:

```bash
hideout run --profile smoke --rm -- <command>
```

## Running A CLI Tool

Hideout does not hardcode product-specific CLIs, and it does not ship package
installation providers. Guest tools arrive on two paths:

- the environment's base image supplies the baseline toolchain;
- the operator installs anything else by running ordinary setup commands
  inside the boundary, under the same network policy and audit as any other
  run.

The old npm-based provisioning path has been removed.

Use `init` and `doctor` to create the profile, then run the CLI you want:

```bash
hideout init \
  --profile agent \
  --backend lima \
  --network direct

hideout doctor --fix --dry-run --profile agent

hideout run --profile agent --backend lima -- <command> --version
```

If a tool is missing from the base image, install it in-boundary with a normal
run against the reusable environment:

```bash
hideout run --profile agent -- <installer command>
```

If a CLI needs persistent login state, put it in the isolated profile home, not
the host home:

```bash
hideout profile home agent import \
  --from /host/path/to/state \
  --to .config/<tool>/state
```

## Network Modes

Direct mode is the compatibility default:

```bash
hideout init \
  --template dev \
  --profile local-dev \
  --backend native \
  --network direct \
  --no-input
hideout run --backend lima --network direct -- <command>
```

Hidden proxy mode uses `tun2socks` inside the guest. The proxy secret stays in a
host-only secret ref and is not passed to the target process.

If your host proxy listens on `127.0.0.1:7890`, the Lima guest should reach it
through `host.lima.internal:7890`. Configure this on a dedicated profile so the
network default is explicit and repeatable:

```bash
export HIDEOUT_SECRET_DEFAULT_PROXY=socks5://host.lima.internal:7890

hideout init --no-input \
  --profile privacy \
  --template privacy \
  --backend lima \
  --network tun2socks \
  --proxy-secret default-proxy \
  --mediated-resolver 1.1.1.1

hideout run --profile privacy --backend lima \
  -- <command>
```

`doctor` and run bootstrap fail closed if the proxy route cannot be verified.

## Host Files Outside The Workspace

The workspace is mounted directly. Everything else should go through HostFS
grants.

Run-scoped grants:

```bash
hideout run --backend lima --fs read:/absolute/file -- <command>
hideout run --backend lima --fs dir:/absolute/dir -- <command>
hideout run --backend lima --fs tree:/absolute/dir -- <command>
hideout run --backend lima --fs 'read:/absolute/dir/*.txt' -- <command>
```

Quote HostFS glob selectors so your shell does not expand them. `*` does not
implicitly include dotfiles such as `.env`; grant those with an explicit dotfile
selector. Use backslash escaping for literal glob characters or a literal
backslash, for example `read:/absolute/dir/\[2026\].txt`.

Persistent profile rules:

```bash
hideout profile fs default list
hideout profile fs default add --fs read:/absolute/file --reason "tool input"
hideout profile fs default deny --no-fs tree:/absolute/dir --reason "too broad"
hideout profile fs default remove <rule-id>
```

The Hideout store is reserved control-plane state and cannot be granted through
HostFS.

## Host Open And Preview

Registered host escapes are typed and audited. `host.open` does not allow raw
host localhost/private URL access.

Profiles can register additional open-like command symbols without adding new
host authority. They still use the same `host.open` policy and `open-target-v1`
argv schema:

```bash
hideout profile command-proxy default add-open browser-open
hideout profile command-proxy default list
hideout profile command-proxy default remove browser-open
```

Profiles can also register local command adapters for explicit command symbols.
Adapters are digest-pinned JavaScript artifacts, or the built-in root-sensitive
intent adapter, and their outcomes are validated by Go before anything happens:

```bash
hideout profile command-adapter default add-builtin-root-sensitive
hideout profile command-adapter default list
```

Reusable command behavior can be installed as a local adapter pack, tested, and
then enabled explicitly per profile:

```bash
hideout adapter-pack install --path ./hideout-adapters
hideout adapter-pack test example.pack
hideout adapter-pack enable --profile default --pack example.pack \
  --revision rev_... --adapter tool
```

Adapter packs are local digest-locked extensions, not a public marketplace or a
publisher-trust system. Pack tests are required before enablement, but Core
validation still owns command ownership, allowed proposal capabilities, digest
drift, and revoked-pack fail-closed behavior.

The root-sensitive adapter records and can deny/propose privileged command
intent such as `sudo`, package managers, mounts, resolver edits, and firewall
changes. 009 privilege separation reports whether the current Lima target is
`enforced`, `degraded`, or `unknown`; even when enforced, command adapters do
not claim to intercept absolute paths, syscalls, setuid binaries, or a target
that already has guest root.

To expose a guest dev server to the host browser:

```bash
hideout run --backend lima \
  --preview 127.0.0.1:5173 \
  -- npm run dev
```

Hideout creates a run-scoped host-to-guest mapping and opens the host browser at
the mapped endpoint. This is separate from `host.open`; the localhost deny rule
remains intact.

## Audit And Cleanup

By default, `hideout run` keeps the terminal close to local command execution:
target stdout and stderr are passed through, while Hideout control-plane
progress is kept quiet. Use `--verbose` when you want the environment hint,
resume command, and boundary summary:

```bash
hideout run --verbose --profile smoke --backend lima -- pwd
```

Verbose runs print a boundary summary and the audit log path:

```text
Hideout boundary:
  audit: .../audit.jsonl
  host.open: allowed=1 denied=0
  hostfs: allowed=0 denied=1 unsupported=0
```

Inspect the latest matching redacted audit events through the Manager-backed CLI
instead of reading raw JSONL by hand:

```bash
hideout audit show --limit 20
hideout audit show --decision deny
hideout audit show --session <session-id> --json
hideout daemon start
hideout tui --profile agent
hideout tui --once --profile agent
```

`hideout tui` is the terminal observer surface. Keep it open in a second
terminal while another terminal runs an agent or CLI. When `hideoutd` is
running, it seeds once from Manager data and then applies typed daemon event
payloads without steady-state overview/audit polling. `--once` is for scripts
and snapshots.

`hideout ui --no-open --print-url` serves the WebUI smoke surface over the
local Manager API and prints its address; it is the fuller management view
and is not required for any first-run flow.

For the resident local control plane, run:

```bash
hideout daemon start
hideout daemon status
hideout daemon stop
```

`hideoutd` serves the same Manager API over a store-rooted Unix socket, serves
the WebUI over a tokened loopback URL, emits typed redacted live events for
daemon-mediated operations, and runs existing environment stop/clean operations
as background work. WebUI and TUI live panels use one seed plus those events
while the stream is healthy, and fall back to daemon-less behavior when no
daemon is running.

Useful cleanup commands:

```bash
hideout env list
hideout stop <env-id>
hideout stop --idle 2h
hideout clean --dry-run --stopped
hideout clean --stopped <env-id>
hideout cleanup --dry-run
hideout cleanup
```

`stop` releases VM memory and keeps the reusable environment. `clean` removes
stopped or selected environments. `cleanup` removes session-local runtime and
secret-bearing files while preserving audit by default.

`stop` and `clean` keep backend control output quiet by default. Add `--verbose`
to those lifecycle commands when debugging `limactl` behavior.

## Programmable Policy And Sharing

Boundary decisions can be scripted through constrained JavaScript (goja)
entrypoints such as `command.decide`, `decideCommandAdapter`, and
`audit.redact`: scripts decide, classify, and redact within supplied context,
and never gain filesystem, network, or process access. Ecosystem sharing covers
policy scripts,
non-sensitive configuration, and declarative base image references; secrets
are parameterized as SecretRef inputs that each user fills locally. See
[docs/script-extension-architecture.md](docs/script-extension-architecture.md)
and
[docs/ecosystem-foundation-design.md](docs/ecosystem-foundation-design.md).

## Verification

Fast local check:

```bash
scripts/test-phase1.sh --quick
```

Full gates and release evidence procedures are defined in
[docs/privacy-run-test-plan.md](docs/privacy-run-test-plan.md).

## Documentation Map

- [Architecture Principles](docs/architecture-principles.md)
- [Main Design](docs/privacy-run-design.md)
- [Threat Model](docs/threat-model.md)
- [Test Plan](docs/privacy-run-test-plan.md)
- [Network Privacy](docs/network-privacy-architecture.md)
- [OpenTarget Architecture](docs/opentarget-architecture.md)
- [Distribution Bootstrap](docs/distribution-bootstrap.md)
- [Ecosystem Foundation](docs/ecosystem-foundation-design.md)
- [Script Extension Architecture](docs/script-extension-architecture.md)
