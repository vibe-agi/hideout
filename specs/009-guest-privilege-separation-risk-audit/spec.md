# Feature Specification: Guest Privilege Separation And Risk Audit

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `[009-guest-privilege-separation-risk-audit]`

**Created**: 2026-07-08

**Status**: Draft

**Input**: User description: "Implement 009 from `.tmp/008-010-plan.md`: separate target execution from Hideout privileged guest setup where possible, make target execution non-root by default, report enforced/degraded/unknown guest privilege separation status, and audit passwordless sudo risk without overclaiming guest-root containment."

## Clarifications

### Session 2026-07-08

- Q: How are environments created before 009 treated? → A: They are not upgraded in place; they report `degraded` or `unknown` until recreated with 009-capable identity setup.
- Q: Can a run report `enforced` when no privileged setup is requested? → A: Yes, if target non-root/no-sudo checks pass and no privileged setup is required for the run.
- Q: What happens when requested privileged setup cannot use a safe setup path? → A: The requested feature fails closed; degraded status cannot silently run broken tun2socks, DNS, HostFS, or cleanup setup.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Know The Guest Privilege State (Priority: P1)

As an operator running an untrusted tool, I want Hideout to tell me whether the target guest user is non-root, whether it can use passwordless sudo, and whether Hideout can honestly claim privilege separation for this run.

**Why this priority**: This is the MVP because the current root-sensitive command adapter is only intent capture until the system can prove the target user is not sharing the same privileged setup identity.

**Independent Test**: Run a Lima-backed command and inspect audit, explain, and Boundary Summary evidence showing one of `enforced`, `degraded`, or `unknown`; verify `enforced` is emitted only when target non-root and no passwordless sudo are proven.

**Acceptance Scenarios**:

1. **Given** a Hideout-created Lima environment with a target user that is non-root and cannot run passwordless sudo, **When** the operator runs a command, **Then** the run evidence reports `enforced` privilege separation.
2. **Given** a base image where the target user can run passwordless sudo, **When** the operator runs a command, **Then** Hideout reports `degraded`, warns with a base-image or recreate hint, audits the risk, and does not claim root containment.
3. **Given** Hideout cannot complete the privilege checks, **When** the operator runs a command, **Then** Hideout reports `unknown`, audits the missing proof, and does not claim enforced separation.

---

### User Story 2 - Keep Hideout Setup Separate From Target Intent (Priority: P2)

As an operator using network privacy and HostFS features, I want Hideout setup tasks that require guest privilege to run through a Hideout-owned setup path, not through the same authority the untrusted target command receives.

**Why this priority**: Separation has security value only if Hideout privileged setup is distinct from target execution. Otherwise a passwordless-sudo target can bypass the same boundary Hideout uses for setup.

**Independent Test**: On Lima, prove tun2socks, DNS mediation, HostFS setup, and cleanup can run through a Hideout-owned privileged setup path while the target user remains non-root and unable to use passwordless sudo.

**Acceptance Scenarios**:

1. **Given** an environment that supports a Hideout setup identity, **When** network or HostFS setup needs guest privilege, **Then** setup succeeds through the Hideout setup path and target execution still runs as the non-sudo target user.
2. **Given** setup identity material exists, **When** the target command runs, **Then** no setup private key, setup token, or privileged setup script is present in target environment variables, target-writable directories, or user-facing evidence.
3. **Given** the setup identity is unavailable for a feature that requires it, **When** Hideout cannot safely perform setup, **Then** the requested setup fails closed and the run does not claim enforced separation.

---

### User Story 3 - Audit Root-Sensitive Attempts Honestly (Priority: P3)

As an operator reviewing tool behavior, I want root-sensitive command attempts to be visible as target intent while understanding which bypasses command proxy cannot intercept.

**Why this priority**: 008 already captures command-name intent. 009 must connect that evidence to the actual guest privilege state and avoid turning command-name interception into a false kernel or guest-root boundary.

**Independent Test**: Enable the root-sensitive adapter, run command-name and absolute-path sudo attempts, and verify command-name attempts are denied/audited while absolute-path bypass risk is documented and reflected by the separation status.

