# Feature Specification: Packaging Sweep

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `017-packaging-sweep`

**Created**: 2026-07-09

**Status**: Implemented — the exact claim surface and non-claims live in [docs/STATUS.md](../../docs/STATUS.md)

**Input**: User description: "Implement .tmp/017-020-internal-hardening-plan.md. Start with 017 Packaging Sweep: make install, upgrade, verify, and uninstall boringly reliable without adding new authority or new security claims."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Upgrade Without Silent Package Leftovers (Priority: P1)

An external alpha operator upgrades Hideout and can immediately see whether the previous installed package left behind files that the new package no longer owns. The operator gets a precise report and repair command instead of a silent orphan or an automatic destructive cleanup.

**Why this priority**: Upgrade is the first recurring lifecycle action external alpha users will perform. Silent package-owned leftovers make later verify, doctor, support, and release evidence ambiguous.

**Independent Test**: Can be tested by installing package A with a package-owned file that is absent from package B, upgrading to package B, and verifying the leftover is reported with ownership evidence and an explicit repair path while unrelated files are not touched.

**Acceptance Scenarios**:

1. **Given** package A installed a package-owned file and package B no longer owns that file, **When** the operator upgrades from A to B, **Then** Hideout reports the obsolete package-owned file, identifies the old manifest ownership, and provides a repair command without deleting it by default.
2. **Given** an obsolete package-owned file remains after upgrade, **When** the operator runs package verification, **Then** verification fails with the stale file path, ownership source, and repair hint.
3. **Given** an unrelated operator-created file under the install prefix, **When** the operator upgrades or verifies the package, **Then** Hideout does not classify that unrelated file as package-owned and does not ask to remove it.
4. **Given** the operator explicitly runs the package repair action for obsolete package-owned files, **When** ownership can be proven under the same install prefix, **Then** Hideout removes only the proven obsolete files and records the repair outcome.

---

### User Story 2 - Verify Helpers And External Prerequisites Honestly (Priority: P2)

An operator or maintainer can verify an installed package and understand exactly which packaged helper is missing or corrupted, and which runtime tool is an external prerequisite rather than a packaged artifact.

**Why this priority**: 013 made Hideout installable, but helper completeness is only useful if diagnostics distinguish packaged artifacts from operator-provided prerequisites. `tun2socks` must not be treated as package-checksummed unless the product actually vendors it.

**Independent Test**: Can be tested by altering packaged helper files, removing schemas/scripts, and hiding `tun2socks` from discovery. Verification must fail or warn with the correct ownership class and must not claim checksum coverage for external prerequisites.

**Acceptance Scenarios**:

1. **Given** a packaged helper binary is missing, non-regular, has the wrong executable expectation, or has the wrong checksum, **When** the operator runs package verification, **Then** verification fails closed and names the exact artifact, expected state, actual state, and repair command.
2. **Given** a required schema or script is missing or mismatched, **When** verification runs, **Then** the same artifact-specific failure format is used.
3. **Given** `tun2socks` is required for a privacy profile but remains an external prerequisite, **When** package verification or doctor reports its status, **Then** the report says it is external and missing or undiscoverable, not a package checksum mismatch.
4. **Given** all packaged artifacts match the manifest and external prerequisites are discoverable when needed, **When** verification runs, **Then** the operator receives a concise pass result with helper coverage and prerequisite status separated.

---

### User Story 3 - Enforce Migration Range Before Mutation (Priority: P3)

A maintainer or operator installing a new package over an old install can trust that unsupported install-state versions are rejected before package files are changed.

**Why this priority**: Migration range fields exist to protect external alpha users from unreviewed upgrades. If those fields are inert, upgrade can mutate files before compatibility is known.

**Independent Test**: Can be tested by creating installed-state fixtures inside, outside, and at the edges of the new package migration range, then proving incompatible upgrades fail before any package-owned file changes.

**Acceptance Scenarios**:

1. **Given** an installed package state whose version is within the new package migration range, **When** the operator upgrades, **Then** upgrade may proceed and records the compatibility decision.
2. **Given** an installed package state below or above the supported migration range, **When** the operator upgrades, **Then** upgrade fails closed before mutating package-owned files and prints the current state, supported range, and recreate or reinstall guidance.
3. **Given** the installed-state version is unknown or malformed, **When** upgrade begins, **Then** Hideout refuses mutation and reports that compatibility cannot be proven.

