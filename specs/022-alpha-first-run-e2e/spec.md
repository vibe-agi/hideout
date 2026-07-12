# Feature Specification: Alpha First-Run E2E

**Feature Branch**: `022-alpha-first-run-e2e`
**Created**: 2026-07-09
**Status**: Draft
**Input**: User description: "Prove the canonical alpha first-run path works
from package install to first successful run, using the package artifact and
docs path a real operator would follow. Avoid install/init collisions by
installing with `--skip-init` before the documented profile init step. Keep
local-fast proof honest and distinguish it from real Lima/privacy proof."

## Clarifications

### Session 2026-07-09

- Q: How should the first-run path avoid package installer and documented init
  collisions? A: All 022 lanes install with `./install.sh --skip-init`, then
  perform exactly one explicit init step.
- Q: How does local-fast relate to the documented privacy init? A: Local-fast
  uses a native/dev first-run profile because the privacy template requires
  `tun2socks`; it proves package mechanics and first-command behavior only.
  The documented privacy init is proved by the real-backend lane or recorded as
  `not-run` when prerequisites are missing.
- Q: Does local-fast native proof count as real isolation or privacy proof? A:
  No. Local-fast proof is a development harness proof only and MUST be labeled
  weak/native/dev-only. Real Lima/privacy proof requires an explicit mode and
  real prerequisites.
- Q: Does 022 add new install authority, dependency installation, templates, or
  UI flows? A: No. It proves and tightens the existing package, verify, init,
  doctor, support, run, audit, Boundary, and evidence surfaces.

## User Scenarios & Testing

### User Story 1 - Install Package And Complete First Run (Priority: P1)

An operator installs Hideout from a package-like artifact into a clean prefix
and store, verifies the installed package, initializes one first-run profile,
runs a first command, and receives audit/Boundary evidence showing what was
used.

**Why this priority**: External alpha cannot start from a source-tree command.
The first thing to prove is that a packaged binary can be installed and used
without hidden source dependencies or duplicate init steps.

**Independent Test**: Run the first-run E2E script in local-fast mode against a
fresh temp prefix/store. The script uses the package installer with
`--skip-init`, then runs one explicit local-fast init command, verifies the
package, runs a low-risk command through the installed `hideout`, and writes a
passing product hardening evidence manifest.

**Acceptance Scenarios**:

1. **Given** a package artifact and an empty install prefix/store, **When** the
   first-run E2E script runs in local-fast mode, **Then** it installs with
   `--skip-init`, initializes one `default` local-fast profile, verifies the
   package, runs a low-risk command, and writes passing evidence.
2. **Given** the installer would otherwise create `default`, **When** the
   first-run path uses `--skip-init`, **Then** the later explicit init command
   does not collide with a pre-existing profile.
3. **Given** the run completes, **When** the evidence manifest is inspected,
   **Then** it records package identity, install prefix class, store class,
   profile, backend/proof mode, run summary, audit presence, Boundary presence,
   and redaction status.

---

### User Story 2 - Fail With Actionable Diagnostics (Priority: P2)

An operator who lacks a prerequisite, has stale package state, or points at an
unsafe prefix/store receives a clear failure or `not-run` result with recovery
hints, never a misleading success.

**Why this priority**: First-run proof is worse than useless if it passes on a
half-installed or stale package. Failures must be actionable enough for an
operator to fix the environment without reverse-engineering the scripts.

**Independent Test**: Run fixture modes for missing helper, stale package
manifest/checksum, unsafe store/prefix, duplicate profile, and missing real
backend prerequisite. Each fixture exits with the expected non-success status,
writes a diagnostic evidence entry, and does not mark the first-run claim
passed.

**Acceptance Scenarios**:

1. **Given** a package verification mismatch, **When** the first-run script
   executes, **Then** it fails closed before reporting first-run success and
   writes the stale package finding.
2. **Given** the default profile already exists before the documented init step,
   **When** the script runs, **Then** it reports the duplicate profile condition
   instead of silently overwriting or claiming a clean first run.
3. **Given** real-backend proof mode is requested but Lima/privacy prerequisites
   are absent, **When** the script runs, **Then** the real-backend proof is
   `not-run` with a prerequisite finding, not passed via native fallback.

---

