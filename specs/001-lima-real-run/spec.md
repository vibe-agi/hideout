<!-- markdownlint-disable MD013 -->

# Feature Specification: Hideout Lima Real Run

**Feature Branch**: `001-lima-real-run`  
**Created**: 2026-07-05  
**Status**: Implemented (supervised dogfood reference smoke; see docs/STATUS.md)  
**Input**: User description: "收窄 dogfood spec：先定义一个真实 Lima dogfood run 的交付切片"

## Current Status Context

This feature is based on the current Hideout contracts and status documents.
HostFS, host.open, workspace safety, Boundary Summary, signal cleanup, generic
tool supply primitives, and Lima backend wiring are treated as existing product
contracts unless implementation planning finds a concrete gap. This feature
does not re-spec those architecture contracts; it defines the first dogfood
delivery slice that proves a real target CLI can run under the product backend
against a safe workspace, complete useful work, use the configured network
policy, and leave inspectable evidence.

## Reference Workload

The independent test for this feature uses a concrete reference workload:

1. the operator selects a sanitized workspace containing a small task file;
2. the configured target CLI updates a workspace file in response to that task;
3. the run verifies the change with an operator-declared success check;
4. the target reaches an operator-declared external endpoint through the
   selected network policy, either direct or privacy mode; and
5. the host-visible workspace diff, success check, network result, audit path,
   and Boundary Summary together prove that the run completed useful work.

The reference workload is intentionally generic. It does not name a specific
third-party agent product. A real agent CLI, a test CLI, or a fake service may
be used as long as the same observable workload is completed.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Run One Target CLI In Lima (Priority: P1)

A Hideout operator with a local development checkout, Lima installed, and an
operator-authored dogfood profile can run one configured generic target CLI
inside Hideout against a sanitized workspace. The run must complete the
reference workload, reach the declared network endpoint through the selected
network policy, and produce the minimum boundary evidence needed for supervised
dogfood.

**Why this priority**: This is the first independently verifiable dogfood
slice. It answers the immediate question: "Can we put a real agent-like CLI in
the box, let it modify a safe project through the intended network path, and
see the boundary evidence afterward?" Release evidence bundles, guided
first-run onboarding, and richer TUI/WebUI observation are separate follow-on
features.

**Independent Test**: With an already installed Hideout checkout and an
operator-authored dogfood profile, run a generic target CLI in Lima against a
sanitized workspace. The test passes only if the target changes the expected
workspace file, the operator-declared success check passes, the target reaches
the declared network endpoint through the selected network policy, unsafe
workspace roots remain blocked, native backend is not counted as isolation
evidence, and the run produces audit plus Boundary Summary evidence.

**Acceptance Scenarios**:

1. **Given** an operator-authored dogfood profile, Lima availability, a
   sanitized workspace, a reference task, and an operator-declared success
   check, **When** the operator runs the configured target CLI, **Then** Hideout
   runs it through the product backend, the target modifies the expected
   workspace file, the success check passes, and the run records a session and
   environment identifier.
2. **Given** the dogfood profile declares a direct or privacy network mode and
   a test endpoint, **When** the reference workload runs, **Then** the target
   reaches that endpoint through the selected policy and the run evidence states
   which network mode was used.
3. **Given** the workspace is `$HOME`, the Hideout store, a credential root, a
   browser profile root, or an ancestor that would mount those roots, **When**
   the operator attempts the run, **Then** Hideout rejects the run before backend
   preparation unless an explicit high-risk override is used.
4. **Given** Lima, the required helper, the configured target CLI, or required
   network preparation is unavailable, **When** the operator attempts the run,
   **Then** Hideout fails closed with a clear diagnostic and does not silently
   fall back to native backend, host execution, or ambient host networking.
5. **Given** the operator selects the native backend, **When** the run
   completes, **Then** the result is labeled wiring evidence only and cannot be
   used to satisfy this feature's dogfood isolation outcome.
6. **Given** the target triggers host boundary decisions during the run,
   **When** the run completes, **Then** the operator can locate run completion
   output and audit evidence for host file, host open, endpoint exposure,
   network, and lifecycle decisions without raw secrets.

### Edge Cases

- The target CLI is missing from the dogfood profile or cannot be prepared.
- Lima, the required helper, or guest bootstrap is unavailable.
- The selected workspace contains project-local secrets; Hideout must treat
  them as visible to the target by design.
- Tool preparation or the reference workload needs network access and the
  selected network policy cannot reach the declared endpoint.
- The target partially edits the workspace and then exits with failure.
- The operator has reusable Lima environments from older runs.
- Ctrl-C or SIGTERM occurs while the target command is running.
- The target attempts host.open to localhost, private networks, host gateway
  aliases, or DNS-rebinding domains.
- HostFS grants use case variants, dotfiles, glob patterns, literal glob
  characters, or reserved store paths.
- A non-operator-authored profile, bundle, recipe, generated plan, or project
  manifest proposes host file authority.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Workspace mount, HostFS, host.open, endpoint exposure
  evidence, network mode, environment/profile setup, backend selection, target
  command lifecycle, and run cleanup.
- **Fail-closed behavior**: Unsafe workspace roots, unavailable Lima backend,
  missing helper, missing target CLI, failed network preparation, denied HostFS,
  local/private host.open targets, unsupported endpoint exposure, untrusted
  non-operator-authored authority proposals, and cleanup failures must deny,
  stop, or be reported as blocking evidence.
- **User authority and policy**: Operator-authored profile/run configuration is
  user-authoritative within the limits of constitution Principle III.
  Non-operator-authored HostFS grant proposals do not inherit that status and
  remain out of scope unless explicitly reviewed.
