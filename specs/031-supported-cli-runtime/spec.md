# Feature Specification: Supported CLI Runtime

<!-- markdownlint-disable MD013 MD036 MD060 -->

**Feature Branch**: `031-supported-cli-runtime`

**Created**: 2026-07-11

**Status**: Implemented

**Input**: Reviewed design draft `.tmp/031-supported-cli-runtime-draft.md`. Deliver one explicit, dependable developer runtime for macOS arm64 so ordinary users can run baseline developer tools and install a real agent CLI inside the Lima boundary without understanding guest image URLs, provisioning, or proxy credentials.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Start With A Dependable Developer Runtime (Priority: P1)

A new operator initializes a privacy profile by selecting the named `developer-standard` runtime. Hideout resolves the correct immutable runtime for the host, prepares it, verifies the actual guest, and lets the operator run the documented baseline tools without first provisioning the guest or finding an image URL.

**Why this priority**: A strong VM boundary is not useful to an ordinary developer when basic tools are unpredictable. This is the smallest independently useful product slice: one named runtime, one supported host architecture, one visible readiness result, and no hidden guest setup.

**Independent Test**: On a clean macOS arm64 host and empty Hideout store, initialize a privacy profile with `developer-standard`, execute every declared baseline command as the target identity, and verify the operator never supplies an image URL or guest package-install command.

**Acceptance Scenarios**:

1. **Given** a clean supported macOS arm64 host, **When** the operator explicitly selects `developer-standard`, **Then** Hideout resolves one architecture-matched immutable runtime revision, verifies it, records its provenance, and reports `preview-ready` before target execution.
2. **Given** a ready runtime, **When** the operator runs the documented shell, source-control, inspection, archive, language-runtime, and build commands, **Then** every declared command executes as the non-root target without passwordless sudo.
3. **Given** an unsupported architecture, missing artifact, digest mismatch, or failed boundary prerequisite, **When** preparation runs, **Then** it fails closed with one actionable recovery result and creates no environment record that claims readiness.
4. **Given** an existing environment or an explicit custom image, **When** the packaged runtime catalog changes, **Then** the existing environment remains pinned and the custom image remains visibly `custom/unverified`.

---

### User Story 2 - Install A Real Agent Through The Privacy Network (Priority: P2)

After the runtime is ready, the operator follows one documented command to install a pinned real agent CLI into the target user's durable writable prefix. The package comes from its public registry through the selected privacy network path, and the installed command immediately reports its expected version.

**Why this priority**: Package installation is the first practical failure point for agent users. A runtime that boots but cannot install the actual CLI through tun2socks and mediated DNS recreates the market's most common sandbox usability problem.

**Independent Test**: With empty package-manager caches, install the selected exact agent package from its public registry in a real privacy-mode Lima guest, execute its version command, and prove the registry DNS and HTTPS traffic used the mediated path without exposing the proxy credential.

**Acceptance Scenarios**:

1. **Given** an empty package cache and a ready runtime, **When** the operator runs the canonical install command, **Then** the exact agent version is fetched from its real public registry and installed only into a target-writable prefix without `sudo`.
2. **Given** privacy networking is active, **When** the install resolves and downloads the package, **Then** DNS and HTTPS use the existing mediated route and no proxy credential appears in target environment, target output, logs intended for sharing, or public evidence.
3. **Given** a successful install, **When** the operator invokes the documented version check, **Then** the selected agent reports the pinned version from the documented stable target path.
4. **Given** network denial, DNS failure, registry failure, or an unwritable target prefix, **When** installation fails, **Then** the operator receives a distinct observed cause and executable next action rather than an ambiguous missing-command message.

---

### User Story 3 - Understand Runtime And Command Readiness (Priority: P2)

An operator can inspect whether the selected environment currently satisfies its runtime contract. When an exact command is absent, Hideout reports that observed fact before target execution and points to the documented operator-owned setup path without silently installing anything.

**Why this priority**: Reusable guests can drift. Catalog membership is historical configuration, not proof that a mutable environment still contains a command. Honest readiness and recovery keep the runtime useful after first boot.

**Independent Test**: Remove one declared command from a reusable test environment, request that command, and verify CLI, doctor, and machine-readable Manager output agree on the failed observation and recovery code without executing a target or setup command.

**Acceptance Scenarios**:

