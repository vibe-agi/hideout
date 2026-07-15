# Hideout

<!-- markdownlint-disable MD013 -->

[简体中文](README.zh-CN.md)

**Let tools do the work without handing them your whole machine.**

Hideout runs AI coding agents and unfamiliar CLI tools inside a local VM. They
can work normally on the project you choose without inheriting access to the
rest of your Mac. When a tool needs VS Code, a browser, or another host
resource, Hideout handles that specific request and records what happened.

```bash
# Work on the selected project inside the VM.
hideout run -- git status --short

# Open that project in host VS Code through a controlled bridge.
hideout run -- code .

# See what crossed the VM boundary.
hideout audit show --limit 5
```

`code .` requires a supported host editor and an approved host-app permission.

## Install

The public alpha supports Apple Silicon Macs. Homebrew installs Hideout and its
[Lima](https://lima-vm.io/) dependency from the official Vibe AGI tap:

```bash
brew install vibe-agi/tap/hideout
hideout init --template dev --profile default --backend lima \
  --network direct --runtime developer-standard --no-input
```

The formula verifies the immutable archive checksum, macOS code signature, and
Hideout package manifest. Installation does not start a VM or create a profile;
the explicit `init` command does. The inspectable standalone installer, manual
download, repair, and uninstall paths remain documented in
[Distribution And Bootstrap](docs/distribution-bootstrap.md).

## Try It

Use a dedicated project checkout. First use downloads the retained developer
runtime separately; expect approximately 1 GB.

```bash
cd /path/to/project

# The target sees Linux and runs as a non-root user.
hideout run -- sh -lc 'uname -s; id -u'

# The project remains an ordinary writable Git checkout.
hideout run -- git status --short
```

The complete 15-minute path includes installing a tested agent CLI, opening the
workspace in a host editor, privacy networking, and recovery:
[First-Run Alpha Path](docs/first-run-alpha.md).

## Where Commands Run

```text
macOS host                                      Lima VM
+----------------------------------+            +---------------------------+
| Terminal                         | start      | target CLI runs here      |
| hideout + Core                   +----------->| agent / git / npm         |
| policy / approval / audit        |            |                           |
|                                  | RW mount   | mounted workspace         |
| selected project checkout        +===========>|                           |
|                                  | approved   | code . / open ...         |
| VS Code / browser <--------------+------------+ typed host request        |
+----------------------------------+            +---------------------------+
```

`hideout` and its policy, approval, and audit logic run on the host. The target
after `hideout run --` runs inside the VM. The selected project is mounted into
the guest at a profile-controlled path. A projected command such as `code .`
sends a structured resource request back through Hideout Core; VS Code runs on
the host, without giving the guest a generic host shell. Other host files
require explicit HostFS permission.

## Why Hideout

- **Keep a real VM boundary.** The target runs under a separate guest kernel,
  not directly in your host process namespace.
- **Keep local development convenient.** The project stays writable, while
  controlled bridges can open mapped resources in supported host applications.
- **Keep host access visible.** Host files, apps, network mediation, approvals,
  and exports are governed by explicit rules and leave audit evidence.

Community adapters and host-app recipes can select reviewed Core capabilities;
they cannot pass arbitrary guest arguments to a host shell or add generic host
execution.

## Current Release

<!-- hideout-public-release:start -->
Current release: [Hideout v0.1.0-alpha.1](https://github.com/vibe-agi/hideout/releases/tag/v0.1.0-alpha.1) for macOS arm64. This is a
public supervised alpha, not a GA or Linux-package claim.

Package SHA-256: `9a35bbb70b298456dd7e001a1c22825cdff180309306e8a27271e995a81473b4`. The release page includes checksums,
the machine-readable release manifest, and bounded verification evidence.
<!-- hideout-public-release:end -->

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

The same machine-owned contract is available with `hideout support matrix`.

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
