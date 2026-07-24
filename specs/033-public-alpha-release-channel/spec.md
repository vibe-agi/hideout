# Feature Specification: Public Alpha Release Channel

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `033-public-alpha-release-channel`
**Created**: 2026-07-13
**Status**: Implemented for v0.1.0-alpha.1 — the exact claim surface and non-claims live in [docs/STATUS.md](../../docs/STATUS.md)
**Input**: Publish one independently verifiable, supervised public alpha for
macOS arm64 without expanding Hideout's authority or overstating product
maturity.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Install One Official Alpha Package (Priority: P1)

A macOS arm64 operator can identify one official Hideout alpha package,
verify it, install it without a source checkout or Go toolchain, and inspect
the installed version and prerequisites before creating any profile.

**Why this priority**: A public repository is not a usable product channel.
The first public package must have one unambiguous identity and a short,
repeatable installation path.

**Independent Test**: On a clean supported host, download the versioned
package and checksums anonymously, verify the digest, install with profile
creation disabled, then run version, package verification, and doctor from the
installed prefix.

**Acceptance Scenarios**:

1. **Given** a clean supported macOS arm64 host with no source checkout or Go,
   **When** the operator follows the official package instructions, **Then**
   the exact published package installs and reports its product version, full
   source commit, target, and package identity.
2. **Given** installation has completed, **When** the operator has not asked to
   initialize a profile, **Then** no profile, daemon, Lima instance, or hidden
   prerequisite installation is created.
3. **Given** an unsupported host or a missing prerequisite, **When** the
   operator attempts installation or verification, **Then** Hideout fails
   closed with a stable reason and an executable recovery action.

---

### User Story 2 - Prove The Download Is The Tested Candidate (Priority: P1)

A security-conscious operator or CI consumer can prove that the anonymously
downloaded bytes are the same immutable candidate that passed package checks,
real isolation gates, signing observation, notarization, and release
readiness.

**Why this priority**: The public channel is credible only if release identity
binds the actual archive bytes rather than merely a tag or source commit.

**Independent Test**: Download the package, checksums, release manifest, and
bounded evidence bundle from the prerelease, then validate their identities,
digests, evidence freshness, signing observations, and required proof set
without consulting local build output.

**Acceptance Scenarios**:

1. **Given** an intact public release, **When** its assets are validated,
   **Then** tag, version, full commit, target, package digest, runtime identity,
   and evidence identity agree across every public artifact.
2. **Given** a package built from the same commit but with one changed byte,
   **When** it is evaluated against the release evidence, **Then** it is stale
   and cannot satisfy release readiness.
3. **Given** malformed, dirty, native-only, missing, or `not-run` evidence,
   **When** publication readiness is evaluated, **Then** publication is denied
   even if a human approval is present.
4. **Given** the public assets have been uploaded, **When** they are downloaded
   anonymously, **Then** the downloaded asset set and digests exactly match the
   tested pre-upload candidate.

---

### User Story 3 - Reach A First Successful Lima Run (Priority: P2)

After installation, an operator can explicitly create a dedicated Lima-backed
development profile and run one command without first configuring a privacy
proxy. The product clearly distinguishes this low-prerequisite compatibility
path from the stronger privacy-network path.

**Why this priority**: Installation is not useful until the package can reach a
real isolated run, but first success must not silently weaken a requested
privacy posture.

**Independent Test**: With the installed package and a dedicated workspace,
create a Lima/direct profile using the retained runtime, run a simple command
as the synthetic non-root target, and separately confirm that the same package
still passes the existing privacy Gate 3 lane.

**Acceptance Scenarios**:

1. **Given** the official package, Lima, and the retained runtime, **When** the
   operator selects the documented direct first-run path, **Then** one command
   succeeds in the managed guest without `sudo`, a custom image URL, or source
   checkout state.
2. **Given** a direct first run succeeds, **When** Hideout reports its posture,
   **Then** it describes compatibility and VM isolation without claiming
   privacy networking.
3. **Given** the operator selected a privacy profile, **When** privacy
   prerequisites are absent, **Then** Hideout fails closed rather than silently
   switching to direct networking.

---

### User Story 4 - Recover Or Remove Without Losing State (Priority: P2)

An operator can reinstall the same package, verify and repair package-owned
files, and uninstall the package while retaining durable operator state unless
purge is explicitly requested.