1. **Given** a catalog-selected environment, **When** readiness is inspected, **Then** the product distinguishes boundary prerequisites, baseline developer tools, and profile-specific expected commands.
2. **Given** a required command was removed from a mutable guest, **When** verification runs, **Then** readiness becomes failed and never remains green from catalog metadata alone.
3. **Given** the exact requested command is missing, **When** a run is requested, **Then** the authoritative pre-execution check stops the run and returns the shared typed recovery record.
4. **Given** a non-boundary baseline command is missing but a different requested command exists, **When** the operator accepts the visible degraded status and runs that existing command, **Then** the unrelated command may run without restoring a preview-ready claim.
5. **Given** an unknown optional command, **When** it is missing, **Then** Hideout does not guess a package name, execute a package manager, or claim that the selected runtime supplies it.

---

### User Story 4 - Preserve Existing Security And Evidence Boundaries (Priority: P3)

A security-sensitive operator can verify that selecting the runtime changes only guest-domain data. It does not add host authority, weaken privilege separation, inject credentials, or replace real backend evidence with a native or local fixture.

**Why this priority**: The runtime improves usability, but it must not trade away the boundary that makes Hideout valuable. This story independently proves that the convenience layer remains subordinate to existing isolation contracts.

**Independent Test**: Run the existing real Lima isolation and hidden-proxy gates against the exact selected runtime digest, then compare environment provenance, privilege status, HostFS behavior, network evidence, and secret scans.

**Acceptance Scenarios**:

1. **Given** the selected runtime, **When** real Gate 2 and Gate 3 execute in independently created disposable environments, **Then** each proof carries its own valid environment identity while both bind to the same runtime revision, image digest, architecture, and clean image-build source state; the enclosing evidence independently binds the later verified Hideout package candidate.
2. **Given** the runtime image and catalog, **When** secrets and authority are inspected, **Then** they contain no host credential, target-specific login, broker token, proxy secret, HostFS grant, or ambient host capability.
3. **Given** degraded or unknown target privilege separation, **When** readiness is evaluated, **Then** the runtime cannot produce a hardened claim.
4. **Given** only native, fixture, or locally fabricated image evidence, **When** product readiness is evaluated, **Then** the real-runtime proof remains unsatisfied.

### Edge Cases

