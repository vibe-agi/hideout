# Feature Specification: Profile Templates And Onboarding

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `014-profile-templates-onboarding`

**Created**: 2026-07-09

**Status**: Draft

**Input**: User description: "Follow .tmp/011-016-plan.md using speckit-* skills; complete and commit one feature at a time. 014 ships curated profile templates and onboarding so first-run setup chooses an honest isolation posture."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Create A Recommended Privacy Profile (Priority: P1)

A new operator installs Hideout, runs the first-run onboarding path, accepts the recommended `privacy` template, and receives a usable profile with DNS mediation/proxy posture visible, no default workspace-external HostFS grants, no adapter packs installed by default, and a local evidence summary of the chosen posture.

**Why this priority**: This is the external-alpha first-run path. Users need one safe default that is clearer than raw flags and profile JSON.

**Independent Test**: Run onboarding in non-interactive mode with explicit `--template privacy` and required network/proxy flags, then validate the generated profile, evidence summary, docs next steps, and absence of HostFS grants and adapter-pack bindings.

**Acceptance Scenarios**:

1. **Given** a fresh store, **When** the operator runs onboarding with `--template privacy --no-input` and explicit required flags, **Then** Hideout creates a valid profile using the privacy template and prints next steps.
2. **Given** the generated profile, **When** it is inspected or exported, **Then** it contains no control-plane secrets, no default workspace-external HostFS grants, and no adapter-pack enablement.
3. **Given** the selected host/backend facts, **When** onboarding finishes, **Then** Hideout writes a local evidence summary describing the selected template, backend, network posture, HostFS posture, privilege posture, and non-claims.

---

### User Story 2 - Hardened Fails Closed Unless Separation Is Enforced (Priority: P2)

An operator who chooses `hardened` expects it to mean enforced privilege separation, not privacy plus a warning. If Hideout cannot prove no-sudo separation for the selected backend/base image, onboarding fails closed unless the operator explicitly chooses a differently named degraded profile.

**Why this priority**: The 009 work deliberately avoided overclaiming a boundary. 014 must not reintroduce that overclaim through a template name.

**Independent Test**: Run hardened onboarding with Lima plus enforced privilege facts and verify success; run with degraded/unknown facts or native-backend enforced claims and verify refusal; then run an explicit degraded fallback request and verify the profile name and evidence clearly say degraded.

**Acceptance Scenarios**:

1. **Given** privilege separation is enforced on a backend that can support product isolation evidence, **When** the operator chooses `hardened`, **Then** onboarding creates a hardened profile and evidence records `privilege=required-enforced`.
2. **Given** privilege separation is degraded or unknown, **When** the operator chooses `hardened`, **Then** onboarding refuses before profile creation with recreate/base-image guidance.
3. **Given** privilege separation is degraded or unknown, **When** the operator explicitly chooses a degraded fallback, **Then** Hideout creates a separately named degraded profile and evidence states that hardened was not achieved.

---

### User Story 3 - CI Can Create The Same Profiles Non-Interactively (Priority: P3)

A CI or scripted operator can create any non-hardened template profile without a TTY by providing explicit flags, and the command fails closed if required choices are missing.

**Why this priority**: External alpha needs reproducible smoke and docs paths. Non-interactive defaults must not silently guess unsafe authority.

**Independent Test**: Run onboarding without a TTY using `--no-input` for `privacy`, `dev`, and `debug`; verify missing required flags fail closed and complete flags create the expected profile.

**Acceptance Scenarios**:

1. **Given** no TTY and no template flag, **When** onboarding runs with `--no-input`, **Then** it fails closed and lists the missing explicit choices.
2. **Given** no TTY and explicit template/backend/network/profile flags, **When** onboarding runs with `--no-input`, **Then** the selected template profile is created without prompting.
3. **Given** the `dev` or `debug` template is selected, **When** onboarding completes, **Then** evidence labels weaker posture and non-claims instead of presenting them as privacy guarantees.

