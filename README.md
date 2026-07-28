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

By default `code .` opens the project in a **safe, isolated editor window** — a
separate VS Code profile with extensions and automatic workspace tasks disabled —
because the workspace was just written by a tool. This default needs no approval
and requires only a supported, code-signed host editor. Opening the project in
your **full, native VS Code** instead is a separate opt-in (trusted mode, granted
per project with `hideout allow host-app code`); see
[docs/first-run-alpha.md](docs/first-run-alpha.md#open-in-a-host-editor)
for how it works.

## Install

The public alpha supports Apple Silicon Macs. Homebrew installs Hideout and its
[Lima](https://lima-vm.io/) dependency from the official Vibe AGI tap:

```bash
brew install vibe-agi/tap/hideout
hideout setup
hideout doctor
```

If you need help diagnosing a problem, create one bounded local report and
inspect it before sharing:

```bash
hideout support report --out ./hideout-support.json
```

The command does not upload anything. It excludes raw audit events, workspace
content, secret/proxy values, control-plane tokens, machine IDs, and raw
host-user paths; the output is capped at 1 MiB and written with mode `0600`.

The formula verifies the immutable archive checksum, macOS code signature, and
Hideout package manifest. Installation does not start a VM or create a profile;
the interactive `setup` review creates the supported default configuration but
still does not start a VM or download the runtime. The inspectable standalone installer, manual
download, repair, and uninstall paths remain documented in
[Distribution And Bootstrap](docs/distribution-bootstrap.md).

Homebrew owns package updates and removal:

```bash
brew upgrade vibe-agi/tap/hideout
brew uninstall vibe-agi/tap/hideout
```

Both operations preserve durable Hideout state under `~/.hideout`. Use
`hideout help uninstall` before removing that state separately; standalone
uninstall and purge require explicit package paths and an exact store
confirmation.

## Try It

Use a dedicated project checkout. First use downloads the retained developer
runtime separately; expect approximately 1 GB.

```bash
cd /path/to/project

# The target sees Linux and runs as a non-root user.
hideout run -- sh -lc 'uname -s; id -u'

# The project remains an ordinary writable Git checkout.
hideout run -- git status --short

# Read or change the connection used by new sessions.
hideout show connection
hideout connect directly
```

Keep a shell open in one terminal and run an agent or another command from the
same project in a second terminal. Both runs reuse the same warm VM and mounted
workspace, while each receives separate session authority:

```bash
# Terminal 1
hideout run -- bash

# Terminal 2, in the same project
hideout run -- git status --short
```

The complete 15-minute path includes installing a tested agent CLI, opening the
workspace in a host editor, privacy networking, and recovery:
[First-Run Alpha Path](docs/first-run-alpha.md).

## Where Commands Run

```text
macOS terminal          macOS control plane                 Lima VM
+----------------+      +-----------------------+           +----------------+
| hideout client |<====>| hideoutd process role |<=========>| fixed session  |
| raw TTY/resize |      | Manager + policy      |  framed   | supervisor     |
| review/output  |      | backend/session owner |  stream   | PTY + process  |
+----------------+      | approval/audit/HostFS |           | target CLI     |
                        +-----------+-----------+           +-------+--------+
                                    |                               |
host app <--- typed request --------+       project checkout =======+ RW mount
```

`hideout` is the thin client: it parses the invocation, presents the canonical
review, owns the local terminal state, and carries input/output/resize frames.
The resident `hideoutd` process role owns Manager Core, policy, Lima/SSH,
per-run authority, active sessions, audit, and cleanup. Inside the VM, a fixed
packaged supervisor owns the target PTY and process tree. The role currently
uses the same installed executable internally; it is not a second package the
operator must install or start.

The selected project is mounted into the guest at a profile-controlled path. A
projected command such as `code .` sends a structured resource request back
through Manager Core; VS Code runs on the host without giving the guest a
generic host shell. Other host files require explicit HostFS permission.

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
Current release: [Hideout v0.1.0-alpha.3](https://github.com/vibe-agi/hideout/releases/tag/v0.1.0-alpha.3) for macOS arm64. This is a
public supervised alpha, not a GA or Linux-package claim.

Package SHA-256: `61807ce60d7a037139713cffe475f492ee8e60cced56674ba3f0be0580e65050`. The release page includes checksums,
the machine-readable release manifest, and bounded verification evidence.
<!-- hideout-public-release:end -->

## Current Boundaries

- Public supervised alpha: macOS arm64 with Lima.
- The selected project workspace is visible and writable to the target.
- `direct` networking does not hide network identity; privacy networking has
  separate proxy and DNS prerequisites.
- Hideout does not claim protection after a target obtains guest root.
- `--backend native` is a development harness, not an isolation boundary.
- Compatible automatic macOS arm64 Lima runs share one profile-backed VM across
  project directories. Each session receives one exact `/workspace` view; this
  shared guest kernel is not a VM wall between projects. Use a dedicated named
  environment when projects require separate VM trust domains.
- After the final VM-dependent resource and provider cleanup release, Hideout
  waits 15 seconds and non-destructively stops the Lima VM. The environment,
  guest disk, caches, audit, and staged HostFS state remain; unknown ownership
  or backend state blocks automatic stop.
- Initial terminal dimensions and live SIGWINCH resize are supported. Exhaustive
  terminal-emulator, theme, OSC/CSI, and detach behavior remains outside the
  current claim.

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