- The selected artifact redirects, disappears, or changes bytes at the same URL: preparation fails digest or availability validation and does not fall back to a template alias.
- Available disk space is below the declared download plus expansion budget: preparation stops before download with a concrete space requirement.
- The runtime artifact is valid but the actual guest lacks one boundary prerequisite: no target command runs until the prerequisite is repaired or a different runtime is selected.
- The guest is ready but one developer-baseline command was removed: the environment becomes visibly degraded; the missing exact command cannot run, while unrelated present commands remain available after the degraded state is shown.
- A catalog revision is added after an environment already exists: no silent rebuild, image switch, or mutation occurs.
- A user supplies both a named runtime and a custom image: the request is rejected as ambiguous.
- The real agent package is yanked or changes its distribution shape: the pinned evidence fails and the runtime itself is not falsely reported broken.
- Package installation succeeds only from a warm cache: the real product proof remains unsatisfied until a clean-cache registry install passes.
- The host loses network during image preparation: output remains visibly in preparation/failed state and no false-ready record is persisted.
- The artifact lacks an SBOM: preview provenance reports that fact; it does not silently inherit a supported or release-ready claim.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Guest runtime selection, environment identity, backend preparation, target command readiness, package/docs/evidence, and existing network/privilege gates. The feature adds guest-domain image data only; it adds no HostFS, endpoint, host-open, command-proxy, script, or guest-root authority.
- **Fail-closed behavior**: Missing architecture, artifact, digest, boundary prerequisite, exact target command, runtime provenance, or required real evidence stops readiness or target execution. There is no fallback to an unpinned template, host-installed tool, package guess, native isolation claim, or silent environment replacement.
- **User authority and policy**: V1 runtime selection is explicit. Existing profiles and environments remain unchanged. Custom images remain available but visibly unmanaged. Package installation is an operator-authored target command under the existing network, privilege, audit, and filesystem policy; Hideout does not become a package-install authority.
- **Generality and provider scope**: Core models a generic named runtime, immutable revision, architecture artifact, and declarative tool contract. The selected VM image, package manager, and real agent are named product fixtures recorded in catalog/evidence data, not generic Core semantics.
- **Evidence surface**: CLI and doctor readiness, Manager read model, environment provenance, typed recovery, local audit, Boundary Summary, package manifest, product-evidence registry, and real Gate 2/Gate 3 manifests.
- **Secret/redaction boundary**: Runtime artifacts and evidence must exclude host credentials, preauthenticated agent state, proxy credentials, broker/daemon/claim tokens, generated machine identity, and hidden Core paths. User-authored package names and registry URLs remain local user data and follow the existing export/share boundary.
- **Backend/gate expectation**: Gate 0 covers catalog/contracts/package integrity, selection, status, recovery, and secret fixtures. A real macOS arm64 Lima image is mandatory for readiness. Real Gate 2 proves boot/tools/non-root/HostFS preservation; real Gate 3 proves the clean-cache agent install through tun2socks and mediated DNS. Native and fixture lanes prove mechanics only.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Hideout MUST provide exactly one v1 named runtime family, `developer-standard`, with one immutable revision for macOS arm64/aarch64.
- **FR-002**: The runtime catalog MUST be included in package integrity checks and MUST bind the revision to one version-addressed bootable artifact, cryptographic digest, architecture, supply mode, source identity, review time, declared download size, package-inventory status, and SBOM status.
- **FR-003**: V1 runtime selection MUST be explicit and mutually exclusive with custom-image selection. It MUST NOT silently change built-in defaults, existing profiles, or existing environments.
- **FR-004**: Runtime selection MUST resolve to the immutable concrete artifact before environment creation. Missing, ambiguous, unavailable, unsupported, or digest-invalid selection MUST fail closed without falling back to a template alias or another architecture.
- **FR-005**: A created environment MUST retain the concrete artifact identity and catalog provenance used to create it. Later catalog changes MUST NOT alter that record or rebuild the environment.
- **FR-006**: The runtime contract MUST be declarative and MUST separate boundary prerequisites, developer-baseline commands, and profile-specific expected commands. It MUST NOT contain executable provisioning, remote scripts, package-install requests, or product-specific authority logic.
- **FR-007**: The initial developer baseline MUST declare and verify ordinary shell/core utilities, Git, HTTPS/download tools, JSON and archive tools, Node.js with npm, Python with pip, Go, and native build tools. Exact versions and commands are catalog data and must be shown by inspection.
- **FR-008**: Readiness MUST be based on observations from the actual guest. Catalog membership or historical success alone MUST NOT produce a passing current-runtime result. A missing boundary prerequisite MUST block every target run; a missing non-boundary baseline command MUST fail preview readiness but MUST NOT block a different present command after the degraded state is surfaced.
- **FR-009**: Exact target command execution MUST use one authoritative pre-execution existence check. A missing command MUST stop before target execution and return the shared typed recovery record; the feature MUST NOT add a competing check path.
- **FR-010**: CLI, doctor, and the Manager read model MUST derive runtime family, revision, provenance, readiness, failed check, and recovery information from the same authoritative model.
- **FR-011**: Runtime status MUST distinguish at least `preview-ready`, `preview-failed`, `custom/unverified`, `unknown`, and `not-running`. Preview status MUST NOT render as supported or release-ready.
- **FR-012**: The selected runtime MUST preserve the existing non-root target identity and separate privileged setup identity. Passwordless target sudo, degraded separation, or unknown separation MUST fail the applicable hardened readiness claim.
- **FR-013**: Product evidence MUST install one pinned real agent CLI package from its public registry as the non-root target into a documented target-writable prefix with empty package-manager caches, then execute its pinned version command.
- **FR-014**: The privacy evidence lane MUST carry the real agent registry DNS and HTTPS traffic through the existing tun2socks and mediated-DNS path and MUST prove that no proxy credential reaches target state or public evidence.
- **FR-015**: Agent installation failures MUST distinguish image/runtime absence, network denial, DNS mediation failure, registry/package failure, and target-prefix write failure through stable recovery records with executable next actions.
- **FR-016**: The canonical first-run path MUST require no image URL, guest provisioning knowledge, `sudo`, host package cache, host global prefix, or host credential. It MUST show the exact tested agent install and version commands and state that login/authentication is outside 031.
- **FR-017**: Runtime artifacts, catalogs, contracts, environment records, logs, and evidence MUST contain no Hideout-minted credential or preauthenticated agent state and MUST pass existing deterministic redaction and export boundaries.
- **FR-018**: Real macOS arm64 Gate 2 and Gate 3 evidence MUST use the same version-addressed, digest-protected runtime artifact. Native, local fixture, converted OCI-only, and first-boot provisioning evidence MUST NOT satisfy the real runtime claim.
- **FR-019**: Before implementation claims preview readiness, a clean supported host MUST download and boot the exact artifact, verify its digest and declared contract, and record source retention, redistribution/license, measured size, expansion, first-boot time, and privilege facts.
- **FR-020**: Runtime selection MUST add zero HostFS, network, endpoint, host-application, command-proxy, script, or guest-root authority beyond the profile's existing effective policy.

### Key Entities

