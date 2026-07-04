# Policy And Config Supply Chain

<!-- markdownlint-disable MD013 -->

## Contract

Policy and Config Supply Chain defines how Hideout scripts, presets, templates,
and configuration packages are authored, shared, installed, updated, verified,
and overridden.

This document follows [architecture-principles.md](architecture-principles.md),
[manager-control-plane.md](manager-control-plane.md), and the bottom-layer
ecosystem runtime contract in
[ecosystem-foundation-design.md](ecosystem-foundation-design.md). It defines
authoring, sharing, installation, update, trust, override, and export behavior.
It does not redefine the ecosystem resource model, effective policy composition
order, Hideoutfile schema, or phase status.

The goal is to make Hideout community-extensible without turning local privacy
policy into unreviewed scattered files.

## Product Goal

Hideout should have a shareable ecosystem similar in spirit to Git-managed
developer setup files:

```text
Brewfile
dotfiles
devcontainer.json
editor settings
tool recipes
```

But Hideout packages affect privacy boundaries, so they require stronger
structure:

- explicit permissions;
- schema validation;
- version pinning;
- compatibility checks;
- checksums and future signatures;
- install/update audit;
- local overrides;
- rollback.

The ecosystem is not just a sharing format. It depends on stable bottom-layer
contracts for artifact verification, permission review, Manager resources,
effective policy compilation, audit attribution, and release gates.

Initialization requirements are declarative. Bundles and project manifests may
declare required capabilities, setup checks, package hints, and tool preset
expectations, but they must not carry executable install or init scripts.

## Core Principle

Profiles are local instances. Bundles are shareable artifacts.

```text
PolicyBundle / ConfigBundle
  shareable, versioned, reviewable, installable

Profile
  local user state, identity, overrides, paths, secrets refs, active decisions
```

A user should be able to share a good web-agent policy without sharing their
real machine identity, host paths, proxy secrets, or audit history.

## What Can Be Shared

### Policy Bundles

Shareable:

- command proxy decisions;
- OpenTarget policies;
- HostFS rule templates;
- Network mode recommendations;
- audit redaction rules;
- goja policy scripts;
- JavaScript adapters that map domain intent into capability proposals;
- persona recipes that compose adapters, policy templates, and doctor checks;
- compatibility rules for known tools;
- doctor checks for known setup patterns.

Example:

```text
web-agent-safe
  command.decide for open
  preview and browser-control adapters
  HostFS templates for project docs and generated reports
  audit tags for broad filesystem probing
  network recommendation: tun2socks preferred, direct allowed with warning
```

### Tool Presets

Shareable:

- guest required commands;
- setup checks;
- package install hints;
- environment assumptions;
- command proxy registrations.

Not shareable as authority:

- actual host credentials;
- host-specific binary paths;
- user-specific install secrets.

Tool presets may produce `InitRequirement` records. Manager turns those
requirements into an InitPlan before anything is applied.

### HostFS Templates

Shareable as templates, not as resolved local grants.

Good:

```json
{
  "kind": "hostfs.template",
  "id": "downloads-text-files",
  "grant": "read:${downloads}/*.txt",
  "inputs": {
    "downloads": {
      "type": "hostPath",
      "description": "Downloads directory"
    }
  }
}
```

Bad:

```json
{
  "grant": "read:/Users/alice/Downloads/*.txt"
}
```

Absolute paths may exist in local profile overrides. Shared bundles should use
inputs, variables, workspace-relative references, or named system locations.

### OpenTarget Presets

Shareable:

- web development preview behavior;
- browser launch defaults;
- browser-control policy defaults;
- mobile simulator workflows;
- IDE open behavior.

Examples:

```text
web-dev-preview
android-agent-basic
ios-simulator-preview
safe-browser-control
```

OpenTarget presets should reference adapters instead of encoding protocol logic
in Core. For example, `android-agent-basic` may depend on an adb adapter that
proposes a constrained PortBridge, while Core still owns bridge validation,
lifetime, audit, and cleanup.

### Network Policy Templates

Shareable:

- recommended mode;
- DNS verification requirement;
- leak warning policy;
- allowed proxy scheme types.

Not shareable:

- proxy URL with credentials;
- proxy secret values;
- local private proxy host unless represented as an input.

### Script Packages

Shareable:

- `command.decide`;
- `audit.redact`;
- future `network.decide`;
- future `opentarget.decide`.

Every script package must declare its entrypoints and required context fields.

### Test Recipes

Shareable:

- expected denied paths;
- expected OpenTarget behavior;
- tool compatibility smoke tests;
- bundle self-tests.

These help the community maintain bundles as Hideout evolves.

### Documentation And Explanations

Shareable:

- README;
- rationale;
- changelog;
- screenshots for TUI/WebUI;
- examples;
- upgrade notes.

## What Must Not Be Shared

Never share by default:

- profile identity material;
- machine-id;
- fake home contents;
- browser profile state;
- session state;
- environment state;
- broker tokens;
- proxy secret values;
- real API keys;
- audit logs unless explicitly sanitized;
- HostFS overlay contents;
- local absolute paths unless intentionally published as examples;
- user-specific allow/deny decisions generated from private usage.

`hideout bundle export` must redact or reject these by default.

## Artifact Supply Rules

The canonical artifact model is owned by
[ecosystem-foundation-design.md](ecosystem-foundation-design.md#domain-resources),
with bundle and project manifest shapes in
[Bundle Manifest Contract](ecosystem-foundation-design.md#bundle-manifest-contract)
and [Hideoutfile Contract](ecosystem-foundation-design.md#hideoutfile-contract).
That includes Bundle, Recipe, Hideoutfile, ProjectLock, BundleReference,
entrypoints, permissions, and the Policy Compiler composition order.

This document does not redefine those shapes. It adds supply-chain rules:

- sources must be pinned to a ref, digest, or local content checksum before an
  artifact can be enabled;
- local absolute paths must stay in local overrides unless explicitly marked as
  examples;
- project discovery must become a reviewable plan before any local policy
  changes;
- project locks may record resolved refs, artifact digests, checksums,
  compatibility results, and schema version;
- install time, local cache paths, user approval history, private trust
  overrides, local profile IDs, and local absolute paths must stay in local
  Hideout install state, not in committed project files.

Friendly formats such as TOML or YAML may be added later, but they must compile
to the same canonical model and pass the same schema and permission checks.

## Local Layout

The authoritative bundle store and local install-state layout are part of the
ecosystem runtime contract in
[ecosystem-foundation-design.md](ecosystem-foundation-design.md#bundle-store)
and the distribution contract in
[distribution-bootstrap.md](distribution-bootstrap.md). Supply-chain logic only
requires immutable installed bundle versions, local install state outside the
project, and commit-safe project lock files.

Project repositories may contain:

```text
project/
  Hideoutfile
  hideout.lock.json
```

## Install Sources

Phase 1 ecosystem should start with Git and local files.

Supported first:

```text
local directory
tarball
git URL
github:owner/repo/path@ref
```

Later:

```text
official registry
private registry
team policy server
signed marketplace
```

Starting with Git lowers friction and lets the community participate before a
central marketplace exists.

## CLI Shape

Bundle commands:

```bash
hideout bundle list
hideout bundle install ./web-agent-safe
hideout bundle install github:vibe-agi/hideout-bundles/web-agent-safe@v1.0.0
hideout bundle update web-agent-safe
hideout bundle rollback web-agent-safe
hideout bundle remove web-agent-safe
hideout bundle verify web-agent-safe
hideout bundle export --from-profile default --out ./my-policy
```

Project commands:

```bash
hideout project init
hideout project apply
hideout project diff
hideout project verify
```

Profile commands:

```bash
hideout profile diff default
hideout profile explain default
```

Bundle enablement should stay under `hideout bundle`:

```bash
hideout bundle enable web-agent-safe --profile default
```

Profile commands should focus on identity, clone, rotate, path, diff, and
explain.

## Manager Supply-Chain Behavior

The ecosystem resource registry is owned by
[ecosystem-foundation-design.md](ecosystem-foundation-design.md#domain-resources).
Supply-chain operations must use those Manager resources instead of writing
profiles, bundle store state, project locks, or trust state directly.

All install, update, remove, enable, project apply, rollback, and export
operations should be plan/apply:

```text
PlanBundleInstall(source) -> InstallPlan
ApplyBundleInstall(planId) -> BundleVersion

PlanBundleUpdate(bundle) -> UpdatePlan
ApplyBundleUpdate(planId) -> BundleVersion
```

Plans must show permission changes before apply. Apply must audit the artifact
source, resolved digest or ref, trust result, changed enabled references, and
the profile or project scope affected.

## TUI And WebUI Surfaces

TUI:

- installed bundles;
- available updates;
- compatibility warnings;
- project Hideoutfile status;
- install/update progress;
- profile bundle references.

WebUI:

- bundle detail;
- changelog;
- policy diff;
- script entrypoints;
- permission review;
- trust status;
- rollback history.

## Update Policy

Recommended defaults:

```text
auto-check: yes
auto-download: optional
auto-apply: no
```

Updates may change privacy boundaries. They require a visible diff before apply.

Update diff should show:

- changed scripts;
- changed permissions;
- changed HostFS templates;
- changed OpenTarget defaults;
- changed network recommendations;
- changed init requirements;
- new or removed recipes;
- schema contract changes.

## Trust Model

Initial trust levels:

```text
local
  User-authored local bundle.

git-unverified
  Installed from Git without signature. Show warning and digest.

verified-checksum
  Digest pinned in lock file.

signed
  Future signed bundle.

official
  Future Hideout-maintained source.
```

Trust does not bypass runtime validation. A signed bundle can still only use
declared permissions and constrained script APIs.

## Validation

Install must validate:

- bundle schema;
- script size;
- declared entrypoints;
- permission list;
- compatibility;
- checksums;
- no forbidden files;
- no absolute local paths in shareable templates unless marked as example;
- no embedded secret-looking values when detectable.

Runtime must validate again when applying the effective policy.

Phase 1 bundles may contain config, scripts, docs, schemas, examples, and test
fixtures. They must not contain executable binaries, dynamic libraries, shell
install scripts, backend helper binaries, or auto-run hooks outside registered
goja entrypoints.

They also must not include first-run init scripts. Setup guidance must be
represented as `InitRequirement` records and executed only through Manager
InitTask plan/apply.

## Override Model

Local overrides are first-class.

The effective policy composition order is owned by the Policy Compiler contract
in
[ecosystem-foundation-design.md](ecosystem-foundation-design.md#policy-compiler).
Supply-chain code must present local overrides in that order; it must not
invent a second composition rule.

Overrides should be visible in `explain`, TUI, WebUI, and `profile diff`.

## Community Model

Recommended ecosystem structure:

```text
hideout-bundles/
  official/
    web-agent-safe/
    node-dev/
    browser-preview/
  community/
    android-basic/
    ios-simulator/
    python-data/
```

Community contribution requirements:

- README explains purpose and risks;
- bundle self-test exists;
- changelog exists;
- permissions are minimal;
- no secret values;
- no user-specific absolute paths;
- compatibility range is declared;
- policy decisions are auditable.

This makes bundles reviewable in pull requests and forkable like dotfiles or
Brewfile-based setup repos.

## Supply-Chain Delivery Checklist

Current product status is owned by [STATUS.md](STATUS.md). Ecosystem delivery
sequence is owned by
[ecosystem-foundation-design.md](ecosystem-foundation-design.md#phase-plan).
This document contributes only the supply-chain checklist for that sequence:

- bundle schema, script size, entrypoint, permission, compatibility, checksum,
  forbidden-file, and secret-looking-value validation;
- local directory and Git source resolution;
- install/update/remove as Manager plan/apply operations;
- explicit install-vs-enable separation;
- export redaction and rejection reports;
- TUI/WebUI bundle list, update, trust, and diff surfaces;
- future signatures, publisher trust, registry, and compatibility farm design.

If this checklist conflicts with the ecosystem phase plan, the phase plan wins
and this section must be updated to point at the new source of truth.

## Open Questions

- Should `Hideoutfile` be JSON only at first, or should we support a friendly
  format immediately?
- What is the first official community bundle repository?
- How strict should secret scanning be during bundle export?