### User Story 3 - Distinguish Real Backend Proof (Priority: P3)

An operator or release reviewer can run a local-fast proof quickly, but can also
request a real Lima/privacy first-run proof that only passes when the real
backend path actually executes.

**Why this priority**: Fast local checks are useful during development, but the
project must not repeat the release-readiness mistake of treating local
existence checks as real isolation evidence.

**Independent Test**: Run local-fast mode and real-backend mode separately.
Local-fast evidence is marked weak/native/dev-only. Real-backend mode either
executes a Lima/privacy path and passes with real proof metadata or records
`not-run`/failed prerequisites.

**Acceptance Scenarios**:

1. **Given** local-fast mode, **When** the proof completes, **Then** evidence
   labels it as weak/native/dev-only and does not claim Lima, DNS mediation,
   HostFS isolation, or hardened privilege separation.
2. **Given** real-backend mode with prerequisites present, **When** the proof
   completes, **Then** evidence records the actual real backend, profile,
   network/privacy posture, and audit/Boundary proof references.
3. **Given** real-backend mode without prerequisites, **When** the proof cannot
   run, **Then** evidence records `not-run` or failure with missing
   prerequisites and no pass claim.

### Edge Cases

- Package installer defaults would initialize `default` unless `--skip-init` is
  used.
- Existing `default` profile, stale profile directory, or partially initialized
  store.
- Install prefix contains obsolete files from a previous package version.
- Package manifest exists but helper checksum, schema checksum, or package
  version mismatches.
- Required runtime helper is missing, including externally provided helper
  prerequisites such as `tun2socks` when the selected proof mode needs them.
- Workspace path is unsafe or points inside a reserved/control-plane directory.
- Native backend succeeds locally but cannot prove isolation or privacy.
- Real Lima/privacy prerequisites are absent, slow, or unsupported on the host.
- Audit/Boundary evidence is missing, malformed, or contains control-plane
  material.

## Requirements

### Constitutional Alignment

- **Authority touched**: package install/verify, first-run docs, init/profile
  lifecycle, support/doctor checks, local Manager run, audit/Boundary evidence,
  and product-hardening evidence. No new JS, HostFS, network, backend,
  browser-control, marketplace, signing, or remote authority is introduced.
- **Fail-closed behavior**: Package verification mismatch, duplicate init,
  unsafe store/prefix, missing helpers, missing real-backend prerequisites,
  stale package state, audit/Boundary absence, and redaction failures prevent
  pass claims.
- **Redaction and evidence**: Evidence is a local diagnostic artifact and MUST
  pass the existing product-hardening redaction path. It may record summaries
  and paths needed for debugging, but not control-plane keys, daemon tokens,
  capability secrets, proxy credentials, machine IDs, or raw env secrets.
- **Native/backend honesty**: Native and local-fast proof modes remain weak/dev
  harnesses. Real Lima/privacy claims require explicit real-backend mode and
  actual real proof.

### Functional Requirements

- **FR-001**: System MUST provide an alpha first-run E2E path that starts from a
  package artifact or local package staging directory, not from `go run` or
  source-tree-only execution.
- **FR-002**: The canonical first-run path MUST install with `--skip-init` or an
  equivalent single-init mechanism before running the documented profile init
  step.
- **FR-003**: The E2E path MUST execute the installed `hideout` binary from the
  install prefix for verify, init, run, audit, and evidence steps.
- **FR-004**: The E2E path MUST run package verification, support matrix or
  doctor checks, and prerequisite checks before reporting success.
- **FR-005**: The E2E path MUST initialize the selected first-run profile exactly
  once and fail closed on duplicate or partially initialized profile state.
- **FR-006**: The E2E path MUST run at least one low-risk first command and
  capture its run summary, audit presence, and Boundary presence.
- **FR-007**: Local-fast/native mode MUST be explicitly labeled
  weak/native/dev-only and MUST NOT claim real Lima isolation, DNS mediation,
  HostFS isolation, or hardened privilege separation.
- **FR-008**: Real-backend mode MUST be explicit and MUST pass only when the
  real backend path actually executes; otherwise it MUST report `not-run` or
  failure with prerequisites.
