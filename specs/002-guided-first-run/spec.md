<!-- markdownlint-disable MD013 -->

# Feature Specification: Tool Model Cleanup

**Feature Branch**: `002-guided-first-run`  
**Created**: 2026-07-05  
**Status**: Draft  
**Input**: User description: "002：Tool model cleanup。清 npm/provider/preset 旧面，落 expectedCommands，让当前代码和新文档不打架。"

## Current Status Context

This feature supersedes the earlier guided-first-run scope that lived on this
branch. The architecture has since converged on a stricter tool model:
Hideout runs a command inside a backend boundary and mediates files,
environment, network, and host capabilities. Hideout does not own package
installation, tool provisioning, package-manager recipes, or product-specific
agent setup as Core semantics.

The current documentation and constitution define the target model:
operator-provided guest tools come from a base image, a dedicated environment
setup run, or another operator-controlled process outside Hideout's tool
authority. Hideout may diagnose whether a command is expected and runnable, but
it must not install or materialize that command. This feature cleans the
remaining npm/provider/preset surfaces from the implementation and replaces
them with diagnostic expected-command declarations.

This feature is intentionally not a first-run onboarding feature. Guided
onboarding, named/global environment creation, base-image selection UX, daemon
mode, TUI/WebUI observation, marketplace trust, and product-specific agent
recipes remain outside this slice.

## Clarifications

### Session 2026-07-05

- Q: Which generic CLI workload is the default first-run success target? → A:
  Hideout-provided generic test CLI; user-declared CLI tool declarations are
  diagnostic follow-on paths, not the P1 success dependency. This answer is
  superseded by the 2026-07-06 scope reset; 002 is now tool-model cleanup, not
  guided first-run onboarding.

### Session 2026-07-06

- Q: Should 002 automatically install or materialize user-declared CLI tools?
  → A: No. User-declared CLIs may be configured or diagnosed but are not
  installed or materialized by this feature.
- Q: After the architecture reset, how far should 002 clean the old tool
  model? → A: Full cleanup. Remove the npm/provider/preset product surface,
  schema/profile/API/CLI compatibility, and provider execution path; introduce
  `tools.expectedCommands` as diagnostic-only state; reject old fields with a
  clear migration diagnostic rather than silently tolerating or migrating them.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Remove Package-Manager Tool Authority (Priority: P1)

A Hideout operator or developer can no longer invoke npm-specific setup,
package-manager provider execution, or preset-based tool provisioning through
Hideout. If an old profile, API request, or command tries to use that model,
Hideout rejects it with a clear diagnostic that points to the expected-command
model instead.

**Why this priority**: The old tool-supply model contradicts the current
architecture. Leaving it in place would keep a hidden installer/provider path
that grants Hideout responsibility for guest tool materialization.

**Independent Test**: Exercise the public command surface, profile validation,
and relevant API/schema validation with old npm/provider/preset inputs. The
test passes only if each old input fails closed with a clear unsupported-field
or unsupported-command diagnostic and no package installation, network
provisioning, profile mutation, or backend preparation occurs because of that
old tool model.

**Acceptance Scenarios**:

1. **Given** a profile or request contains an old npm global, package-provider,
   or preset tool declaration, **When** Hideout validates it, **Then** Hideout
   rejects it and explains that tool installation is no longer a Hideout
   product authority.
2. **Given** an operator attempts an old npm/provider/preset CLI path, **When**
   Hideout parses or plans the command, **Then** Hideout fails closed before
   mutating the profile or starting backend preparation.
3. **Given** tests scan the public help and documentation surfaces, **When**
   they look for the removed npm/provider/preset product paths, **Then** only
   explicit migration or removal notes remain.

---

### User Story 2 - Declare Expected Commands For Diagnosis (Priority: P2)

An operator can declare which guest commands they expect to be available in an
environment. Hideout uses those declarations to diagnose readiness when an
existing or controlled check context is available, explain missing or
not-checkable commands, and contribute to environment fingerprinting, without
trying to install or repair the command.

**Why this priority**: Operators still need a generic way to say "this profile
expects `claude`, `codex`, or another CLI to exist" without turning Hideout
into a package manager or product-specific installer.

