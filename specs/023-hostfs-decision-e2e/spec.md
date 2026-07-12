# Feature Specification: HostFS And Decision E2E

**Feature Branch**: `023-hostfs-decision-e2e`

**Created**: 2026-07-09

**Status**: Draft

**Input**: User description: "Continue product hardening without expanding
authority: prove HostFS write overlay and decision center semantics end to end,
with honest local-fast versus real Gate 2 evidence."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Prove Staged HostFS Write Approval (Priority: P1)

An operator needs confidence that a target write through HostFS does not mutate
the host lower file until a local authenticated decision is applied. The proof
must show the target sees staged content, the host remains unchanged before
apply, the pending decision is visible, and apply changes exactly the intended
host file.

**Why this priority**: HostFS write overlay is the most sensitive local
mutation path. If staging and apply semantics drift, Hideout may appear safe
while allowing unintended host mutation.

**Independent Test**: Run the HostFS/decision E2E script in local-fast mode and,
when Gate 2 prerequisites exist, real Gate 2 mode. The test must produce
product-hardening evidence that names the exact HostFS operations, backend
mode, approval surface, and artifacts covered.

**Acceptance Scenarios**:

1. **Given** a host file fixture and an overlay write grant, **When** the target
   writes new content, **Then** target reads through the overlay show the staged
   content while the host lower file remains unchanged before apply.
2. **Given** the staged write decision is pending, **When** an authenticated
   local operator claims and applies it through a supported deterministic
   surface, **Then** the host lower file changes exactly as planned and audit
   records the claim/apply outcome.
3. **Given** the host lower file changes after staging but before apply,
   **When** the operator tries to apply the stale staged write, **Then** apply
   fails closed and leaves the conflicting host file unchanged.

---

### User Story 2 - Prove Decision Center Concurrency And Outcomes (Priority: P2)

An operator needs the generic decision center to behave predictably: exactly one
claim wins, losing claimers see claimed state, approve/deny transitions are
audited, and timeout/default-deny remains visible as a denied outcome.

**Why this priority**: HostFS write approval depends on the same decision
center semantics used by other actionable decisions. Race or timeout regressions
would undermine every local approval workflow.

**Independent Test**: Create one HostFS write decision and one generic
share/export-style decision in a temporary store, claim them through separate
local actors, resolve them, and verify status, audit, and public redaction.

**Acceptance Scenarios**:

1. **Given** a pending decision, **When** two claim attempts race or run
   sequentially from independent clients, **Then** exactly one claim owns the
   decision and the loser sees an already-claimed result.
2. **Given** a claimed decision, **When** the operator denies it, **Then** the
   decision becomes denied, provider-private data remains hidden, and audit
   shows the denial.
3. **Given** a timeout/default-deny decision, **When** the timeout expires or is
   simulated in the E2E fixture, **Then** the decision becomes denied by
   timeout and no provider apply side effect occurs.

---

### User Story 3 - Prove Visibility Without New Approval Surfaces (Priority: P3)

An operator needs pending HostFS and decision state to appear consistently in
CLI/API and the existing WebUI/TUI model, while approval authority remains in
the existing typed Manager decision workflow.

**Why this priority**: 021 already proves UI plumbing. 023 should prove HostFS
and decision state are visible to those surfaces without duplicating browser
click coverage or inventing new approval logic.

**Independent Test**: Produce a pending decision fixture and verify CLI/API
records plus WebUI/TUI model evidence expose redacted decision summaries and do
not expose claim tokens or provider-private refs.

**Acceptance Scenarios**:

1. **Given** a pending HostFS write decision, **When** CLI, API, WebUI model, and
   TUI model views are inspected, **Then** each shows a redacted pending
   decision summary with the same decision id and no claim token.
2. **Given** a decision is resolved, **When** the model is refreshed through the
   existing live-console state, **Then** the pending count and decision status
   change consistently.

### Edge Cases

- Real Gate 2 prerequisites may be absent. In that case real mode must write
  `not-run` evidence with missing prerequisites and must not satisfy a real
  HostFS proof claim unless explicitly allowed by the caller.
- Local-fast evidence may prove decision state and local model behavior, but it
  must not claim Linux guest FUSE behavior or Gate 2 HostFS data-plane proof.
- If the E2E covers only a representative subset of HostFS write operations, the
  evidence and docs must say "representative" and list uncovered operations.
- If a staged object, claim token, provider ref, or private overlay storage path
  appears in public CLI/API/UI/evidence output, the proof fails.
- If workspace writes are involved, the proof must label them as normal
  workspace mutation, not HostFS overlay mutation.

## Constitutional Alignment *(mandatory for Hideout features)*

- **Authority touched**: Existing HostFS write overlay, operator decision
  center, Manager decision plan/apply, CLI/API, WebUI/TUI model visibility,
  audit, and product-hardening evidence. No new HostFS operation, approval
  surface, backend, network authority, script authority, or remote approval is
  added.
- **Fail-closed behavior**: Missing HostFS support, unsupported backend,
  missing decision provider, stale lower-file snapshot, conflicting claim,
  timeout/default-deny, redaction failure, schema failure, or missing real Gate
  2 prerequisites must deny, fail, or record `not-run` before a pass claim.
- **User authority and policy**: Only explicit operator-authored HostFS overlay
  grants and authenticated Manager decisions authorize host lower mutation.
  Deny rules and reserved-root checks still win. The workspace remains a shared
  surface and is not blocked by this feature.
- **Generality and provider scope**: This is a proof layer over existing Hideout
  HostFS/decision product paths. Any fixture backend or operation matrix is
  evidence scope, not new Core semantics.