- **FR-009**: The E2E path MUST write a stable product-hardening evidence
  manifest with proof ids, statuses, package identity, install prefix class,
  store class, backend/proof mode, profile, workspace, command summary,
  prerequisite findings, audit references, Boundary references, and redaction
  result.
- **FR-010**: Missing package artifacts, helper prerequisites, schema files,
  manifest entries, or checksum matches MUST fail or mark the corresponding
  proof `not-run`; they MUST NOT produce a passing first-run proof.
- **FR-011**: Stale package state or obsolete installed files MUST be reported
  with actionable repair guidance. Automatic deletion is out of scope unless an
  existing explicit repair command is invoked.
- **FR-012**: Evidence and script output MUST avoid raw control-plane material
  and MUST be validated by schema plus redaction checks.
- **FR-013**: First-run documentation MUST match the scripted install/init
  order, including the `--skip-init` default path, and MUST NOT instruct a
  duplicate `default` init after a default-instantiating installer run.
- **FR-014**: The E2E path MUST distinguish local-fast, real-backend, skipped,
  failed, and passed outcomes in machine-readable evidence.
- **FR-015**: 022 MUST NOT add new templates, dependency installers, package
  signing, public release publishing, marketplace behavior, or UI flows.

### Key Entities

- **First-Run Evidence**: Product-hardening manifest entries proving package
  install, verification, init, first command, audit, Boundary, prerequisite, and
  redaction outcomes.
- **Package Under Test**: The package artifact or staging directory used for
  installation, including manifest identity, version, checksum summary, and
  helper inventory.
- **Install Context**: Temporary install prefix, PATH, store root, backend mode,
  proof mode, and environment variables used by the proof.
- **First-Run Profile**: The selected profile created exactly once by the proof
  lane's explicit init step.
- **Proof Mode**: `local-fast`, `real-backend`, `not-run`, or `failed`, with
  explicit backend and privacy posture metadata.
- **Prerequisite Finding**: Structured diagnostic for missing helper, stale
  package, duplicate init, unsafe path, unsupported backend, or absent
  real-backend capability.

## Success Criteria

### Measurable Outcomes

- **SC-001**: A local-fast first-run E2E run installs from a package path,
  verifies the package, initializes one weak/dev profile, runs one
  installed-binary command, captures audit/Boundary presence, and writes a
  passing evidence manifest.
- **SC-002**: Duplicate init and installer/default-profile collision scenarios
  produce 0 misleading success results and 0 silent overwrites.
- **SC-003**: Evidence artifacts distinguish local-fast/native from real-backend
  proof in 100% of first-run runs.
- **SC-004**: Missing prerequisite, stale package, unsafe prefix/store, and
  duplicate profile fixtures produce failed or `not-run` evidence, never passed
  evidence.
- **SC-005**: First-run evidence includes audit and Boundary references or
  explicitly fails the relevant proof when either is absent.
- **SC-006**: Redaction checks find 0 raw control-plane token, capability
  secret, proxy credential, machine-id, daemon token, or env secret matches in
  first-run evidence and logs.
- **SC-007**: First-run docs and script agree on install/init ordering, and the
  documented path contains no duplicate-init instruction after a
  default-instantiating install.
- **SC-008**: Real-backend proof is skipped only with an explicit `not-run`
  prerequisite finding and passes only when the actual real backend path
  executes.

## Assumptions

- The default 022 local-fast proof path uses `./install.sh --skip-init`, then a
  native/dev init command that is explicitly labeled weak/dev-only.
- The real-backend proof path uses `./install.sh --skip-init`, then
  `hideout init --template privacy --profile default --backend lima ...` when
  prerequisites are present.
- Local-fast mode is allowed for fast development proof but is labeled
  weak/native/dev-only.
- Real Lima/privacy proof is explicit, prerequisite-gated, and may be `not-run`
  on hosts without required support.
- `tun2socks` remains an external runtime prerequisite unless packaging work
  promotes it to a packaged helper in a later feature.
- 022 reuses existing package verification, doctor/support, init, run, audit,
  Boundary, and product-hardening evidence surfaces.

## Out Of Scope

- New package signing or public release publishing.
- Installing system dependencies automatically.
- New UI onboarding or wizard flows.
- New base images, templates, backend capabilities, HostFS authority, network
  authority, or JS policy authority.
- Treating native/local-fast success as real isolation evidence.