**Why this priority**: A supervised alpha must be recoverable without asking
operators to delete the store or risk profiles, evidence, and audit history.

**Independent Test**: Install the same package twice, introduce a bounded
package-owned drift, verify and repair it, then uninstall normally and prove
that durable store content remains intact.

**Acceptance Scenarios**:

1. **Given** an intact installation, **When** the same version is reinstalled,
   **Then** package-owned files converge without changing durable operator
   state.
2. **Given** a missing or obsolete package-owned file, **When** the operator
   verifies and repairs the installation, **Then** only proven package-owned
   paths are changed and the result is auditable.
3. **Given** a normal uninstall, **When** removal completes, **Then** profiles,
   environments, audit, decisions, evidence, adapter packs, and host-app
   recipes remain; purge requires a separate explicit action.
4. **Given** an unsupported older install-state transition, **When** upgrade is
   attempted, **Then** it fails closed with export/recreate guidance rather
   than mutating unknown state.

---

### User Story 5 - Know What The Alpha Does Not Promise (Priority: P3)

An operator sees the same release maturity, supported platform, and major
security non-claims in the release page, README, status, support matrix,
doctor, and machine-readable release manifest.

**Why this priority**: Public availability must not turn existing narrow
claims into claims of GA maturity, complete containment, or cross-platform
package support.

**Independent Test**: Compare the machine-readable release inventory with all
human support surfaces and verify that removing or contradicting any required
non-claim causes validation to fail.

**Acceptance Scenarios**:

1. **Given** the public alpha, **When** an operator inspects any supported
   product surface, **Then** it identifies macOS arm64 and Lima as the packaged
   first-class path and labels native as a development harness.
2. **Given** the release documentation, **When** it describes isolation and
   privacy, **Then** it preserves the existing non-claims for workspace DLP,
   guest-root containment, privacy prerequisites, runtime freshness, community
   authority, and UI maturity.
3. **Given** candidate documentation before publication, **When** truth checks
   run, **Then** any claim that the package is already publicly available
   fails; after publication, availability requires anonymous download proof.

---

### User Story 6 - Report A Problem Without Publishing Secrets (Priority: P3)

An alpha operator can route normal product feedback to a structured public
issue and security-sensitive findings to a private vulnerability channel. The
official support path asks for bounded facts and redacted exports rather than
raw logs or credentials.

**Why this priority**: A public alpha exists to learn, but a security product
must not make public disclosure or unsafe evidence sharing the default support
path.

**Independent Test**: Starting only from the public repository and release
notes, open the normal issue workflow, independently verify that private
vulnerability reporting is available, and generate the documented bounded
doctor/export evidence without exposing an injected secret.

**Acceptance Scenarios**:

1. **Given** an ordinary product defect, **When** the operator opens the issue
   workflow, **Then** it requests version, package digest, platform/backend,
   recovery code, bounded doctor summary, and sanitized reproduction details.
2. **Given** a security-sensitive report, **When** the operator follows public
   security guidance, **Then** a private report can be initiated without first
   publishing an issue.
3. **Given** support evidence contains user data, **When** it is prepared for
   sharing, **Then** it passes the existing export decision boundary and the
   operator is warned that control-plane redaction does not remove all user
   content.

### Edge Cases

- Signing credentials are unavailable, expired, or associated with an
  unexpected identity.
- Notarization is rejected, delayed, or accepted for bytes different from the
  final archive.
- Upload succeeds for only a subset of the allowlisted release assets.
- The draft asset passes locally but anonymous post-publication download is
  unavailable or has a different digest.
- A release tag and source commit agree while archive bytes differ.
- Required evidence is empty, malformed, dirty, stale, native-only, `not-run`,
  symlinked, missing, outside the evidence root, or digest-invalid.
- The host is Intel macOS, Linux, Windows, or an unsupported macOS release.
- Go, the source tree, a prior store, and developer-specific `PATH` entries are
  unavailable during clean-install validation.
- Lima or the separate runtime is missing; the package must explain the
  prerequisite without installing it silently.
- A direct first run succeeds while the privacy proxy is absent; no privacy
  claim may be inferred.
- Reinstall, repair, uninstall, or downgrade encounters operator-owned files or
  an unsupported state schema.
- The approximately 1 GB runtime download is confused with the smaller host
  package.
