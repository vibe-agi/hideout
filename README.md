# Hideout

[简体中文](README.zh-CN.md)

Hideout is a local privacy runner for untrusted developer tools and agentic
CLIs. It runs the target inside a reusable Lima environment, gives it an
isolated identity, routes host access through typed capabilities, and records
boundary evidence.

Current status: private alpha / supervised dogfood. The core v1 path has one
local release-candidate evidence bundle covering Gate 0, Gate 1, Gate 2 Lima
E2E, Gate 3 strict proxy, Gate 4 real browser host escape, capability probes,
and generic CLI dogfood smoke. Public GA still requires repeatable evidence for
release artifacts and release-specific signoff.

## What Hideout Protects

Hideout replaces ambient host authority with explicit capabilities:

- the target gets an isolated home, XDG paths, machine identity, and git config;
- the project workspace is mounted read/write for normal development;
- host files outside the workspace require explicit HostFS grants;
- host escapes such as `open` and `preview.open` go through typed brokered
  routes;
- proxy credentials can be used by Hideout without appearing in target env;
- every run writes audit and boundary summary evidence.

Important non-claims:

- secrets already inside the mounted workspace are visible to the target;
- `direct` network mode does not hide network identity;
- `tun2socks` hides the network origin path, but it is not a data-loss
  prevention system;
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

For local development, install from the source tree. The default install path
initializes a Lima-backed profile with direct networking:

```bash
scripts/install-local.sh
export PATH="$HOME/.local/bin:$PATH"
hideout version
hideout doctor
```

For a release-like tarball, extract it and run the package installer from the
package root. The package installer follows the same default init path and does
not require Go:

```bash
tar -xzf hideout-<platform>.tar.gz
cd hideout
./install.sh
export PATH="$HOME/.local/bin:$PATH"
hideout version
hideout doctor
```

The source-tree installer builds:

- `hideout`;
- the host command shim;
- the Linux guest shim;
- the Linux HostFS daemon.

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

This first run should only verify the backend, workspace mount, and isolated
identity. Use a dedicated `smoke` profile so existing `default` profile tool
policy cannot trigger package provisioning during the first check. Do not
configure CLI tool provisioning until the next section.

Reusable Lima environments are keyed by profile, workspace, backend, and tool
policy. Use `hideout list` to see resumable environments:

```bash
hideout list
hideout run --profile smoke --resume <env-id> -- <command>
hideout stop <env-id>
hideout clean --stopped <env-id>
```

Use `--rm` for a disposable environment:

```bash
hideout run --profile smoke --rm -- <command>
```

## Running A CLI Tool

Hideout does not hardcode product-specific CLIs. Configure generic tool
provisioning on the profile, then run the command.

For an npm-based CLI:

```bash
hideout init \
  --profile agent \
  --backend lima \
  --network direct \
  --npm-package <npm-package> \
  --npm-command <command>

hideout run --profile agent --backend lima -- <command> --version
```

`node-dev` and npm global installs run during managed guest setup after the
selected network mode has been applied. They are provisioned for the reusable
environment before the target command starts, so the first run after changing
tool policy may download packages even when the target command itself is small.

The same setup can be planned or repaired later:

```bash
hideout doctor --fix --dry-run \
  --profile agent \
  --npm-package <npm-package> \
  --npm-command <command>
```

For lower-level profile edits:

```bash
hideout profile tools agent preset add node-dev
hideout profile tools agent npm add --package <npm-package> --command <command>
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
hideout init --no-input --backend lima --network direct
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
  --backend lima \
  --network tun2socks \
  --proxy-secret default-proxy

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
hideout run --backend lima --fs read:/absolute/dir/*.txt -- <command>
```

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
hideout tui --profile agent
hideout tui --once --profile agent
```

`hideout tui` is the terminal observer surface. Keep it open in a second
terminal while another terminal runs an agent or CLI. `--once` is for scripts
and snapshots.

Useful cleanup commands:

```bash
hideout list
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

## Verification

Fast local check:

```bash
scripts/test-phase1.sh --quick
```

Required automated gates:

```bash
scripts/test-phase1.sh --required
```

Release dogfood proof on macOS with Lima, a real browser, and an operator proxy:

```bash
export HIDEOUT_SECRET_DEFAULT_PROXY=socks5://host.lima.internal:7890
scripts/test-release-dogfood.sh
```

This runs Gate 0, the native harness, real Lima E2E, strict hidden proxy,
real-browser host escape, capability probes, and the generic CLI dogfood smoke.
It writes a redacted evidence bundle under `.hideout-release-evidence/` by
default. Set `HIDEOUT_RELEASE_EVIDENCE_DIR` to choose an exact output directory.

## Documentation Map

- [Architecture Principles](docs/architecture-principles.md)
- [Main Design](docs/privacy-run-design.md)
- [Threat Model](docs/threat-model.md)
- [Test Plan](docs/privacy-run-test-plan.md)
- [Network Privacy](docs/network-privacy-architecture.md)
- [OpenTarget Architecture](docs/opentarget-architecture.md)
- [Distribution Bootstrap](docs/distribution-bootstrap.md)