**Acceptance Scenarios**:

1. **Given** the root-sensitive adapter is enabled, **When** the target invokes `sudo whoami` by command name, **Then** Hideout denies or records a non-applied proposal and audits it as target root intent.
2. **Given** the target invokes `/usr/bin/sudo -n true`, **When** command proxy does not intercept the absolute path, **Then** Hideout relies on the non-sudo target user check for containment and documents the command-proxy non-claim.
3. **Given** the run is `degraded`, **When** root-sensitive evidence is displayed or exported, **Then** the evidence states that the operator-selected base image still permits passwordless sudo and that root containment is not claimed.

---

### Edge Cases

- The selected backend is native or otherwise cannot prove guest privilege state.
- The target user is non-root but can run `sudo -n true`.
- `sudo -n true` fails but `/usr/bin/sudo -n true` succeeds.
- Hideout setup succeeds only through a shared sudo-capable user.
- The base image changes after an environment was created.
- A reusable environment was created before 009 identity separation existed.
- Setup identity credentials are missing, unreadable, too permissive, or accidentally placed under a target-writable directory.
- Root-sensitive command attempts happen before privilege status is known.
- A target obtains guest root by a mechanism outside command-name routing.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Backend, profile, environment lifecycle, network setup, DNS mediation, HostFS setup, command proxy, audit, explain, doctor, Manager overview, TUI/WebUI status, and cleanup.
- **Fail-closed behavior**: Hideout must fail closed when a required privileged setup step cannot be represented by the Hideout setup path, when setup credentials are unsafe, when an `enforced` claim cannot be proven, or when profile/backend configuration asks for a strict mode that the environment cannot satisfy. Default v1 behavior for operator-selected passwordless-sudo images is `degraded` warning plus audit, not a run-blocking failure.
- **User authority and policy**: The operator owns the base image choice. If that image grants passwordless sudo to the target user, Hideout must surface the risk and provide recreate/base-image guidance, but v1 does not force an image switch by default. Deny outcomes from command proxy still win for command-name root-sensitive attempts.
- **Generality and provider scope**: This is a generic Hideout guest privilege status and setup separation feature. Package managers, distributions, sudo, and Lima are examples or backend mechanisms, not Core product semantics.
- **Evidence surface**: Audit records, Boundary Summary, explain, doctor, Manager overview, TUI/WebUI status, and Gate evidence must show the separation status and reasons. Evidence must distinguish target root intent from Hideout privileged setup.
- **Secret/redaction boundary**: Setup private keys, setup tokens, broker tokens, UI tokens, `HIDEOUT_SECRET_*` backing material, generated machine-id, and privileged setup internals must not appear in target environment, target-writable paths, audit, UI, logs, or exports.
- **Backend/gate expectation**: Gate 0 covers schemas, status logic, redaction, and fail-closed tests. Real Lima proof is required before claiming `enforced` separation or no regression for DNS/HostFS privileged setup. Gate 3 must be rerun when tun2socks or DNS setup paths change.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST run target commands as a non-root guest user for Hideout-created Lima environments unless the backend explicitly reports that it cannot prove this property.
- **FR-002**: System MUST check and record the target user's numeric identity before target launch.
- **FR-003**: System MUST check and record whether the target user can run `sudo -n true` and `/usr/bin/sudo -n true`.
- **FR-004**: System MUST classify guest privilege separation status as exactly one of `enforced`, `degraded`, or `unknown`.
- **FR-005**: System MUST emit `enforced` only when the target user is non-root, passwordless sudo checks fail, and all privileged setup required by the run either uses a distinct Hideout-owned setup path or is not required.
- **FR-006**: System MUST emit `degraded` when the target user is non-root but can run passwordless sudo, or when Hideout setup still relies on the same sudo-capable user as target execution.
- **FR-007**: System MUST emit `unknown` when privilege checks cannot complete, produce ambiguous output, or run on a backend that cannot prove guest privilege state.
- **FR-008**: System MUST warn and audit degraded status with a recreate or base-image hint, while allowing the operator to continue by default in v1.
- **FR-009**: System MUST NOT claim guest-root containment, root escalation prevention, or unique host-mutation mediation when status is `degraded` or `unknown`.
- **FR-010**: System MUST run Hideout privileged setup and cleanup through Go-owned setup/apply paths, not through JavaScript adapters or target-owned command execution.
- **FR-011**: System MUST keep setup identity credentials out of target environment variables, target-writable paths, user-facing output, audit evidence, and exported artifacts.
- **FR-012**: System MUST audit Hideout privileged setup separately from target root attempts.
- **FR-013**: System MUST preserve 008 root-sensitive command-name handling and label those events as target intent, not proof of absolute-path or syscall interception.
- **FR-014**: System MUST document and surface that command proxy does not intercept absolute paths, direct syscalls, setuid binaries, or guest-root behavior outside command-name routing.
- **FR-015**: System MUST keep tun2socks, DNS mediation, HostFS setup, and cleanup working when enforced separation is active.
- **FR-016**: System MUST fail closed if a profile or test path requests an enforced-only run and the environment cannot prove enforced separation.
- **FR-017**: System MUST fail closed when requested tun2socks, DNS mediation, HostFS, or cleanup setup cannot run through an allowed Hideout setup path.
- **FR-018**: System MUST expose separation status and reasons through audit, Boundary Summary, explain, doctor, Manager overview, and TUI/WebUI surfaces.
- **FR-019**: System MUST update docs and threat-model non-claims so A3 guest-root remains out of scope and degraded images are described as operator-owned risk.
- **FR-020**: System MUST provide real Lima evidence for at least one enforced environment and one degraded environment before marking 009 complete.

