# Feature Specification: Packaging, Install, Upgrade, And Uninstall

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `013-packaging-install-upgrade-uninstall`

**Created**: 2026-07-09

**Status**: Implemented — the exact claim surface and non-claims live in [docs/STATUS.md](../../docs/STATUS.md)

**Input**: User description: "Follow .tmp/011-016-plan.md using speckit-* skills; complete and commit one feature at a time. 013 makes Hideout installable and upgradeable outside a development checkout while preserving helper binaries, schemas, gates, profile compatibility, and cleanup."

## User Scenarios & Testing _(mandatory)_

### User Story 1 - Install From An Alpha Package (Priority: P1)

A technically inclined operator downloads a Hideout alpha package, chooses an install prefix, runs the installer, and can use `hideout` plus its helper binaries without `go run`, a source checkout, or repo-internal paths.

**Why this priority**: Installability is a constitutional product requirement. Hideout is not ready for external alpha if operators must manually assemble the CLI, helper binaries, schemas, scripts, or runtime layout.

**Independent Test**: Can be tested by building a package into a temporary directory, installing it under a temporary prefix, setting a fresh store root, and running packaged smoke checks that prove `hideout`, helpers, schemas, and Gate 0 local checks are usable from the installed layout.

**Acceptance Scenarios**:

1. **Given** a generated package artifact and an empty install prefix, **When** the operator runs the package installer, **Then** the installer writes the CLI, all required helper binaries, schemas, scripts, docs, and package manifest under that prefix and reports the installed paths.
2. **Given** an installed prefix and no source checkout on `PATH`, **When** the operator runs `hideout version`, `hideout package verify`, and Gate 0 package smoke checks, **Then** each command succeeds using installed artifacts only.
3. **Given** a package with a missing or mismatched helper binary, **When** the operator verifies the package or runs install smoke, **Then** the command fails closed with a doctor-style hint naming the missing or mismatched artifact.

---

### User Story 2 - Upgrade Without Losing State (Priority: P2)

An existing operator installs a newer package over a previous install and expects the toolchain to upgrade while profiles, audit logs, evidence, adapter registry, decisions, and durable state remain intact.

**Why this priority**: Alpha users will need frequent updates. Upgrade behavior must not look like a reinstall that silently destroys evidence or profile state.

**Independent Test**: Can be tested by installing version A into a temporary prefix with a populated store, installing version B over the same prefix, and proving package files changed while durable store state and profile evidence remain.

**Acceptance Scenarios**:

1. **Given** an installed package and durable Hideout store data, **When** the operator installs a newer package to the same prefix, **Then** package-owned files are replaced or verified while profiles, audit, evidence, adapter registry, and decisions remain untouched.
2. **Given** a package whose migration range does not include the current installed state, **When** the operator attempts upgrade, **Then** the installer fails closed before mutating package files and prints the incompatible range.
3. **Given** an installed package with unchanged checksums, **When** the operator reruns install to the same prefix, **Then** the operation is idempotent and reports no destructive state action.

---

### User Story 3 - Uninstall Safely (Priority: P3)

An operator can preview and perform uninstall of package-owned files while preserving user state by default, and can only remove durable profile/audit/evidence state with an explicit purge flag.

**Why this priority**: Cleanup is part of the runtime lifecycle, but uninstall must not become a data-loss footgun.

**Independent Test**: Can be tested by installing a package into a temporary prefix, creating durable store state, running uninstall dry-run and uninstall, then separately running uninstall with `--purge` and verifying the exact removal boundary.

**Acceptance Scenarios**:

1. **Given** an installed package, **When** the operator runs uninstall dry-run, **Then** Hideout reports the exact package-owned files and directories that would be removed and does not remove anything.
2. **Given** an installed package and durable user state, **When** the operator runs uninstall without `--purge`, **Then** package-owned files are removed but profiles, audit, evidence, adapter registry, decisions, and store data remain.
3. **Given** an installed package and durable user state, **When** the operator runs uninstall with `--purge`, **Then** durable state removal is explicit in the plan and survivor audit evidence outside the deleted store records the purge decision.