- Public documentation is generated before anonymous assets exist, or public
  assets exist before checked-in release facts are updated.
- Private vulnerability reporting is disabled even though `SECURITY.md`
  mentions it.
- A public issue or evidence fixture contains a token, proxy URL, private path,
  or raw workspace content.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Fail closed**: Failed identity, evidence, signing, notarization, platform,
  compatibility, or public-download checks prevent publication; human approval
  cannot override them.
- **Typed Core authority**: Release identity, proof requirements, support
  claims, compatibility, and recovery records have one authoritative product
  model. Workflow scripts orchestrate that model but cannot invent passing
  evidence or new authority.
- **Workspace and HostFS boundaries**: 033 adds no filesystem, network,
  endpoint, guest-root, command, host-app, or script capability. Existing
  workspace and HostFS semantics remain unchanged.
- **Evidence is a product requirement**: The exact package bytes must satisfy
  local checks, registered product proofs, real Gate 2, real Gate 3, signing,
  notarization, and anonymous post-publication verification.
- **Lifecycle ownership**: Installation, repair, uninstall, compatibility, and
  cleanup preserve the existing package/store ownership split and explicit
  purge boundary.
- **Professional individual operator**: The first channel is a supervised
  alpha with explicit prerequisites, bounded recovery, and no enterprise fleet
  or automatic-update claim.
- **Scripts carry zero authority**: Build and publication automation may select
  only registered proof and release contracts; protected credentials and
  observed platform results remain outside package-controlled input.
- **Honest backends and public ecosystem**: Lima is the first-class packaged
  isolation path; native remains a development harness. Public source,
  community material, signatures, and repository visibility do not themselves
  create runtime authority or broader security claims.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Hideout MUST represent one immutable public release identity with
  product version, tag, full source commit, clean-state observation, target,
  package digest, runtime catalog release, and evidence digest.
- **FR-002**: Public release construction MUST start from a clean checkout at
  the exact release tag.
- **FR-003**: Binary identity, package identity, release manifest, asset name,
  release tag, and install state MUST agree on product version; machine-readable
  binary and package identity MUST also expose the full source commit.
- **FR-004**: The package MUST be built once, retained, tested, signed,
  notarized, and published without rebuilding after real gates.
- **FR-005**: Release readiness and product evidence MUST bind the exact package
  archive digest, not only the source commit.
- **FR-006**: A same-commit package with different bytes MUST be stale and MUST
  NOT satisfy release readiness.
- **FR-007**: V1 MUST publish only the macOS arm64 package claim.
- **FR-008**: The package MUST install without Go and without source-tree state.
- **FR-009**: Package installation MUST remain separate from profile creation,
  daemon startup, prerequisite installation, and first run.
- **FR-010**: The public macOS package MUST expose independently verified
  Developer ID signing and accepted notarization status. Without those
  conditions, the same bytes MAY be labeled only as an unsigned developer
  preview, not as the ordinary public alpha install channel.
- **FR-011**: Signing and notarization authority MUST come only from protected
  release credentials and independently observed platform results.
- **FR-012**: Release credentials and control-plane secrets MUST NOT appear in
  the package, manifests, evidence, logs, receipts, or documentation.
- **FR-013**: V1 MUST publish a checksums artifact covering the exact public
  asset set.
- **FR-014**: V1 MUST publish a strict machine-readable public release
  manifest.
- **FR-015**: V1 MUST publish a bounded evidence bundle whose internal artifact
  references are relative, contained, non-symlinked, present, and digest
  matched.
- **FR-016**: Public evidence MUST use the existing export and redaction
  boundary for every user-data-bearing artifact.
- **FR-017**: Empty, malformed, dirty, stale, native-only, or `not-run` evidence
  MUST fail closed.
- **FR-018**: Release publication MUST require local checks, registered product
  evidence, real Gate 2, real Gate 3, package identity, runtime identity,
  signing/notarization, and public artifact checks to pass.
- **FR-019**: Manual release approval MUST NOT override a failed machine gate.
- **FR-020**: The release process MUST verify authenticated candidate bytes
  against the tested candidate before publication and MUST verify anonymous
  public downloads against the same identity after publication.
- **FR-021**: Publication MUST expose the complete four-asset set atomically as
  one immutable prerelease. A failed post-public anonymous check MUST NOT emit
  a `public-verified` receipt, update public inventory, or endorse that
  immutable prerelease as the current alpha.