**Independent Test**: Configure expected commands against controlled diagnostic
contexts that report present, missing, and not-checkable states. The test
passes only if Hideout reports those states accurately, keeps the declarations
in diagnostic state, and does not invoke package managers, setup providers, or
host commands to make an absent command appear.

**Acceptance Scenarios**:

1. **Given** a profile declares an expected command and the diagnostic context
   reports it present, **When** the operator runs the relevant diagnostic,
   **Then** Hideout reports the command as runnable and records the declaration
   as diagnostic evidence.
2. **Given** a profile declares an expected command and the diagnostic context
   reports it missing or not-checkable, **When** the operator runs the relevant
   diagnostic or attempts that target command, **Then** Hideout reports that
   state and fails closed without installing it when the command is required.
3. **Given** expected-command declarations change, **When** Hideout reports or
   fingerprints environment readiness, **Then** the declarations are reflected
   as expectations, not as completed provisioning work.

---

### User Story 3 - Keep Documentation And Status Honest (Priority: P3)

A contributor reading the spec, public docs, and status files sees one tool
model: Hideout diagnoses expected guest commands and mediates the command once
it runs; it does not provide npm globals, package-manager providers, tool
presets, or installer recipes.

**Why this priority**: The implementation and documentation have recently
changed direction. If specs and status files keep old setup language, future
tasks will reintroduce installer paths.

**Independent Test**: Run a repository scan over specs, docs, schemas, help
text, and tests for the old npm/provider/preset vocabulary. The test passes
only if remaining uses are either removed, explicitly marked as deleted legacy
surface, or part of negative tests that prove the old surface is rejected.

**Acceptance Scenarios**:

1. **Given** a new contributor reads the architecture and 002 artifacts,
   **When** they look for tool setup behavior, **Then** they find
   expected-command diagnostics and do not find package-manager provisioning
   instructions.
2. **Given** release or status documentation describes 002, **When** it names
   the remaining work, **Then** it says tool-model cleanup rather than guided
   first-run onboarding.
3. **Given** future planning references tool setup, **When** it needs actual
   environment materialization, **Then** it points to base-image or named
   environment design rather than adding a new provider in this feature.

### Edge Cases

- An existing local profile contains `tools.npmGlobals`, `tools.presets`, a
  package-manager provider declaration, or another old installer field.
- A command or API client sends both `tools.expectedCommands` and removed tool
  fields in the same request.
- An expected command is declared but the guest environment is unavailable, so
  Hideout cannot prove whether the command exists.
- An expected command name is malformed, empty, path-like, or attempts to carry
  arguments instead of naming a command.
- A missing expected command is also the target command for a run.
- Documentation or tests mention npm/provider/preset as historical context.
- A contributor attempts to reintroduce a product-specific tool recipe as a
  convenience feature.
- A profile generated before this cleanup is loaded from a clean store or from
  a test fixture.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Profile validation, diagnostic setup state,
  command-readiness reporting, status wording, schema validation, and any
  remaining tool-provider execution hooks.
- **Fail-closed behavior**: Old npm/provider/preset inputs must not be ignored,
  silently migrated, or partially applied. They must fail closed with an
  operator-facing diagnostic. Missing expected commands must also fail closed
  when they are required for the requested target command.
- **Go primitive / JS and config boundary**: Go may store and validate
  expected-command declarations and report facts. Go must not grow
  product-specific package-manager logic. JS/config may later interpret those
  facts for policy, but this feature does not create installer authority.
- **User authority and ecosystem boundary**: Operator-authored environment
  setup remains outside this feature. Imported recipes, generated plans, or
  third-party bundles must not gain tool-install authority through compatibility
  with the removed model.
- **Evidence surface**: Diagnostics and status output must distinguish
  "expected", "present", "missing", and "unsupported old field" from
  "installed" or "repaired". Evidence must be derived from the actual
  validation/diagnostic path, not recomputed from stale docs.
- **Backend/gate expectation**: This feature can be validated with unit tests,
  schema checks, help/docs scans, and Gate 0. Real environment materialization
  and Lima dogfood onboarding belong to later environment/onboarding slices.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST remove npm-specific package installation,
  package-manager provider execution, and tool preset provisioning from the
  product command surface, profile model, schema model, and runtime setup path.