---

### User Story 4 - Interactive Onboarding Explains Choices (Priority: P4)

An operator running in a TTY gets a short guided flow that recommends a template, explains base-image/privilege requirements, shows HostFS and adapter-pack defaults, and asks for explicit confirmation before creating a profile.

**Why this priority**: Good defaults reduce setup mistakes, but interactive polish is lower priority than deterministic non-interactive behavior and hardened honesty.

**Independent Test**: Simulate TTY answers in tests, verify prompts name recommended template and high-impact defaults, and verify cancellation creates no profile.

**Acceptance Scenarios**:

1. **Given** a TTY, **When** onboarding starts without `--no-input`, **Then** Hideout recommends `privacy`, explains base-image requirements and weaker-template warnings, and asks for confirmation.
2. **Given** a TTY operator cancels, **When** onboarding exits, **Then** no profile or evidence summary is created.
3. **Given** a TTY operator confirms, **When** onboarding exits, **Then** the generated profile equals the non-interactive profile for the same choices.

### Edge Cases

- Existing profile name must fail closed unless an explicit replace/recreate mode is provided by a later feature.
- `hardened` must not silently become `privacy`; a degraded profile must have a distinct name or explicit degraded marker.
- Native backend cannot create an effective `hardened` profile, even when an operator declares `--privilege-status enforced`; it may only create an explicitly degraded fallback.
- `privacy` warns when privilege status is degraded or unknown but does not claim guest-root containment.
- `dev` and `debug` may use weaker settings, but evidence must label those settings as weaker/development posture.
- No template may create workspace-external HostFS grants by default.
- Onboarding must not install adapter packs by default; it may only print a follow-up command.
- Non-interactive onboarding must fail when required choices are absent.
- Generated evidence must not include proxy secret values, UI tokens, broker tokens, generated machine ids, or hidden store paths containing credentials.
- Native backend remains a weak development harness and must be labeled as such.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Profile state, network posture selection, backend/template selection, onboarding UI/CLI, local evidence summary. No new HostFS, network, privilege, daemon, or adapter authority is executed beyond existing profile/init paths.
- **Fail-closed behavior**: Missing non-interactive choices, existing profile collisions, invalid template names, hardened without enforced separation, unsupported backend/template combination, ambiguous degraded fallback, or evidence write failure MUST stop before profile creation or leave a clearly failed result.
- **User authority and policy**: Template selection is operator-authored profile creation. Deny precedence and reserved-root rules remain lower-layer HostFS/profile behavior. Templates must not grant workspace-external HostFS access by default.
- **Generality and provider scope**: `privacy`, `hardened`, `dev`, and `debug` are generic Hideout templates. Base-image examples remain examples; template semantics must not encode one provider, package manager, agent, editor, proxy port, or marketplace artifact.
- **Evidence surface**: Onboarding output, local evidence summary, profile metadata, docs, and Gate 0 smoke prove selected posture and non-claims. Real Lima gates are required only if implementation changes backend isolation.
- **Secret/redaction boundary**: Evidence, profile metadata, docs, and UI output must not expose proxy secret values, UI tokens, broker tokens, `HIDEOUT_SECRET_*`, generated machine ids, or hidden credential-bearing runtime paths.
- **Backend/gate expectation**: Gate 0 plus targeted profile/onboarding tests. Real Lima is not required for template creation, but 014 must preserve 009's privilege separation claim boundary and use injected/observed privilege facts in tests.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST define four built-in profile templates named `privacy`, `hardened`, `dev`, and `debug`.
- **FR-002**: The recommended default template MUST be `privacy`.
- **FR-003**: Template-created profiles MUST validate against the existing profile schema.
- **FR-004**: The `privacy` template MUST require `tun2socks` with an explicit proxy secret ref and mediated resolver, and MUST configure strict evidence defaults without granting workspace-external HostFS access by default.
- **FR-005**: The `hardened` template MUST require enforced privilege separation on a backend that can support product isolation evidence, plus `tun2socks` with an explicit proxy secret ref and mediated resolver, and MUST fail closed when selected backend/base-image facts are degraded, unknown, or native-backend operator declarations.
- **FR-006**: If hardened cannot be enforced, System MUST NOT silently create a profile named or labeled `hardened`; any degraded fallback must be explicitly requested and separately named or marked.
- **FR-007**: The `dev` template MAY choose practical weaker defaults, but generated evidence MUST label weaker or degraded posture and non-claims.
- **FR-008**: The `debug` template MUST label itself local/development-only and MUST NOT make privacy or hardened claims.
- **FR-009**: Onboarding MUST create no default workspace-external HostFS grants for any template.
- **FR-010**: Onboarding MUST NOT install or enable adapter packs by default.
- **FR-011**: Non-interactive onboarding with `--no-input` MUST require explicit template, profile, backend, and network choices and fail closed if required choices are missing.
- **FR-012**: Interactive onboarding on a TTY MUST recommend a template, explain base-image/privilege requirements, show HostFS and adapter-pack defaults, and ask for confirmation before creating a profile.
- **FR-013**: Cancellation or failed confirmation MUST create no profile and no success evidence summary.
- **FR-014**: Successful onboarding MUST write a local evidence summary recording template, backend, network posture, HostFS posture, adapter-pack posture, privilege posture, warnings, and non-claims.
- **FR-015**: Evidence summaries and profile metadata MUST pass deterministic control-plane redaction and contain no Hideout-minted control-plane secrets.
- **FR-016**: Existing profile name collisions MUST fail closed unless an explicit replace/recreate feature later promotes a safe flow.
- **FR-017**: Docs MUST show one short recommended onboarding path and one advanced customization path.

