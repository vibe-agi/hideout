# Distribution And Bootstrap

<!-- markdownlint-disable MD013 -->

## Contract

Distribution is a product architecture concern for Hideout. A feature is not
product-complete if users must manually assemble hidden helper binaries,
backend prerequisites, schemas, or runtime directories.

This document follows [architecture-principles.md](architecture-principles.md)
and [init-task-architecture.md](init-task-architecture.md).

## Public Alpha Install

The primary macOS arm64 path is the official Vibe AGI Homebrew tap. It consumes
the exact signed and notarized GitHub Release package rather than rebuilding
source:

```bash
brew install vibe-agi/tap/hideout
hideout setup
```

Homebrew verifies the immutable archive checksum and installs Lima as a formula
dependency. The Hideout formula independently verifies the macOS code signature
and delegates to the package-owned typed installer with `--skip-init`. Formula
installation is therefore non-interactive and Cellar-scoped: it does not start
a VM, download the retained runtime, or write profile state under `~/.hideout`.
Interactive `hideout setup` is the authority-bearing first-run step. It presents
the fixed supported configuration and requires explicit confirmation, but does
not start a VM or download the retained runtime. Automation and advanced
configuration use `hideout init --no-input`.
Normal Homebrew uninstall preserves Hideout user state.

The canonical published formula lives in
[`vibe-agi/homebrew-tap`](https://github.com/vibe-agi/homebrew-tap), while
`packaging/homebrew/hideout.rb` is the release-synchronized source copy shipped
with this repository and package.

## Standalone Installer

The standalone installer remains an inspectable fallback for operators who do
not use Homebrew:

```bash
curl -fsSL https://raw.githubusercontent.com/vibe-agi/hideout/master/install.sh | sh
export PATH="$HOME/.local/bin:$PATH"
```

It reads the published identity from
`releases/current.json`, downloads the exact GitHub Release asset, verifies its
SHA-256, checks package version and source-commit binding, verifies the macOS
code signature, and then invokes the package's own typed installer with an
explicit prefix and store. It does not install Lima, use `sudo`, or modify shell
startup files. Override `--prefix` or `--store` when those defaults are not
appropriate. The standalone installer never configures a profile; run
`hideout setup` afterward.

Operators who do not run remote scripts directly can inspect the same script:

```bash
curl -fsSL https://raw.githubusercontent.com/vibe-agi/hideout/master/install.sh \
  -o /tmp/hideout-install.sh
less /tmp/hideout-install.sh
sh /tmp/hideout-install.sh
```

The equivalent manual package path is:

```bash
version=0.1.0-alpha.3
package="hideout-v${version}-darwin-arm64.tar.gz"
base="https://github.com/vibe-agi/hideout/releases/download/v${version}"
curl -fLO "$base/$package"
curl -fLO "$base/SHA256SUMS"
grep "  $package\$" SHA256SUMS | shasum -a 256 -c -
tar -xzf "$package"
./hideout/install.sh \
  --prefix "$HOME/.local" \
  --store "$HOME/.hideout" \
  --skip-init
```

After a manual install, run the same setup used by the Homebrew path:

```bash
hideout setup
```

## Update, Repair, And Remove

The installation provider owns package files. Homebrew users update, repair, or
remove the formula through Homebrew rather than mutating its Cellar prefix:

```bash
brew upgrade vibe-agi/tap/hideout
brew reinstall vibe-agi/tap/hideout
brew uninstall vibe-agi/tap/hideout
```

Those operations preserve the durable store at `~/.hideout`. The standalone
bootstrap may be rerun to install the current published package over a
compatible prior package. Verify before repair or removal:

```bash
hideout package verify "$HOME/.local"
hideout package repair --prefix "$HOME/.local" --dry-run
hideout package repair --prefix "$HOME/.local"
hideout package uninstall --prefix "$HOME/.local" --dry-run
hideout package uninstall --prefix "$HOME/.local"
```

Standalone repair removes only checksum-bound obsolete package-owned files.
Normal standalone uninstall removes package-owned files and reports the
preserved durable store; it does not remove unrelated prefix files. To remove
the durable store too, first preview the exact scope, then repeat the reported
store path as confirmation:

```bash
hideout package uninstall \
  --prefix "$HOME/.local" \
  --store "$HOME/.hideout" \
  --purge \
  --dry-run
hideout package uninstall \
  --prefix "$HOME/.local" \
  --store "$HOME/.hideout" \
  --purge \
  --confirm-purge "$HOME/.hideout"
```

The purge confirmation must resolve to the exact recorded store. The complete
file and directory scope is validated before the first deletion; an invalid or
out-of-root installed-state entry fails before a partial uninstall.

## Problem

Hideout is more than one binary. A working installation may need:

- `hideout`;
- Linux guest `hideout-shim`;
- Linux guest `hideout-hostfsd`;
- `tun2socks`;
- Lima or another backend;
- backend images, including declared guest base images;
- profile store;
- schema/version metadata;
- browser profile directories;
- policy script directories.

Without a bootstrap plan, the product feels like a repo experiment.

## User Goals

Source developers should be able to run:

```bash
scripts/install-local.sh
hideout init
hideout doctor --fix --apply
hideout run -- example-cli
```

and understand:

- what was installed;
- where runtime state lives;
- which backend will be used;
- whether network privacy is direct or tunneled;
- how to remove state.

## Install Artifacts

Core artifacts:

```text
hideout
hideout-shim
hideout-shim-linux-<arch>
hideout-hostfsd-linux-<arch>
install.sh
package-manifest.json
README.md
README.zh-CN.md
schemas
default profile templates
```

The package does not own or checksum `limactl`; Lima remains an explicit host
prerequisite for the supported VM path. The package does own the guest Linux
`tun2socks` privacy helper and verifies its artifact digest, pinned upstream
version, target, build mode, package ownership, and redistributed license.
`hideout package verify` and `doctor` distinguish the package-owned helper from
the remaining host prerequisite.

Optional artifacts:

```text
TUI assets
WebUI assets
backend templates
```

## Store Layout

Suggested durable root:

```text
~/.hideout/
  profiles/
  environments/
  sessions/
  bundles/
  install-state.json
  bin/
  cache/
  logs/
    init-audit.jsonl
  schemas/
```

`bundles/` and `install-state.json` follow the layout owned by
[ecosystem-foundation-design.md](ecosystem-foundation-design.md#bundle-store).

Suggested runtime root:

```text
~/.hideout/runtime/
  manager.sock
  ui/
  install-tasks/
```

Helper binaries should be discoverable in:

```text
~/.hideout/bin/
PATH
explicit env override
```

Explicit env overrides remain useful for development:

```text
HIDEOUT_LINUX_SHIM_PATH
HIDEOUT_LINUX_HOSTFSD_PATH
HIDEOUT_LINUX_TUN2SOCKS_PATH
```

An explicit helper override is considered discovered only when the path exists
and is not a directory. Missing override paths must be ignored by resolver
functions and reported by `doctor` or the relevant gate as missing helpers,
without printing the full search path list.

## First-Run Flow

First-run setup is an `InitPlan`, not a script. `hideout init`,
`hideout doctor --fix --dry-run|--apply`, future TUI first-run, and automatic
safe setup triggered by `hideout run` must use the same Manager-owned Init Task
Engine.

```text
hideout init
  -> detect platform
  -> create InitPlan
  -> apply safe tasks
  -> request confirmation for risky tasks
  -> verify task results
  -> audit changed resources
```

Typical task sequence:

```text
hideout init
  -> store.create
  -> profile.create
  -> identity.materialize
  -> schema.metadata.write
  -> select recommended backend
  -> backend.probe
  -> helper.locate or helper.install.linux-shim/helper.install.linux-hostfsd/
     helper.install.linux-session-supervisor
  -> network.mode.select
  -> doctor.check.light
  -> print next command
```

TUI may provide the interactive version (plain `hideout init` is already
interactive by default):

```bash
hideout init
```

or:

```bash
hideout tui
```

## Doctor Fix Flow

Plain `hideout doctor` gives the ordinary user a concise `Ready`,
`Needs attention`, or `Blocked` answer, the effective isolation/network
boundary, and safe next commands. It deliberately omits passing internal
checks and source-tree gate instructions. Use:

```bash
hideout doctor --verbose
```

to inspect every human-readable finding, or:

```bash
hideout doctor --format json
```

for the complete stable machine-readable report. Selecting `--level deep` or a
specific `--feature` also opts into detailed human output.

Doctor explains but does not silently repair. `doctor --fix --dry-run` builds
an InitPlan preview; `doctor --fix --apply` remediates safe missing pieces.

Safe fixes:

- create store directories;
- initialize default profile;
- build or install helper binaries:
  - `helper.install.linux-shim` builds `hideout-shim-linux-<arch>` into the
    store bin directory when the Lima backend is selected and no packaged helper
    is already discoverable;
  - `helper.install.linux-hostfsd` builds `hideout-hostfsd-linux-<arch>` into
    the store bin directory when the Lima backend is selected and no packaged
    helper is already discoverable;
  - `helper.install.linux-session-supervisor` builds
    `hideout-session-supervisor-linux-<arch>` into the store bin directory when
    the Lima backend is selected and no packaged helper is already
    discoverable;
- write helper manifests next to store-built helper binaries. A store helper is
  current only when the sibling manifest has schema
  `hideout.helper-manifest/v1`, the expected command, `linux/<arch>`, artifact
  name, and matching SHA-256. Explicit development override paths and packaged
  helpers outside the store may be used without a store manifest, but store
helpers without a current manifest are repairable by `doctor --fix --apply`;
- repair file permissions;
- download backend template metadata;
- repair current schema/version metadata;
- clean stale sessions.

Safe init and repair applies append `hideout.init-audit/v1` JSONL events to
`logs/init-audit.jsonl`. Events record the operation (`init.apply`,
`run.init.apply`, or `doctor.fix.apply`), profile, backend, network mode, task
ID, task kind, source, risk, result, decision, inputs, outputs, and error when
present. Dry runs do not write init audit. `hideout run` only writes these audit
events when pending lightweight store/profile/schema metadata InitTasks actually
need to be applied. Run-triggered auto init does not build backend helper
binaries; helper repair stays under explicit `hideout init --backend ...` or
`hideout doctor --fix --apply`, and runtime still fails closed if a required
helper is missing.

When `scripts/install-local.sh` is asked to initialize `tun2socks`, it must pass
only the proxy secret ref to `hideout init`. The raw proxy URL remains in the
operator environment and must not be written into the store, profile, package
metadata, or init audit.

The Phase 1 source-tree repair path uses `go build` from a verified Hideout
source root. Packaged release installers should place the same helpers in the
official artifact layout or store bin directory, making these tasks `ok` instead
of `pending`. When installers place helpers in the store bin directory, they
must also write current helper manifests.
Release-like tarballs must keep Linux guest helpers next to the installed
`hideout` binary so the first Lima `doctor --fix --dry-run` can discover them
without rebuilding from the source tree.
Packaging smoke follows the MVP delivery order: the CLI is the committed
surface, so the CLI path must always be proven, and TUI/WebUI smoke checks
join the packaging gate as those surfaces ship. The current tarball already
embeds both smoke surfaces, so today `hideout tui --once` renders once without
starting WebUI, and `hideout ui --no-open --print-url` starts the local
Manager/WebUI server, prints redacted entrypoint information, and exits
without opening a browser.

Unsafe fixes require explicit confirmation:

- install system backend dependency;
- change network route prerequisites;
- delete environment state;
- rotate identity;
- reset profile.

Disallowed fixes:

- execute arbitrary shell;
- run bundle or project install scripts;
- perform `curl | sh`;
- automatically use `sudo`;
- silently modify shell rc files, Git config, SSH config, browser profiles, or
  profile authority;
- add HostFS grants, passthrough mounts, PortBridge, OpenTarget, or network
  routes without a typed Manager plan.

The user-invoked distribution bootstrap above is not an Init Task or project
hook. It only installs a release already selected by the operator and then
delegates initialization to the typed Manager engine. Bundles, projects,
targets, and policy scripts cannot invoke or supply that bootstrap.

## Init Task Boundary

The product supports typed initialization tasks. It does not support arbitrary
initialization scripts.

The allowed Phase 1 task set, including `doctor.fix.safe`, is defined in
[init-task-architecture.md](init-task-architecture.md) and is not duplicated
here.

Project and bundle artifacts may declare requirements or setup hints. They
cannot provide executable task bodies. Manager turns their requirements into an
InitPlan and asks for explicit apply when authority changes.

Session bootstrap files remain separate. They are generated by Hideout for a
single run and are not controlled by project or bundle initialization.

## Guest Image Distribution

A bundle or project may declare a guest base image reference: an image name
plus digest. The declaration is guest-domain data and does not pass the host
trust gate. Fetching the image, caching it, verifying its digest, and
upgrading the cache are Go-owned distribution concerns. Backends consume only
digest-verified images, and the image digest participates in the environment
fingerprint, so changing the declared image means a new environment.

## Versioning

Hideout must track:

```text
product version
profile schema version
environment schema version
session schema version
helper binary version
backend template version
```

Rules:

- before the first public release, Hideout does not maintain compatibility with
  draft install-state metadata or profile schemas;
- stale or invalid install-state metadata is repaired by writing the current
  `install-state.json`, not by preserving draft metadata semantics;
- post-release schema upgrade handling is a separate future design and is not
  part of the current Phase 1 implementation;
- helper binary version mismatch is a doctor warning or failure depending on
  capability;
- release notes must call out schema changes.

## macOS Distribution

macOS product distribution needs:

- signed `hideout` binary;
- notarization for user trust;
- helper binary integrity;
- Lima prerequisite detection;
- clear prompts for installing backend dependencies.

Hideout should not silently install privileged system components.

Source-tree installs use `scripts/install-local.sh` for development and
dogfood. It builds `hideout`, the host command shim, and Linux guest
helpers into one prefix, then runs template-aware `hideout init --no-input`
through the normal
Manager-owned Init Task Engine unless `--skip-init` is set. This is not a
separate bootstrap path and must not grow its own initialization semantics.
The default install backend follows the runtime default, Lima; native remains an
explicit weak-isolation development option. Source-tree repair may use
`HIDEOUT_SOURCE_ROOT` when `doctor --fix --apply` is run outside the repository.
Source-tree installs require Go.

The public-alpha GitHub Release channel uses `scripts/package-local.sh` to stage
one macOS arm64 package tree, sign every Mach-O, and finalize the same frozen
tree without rebuilding it. The published release identity is recorded in
`releases/current.json`; candidate and no-publish workflows still must not claim
availability before protected promotion and anonymous redownload succeed. The
tarball contains `bin/`, package-root `install.sh`,
`package-manifest.json`, `LICENSE`, `THIRD_PARTY_NOTICES.md`, `SECURITY.md`,
English and Chinese README entrypoints, `schemas/`, `docs/`, host-app data,
packaging metadata, and the retained runtime catalog/build metadata under a
single `hideout/` root. The manifest records product version, full source
commit, clean-state observation, build identity, target platform, retained
runtime identity, signing mode, package-relative layout, and SHA-256 for every
package-owned file. The package-root installer calls the packaged `hideout`
binary to validate `package-manifest.json` and recalculate all declared
checksums before copying binaries. `scripts/test-package-smoke.sh`
extracts that tarball into a temporary prefix, validates the manifest, proves
each manifest-declared path exists with the expected file type, recalculates
declared file checksums, then runs extracted template-aware `hideout init`,
`hideout doctor`, `hideout tui --once`, and `hideout ui --no-open --print-url`. It also
runs package-root `install.sh` into a separate temporary prefix/store, verifies
the installed layout works without source-tree state, verifies installed Lima
helper discovery from that prefix, and checks that `install.sh --skip-init`
copies binaries without writing init state. The package-root installer must fail
before copying binaries when the extracted package is missing
`package-manifest.json`, the host shim, Linux guest shim, Linux HostFS daemon,
guest-local DNS stub, or any manifest-declared checksum does not match the
extracted file.
Release-like tarball installs must not require Go; they use the packaged
`hideout` binary for package verification and packaged Linux helpers for Lima.

The release itself is exactly four immutable assets: the versioned package,
the bounded evidence bundle, the machine-readable release manifest, and
`SHA256SUMS`. Candidate construction retains a private draft; a separate
protected promotion validates exact bytes, publishes once, anonymously
redownloads every asset, and only then emits the receipt that can update
`releases/current.json` and public documentation.

The official Homebrew formula consumes the immutable public release archive and
installs the same package-owned artifact layout without requiring Go or source
checkout state:

```text
bin/hideout
bin/hideout-shim
bin/hideout-shim-linux-<arch>
bin/hideout-hostfsd-linux-<arch>
```

Package installers may leave `hideout init` to the first run, but package or
formula smoke tests must prove that template-aware `hideout init --no-input`,
`hideout doctor`, helper discovery, and helper manifests work from the
installed layout.

## Linux Distribution

Linux product distribution needs:

- package or tarball;
- container backend prerequisite checks;
- helper binaries for matching architecture;
- TUN/tun2socks permission guidance;
- systemd integration for `hideoutd` as the daemon ships on Linux.

## CI And Release Gates

Release candidate should verify:

- `go test ./...`;
- docs lint;
- schema parse and metadata repair tests;
- source-tree install smoke;
- release-like tarball package smoke;
- release dogfood evidence manifest schema validation, including the generated
  tarball artifact file name, byte size, and SHA-256;
- Gate 0 through Gate 4;
- Lima Gate2 on macOS;
- generic CLI dogfood smoke through
  `scripts/test-phase1.sh --release-candidate` or
  `scripts/test-release-dogfood.sh`;
- hidden proxy gate;
- helper binary build for supported architectures;
- package smoke install;
- install-state metadata repair smoke.

## Phase Plan

### Current

- developer builds helper binaries;
- env overrides exist;
- `doctor` reports many core checks.
- `scripts/install-local.sh` provides a source-tree install path for private
  pre-release development and runs typed template-aware `hideout init --no-input` by default;
- `scripts/test-install-smoke.sh` verifies installed binaries, helper
  manifests, init metadata, idempotent init, doctor, `doctor --fix --dry-run`,
  and safe `doctor --fix --apply` from temporary prefix/store roots;
- `scripts/package-local.sh` and `scripts/test-package-smoke.sh` verify a
  release package layout can be extracted, installed through package-root
  `install.sh`, and initialized without hidden repository state;
- `hideout-alpha-candidate.yml` stages, signs, notarizes, and retains one exact
  private draft; `test-public-alpha-candidate.sh` binds that package to clean
  install and real Gate 2/Gate 3 evidence; and `hideout-alpha-promote.yml`
  requires protected approval plus anonymous redownload before a public receipt;
- the release named by `releases/current.json` has passed that workflow and is
  the current public supervised alpha;
- root `install.sh` provides the stable standalone bootstrap and is covered by
  a local exact-package install test that does not redownload the retained
  runtime;
- `vibe-agi/homebrew-tap` publishes the supported `hideout` formula; its CI
  performs strict online audit, install, package verification, test, and
  uninstall without creating `~/.hideout`;
- `packaging/homebrew/hideout.rb` records the release-synchronized formula
  source shipped with the package;
- template-aware `hideout init --no-input` applies safe machine initialization tasks;
- `hideout run` applies pending lightweight store/profile/schema metadata
  InitTasks through Manager before starting a session or backend prepare step;
- `doctor --fix --dry-run` and safe `doctor --fix --apply` use InitTask plan/apply;
- Manager API exposes initial init, bundle, and project status summaries.

### Next Product Increment

- automate the post-release formula revision and checksum update while keeping
  the immutable GitHub Release manifest as package identity truth;
- add richer `doctor --fix --dry-run|--apply` remediation coverage for backend
  prerequisites and helper repair beyond source-tree builds;
- automate rebuilding and review of the pinned `tun2socks` helper when its
  upstream version changes.

### Later

- signed installer;
- auto-update;
- `hideoutd` service packaging, such as launchd and systemd units.

## Open Questions

- Which release cadence should trigger review of a newer pinned `tun2socks`
  version?