### Key Entities *(include if feature involves data)*

- **Guest Privilege Status**: The per-run status value `enforced`, `degraded`, or `unknown`, plus reason, checks performed, and operator guidance.
- **Target Identity**: The non-root guest user that runs untrusted target commands, with recorded user ID and sudo capability checks.
- **Hideout Setup Identity**: The Hideout-owned privileged setup path used only for Go-owned setup/apply work.
- **Privilege Check Result**: The observed result of identity and sudo checks, including command, outcome, timestamp, and failure reason.
- **Privileged Setup Event**: Audit evidence for Hideout setup or cleanup actions that required guest privilege.
- **Target Root Attempt Event**: Audit evidence for target command-name attempts captured by command proxy or root-sensitive adapters.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of Lima runs emit exactly one guest privilege status before target command completion.
- **SC-002**: 0 runs emit `enforced` unless target non-root, both sudo checks fail, and setup identity separation is proven.
- **SC-003**: 100% of passwordless-sudo target environments are marked `degraded` with warning, audit reason, and recreate/base-image guidance.
- **SC-004**: 100% of setup credentials in test fixtures are absent from target environment, target-writable paths, audit output, UI output, and export output.
- **SC-005**: Real Lima evidence demonstrates enforced separation with target `id -u != 0`, `sudo -n true` failure, `/usr/bin/sudo -n true` failure, and successful Hideout setup through a separate setup path.
- **SC-006**: Real Lima evidence demonstrates degraded reporting when the target can run passwordless sudo.
- **SC-007**: Gate 3 DNS mediation continues to pass after privileged setup separation changes.
- **SC-008**: 0 docs, UI surfaces, audit summaries, or exports claim that 009 prevents guest-root bypasses when status is `degraded` or `unknown`.

## Assumptions

- v1 targets Lima for enforced separation proof; native backend reports `unknown` or weak status and remains a development harness.
- A Hideout-created Lima environment can use a non-sudo target user plus a Hideout-owned setup identity based on the completed local feasibility spike.
- Operator-selected base images that preserve passwordless sudo are allowed in v1, but always report `degraded`.
- Reusable environments created before 009 are not modified in place; they need recreate to qualify for `enforced`.
- Command proxy remains command-name interception only; 009 relies on non-root target execution for absolute-path sudo containment.
- Package installation or privileged guest mutation apply is not introduced by 009.