- **FR-022**: Clean-install validation MUST run without Go, source-root state,
  an existing profile/store, or developer `PATH` fallback.
- **FR-023**: First-success validation MUST run one Lima/direct command with the
  exact retained runtime and MUST NOT claim privacy networking.
- **FR-024**: The exact package candidate MUST retain the existing real privacy
  Gate 3 proof without silently falling back to direct networking.
- **FR-025**: Reinstall MUST be idempotent, and normal uninstall MUST preserve
  durable operator state unless purge is explicit.
- **FR-026**: Published alpha compatibility MUST fail closed on unsupported
  install-state or durable-state transitions.
- **FR-027**: README, STATUS, support matrix, release notes, release manifest,
  and CLI support output MUST derive release facts from one authoritative
  inventory.
- **FR-028**: Public documentation MUST distinguish public-source status,
  public-package status, runtime status, and product maturity.
- **FR-029**: Public install documentation MUST NOT use a source-building
  rolling-head package recipe as the verified package path.
- **FR-030**: V1 MUST expose exact recovery commands for package verification,
  repair, uninstall, prerequisite failure, and unsupported platform.
- **FR-031**: Release validation MUST leave no candidate-created Lima instance,
  browser process, temporary directory, or secret-bearing session state after
  completion.
- **FR-032**: Publication receipts MUST be shareable and MUST NOT contain
  release credentials, local absolute paths, or private workflow state.
- **FR-033**: 033 publication MUST be blocked until the selected Apache-2.0
  license is present and consistent in the repository, package, release
  manifest, and release page.
- **FR-034**: Third-party software redistributed in the package or retained
  runtime MUST have an explicit inventory and notice review; the project
  license MUST NOT be presented as covering those components.
- **FR-035**: Publication MUST require public security guidance and an enabled
  private vulnerability reporting path whose availability is verified
  independently of documentation text.
- **FR-036**: README, release notes, doctor/support guidance, and issue
  templates MUST direct normal reports to bounded facts and existing redacted
  export surfaces, and security reports to the private channel.
- **FR-037**: Every 033 proof ID MUST be registered in the authoritative proof
  registry and mapped to its claim boundary before any release gate emits it.
- **FR-038**: Human-readable and machine-readable support output MUST render the
  same authoritative entries and required non-claims.
- **FR-039**: Documentation truth MUST distinguish candidate-local and
  post-publication phases. Candidate documentation MUST NOT claim public
  availability, and public documentation MUST require anonymous download
  proof.

### Key Entities

- **Public Release Identity**: The immutable product version, tag, full source
  commit, target, package archive digest, runtime catalog release, and evidence
  digest that identify one published release.
- **Package Identity**: Product version, full source commit, target, package
  manifest identity, and outer archive digest. Product version, source commit,
  and archive digest are distinct values and cannot substitute for one another.
- **Public Release Manifest**: Strict machine-readable inventory of release
  identity, exact assets, digests, signing/notarization observations, required
  evidence, license, support scope, and maturity.
- **Public Asset Set**: Exactly the allowlisted package, checksums, release
  manifest, and bounded evidence bundle for one version. License, notices,
  security guidance, and package README files are checksum-covered content
  inside the package rather than separate release assets.
- **Evidence Bundle**: Bounded, digest-checked proof artifacts for the exact
  candidate, including registered product proofs and retained real-gate
  results, without raw user data or release credentials.
- **Publication Receipt**: Shareable post-publication observation that records
  the final release identity, anonymously downloaded asset digests, and
  publication outcome without local paths or credentials.
- **Release Inventory**: The single authoritative source for version,
  platform, maturity, support entries, major non-claims, asset names, and
  public-documentation facts.
- **Compatibility Decision**: A typed accept or fail-closed result for install
  and durable-state transitions, including an executable recovery path.
- **Signing Observation**: Independently observed signing identity and
  notarization outcome for the final candidate bytes; it is not authority
  supplied by package input.
- **Support Report**: Bounded version, digest, platform, recovery, doctor, and
  redacted-export references suitable for public feedback without raw secrets.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: One versioned macOS arm64 package downloads anonymously and
  matches its published SHA-256 in 100% of release verification runs.
- **SC-002**: The downloaded package passes package verification and records
  the same version, full commit, target, and package identity as the public
  release manifest.