---

### User Story 4 - Preserve Clear Uninstall And Repair Evidence (Priority: P4)

An operator can understand exactly what uninstall, purge, and repair did after the fact, including which package-owned files were removed and whether durable store state was preserved or purged.

**Why this priority**: Cleanup is part of the runtime lifecycle. External alpha support depends on evidence that distinguishes package cleanup from durable user-state deletion.

**Independent Test**: Can be tested by running uninstall preserve, uninstall purge, and obsolete-file repair with fixture package state, then inspecting command output and survivor evidence.

**Acceptance Scenarios**:

1. **Given** an installed package with durable store state, **When** uninstall preserve completes, **Then** output records the package-owned removal list and confirms durable state was preserved.
2. **Given** the operator explicitly selects purge, **When** uninstall purge completes, **Then** survivor audit evidence outside the deleted store records the purge decision and durable-state action.
3. **Given** obsolete package-owned repair removes files, **When** the repair completes, **Then** the repair output and package lifecycle evidence list each removed package-owned file and the manifest proof used.

### Edge Cases

- The old installed manifest is missing, unreadable, malformed, or outside the new package's supported migration range.
- A stale file path escapes the install prefix through symlinks, `..`, path case differences, or platform-specific path normalization.
- An obsolete path is now a directory, symlink, socket, device, or non-regular file.
- The operator-created file has the same basename as an old package-owned file but was not recorded in the old manifest.
- Repair removes one obsolete file and then encounters a permission error on another file.
- Package verification runs on a host where `tun2socks` is intentionally absent.
- Linux package smoke has narrower host capabilities than macOS but must still prove installed artifact discovery.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Package install/upgrade/verify/uninstall lifecycle, package manifests, helper artifact verification, external prerequisite reporting, lifecycle evidence, and package smoke. No new guest, HostFS, network, browser, endpoint, adapter, or decision authority is introduced.
- **Fail-closed behavior**: Missing or malformed manifests, unprovable ownership, unsupported migration ranges, path escape, helper mismatch, schema/script mismatch, and ambiguous repair targets MUST refuse mutation or verification success before side effects.
- **User authority and policy**: The operator chooses install prefix, upgrade, verify, repair, uninstall preserve, and uninstall purge. Durable profile/audit/evidence/adapter/decision state remains user-owned and is preserved unless an explicit purge action is selected.
- **Generality and provider scope**: This is the generic Hideout alpha package lifecycle. It does not promote Homebrew, public signing, notarization, enterprise package managers, or `tun2socks` vendoring into product scope.
- **Evidence surface**: Package verify output, upgrade output, repair output, uninstall output, package lifecycle audit when a store is available, survivor purge audit, package smoke, Gate 0, and doctor-style hints.
- **Secret/redaction boundary**: Package reports and lifecycle evidence MUST NOT record control-plane tokens, proxy secrets, UI tokens, claim tokens, generated machine ids, `HIDEOUT_SECRET_*` backing material, or hidden credential paths. Operator-visible install prefixes and store paths may be reported when needed for repair.
- **Backend/gate expectation**: Gate 0 plus package smoke. Real Lima gates are not required because 017 tightens package lifecycle behavior rather than changing isolation, DNS, HostFS, or backend runtime boundaries.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST detect package-owned files from the old installed manifest that are absent from the new package manifest during upgrade.
- **FR-002**: System MUST report obsolete package-owned files by default and MUST NOT delete them during normal upgrade.
- **FR-003**: System MUST provide an explicit repair action for obsolete package-owned files, and repair MUST remove only files whose package ownership is proven by the installed-state manifests and whose resolved paths remain under the same install prefix.
- **FR-004**: Package verification MUST fail when proven obsolete package-owned files remain, and the failure MUST include the stale path, old ownership source, and repair hint.
- **FR-005**: System MUST ignore unrelated files that are not recorded as package-owned in the old installed manifest.
- **FR-006**: System MUST reject upgrade before mutating package-owned files when installed-state migration range compatibility cannot be proven.
- **FR-007**: Migration compatibility output MUST include the installed state version, supported range, and operator guidance for incompatible or unknown state.
- **FR-008**: Package verification MUST validate required packaged helpers, schemas, scripts, manifest entries, file type, executable expectation, checksum, and target compatibility.
- **FR-009**: Package verification failures MUST name the exact artifact, expected state, actual state, and repair or reinstall command.
- **FR-010**: System MUST classify `tun2socks` as an external prerequisite in v1 unless a later spec explicitly vendors it, and MUST NOT claim package checksum verification for it.
- **FR-011**: Package and doctor reporting MUST distinguish packaged helper failures from missing or undiscoverable external prerequisites.
- **FR-012**: Uninstall preserve, uninstall purge, and obsolete-file repair MUST record package-owned removal lists and durable-state action in output and available lifecycle evidence.
- **FR-013**: Purge MUST continue to write survivor evidence outside the deleted store when a store path is available.
- **FR-014**: Package smoke in Gate 0 MUST cover install, compatible upgrade, incompatible migration, obsolete package-owned file reporting, explicit repair, uninstall preserve, uninstall purge, and helper/prerequisite diagnostics.
- **FR-015**: System MUST preserve durable profiles, audit, evidence, adapter registry, decisions, notices, and store state during upgrade, verification, repair, and uninstall preserve.
- **FR-016**: System MUST defer Homebrew, signing, notarization, auto-update, Windows packaging, public release infrastructure, and vendoring external prerequisites unless a later spec promotes them.

