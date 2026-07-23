# Feature Specification: Projection Readiness Proof

<!-- markdownlint-disable MD013 -->

**Feature Branch**: `043-projection-readiness-proof`

**Created**: 2026-07-23

**Status**: Implemented

**Input**: User description: "Continue overall Hideout convergence by making projected host-app commands available on the first fresh environment run without a retry, closing the remaining 030 projection verification debt, and replacing dirty development receipts with clean exact-package real evidence for the built-in projection, external host-app pack, and persistent trusted grant paths."

## Context

Hideout's projected host-app path is implemented and works on real macOS arm64
Lima. The current data plane materializes the session's exact projected command
catalog before backend preparation and target launch, and the guest bootstrap
checks a subset of required shim files. Real aggregate Gate 2 also exercises
safe `code .`, trusted grant/revoke, and an external pack.

Three convergence gaps remain:

1. A first `code .` immediately after fresh environment creation has
   intermittently reported that `code` is missing, while an immediate retry
   succeeds. The current gates prove eventual function but do not prove a
   first-attempt guest readiness barrier for the complete session catalog.
2. The 030 debt ledger asks for four old acceptance observations to be
   re-verified. Later work appears to cover broker registration and workspace
   drift, while direct template-default and schema/struct parity assertions
   remain incomplete. The ledger cannot be retired from inference alone.
3. The retained 030 and 032 receipts are explicitly dirty development
   evidence, and 039's real persistent-grant lane lacks a clean,
   feature-specific promotion receipt. Aggregate success is valuable regression
   evidence but does not by itself provide exact-package provenance for these
   claims.

This feature closes those gaps without adding commands, configuration,
capabilities, application providers, or host authority.

Implementation outcome (2026-07-23): the complete session catalog is now
authenticated before commit; all four historical 030 observations have direct
mutation-sensitive proofs; and a clean exact-package macOS arm64 Lima Gate 2
passed 10 fresh plus 30 warm first attempts, concurrency, built-in projection,
external pack, and persistent grant/revoke. Matching clean Gate 3 evidence was
not produced, so alias privacy remains explicitly unpromoted.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Open The Host App On The First Attempt (Priority: P1)

An operator creates or automatically receives a fresh supported environment
and runs `code .` as the first target command. Hideout waits until the exact
session projection is usable in the guest, then opens the approved workspace
through the existing safe host-app path. The operator does not need to rerun
the command.

**Why this priority**: The primary product path currently has a visible
first-run reliability gap. A feature that usually works after retry is not a
finished default workflow.

**Independent Test**: Repeatedly create clean supported environments and use a
projected command as the first target. Every admitted run must either complete
the exact approved projection on its first attempt or stop before the target
with a bounded, typed readiness error and no host fallback.

**Acceptance Scenarios**:

1. **Given** a fresh supported environment and a safe built-in projection,
   **When** the first target is `code .`, **Then** the exact session projection
   is ready before target launch and the approved host application opens the
   mapped workspace on that first attempt.
2. **Given** a warm reusable environment and a newly created session,
   **When** its first target uses a projected command, **Then** it sees only
   that session's current projection catalog and succeeds without a retry.
3. **Given** projection readiness cannot be proved before the bounded deadline,
   **When** the run reaches the pre-target boundary, **Then** Hideout refuses
   the run with a typed recovery hint, launches no target, invokes no ambient
   guest or host fallback, and retains redacted diagnostic evidence.
4. **Given** two concurrent sessions with different projected command
   catalogs, **When** both reach readiness, **Then** each guest view resolves
   only its own catalog and neither session can execute the other's shim.

---

### User Story 2 - Keep Projection Authority Closed While Fixing Readiness (Priority: P2)

An operator can rely on the readiness fix without receiving broader host
authority. Only commands in the exact validated session catalog become
available; names, guest-supplied audit fields, stale plans, malformed
descriptors, and workspace-path changes cannot manufacture or silently retain
authority.

**Why this priority**: Readiness is a synchronization property, not permission
to loosen the command registry, broker validation, workspace identity, or
schema boundary.