- **Evidence surface**: Product-hardening evidence manifest, audit records,
  decision records, HostFS write status, Manager API records, WebUI/TUI model
  output, and optional Gate 2 artifacts.
- **Secret/redaction boundary**: Public records and artifacts must not expose
  claim tokens, provider-private refs, private overlay object paths, broker/UI
  tokens, `HIDEOUT_SECRET_*` material, generated machine-id material, or raw
  control-plane field names.
- **Backend/gate expectation**: Local-fast proves decision semantics and model
  visibility only. Real HostFS guest staging/apply semantics require Gate 2
  mode and must be marked `not-run` when prerequisites are unavailable.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST provide an E2E proof lane for HostFS write overlay
  staging that records backend mode, HostFS operations covered, approval surface,
  and artifact references.
- **FR-002**: The proof MUST assert target-side reads reflect staged overlay
  content before operator apply for every operation and backend mode it claims
  to cover.
- **FR-003**: The proof MUST assert host lower files or directories remain
  unchanged before operator apply.
- **FR-004**: The proof MUST assert operator apply changes host lower state
  exactly as planned and no unrelated host path changes.
- **FR-005**: The proof MUST include a stale/conflict path where apply fails
  closed after the host lower snapshot changes.
- **FR-006**: The proof MUST create or observe a pending decision and verify it
  is visible through CLI/API and the existing WebUI/TUI model without exposing
  private tokens or provider refs.
- **FR-007**: The proof MUST verify exactly one claimant wins a decision claim
  race or independent two-client claim sequence.
- **FR-008**: The proof MUST verify approve, deny, and timeout/default-deny
  outcomes update decision status and audit without unauthorized provider side
  effects.
- **FR-009**: The proof MUST write or append `hideout.product-hardening-evidence/v1`
  entries with stable 023 proof ids, covered claims, prerequisite status,
  redaction status, and explicit `not-run` reasons.
- **FR-010**: The proof MUST distinguish local-fast decision evidence from real
  Gate 2 HostFS evidence and MUST NOT let native/local-fast output satisfy real
  HostFS data-plane claims.
- **FR-011**: The proof MUST include a control-plane redaction injection check
  for decision records, audit, UI/TUI model output, and generated evidence.
- **FR-012**: The proof MUST fail if it cannot prove both sides of the staged
  overlay contract for any operation it marks as covered.
- **FR-013**: The proof MUST list uncovered HostFS write operations when it
  covers a representative subset instead of the full supported operation set.
- **FR-014**: Existing 021 browser/TUI action E2E remains the UI click proof;
  023 MUST NOT require a second browser click workflow unless it is needed to
  prove HostFS-specific visibility.

### Key Entities *(include if feature involves data)*

- **HostFS Write Proof**: Evidence record describing one staged write operation,
  backend mode, grant scope, target-observed staged state, host-lower pre/post
  state, and apply outcome.
- **Decision Proof**: Evidence record describing one decision id, type,
  claim/resolve/timeout path, winning claimant, losing claimant observation,
  public record fields, and audit references.
- **Operation Coverage Matrix**: Declared set of HostFS write classes covered by
  the run, including whether each class is local-fast, real Gate 2,
  representative, or not-run.
- **Redaction Scan Result**: Proof that public artifacts and records omit
  control-plane material while preserving ordinary user-visible path and content
  summaries needed for evidence.
- **Product-Hardening Evidence Manifest**: Stable manifest aggregating 023 proof
  entries for docs truth and future release-readiness references.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A local-fast run produces schema-valid product-hardening evidence
  proving decision visibility, claim, approve/deny, timeout/default-deny, and
  redaction with zero real HostFS data-plane overclaims.
- **SC-002**: A real Gate 2 run, when prerequisites are present, proves at least
  one file replace operation and one directory operation satisfy staged
  guest-read-before-apply and host-lower-unchanged-before-apply semantics.
- **SC-003**: If real Gate 2 prerequisites are absent, real mode records
  `not-run` evidence and exits according to caller policy; it never falls back
  to native/local-fast as a pass.
- **SC-004**: Claim concurrency proof demonstrates exactly one winning claimant
  and one losing claimant observation for the same decision id.
- **SC-005**: Conflict/stale proof leaves the host lower file unchanged and
  records a failed/denied apply outcome.
- **SC-006**: Redaction proof finds zero claim tokens, provider-private refs,
  private overlay object paths, broker/UI tokens, `HIDEOUT_SECRET_*` material,
  generated machine-id material, or control-plane field names in public
  artifacts.
- **SC-007**: Evidence and docs list every covered HostFS write class and every
  uncovered supported write class for the E2E run.
- **SC-008**: Gate 0 includes the local-fast 023 proof or an explicit local-fast
  proof script invocation; real Gate 2 remains explicit and prerequisite-gated.

## Assumptions

- CLI/API apply is the deterministic approval surface for 023. WebUI/TUI
  visibility is verified through existing model/state paths; 021 remains the
  browser/PTY click and rendering proof.
- Local-fast mode may use native/local fixtures to prove decision center state,
  concurrency, redaction, and UI/TUI model visibility, but cannot prove Linux
  guest FUSE behavior.
- Real Gate 2 mode is the authority for guest-visible HostFS staging semantics.
  It may be `not-run` on machines without the required backend prerequisites.
- Representative real Gate 2 coverage is acceptable for v1 if the manifest and
  docs explicitly name uncovered operations. A broader local-fast matrix may
  cover decision-state behavior for more operations.
- This feature does not alter workspace semantics; workspace writes remain
  intentionally visible to the target and are audited rather than blocked.