- **Runtime Family**: Stable operator-facing name for a maintained developer environment contract; v1 has only `developer-standard`.
- **Runtime Revision**: Immutable version of a family, including tool-contract identity and support maturity.
- **Runtime Artifact**: Architecture-specific bootable guest image identified by versioned source, digest, supply mode, size, inventory, and SBOM status.
- **Runtime Contract**: Declarative boundary prerequisites and baseline tool observations expected from the actual guest.
- **Runtime Selection**: Explicit profile/environment request that resolves a family to one revision and artifact before creation.
- **Runtime Provenance**: Environment-bound record of family, revision, artifact digest, architecture, catalog release, and source facts.
- **Runtime Verification**: Current observations and typed failures for a concrete running environment.
- **Runtime Evidence**: Gate and product-hardening records binding artifact, environment, network path, tool checks, image-build source state, and the independently verified package candidate.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On a clean supported macOS arm64 host, an operator reaches a passing baseline command using at most two documented product commands and supplies no image URL or guest provisioning command.
- **SC-002**: 100% of catalog selection tests resolve to exactly one architecture-matched artifact and digest, while every unsupported, missing, ambiguous, or digest-invalid case creates zero false-ready environment records.
- **SC-003**: 100% of commands declared in the selected baseline execute in the real guest as the non-root target, and passwordless sudo remains unavailable.
- **SC-004**: A cached ready environment reaches target execution within 120 seconds on the supported reference host; the selected artifact remains within a reviewed maximum of 4 GiB download and 16 GiB expanded disk use.
- **SC-005**: Removing a required runtime command causes the next verification to fail, and removing the exact requested command causes the next run to stop before target execution in 100% of tested cases.
- **SC-006**: With empty caches, the selected pinned real agent package installs from its public registry through the real privacy path and its version command succeeds.
- **SC-007**: Network, DNS, registry/package, target-prefix, artifact, and command absence fixtures each produce their distinct stable recovery code and one executable next action.
- **SC-008**: Real Gate 2 and Gate 3 evidence each report a valid non-empty,
  distinct environment identity while reporting the same runtime revision,
  artifact digest, architecture, and clean image-build source state. The
  enclosing product evidence independently reports one clean verified package
  candidate; the package commit is not required to equal the earlier image
  build commit. Reusing one
  mutable managed environment across the two gates MUST fail readiness.
- **SC-009**: Secret scans find zero host credentials, proxy credentials, broker/daemon/claim tokens, generated machine identity, or preauthenticated agent state in the runtime artifact metadata, target environment, shared logs, and public evidence.
- **SC-010**: 100% of custom-image and legacy-environment cases remain usable but render as `custom/unverified` or unmanaged rather than inheriting preview readiness.
- **SC-011**: Runtime selection changes zero effective HostFS, network, endpoint, host-app, command-proxy, script, or guest-root grants in policy comparison tests.
- **SC-012**: The clean-host artifact spike records real download bytes, expanded disk use, first-boot time, baseline results, source retention, redistribution/license status, and privilege posture before the catalog revision is accepted.
- **SC-013**: Gate 0 rejects any catalog/contract/package drift, secret fixture, unregistered recovery code, or documentation example that diverges from the tested explicit runtime path.

## Assumptions

- V1 is a production-quality preview for macOS arm64 with the Lima backend; Linux and other architectures require their own later real-backend proof.
- Runtime selection remains explicit in 031. Making it a built-in default is a separate product decision after preview evidence and maintenance ownership exist.
- One lean multi-language runtime is preferable to multiple variants. The baseline includes Node.js, Python, Go, and native build tools only if the real artifact remains within the stated size and boot budgets.
- The implementation plan selects one real publicly downloadable agent CLI and exact version whose install and version check require no login. The fixture is evidence data, not Core product semantics.
- Interactive agent authentication, browser callback leases, durable login proof, runtime update/recreate, byte-level download progress, cancellation, TUI/WebUI runtime controls, automated refresh/SBOM release policy, Linux support, optional-tool auto-setup, and combined `code .` dogfood are out of 031 scope.
- Existing image declaration, Lima download/cache behavior, environment reuse, command readiness, privilege separation, network mediation, evidence, redaction, and package integrity mechanisms are reused unless planning proves a contract gap.
- A compatible retained VM artifact may be upstream-pinned or minimally Hideout-built. The plan must make that decision from a real bootable artifact and honest license/provenance evidence; an OCI image alone is not sufficient.
- The current 017–030 working line is an uncommitted development baseline. It may support implementation work, but release-candidate evidence requires an immutable clean candidate anchor.