### Key Entities *(include if feature involves data)*

- **Profile Template**: Built-in template definition with id, purpose, defaults, required facts, warnings, non-claims, and follow-up hints.
- **Onboarding Request**: Operator choices for profile name, template, backend, network, degraded fallback, interactivity, and confirmation.
- **Privilege Fact**: Enforced/degraded/unknown separation fact used to decide hardened eligibility and warnings.
- **Generated Profile**: Durable profile created from a template and existing profile schema.
- **Onboarding Evidence Summary**: Local record of selected posture, warnings, non-claims, and follow-up commands.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: All four built-in templates validate into schema-valid profiles in targeted tests.
- **SC-002**: `privacy` non-interactive onboarding creates a profile with zero HostFS grants and zero adapter-pack bindings.
- **SC-003**: `hardened` onboarding succeeds with Lima enforced privilege facts and fails 100% of degraded/unknown/native-backend privilege fact tests before profile creation.
- **SC-004**: Hardened degraded fallback, when explicitly requested, creates a profile whose name or metadata clearly contains `degraded`.
- **SC-005**: Non-interactive onboarding without required choices fails 100% of missing-choice tests before profile creation.
- **SC-006**: Interactive cancellation creates 0 profile files and 0 success evidence summaries.
- **SC-007**: Onboarding evidence summaries contain 0 control-plane secret matches in smoke tests.
- **SC-008**: Gate 0 includes onboarding/template smoke coverage and fails if any template grants workspace-external HostFS by default.
- **SC-009**: README/docs include one recommended onboarding command and one advanced customization command.

## Assumptions

- The operator accepted `privacy` as the default recommendation.
- `hardened` is enforced-only; degraded fallback is allowed only as an explicit, separately labeled choice.
- Onboarding may use injected privilege facts in tests so Gate 0 can validate decisions without real Lima.
- Adapter pack installation remains a follow-up command, not first-run default behavior.
- HostFS write overlay grants remain explicit later user decisions; templates do not pre-grant write authority.