### Key Entities *(include if feature involves data)*

- **Installed Package Manifest**: Versioned record of package-owned files, install prefix, artifact checksums, executable expectations, target, schema version, migration range, and lifecycle metadata for the current installed package.
- **New Package Manifest**: Versioned record supplied by the package being installed or upgraded to; used to compare ownership and compatibility with installed state.
- **Obsolete Package-Owned File**: A path recorded as package-owned by the old installed manifest that is not recorded as package-owned by the new package manifest.
- **Repair Plan**: Explicit operator-reviewed plan that lists obsolete package-owned files eligible for removal, rejected paths, ownership proof, and expected durable-state impact.
- **External Prerequisite**: Runtime dependency required by some profiles or hosts but not owned by the package manifest in v1; `tun2socks` is the named prerequisite for this feature.
- **Lifecycle Evidence**: Command output, audit records, survivor purge audit, and smoke artifacts that prove upgrade, verify, repair, or uninstall behavior.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Upgrade from package A to package B reports 100% of package-owned files present only in A and removes 0 obsolete files without explicit repair.
- **SC-002**: Package verification fails 100% of fixtures with proven obsolete package-owned files and includes the stale path plus repair hint.
- **SC-003**: Explicit repair removes only proven obsolete package-owned paths and removes 0 unrelated files in fixture runs.
- **SC-004**: Incompatible migration fixtures reject upgrade before any package-owned file changes.
- **SC-005**: Missing, corrupted, non-regular, or mode-mismatched packaged helpers, schemas, or scripts produce artifact-specific verification failures.
- **SC-006**: `tun2socks` absence is reported as an external prerequisite issue in 100% of relevant package/doctor diagnostics and never as a package checksum failure.
- **SC-007**: Upgrade, verify, repair, uninstall preserve, and uninstall purge preserve 100% of durable store fixtures unless purge is explicitly selected.
- **SC-008**: Survivor purge audit remains available after 100% of purge fixture runs where a store path was available.
- **SC-009**: Gate 0 package smoke covers all lifecycle outcomes named in FR-014 and fails if obsolete-file reporting, repair, migration rejection, or helper/prerequisite diagnostics regress.
- **SC-010**: Lifecycle output and evidence contain 0 raw control-plane secret matches in automated redaction scans.

## Assumptions

- 017 extends the alpha package lifecycle created in 013 rather than replacing it.
- Obsolete package-owned files default to report-first because automatic deletion is more destructive than the current alpha reliability problem.
- Explicit repair may remove proven obsolete files only after ownership and prefix containment are rechecked at repair time.
- Migration v1 remains schema/range allowlist plus fail-closed; arbitrary package migration scripts and data transformation hooks remain deferred.
- `tun2socks` remains operator-provided or host-provided in v1; package verification can report its absence but cannot checksum it as a package-owned artifact.
- macOS remains the primary local package smoke target, with Linux smoke required for installed artifact discovery on the chosen Linux target.
