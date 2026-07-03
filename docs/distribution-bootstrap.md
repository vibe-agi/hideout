# Distribution And Bootstrap

<!-- markdownlint-disable MD013 -->

## Contract

Distribution is a product architecture concern for Hideout. A feature is not
product-complete if users must manually assemble hidden helper binaries,
backend prerequisites, schemas, or runtime directories.

This document follows [architecture-principles.md](architecture-principles.md)
and [init-task-architecture.md](init-task-architecture.md).

## Problem

Hideout is more than one binary. A working installation may need:

- `hideout`;
- Linux guest `hideout-shim`;
- Linux guest `hideout-hostfsd`;
- `tun2socks`;
- Lima or another backend;
- backend images;
- profile store;
- schema/version metadata;
- browser profile directories;
- policy script directories.

Without a bootstrap plan, the product feels like a repo experiment.

## User Goals

Users should be able to run:

```bash
scripts/install-local.sh
hideout init
hideout doctor --fix
hideout run -- claude
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
hideout-shim-linux-<arch>
hideout-hostfsd-linux-<arch>
tun2socks-<platform>-<arch>
schemas
default profile templates
```

Optional artifacts:

```text
TUI assets
WebUI assets
policy recipes
backend templates
```

## Store Layout

Suggested durable root:

```text
~/.hideout/
  profiles/
  environments/
  sessions/
  bin/
  cache/
  logs/
    init-audit.jsonl
  schemas/
  policy-recipes/
```

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
HIDEOUT_TUN2SOCKS_PATH
```

An explicit helper override is considered discovered only when the path exists
and is not a directory. Missing override paths must be ignored by resolver
functions and reported by `doctor` or the relevant gate as missing helpers,
without printing the full search path list.

## First-Run Flow

First-run setup is an `InitPlan`, not a script. `hideout init`,
`hideout doctor --fix`, TUI first-run, and automatic safe setup triggered by
`hideout run` must use the same Manager-owned Init Task Engine.

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
  -> helper.locate or helper.install.official
  -> network.mode.select
  -> doctor.check.light
  -> print next command
```

TUI may provide the interactive version:

```bash
hideout init --interactive
```

or:

```bash
hideout tui
```

## Doctor Fix Flow

`doctor` explains. `doctor --fix` builds an InitPlan that remediates safe
missing pieces.

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
- write helper manifests next to store-built helper binaries. A store helper is
  current only when the sibling manifest has schema
  `hideout.helper-manifest/v1`, the expected command, `linux/<arch>`, artifact
  name, and matching SHA-256. Explicit development override paths and packaged
  helpers outside the store may be used without a store manifest, but store
helpers without a current manifest are repairable by `doctor --fix`;
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
`hideout doctor --fix`, and runtime still fails closed if a required helper is
missing.

The Phase 1 source-tree repair path uses `go build` from a verified Hideout
source root. Packaged release installers should place the same helpers in the
official artifact layout or store bin directory, making these tasks `ok` instead
of `pending`. When installers place helpers in the store bin directory, they
must also write current helper manifests.

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

## Init Task Boundary

The product supports typed initialization tasks. It does not support arbitrary
initialization scripts.

Allowed Phase 1 tasks:

```text
store.create
profile.create
identity.materialize
schema.metadata.write
helper.locate
helper.install.official
backend.probe
network.mode.select
doctor.check.light
doctor.fix.safe
project.manifest.create
```

Project and bundle artifacts may declare requirements or setup hints. They
cannot provide executable task bodies. Manager turns their requirements into an
InitPlan and asks for explicit apply when authority changes.

Session bootstrap files remain separate. They are generated by Hideout for a
single run and are not controlled by project or bundle initialization.

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

Source-tree installs use `scripts/install-local.sh` during the private
pre-release phase. It builds `hideout`, the host command shim, and Linux guest
helpers into one prefix, then runs `hideout init --no-input` through the normal
Manager-owned Init Task Engine unless `--skip-init` is set. This is not a
separate bootstrap path and must not grow its own initialization semantics.

Release-like tarball packaging uses `scripts/package-local.sh` during private
pre-release development. The tarball contains `bin/`, `schemas/`, `docs/`, and
`packaging/` under a single `hideout/` root. `scripts/test-package-smoke.sh`
extracts that tarball into a temporary prefix, then runs installed
`hideout init --no-input` and `hideout doctor` from the extracted binary.

The draft Homebrew formula lives at `packaging/homebrew/hideout.rb` and supports
private `brew install --HEAD` workflows once the operator has repository access.
It builds the same installed artifact layout:

```text
bin/hideout
bin/hideout-shim
bin/hideout-shim-linux-<arch>
bin/hideout-hostfsd-linux-<arch>
```

Package installers may leave `hideout init` to the first run, but package or
formula smoke tests must prove that `hideout init --no-input`, `hideout doctor`,
helper discovery, and helper manifests work from the installed layout.

## Linux Distribution

Linux product distribution needs:

- package or tarball;
- container backend prerequisite checks;
- helper binaries for matching architecture;
- TUN/tun2socks permission guidance;
- systemd integration only if a manager daemon becomes product.

## CI And Release Gates

Release candidate should verify:

- `go test ./...`;
- docs lint;
- schema parse and metadata repair tests;
- source-tree install smoke;
- release-like tarball package smoke;
- Gate 0 through Gate 4;
- Lima Gate2 on macOS;
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
  pre-release development and runs typed `hideout init --no-input` by default;
- `scripts/test-install-smoke.sh` verifies installed binaries, helper
  manifests, init metadata, idempotent init, doctor, `doctor --fix --dry-run`,
  and safe `doctor --fix` from temporary prefix/store roots;
- `scripts/package-local.sh` and `scripts/test-package-smoke.sh` verify a
  release-like tarball layout can be extracted and initialized without hidden
  repository state;
- `packaging/homebrew/hideout.rb` defines the draft Homebrew `--HEAD` formula
  and its formula-level `init`/`doctor` smoke;
- `hideout init --no-input` applies safe machine initialization tasks;
- `hideout run` applies pending lightweight store/profile/schema metadata
  InitTasks through Manager before starting a session or backend prepare step;
- `doctor --fix --dry-run` and safe `doctor --fix` use InitTask plan/apply;
- Manager API exposes initial init, bundle, and project status summaries.

### Next Product Increment

- decide whether the draft Homebrew formula is the first public install channel
  or whether it ships after a signed macOS package;
- package release artifacts with the same helper layout verified by
  `scripts/test-install-smoke.sh`;
- add package smoke install for the chosen channel;
- add richer `doctor --fix` remediation coverage for backend prerequisites and
  helper repair beyond source-tree builds;
- decide whether `tun2socks` ships as a bundled helper or an explicitly checked
  external prerequisite.

### Later

- signed installer;
- auto-update;
- team-managed distribution;
- background manager service.

## Open Questions

- What is the first official install channel?
- How should backend images be cached and upgraded?