- **Evidence surface**: The in-scope evidence surface is the run completion
  output plus the referenced audit record and Boundary Summary. Existing
  explain, doctor, TUI, WebUI, and release gates may help operators but are not
  the primary deliverable for this slice.
- **Secret/redaction boundary**: Proxy secret values, broker tokens, hidden
  endpoint internals, browser automation secrets, callback/open URL secrets, and
  raw host file contents must not appear in the target environment, routine run
  summary, or dogfood evidence.
- **Backend/gate expectation**: Lima evidence is required for this feature.
  Native backend smoke may support wiring diagnosis only and is not isolation
  evidence. Full release-candidate gate bundles are follow-on work.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST allow an operator to start one configured target
  CLI run in Lima against a sanitized workspace.
- **FR-002**: The target CLI MUST complete the reference workload by modifying
  the expected workspace file and satisfying the operator-declared success
  check.
- **FR-003**: The target CLI MUST reach an operator-declared external endpoint
  through the selected network policy, either direct or privacy mode.
- **FR-004**: The system MUST support the target CLI through generic
  operator-authored configuration, without naming a third-party product in Core
  requirements.
- **FR-005**: The system MUST reject unsafe workspace roots before backend
  preparation begins.
- **FR-006**: The system MUST distinguish Lima isolation evidence from native
  wiring evidence in run output, status, or documentation associated with this
  feature.
- **FR-007**: The system MUST fail closed when Lima, the required helper, the
  configured target CLI, declared tool supply, or network preparation is
  unavailable.
- **FR-008**: The system MUST NOT silently fall back to native backend, host
  execution, ambient host files, or ambient host networking when the product
  backend path cannot satisfy the run.
- **FR-009**: The system MUST preserve target stdout and stderr as command
  output while making control-plane evidence available separately.
- **FR-010**: The system MUST record the session identifier, environment
  identifier, audit location, Boundary Summary, network mode, and reference
  workload result for the run.
- **FR-011**: The system MUST not expose Hideout store or host credential roots
  through workspace selection or HostFS grants during this dogfood run.
- **FR-012**: The system MUST keep proxy secrets, broker tokens, hidden endpoint
  details, and browser automation secrets out of target environment and routine
  evidence.
- **FR-013**: The system MUST provide enough post-run guidance for the operator
  to resume, stop, or clean the environment created or reused by this run.
- **FR-014**: The system MUST label this feature as supervised dogfood only and
  MUST NOT claim unattended daily use or GA readiness.

### Key Entities

- **Dogfood Run**: One supervised invocation of a configured target CLI in Lima,
  including session ID, environment ID, backend, workspace, command outcome,
  network mode, reference workload result, and evidence references.
- **Reference Workload**: The concrete task used by this feature: update a
  workspace file, pass an operator-declared success check, and reach a declared
  network endpoint through the selected network policy.
- **Dogfood Workspace**: A sanitized project directory intentionally shared with
  the target. It is fully visible to the target and must pass workspace safety
  checks.
- **Dogfood Profile**: Operator-authored configuration that selects backend,
  target CLI, tool supply, network mode, HostFS grants, environment policy, and
  isolated identity state for this run.
- **Target CLI**: The generic command-line tool executed by the operator inside
  Hideout. It is configured by the operator, not hardcoded into Hideout Core.
- **Run Evidence**: Run completion output, audit path, Boundary Summary, session
  metadata, environment metadata, network mode, denied boundary events, and
  cleanup/resume guidance generated from authoritative runtime facts.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator who already has Hideout, Lima, and a dogfood profile
  prepared can complete one target CLI reference workload in 10 minutes or less.
- **SC-002**: 100% of completed runs for this feature report whether the backend
  evidence is Lima isolation evidence or native wiring evidence.
- **SC-003**: 100% of reference workload runs either reach the declared network
  endpoint through the selected network policy or fail closed before claiming
  dogfood success.
- **SC-004**: 100% of tested unsafe workspace roots, including case-variant
  macOS paths, are rejected before backend preparation.
- **SC-005**: 100% of completed Lima dogfood runs report session ID,
  environment ID, audit path, Boundary Summary, network mode, and reference
  workload result.
- **SC-006**: For a known test set of boundary-triggering actions, 100% of the
  resulting allowed, denied, unsupported, error, or audit-only decisions appear
  in run evidence derived from authoritative runtime facts.
- **SC-007**: 100% of sampled routine run evidence omits proxy secrets, broker
  tokens, hidden endpoint internals, browser automation secrets, and callback or
  open URL query secrets.
- **SC-008**: Ctrl-C and SIGTERM stop tests leave no active run-scoped HostFS,
  endpoint exposure, broker token, or proxy secret artifact in normal-stop
  cases.
- **SC-009**: At least one generic target CLI workflow completes two consecutive
  Lima runs using a reusable environment while preserving isolated profile
  state.

## Assumptions

- This is the first dogfood delivery slice, not the full dogfood readiness
  milestone.
- The operator has or can create an operator-authored profile before running
  this feature. Guided first-run setup is a separate feature.
- The configured target CLI is already executable inside the guest or can be
  prepared by existing operator-authored tool supply. Productizing setup for new
  operators is a separate feature.
- Full release evidence bundles are a separate feature. This feature only
  requires per-run evidence sufficient for supervised dogfood.
- Rich live TUI/WebUI observation is a separate feature. This feature only
  requires routine run completion output plus audit and Boundary Summary
  evidence.
- Public GA, unattended daemon operation, guest-to-host host-control
  capabilities, public ecosystem trust, and generalized host app command proxy
  providers are out of scope.