- **FR-002**: The system MUST reject legacy tool-install fields and commands
  with clear diagnostics rather than silently accepting, ignoring, or migrating
  them.
- **FR-003**: The system MUST provide a canonical expected-command declaration
  model for operator-authored profiles or setup state.
- **FR-004**: Expected-command declarations MUST be stored as command-name
  strings under `tools.expectedCommands`; object entries, descriptions,
  package coordinates, installers, providers, and per-command required flags
  are out of scope for this feature.
- **FR-005**: Expected-command declarations MUST be diagnostic-only. They MUST
  NOT install packages, invoke package managers, download artifacts, run setup
  scripts, mutate guest environments, or grant host authority.
- **FR-006**: Hideout diagnostics MUST report expected-command state as present,
  missing, or not-checkable when a check context supplies that evidence; when
  no environment inspection is available, the state MUST be not-checkable rather
  than inferred.
- **FR-007**: If a missing expected command is required for the requested target
  command, Hideout MUST fail closed before claiming the run is ready.
- **FR-008**: Expected-command declarations MAY contribute to environment
  readiness evidence or environment fingerprints, but such evidence MUST label
  them as expectations, not provisioning results.
- **FR-009**: Public documentation, specs, status files, and quickstart
  material MUST describe tool setup as external to Hideout's Core authority and
  MUST direct users toward base images, named environment setup, or ordinary
  in-boundary setup runs for materialization.
- **FR-010**: Tests MUST cover rejection of removed npm/provider/preset inputs
  and acceptance of valid expected-command declarations.
- **FR-011**: The cleanup MUST NOT add product-specific support for any
  third-party agent CLI, package ecosystem, editor, marketplace, or installer.
- **FR-012**: The cleanup MUST NOT remove Hideout's ability to run an arbitrary
  operator-provided target command that already exists inside the selected
  backend environment.
- **FR-013**: Any remaining references to npm/provider/preset vocabulary MUST
  be limited to explicit removal notes, migration diagnostics, or negative
  tests.

### Key Entities

- **Expected Command Declaration**: Operator-authored diagnostic state naming a
  command that is expected to exist inside a guest environment. It is stored as
  a command-name string and carries no package source, installer, provider,
  description, per-command required flag, or setup script.
- **Tool Diagnostic**: A result that reports expected command status as
  present, missing, not checkable, or blocked by unsupported legacy fields.
- **Deprecated Tool-Supply Surface**: Any old npm global, package provider,
  preset, package hint, or installer-oriented field/command that would cause
  Hideout to materialize a guest tool.
- **Environment Readiness Evidence**: User-facing or test-facing evidence that
  records the expected-command declarations and their diagnostic state without
  claiming installation.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Repository scans find no live npm/provider/preset tool-supply
  product paths outside explicit removal notes, migration diagnostics, or
  negative tests.
- **SC-002**: Profiles or requests using legacy tool-install fields fail with a
  clear diagnostic in automated tests.
- **SC-003**: Profiles or requests using valid expected-command declarations
  validate successfully and produce diagnostic output without installing tools.
- **SC-004**: Missing expected commands are reported as missing, and missing
  target commands fail closed without fallback to host execution or
  package-manager setup.
- **SC-005**: Gate 0 and the relevant package/schema tests pass after the old
  tool model is removed.
- **SC-006**: Public English and Chinese user-facing docs no longer instruct a
  new user to configure npm globals, tool presets, or provider-based tool
  installation as part of Hideout setup.

## Assumptions

- The product is not released, so compatibility with old npm/provider/preset
  profile fields is not required.
- Operators who need a real agent CLI inside a guest environment will use a
  base image, a named environment setup flow, or an ordinary in-boundary command
  outside this feature's authority.
- Existing guided first-run and global environment goals will be handled by
  later specs after the tool model is cleaned up.
- The branch and directory name may still say `002-guided-first-run` until the
  feature artifact is renamed; the normative scope of this spec is tool-model
  cleanup.