---

### User Story 4 - Documentation Uses Packaged Paths (Priority: P4)

A new operator following README and docs sees packaged commands as the main path, with source checkout commands reserved for development.

**Why this priority**: External alpha users should not have to infer which commands are product paths and which are maintainer workflows.

**Independent Test**: Can be tested by scanning README/docs and running the package smoke script to ensure primary examples use installed commands and package verification rather than repo-only invocations.

**Acceptance Scenarios**:

1. **Given** the docs for installation, first run, Gate 0 smoke, and troubleshooting, **When** a user follows the primary path, **Then** commands refer to installed `hideout` and package artifacts rather than `go run` or repo-local helper paths.
2. **Given** a maintainer/developer section, **When** docs mention source checkout commands, **Then** those commands are clearly labeled as development-only.

### Edge Cases

- Package verification must reject missing, non-regular, executable-bit-mismatched, or checksum-mismatched helper binaries, schemas, scripts, and manifest entries.
- Install prefix paths may contain spaces and must be recorded exactly in the manifest after resolution.
- A package is not transparently relocatable after install; moving installed files without reinstalling must make verification fail with a relocation hint.
- Uninstall must ignore unrelated files under the prefix unless the manifest records them as package-owned.
- Uninstall must fail closed when the manifest is missing or cannot prove package ownership.
- Upgrade must not run arbitrary shell from package metadata; only typed installer behavior may mutate package-owned files.
- Package smoke on Linux may run in a narrower mode than macOS, but it must still prove installed CLI/helper/schema discovery for the chosen Linux target.

## Constitutional Alignment _(mandatory for Hideout features)_

- **Authority touched**: Install/runtime lifecycle, helper binaries, schemas, docs, scripts, package manifests, and uninstall cleanup. No new guest isolation, HostFS data-plane, DNS, network, or browser authority is introduced.
- **Fail-closed behavior**: Missing artifacts, checksum mismatches, unsupported target, incompatible migration range, ambiguous prefix, unowned uninstall path, or manifest parse failure MUST deny before mutation or runtime side effects.
- **User authority and policy**: The operator chooses install prefix and whether to purge durable state. Profiles, audit, evidence, adapter registry, decisions, and store state remain user-owned and are preserved by default.
- **Generality and provider scope**: This is the generic Hideout alpha packaging path. Homebrew/signing/notarization/OS package managers remain deferred provider-specific packaging surfaces.
- **Evidence surface**: Package manifest, package verify output, install/upgrade/uninstall audit records where a store is available, Gate 0 package smoke, README/docs, and doctor-style hints.
- **Secret/redaction boundary**: Package manifests and logs MUST NOT record control-plane tokens, proxy secrets, UI tokens, generated machine ids, or transient runtime paths that reveal hidden credentials. Durable user paths may be recorded as operator-visible install/store locations.
- **Backend/gate expectation**: Gate 0 plus package smoke. Real Lima gates are not required because this feature packages existing binaries and schemas rather than changing isolation behavior.

## Requirements _(mandatory)_

### Functional Requirements