- **SC-003**: Every host Mach-O in the public-alpha package passes the declared
  Developer ID identity check, and the final release payload has accepted
  notarization evidence.
- **SC-004**: On a fresh supported host with Go and the source tree unavailable,
  an operator completes version inspection, package verification, doctor,
  explicit initialization, and one Lima run using documented commands.
- **SC-005**: A same-commit archive with one changed byte fails package identity,
  evidence freshness, and release readiness in 100% of mutation tests.
- **SC-006**: Real Gate 2 and Gate 3 pass for the same package digest and retained
  runtime build while using distinct managed environment identities.
- **SC-007**: All release-required 029-032 proofs are satisfied by registered,
  digest-backed evidence; zero required proof is satisfied by `not-run` or a
  local-only substitute.
- **SC-008**: Downloading and validating candidate and public assets yields the
  exact digest set tested before upload.
- **SC-009**: Reinstall changes zero durable profile, audit, or evidence content,
  and normal uninstall preserves the store.
- **SC-010**: Secret and path scans find zero known control-plane credential and
  zero local absolute developer path in public evidence and publication
  receipts.
- **SC-011**: README, STATUS, support matrix, CLI support output, release
  manifest, and release page agree on version, platform, maturity, and major
  non-claims.
- **SC-012**: Missing signing credentials, rejected notarization, failed
  readiness, or pre-publication asset-set drift prevents publication in 100%
  of fault fixtures. A failed anonymous download produces zero
  `public-verified` receipts and zero public-inventory changes in 100% of
  post-public fault fixtures.
- **SC-013**: Post-run cleanup reports zero candidate-created Lima instances,
  browser processes, temporary directories, and secret-bearing session state.
- **SC-014**: The release is labeled prerelease/alpha and makes zero GA, stable
  update, Linux package, guest-root containment, workspace DLP, or marketplace
  trust claim.
- **SC-015**: Repository, package, release manifest, and release page expose one
  matching Apache-2.0 license, while third-party notices remain separately
  attributable.
- **SC-016**: A private vulnerability report can be initiated from the public
  repository without opening a public issue, and no validation fixture
  publishes its injected secret.
- **SC-017**: The normal issue workflow accepts version, digest, recovery code,
  doctor summary, and redacted-export references without requesting raw audit
  or secret material.
- **SC-018**: Candidate documentation fails validation if it claims the package
  is already public; post-publication documentation fails unless anonymous
  asset download and digest proof pass.
- **SC-019**: Removing any registered 033 proof, required claim mapping, or
  human support-matrix non-claim causes a failing test.

## Assumptions

- The initial product version is `v0.1.0-alpha.1` and is published as a GitHub
  prerelease, not as stable or GA.
- Apache-2.0 is the product-owner-approved project license. Runtime and bundled
  third-party obligations are reviewed and attributed separately.
- The first package target is macOS arm64. Linux retains its narrower source
  coverage but has no 033 package claim; native remains a development harness.
- Developer ID signing and accepted online notarization are required for the
  ordinary public-alpha install path. A credential-blocked build may be
  published only as an explicitly unsigned developer preview with no
  Gatekeeper trust claim.
- The v1 archive remains a versioned `tar.gz`; no offline stapling claim is
  made. The final downloaded archive is checked through a real platform trust
  probe during planning and validation.
- The separately retained, digest-pinned runtime remains outside the host
  package and is described as an approximately 1 GB first-use download.
- Lima and `tun2socks` remain explicit external prerequisites; 033 does not
  install them silently.
- Direct networking is the low-prerequisite first-success lane. The privacy
  path remains separately documented and must retain its real Gate 3 proof.
- One authenticated manual approval occurs only after every machine gate has
  passed and cannot override any failure.
- The first published compatibility floor covers the immediately previous
  published alpha package state once one exists; broader unknown durable-state
  transitions fail closed with export/recreate guidance.
- Public evidence is a bounded asset on the same release rather than generated
  source-tree content. Candidate-neutral documentation precedes publication;
  post-publication facts require anonymous verification.
- GitHub private vulnerability reporting is the required private security
  channel for v1, with no bounty or response-time SLA claim.
- Homebrew distribution, automatic updates, Linux packages, Windows support,
  marketplace signing, runtime bundling, and a final WebUI/TUI are outside 033.
