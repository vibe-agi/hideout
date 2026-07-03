# Policy And Config Supply Chain

<!-- markdownlint-disable MD013 -->

## Contract

Policy and Config Supply Chain defines how Hideout scripts, presets, templates,
and configuration packages are authored, shared, installed, updated, verified,
and overridden.

This document follows [architecture-principles.md](architecture-principles.md)
and [manager-control-plane.md](manager-control-plane.md). The bottom-layer
runtime contract is defined in
[ecosystem-foundation-design.md](ecosystem-foundation-design.md).

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

## Artifact Types

### Bundle

Bundle is the publishable unit.

```json
{
  "apiVersion": "hideout.bundle/v1",
  "kind": "PolicyBundle",
  "name": "web-agent-safe",
  "version": "1.0.0",
  "publisher": "hideout-community",
  "description": "Safe defaults for web development agents",
  "compatibility": {
    "hideout": ">=0.1.0 <0.2.0",
    "profileSchema": "hideout.profile/v1"
  },
  "entrypoints": {
    "command.decide": "policy/command_decide.js",
    "audit.redact": "policy/audit_redact.js"
  },
  "permissions": [
    "command.decide",
    "audit.redact",
    "hostfs.template",
    "opentarget.template"
  ],
  "inputs": {},
  "checksums": {},
  "signature": null
}
```

### Recipe

Recipe is a smaller reusable policy pattern inside a bundle.

Examples:

```text
allow external docs URLs
deny localhost browser open
allow read docs/*.md
tag broad machine identity probes
```

Persona recipes are higher-level recipes for developer workflows. They may
compose multiple adapters, templates, and checks:

```text
h5-dev
android-dev
ios-assist
backend-agent
```

### Hideoutfile

Hideoutfile is the project-level shareable manifest.

It should be safe to commit to Git. It is a project suggestion and constraint
file, not a profile dump.

Example:

```json
{
  "apiVersion": "hideout.project/v1",
  "kind": "ProjectManifest",
  "requirements": {
    "hideout": ">=0.1.0 <0.2.0",
    "capabilities": [
      "hostfs.v1",
      "opentarget.host_open"
    ]
  },
  "bundles": [
    {
      "source": "github:vibe-agi/hideout-bundles/web-agent-safe@1.0.0",
      "name": "web-agent-safe"
    }
  ],
  "projectPolicy": {
    "network": {
      "mode": "direct",
      "warning": "network identity visible"
    },
    "hostfsTemplates": [
      {
        "bundle": "web-agent-safe",
        "id": "project-docs",
        "inputs": {
          "path": "${workspace}/docs/*.md"
        }
      }
    ]
  }
}
```

The canonical machine-readable format should be JSON with schema validation.
Friendly formats such as TOML or YAML may be added later, but they must compile
to the same canonical model.

Avoid a top-level `profile` field. A project can request policy and declare
requirements, but it does not own the user's local profile, identity, secrets,
or private overrides.

### Lock File

Installed or project-resolved packages should be pinned.

```text
hideout.lock.json
```

Lock file records:

- bundle source;
- version;
- resolved commit or artifact digest;
- checksums;
- compatibility result.

Install time, local cache paths, user approval history, and private trust
overrides belong in local Hideout install state, not the project lock.

## Local Layout

Suggested store:

```text
~/.hideout/
  bundles/
    <publisher>/
      <name>/
        <version>/
          bundle.json
          policy/
          recipes/
          schemas/
          README.md
  profiles/
    default/
      profile.json
      overrides.json
  package-lock.json
```

Project layout:

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

## Manager Resources

Manager Control Plane should expose:

```text
Bundle
BundleSource
BundleVersion
BundleReference
BundleEntrypoint
BundlePermission
Recipe
InitRequirement
InitPlan
ProjectManifest
ProjectLock
ProjectApplyPlan
ProfileOverride
InstallTask, as an InitTask subtype for bundle artifact placement
UpdatePlan
CompatibilityReport
VerificationReport
TrustPolicy
ExportPlan
RedactionRule
```

All install/update/remove operations should be plan/apply:

```text
PlanBundleInstall(source) -> InstallPlan
ApplyBundleInstall(planId) -> BundleVersion

PlanBundleUpdate(bundle) -> UpdatePlan
ApplyBundleUpdate(planId) -> BundleVersion
```

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

Composition:

```text
Hideout defaults
  + bundle defaults
  + project Hideoutfile
  + profile local overrides
  + run flags
  - deny rules
  = effective policy
```

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

## Phase Plan

### Phase 1 Design

- document bundle model;
- keep scripts constrained;
- local profile remains canonical runtime state.
- keep install separate from enable;
- route bundle and project state through Manager resources.

### Next Product Increment

- `hideout bundle install/list/verify`;
- local directory and Git source;
- bundle schema;
- lock file;
- Manager resources;
- TUI bundle list and update warnings.

### Later

- registry;
- signatures;
- publisher trust;
- marketplace;
- team policy server;
- automatic compatibility test matrix.

## Open Questions

- Should `Hideoutfile` be JSON only at first, or should we support a friendly
  format immediately?
- What is the first official community bundle repository?
- How strict should secret scanning be during bundle export?