- **FR-001**: System MUST build an alpha package artifact containing the main `hideout` CLI, required helper binaries, schemas, scripts, package manifest, and release docs needed for local install.
- **FR-002**: System MUST install the package into an operator-selected prefix and record the actual installed prefix, artifact paths, artifact checksums, build target, version, commit, schema version, and supported migration range.
- **FR-003**: System MUST treat installed packages as non-relocatable after install; verification MUST detect moved or mismatched installed artifact paths and provide a reinstall hint.
- **FR-004**: System MUST provide a package verification command that validates manifest schema, regular-file expectations, executable expectations, helper presence, schema presence, script presence, checksum integrity, and target compatibility.
- **FR-005**: System MUST fail closed with a doctor-style hint when a required helper binary, schema, script, or manifest field is missing or mismatched.
- **FR-006**: System MUST provide upgrade behavior that replaces or verifies package-owned files while preserving profiles, audit, evidence, adapter registry, decisions, and durable store state.
- **FR-007**: System MUST reject upgrade when the installed package state is outside the new package migration range.
- **FR-008**: System MUST make reinstall to the same prefix idempotent when the package and installed state already match.
- **FR-009**: System MUST provide uninstall dry-run that reports exactly which package-owned files and directories would be removed without removing them.
- **FR-010**: System MUST uninstall package-owned files without deleting profiles, audit, evidence, adapter registry, decisions, or durable store state unless `--purge` is explicitly selected.
- **FR-011**: System MUST require explicit purge before durable user state is removed, and purge MUST be visible in uninstall output plus survivor audit evidence when a store path is available.
- **FR-012**: System MUST ignore unrelated files that are not recorded as package-owned in the installed manifest during uninstall.
- **FR-013**: System MUST include package smoke in Gate 0 and prove the installed layout can run without a source checkout.
- **FR-014**: System MUST update README and docs so the main external-alpha path uses packaged commands, while source checkout commands are labeled development-only.
- **FR-015**: System MUST defer Homebrew, public signing, notarization, auto-update daemon, marketplace packaging, Windows, and enterprise package manager claims unless a later release spec promotes them.
- **FR-016**: System MUST provide macOS and chosen Linux target package smoke coverage for installed CLI/helper/schema discovery.

### Key Entities _(include if feature involves data)_

- **Package Artifact**: A tarball or equivalent local package containing installable Hideout files plus package metadata.
- **Package Manifest**: Structured record of version, commit, build target, install prefix, package-owned files, checksums, executable expectations, schema version, and migration range.
- **Installed Package State**: The manifest and files currently installed under a prefix, used for verify, upgrade, and uninstall ownership decisions.
- **Install Prefix**: Operator-selected destination for package-owned binaries, schemas, scripts, and docs.
- **Durable Store State**: Profiles, audit, evidence, adapter registry, decisions, notices, and other user-owned data preserved by default.
- **Package Smoke Evidence**: Gate 0 evidence proving the installed layout works without source checkout paths.

## Success Criteria _(mandatory)_

### Measurable Outcomes

- **SC-001**: A fresh install from a generated package into a temporary prefix succeeds and `hideout package verify` reports 100% of manifest files valid.
- **SC-002**: Package smoke proves installed `hideout`, helper binaries, schemas, and scripts are discoverable without `go run` or repository-local paths.
- **SC-003**: Removing or modifying any required helper binary causes package verification or smoke to fail closed with the artifact name in the diagnostic.
- **SC-004**: Upgrade over an existing install preserves 100% of durable store files created by the test fixture.
- **SC-005**: An incompatible migration range rejects upgrade before any package-owned file is changed.
- **SC-006**: Uninstall dry-run removes 0 files and reports the full package-owned removal set.
- **SC-007**: Uninstall without `--purge` removes package-owned files and preserves 100% of durable profile/audit/evidence state.
- **SC-008**: Durable state removal occurs only when `--purge` is explicitly present and is recorded in uninstall output.
- **SC-009**: README/docs primary install and first-run path contains no `go run` or source-checkout-only helper invocation.
- **SC-010**: Gate 0 includes package smoke and fails if the packaged layout is incomplete.

## Assumptions

- Alpha packaging is tarball plus install script plus package manifest.
- The install prefix is chosen by the operator and recorded after path resolution; installed packages are not advertised as relocatable.
- Existing profile/store data schemas remain compatible unless a later spec introduces migrations.
- Homebrew formula work may remain a smoke/development artifact and is not the alpha release bar.
- macOS is the primary local smoke target; Linux package smoke is required for the chosen Linux target but may use narrower checks when host capabilities differ.