**Independent Test**: Contradict each admission fact independently and verify
that the command is refused before a host effect. Re-run the four historical
030 acceptance observations against the current product and retain a direct
test or explicit still-open debt disposition for each.

**Acceptance Scenarios**:

1. **Given** a guest request names a command absent from the current session
   catalog, **When** it reaches the broker, **Then** the request is refused and
   its name cannot be presented as a validated command in audit.
2. **Given** newly created privacy, hardened, development, and debug profiles,
   **When** their workspace presentation is inspected, **Then** every template
   directly proves its current neutral alias posture rather than inheriting an
   untested default.
3. **Given** an existing environment and a profile workspace-path change,
   **When** a later run is planned, **Then** the environment reports drift and
   requires explicit recreation rather than silently remapping.
4. **Given** a capability descriptor or open-resource intent emitted by the
   product, **When** it is validated against the public contract, **Then** the
   real field set passes and an unknown, missing, or incompatible field fails.
5. **Given** a projected command catalog changes after review but before
   readiness admission, **When** the run starts, **Then** the stale catalog is
   refused rather than substituted with current ambient state.

---

### User Story 3 - Promote Projection Claims With Clean Provenance (Priority: P3)

A maintainer can produce one retained, evaluator-checked evidence set from a
clean source commit and exact package that proves first-attempt readiness plus
the existing built-in projection, external pack, and persistent trusted-grant
flows on the supported real backend.

**Why this priority**: Current dirty receipts establish useful development
behavior but cannot support a release-candidate claim. Clean provenance turns
the implemented path into a reproducible promoted path.

**Independent Test**: Run the strict producer against a clean exact package,
then evaluate the retained manifest independently. Dirty, reduced,
command-only, incomplete, edited, or package-mismatched fixtures must be
rejected.

**Acceptance Scenarios**:

1. **Given** a clean supported candidate with its exact verified package and
   runtime, **When** the real projection gate completes, **Then** retained
   evidence binds the source, package, runtime, platform, guest, application
   identity, complete check inventory, timings, and redaction result.
2. **Given** the built-in safe projection, an external declarative host-app
   pack, and a durable trusted grant, **When** each real flow is exercised from
   the exact package, **Then** every flow passes its existing authority,
   lifecycle, revoke, no-fallback, and host-effect checks.
3. **Given** the alias privacy claim is promoted from the candidate, **When**
   privacy verification runs, **Then** the required identity channels and
   adjacent non-claims remain intact under the corresponding real privacy gate.
4. **Given** evidence is dirty, reduced, missing a first-attempt sample,
   package-mismatched, manually relabeled, or contains an unknown field,
   **When** the production evaluator reads it, **Then** promotion is refused.

### Edge Cases

- The host shim files exist but the guest session view is not yet the current
  view.
- One expected projected command is missing while other commands are ready.
- A shim exists but is not executable, is a symlink, or belongs to another
  session catalog.
- The session is cancelled while readiness is being proved.
- The environment becomes stopped, replaced, or changes boot identity during
  the readiness check.
- An external pack is disabled, revoked, modified, or loses command ownership
  between planning and target launch.
- The host application is absent or its signed identity changes after shim
  readiness; application identity validation still fails independently.
- A target command is a normal guest executable rather than a projected
  command; readiness work does not turn into a generic target retry.
- A dedicated/static environment and the shared default environment expose the
  same command contract through different attachment mechanisms.
- A real gate passes behavior but cannot prove a clean source, exact package,
  or complete artifact inventory.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Existing host-app projection, command registry,
  broker, environment/session attachment, lifecycle readiness, audit, evidence,
  and real backend gates. No new authority family or operator command is added.
- **Fail-closed behavior**: Missing, foreign, stale, non-executable, symlinked,
  unregistered, or unproved shims stop before target launch. Readiness expiry,
  cancellation, boot drift, catalog drift, application identity failure, and
  evidence mismatch never fall back to an ambient guest binary or host
  execution.
- **User authority and policy**: Existing safe mode, run-bound decisions,
  durable per-profile/workspace trusted grants, explicit pack enablement, and
  deny/revoke behavior remain unchanged. Readiness proves availability only; it
  never grants host-app access.
