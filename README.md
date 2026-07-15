# Hideout

<!-- markdownlint-disable MD013 -->

[简体中文](README.zh-CN.md)

**Let tools do the work without handing them your whole machine.**

Hideout runs agentic and untrusted CLIs in a local VM. Your project stays
writable, host experiences such as `code .` still work through explicit
capabilities, and every host-facing action remains visible and auditable.

```console
hideout run -- codex
hideout run -- code .
hideout audit show --limit 5
```

The first command assumes the CLI is installed in the guest. `code .` requires
a supported host editor and an approved host-app capability.

## Where Commands Run

```text
macOS host                                      Lima VM
+----------------------------------+            +---------------------------+
| Terminal                         | start      | target CLI runs here      |
| hideout + Core                   +----------->| codex / git / npm         |
| policy / approval / audit        |            |                           |
|                                  | RW mount   | /workspace                |
| project checkout                 +===========>|                           |
|                                  | approved   | code . / open ...         |
| VS Code / browser <--------------+------------+ typed host request        |
+----------------------------------+            +---------------------------+
```

`hideout` and its policy, approval, and audit logic run on the host. The target
after `hideout run --` runs inside the VM. The selected project checkout is
mounted at `/workspace`. A projected command such as `code .` sends a typed
resource request back through Hideout Core; VS Code runs on the host, without
giving the guest a generic host shell. Other host files require explicit HostFS
capabilities.

<!-- hideout-public-release:start -->
Current release: [Hideout v0.1.0-alpha.1](https://github.com/vibe-agi/hideout/releases/tag/v0.1.0-alpha.1) for macOS arm64. This is a
public supervised alpha, not a GA or Linux-package claim.

Package SHA-256: `9a35bbb70b298456dd7e001a1c22825cdff180309306e8a27271e995a81473b4`. The release page includes checksums,
the machine-readable release manifest, and bounded verification evidence.
<!-- hideout-public-release:end -->

## Install

The public alpha supports Apple Silicon Macs and uses
[Lima](https://lima-vm.io/) for VM isolation.

```bash
brew install lima
curl -fsSL https://raw.githubusercontent.com/vibe-agi/hideout/master/install.sh | sh
export PATH="$HOME/.local/bin:$PATH"
```

The installer uses no `sudo` and does not edit shell startup files. It verifies
the published release inventory, archive SHA-256, package identity, and macOS
code signature before installing. To inspect it before execution:

```bash
curl -fsSL https://raw.githubusercontent.com/vibe-agi/hideout/master/install.sh \
  -o /tmp/hideout-install.sh
less /tmp/hideout-install.sh
sh /tmp/hideout-install.sh
```

Manual download, verification, repair, and uninstall instructions are in
[Distribution And Bootstrap](docs/distribution-bootstrap.md).

## First Run

Use a dedicated project checkout. The retained developer runtime is a separate,
approximately 1 GB download on first use.

```bash
cd /path/to/project
hideout run -- git status --short
hideout audit show --limit 5
```

The complete 15-minute path includes installing a tested agent CLI, opening the
workspace in a host editor, privacy networking, and recovery:
[First-Run Alpha Path](docs/first-run-alpha.md).

## Why Hideout

- **A real local VM boundary.** The target runs under a separate guest kernel,
  not directly in your host process namespace.
- **Local work still feels local.** The workspace is writable, while typed host
  capabilities can open mapped resources in supported host applications.
- **Authority is explicit.** Host files, host apps, network mediation,
  approvals, and export paths use typed plans, decisions, and audit evidence.

Hideout does not pass arbitrary guest arguments to a host shell. Community
adapters and host-app recipes select reviewed Core capabilities; they do not
gain generic host execution.

## Current Boundaries

- Public supervised alpha: macOS arm64 with Lima.
- The selected project workspace is visible and writable to the target.
- `direct` networking does not hide network identity; privacy networking has
  separate proxy and DNS prerequisites.
- Hideout does not claim protection after a target obtains guest root.
- `--backend native` is a development harness, not an isolation boundary.

See the exact supported and unsupported combinations in the
[Support Matrix](docs/support-matrix.md) and security wording in
[Claim Boundaries](docs/claim-boundaries.md).

## Explore

- [First 15 minutes](docs/first-run-alpha.md)
- [What is implemented](docs/STATUS.md)
- [Security model](docs/threat-model.md)
- [Support matrix](docs/support-matrix.md)
- [Documentation index](docs/README.md)

## Build From Source

Source development requires Go. The public package does not.

```bash
scripts/install-local.sh
export PATH="$HOME/.local/bin:$PATH"
hideout doctor
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for development gates. Report security
issues through [SECURITY.md](SECURITY.md); ordinary bugs and product feedback
can use GitHub Issues.