- **Generality and provider scope**: The readiness contract applies to the
  generic projected-command catalog. VS Code and the external test pack are
  named real providers used to prove the contract, not new Core semantics.
- **Evidence surface**: A bounded readiness disposition is available through
  structured audit and diagnostics. Promotion uses closed production evidence
  evaluated independently from the producer. Existing explain, doctor,
  Manager, TUI, and WebUI authority views remain derived from the same catalog.
- **Secret/redaction boundary**: Evidence never exposes host paths, usernames,
  application private state, broker/UI tokens, grant store paths, raw target
  arguments, machine identifiers, or capability credentials. Public command
  names, stable proof identifiers, bounded timings, and redacted failure codes
  may be retained.
- **Backend/gate expectation**: Local and native lanes prove mechanics only.
  First-attempt visibility, host effect, external-pack operation, and durable
  grant reuse require clean exact-package macOS arm64 Lima evidence with an
  aarch64 guest. Alias privacy promotion also requires the corresponding clean
  real privacy gate.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Every supported run whose first target is a projected command
  MUST prove that the exact command is usable in the current guest session
  before launching the target.
- **FR-002**: The readiness proof MUST be derived from the complete validated
  projected-command catalog for that session, including built-in and enabled
  external bindings.
- **FR-003**: Readiness MUST require the expected command entry and dispatcher
  to be executable, non-symlinked, and bound to the current session view.
- **FR-004**: Readiness MUST have a documented bounded deadline and MUST honor
  caller cancellation.
- **FR-005**: An unproved readiness outcome MUST launch zero target commands,
  invoke zero host effects, and attempt zero ambient guest or host fallbacks.
- **FR-006**: The product MUST NOT automatically retry a target command after
  target launch; readiness retries MAY occur only before the target side-effect
  boundary.
- **FR-007**: Shared, dedicated, fresh, warm, and concurrently attached
  supported sessions MUST preserve the same projected-command readiness
  contract without sharing session-local command authority.
- **FR-008**: A readiness proof MUST become invalid if the session catalog,
  environment identity, backend instance, or boot identity changes before
  target launch.
- **FR-009**: A readiness failure MUST expose a stable typed reason and bounded
  operator guidance without leaking private paths or credentials.
- **FR-010**: Broker admission and audit classification MUST derive the
  projected command name from the current validated catalog, not solely from a
  guest-supplied request field.
- **FR-011**: Requests for unregistered, disabled, revoked, foreign, malformed,
  or stale projected commands MUST remain fail-closed with zero host effect.
- **FR-012**: Newly created privacy, hardened, development, and debug profiles
  MUST each directly prove the current neutral workspace alias default.
- **FR-013**: Changing workspace path presentation for an existing environment
  MUST directly prove a recreate-required drift result and zero silent remap.
- **FR-014**: Public capability-descriptor and open-resource-intent contracts
  MUST be validated against actual product-emitted values and MUST reject
  unknown, missing, and incompatible fields.
- **FR-015**: Each of the four historical 030 acceptance observations in the
  debt ledger MUST receive an evidence-backed disposition: closed by a named
  current test or retained with a revised trigger and exact remaining gap.
- **FR-016**: Any debt item proved closed MUST be removed or narrowed in the
  same change; no debt entry may disappear without its retained disposition.
- **FR-017**: New readiness, broker, default, drift, and schema assertions MUST
  each have an observed implementation mutation that turns the intended
  negative fixture red before restoration.
- **FR-018**: Any new evidence evaluator or check MUST have a negative fixture
  proving rejection of false-green input.
- **FR-019**: The strict real producer MUST bind a clean full source commit,
  exact verified package, declared runtime artifact, supported host/guest
  platform, complete closed check inventory, raw samples, artifact digests, and
  redaction status.
- **FR-020**: The real producer MUST include repeated first-attempt projected
  command runs across genuinely fresh environments and new sessions on warm
  environments.
- **FR-021**: The real producer MUST re-prove the built-in safe projection,
  external declarative pack, and durable trusted grant reuse/revoke paths with
  zero fallback and exact host-effect observation.
- **FR-022**: A promoted alias privacy claim MUST be accompanied by clean real
  privacy evidence and the existing adjacent non-claims; otherwise its prior
  dirty provenance MUST remain explicit.
- **FR-023**: Dirty, reduced, local/native, command-only, incomplete,
  package-mismatched, edited, unknown-field, or `not-run` evidence MUST NOT
  satisfy promotion.
- **FR-024**: Ordinary non-projected guest commands MUST retain their existing
  command lookup and error semantics and MUST NOT inherit projection-specific
  readiness retries.
- **FR-025**: The feature MUST add no new CLI/configuration surface, host
  capability, provider type, raw host execution, HostFS authority, workspace
  copy, or guest-to-host projection direction.
- **FR-026**: Product status, claim boundaries, test plans, debt, and the
  feature adversarial report MUST state the exact promoted evidence,
  implementation mutation results, remaining non-claims, and any gate that
  remains pending.

### Key Entities

- **Projection Readiness Expectation**: The immutable session, environment,
  backend/boot, catalog, and command facts that must be observed before a
  projected target can launch.
- **Projection Readiness Observation**: One bounded guest-side observation of
  the expected dispatcher and command entries, their file properties, session
  binding, outcome, reason, and timing.
- **Projection Readiness Disposition**: The closed result `ready`, `refused`,
  `timed-out`, or `cancelled`, with a stable reason and no authority of its own.
- **Projection Promotion Evidence**: The retained clean-candidate manifest and
  exact artifacts covering readiness samples, built-in projection, external
  pack, durable grant, privacy status, redaction, and non-claims.
- **030 Debt Disposition**: The retained mapping from each historical
  observation to a current direct proof or an explicitly revised deferred
  trigger.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The projected command succeeds on the first attempt in 100% of at
  least 10 clean environment creations; no sample requires an operator retry.
- **SC-002**: The projected command succeeds on the first attempt in 100% of at
  least 30 new sessions attached to warm environments, including concurrent
  disjoint session catalogs.
- **SC-003**: All readiness refusal fixtures observe zero target launches, zero
  host effects, zero ambient fallbacks, and zero cross-session command access.
- **SC-004**: Readiness completes within 2 seconds at p95 after the guest
  session view is otherwise ready, with zero unbounded waits and cancellation
  completing within 2 seconds.
- **SC-005**: All four historical 030 observations have an evidence-backed
  disposition, and every closed item has at least one named direct regression
  test.
- **SC-006**: Actual product-emitted capability descriptors and open-resource
  intents pass their public contracts, while 100% of unknown/missing/
  incompatible-field fixtures fail.
- **SC-007**: A clean exact-package real run passes the complete built-in
  projection, external pack, persistent grant/revoke, first-attempt readiness,
  host-effect, lifecycle, no-fallback, and redaction inventory.
- **SC-008**: The production evaluator rejects 100% of dirty, reduced,
  incomplete, edited, package-mismatched, unknown-field, and `not-run` negative
  fixtures.
- **SC-009**: Targeted tests, race tests, full Gate 0, aggregate real Gate 2,
  and the applicable real privacy gate pass with no regression to ordinary
  guest commands or existing authority boundaries.

## Assumptions

- The supported promotion target remains macOS arm64 with the Lima backend and
  an aarch64 guest; native remains mechanics-only.
- The signed built-in host application and a bounded external declarative pack
  fixture are available to the real gate.
- The intermittent first-run symptom is treated as unproved readiness even if
  the eventual root cause is guest attachment visibility rather than file
  creation order.
- Existing host-app modes, grant semantics, command catalogs, workspace
  mappings, and application identity validation are reused without widening
  their authority.
- HostFS discoverable-namespace provenance is outside this slice unless the
  projection investigation exposes a direct shared defect; 029 remains a
  separate claim.
- Clean privacy promotion is conditional on the existing privacy-gate
  prerequisites. If they are unavailable, documentation must retain the dirty
  provenance instead of substituting a local or aggregate result.
