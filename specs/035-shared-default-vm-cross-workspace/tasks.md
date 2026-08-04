# Tasks: Shared Default VM Across Workspaces

<!-- markdownlint-disable MD013 MD060 -->

**Input**: Design documents from
`specs/035-shared-default-vm-cross-workspace/`

**Tests**: Required. This feature changes direct filesystem, backend, daemon,
lifecycle, host-projection, UI and evidence boundaries.

**Hard barrier**: T006-T022 are Phase R. T023 and every later task are blocked
unless T022 produces a schema-valid `accepted` decision for exactly one complete
transport/path-identity pair. If T022 produces `rejected`, stop the implement
run, leave T023-T106 unchecked, and do not alter product defaults or claims.

## Phase 1: Setup

**Purpose**: Create behavior-neutral research/evidence infrastructure.

- [X] T001 Add strict Phase R decision and attachment schemas in `schemas/workspace-research-decision.schema.json` and `schemas/workspace-attachment.schema.json`
- [X] T002 [P] Add production-shaped probe command skeleton with no product dispatch in `cmd/hideout-workspace-probe/main.go`
- [X] T003 [P] Add deterministic workspace correctness, same/nested/disjoint, 10k Git and 20k package fixtures in `test/fixtures/workspaceattach/`
- [X] T004 [P] Add raw-sample benchmark and digest helpers in `scripts/lib/workspace-research.sh`
- [X] T005 Register research-only schema/lint checks without product claims in `scripts/test-gate0.sh`

---

## Phase 2: Phase R - Transport And Path-Identity Existence Gate

**Purpose**: Prove one complete candidate or stop without changing the product.

**Critical**: No task in Phase 3 or later may begin before T022 is accepted.

- [X] T006 Write strict decision parsing, artifact containment/digest, provenance, threshold, single-selection and stale-result tests in `internal/workspaceattach/research_test.go`
- [X] T007 Record equivalent warm/cold static virtiofs baselines and raw samples with fixed fixture/tool versions in `scripts/test-workspace-transport-research.sh`
- [X] T008 [P] Write VZ empty-share, authenticated exact-incarnation control, opaque-key, staging-root, sibling-handle and cleanup-observation probe tests in `internal/backend/lima/workspace_vz_probe_test.go`
- [X] T009 Implement the quarantined VZ running-device/share-map control spike in `internal/backend/lima/workspace_vz_probe_darwin.go` and `cmd/hideout-workspace-probe/`
- [X] T010 Prove VZ root-identity admission races, dynamic watcher enrollment/removal, TCC principals and no raw path/tag leakage in `internal/backend/lima/workspace_vz_probe_darwin_test.go`
- [X] T011 Document and machine-check VZ upstream/package/license/entitlement/signing/support feasibility in `specs/035-shared-default-vm-cross-workspace/research/vz-support.json`
- [X] T012 [P] Write Portal frame, handle, cancellation, backpressure, independent lock-owner, credential-expiry and disconnect tests in `internal/workspaceattach/portal_probe_test.go`
- [X] T013 Implement the quarantined binary multiplexed Portal host/guest probe in `internal/workspaceattach/portal_probe.go` and `cmd/hideout-workspace-probe/`
- [X] T014 Run the complete direct-write operation/errno/durability/watcher matrix against the Portal probe in `internal/workspaceattach/portal_filesystem_probe_test.go`
- [X] T015 Prove Portal per-session limits, fairness, teardown-reserved capacity, lock conflicts and mid-stream credential rotation/revocation in `internal/workspaceattach/portal_limits_probe_test.go`
- [X] T016 [P] Write logical `/workspace`, opaque physical cwd, Git safe-directory and representative tool project-state tests in `internal/workspaceattach/path_identity_probe_test.go`
- [X] T017 Implement and evaluate candidate session-private logical/physical path constructions in `internal/backend/lima/workspace_path_probe.go`
- [X] T018 Inventory and probe every host process that opens/watches a root, recording TCC as available/denied/unknown without fake decisions in `internal/workspaceattach/prerequisite_probe.go`
- [X] T019 Run at least 30 warm samples per candidate for Git/package/attach/first-byte plus atomic-save convergence and bounded saturation in `scripts/test-workspace-transport-research.sh`
- [X] T020 Prove candidate crash, detach, daemon restart, VM stop and ambiguous cleanup behavior without product authority adoption in `internal/workspaceattach/recovery_probe_test.go`
- [X] T021 Implement the strict binary research evaluator and artifact writer in `internal/workspaceattach/research.go`
- [X] T022 Produce and validate one bound `accepted` or `rejected` decision in `dist/workspace-research/035/decision.json`; remove losing prototype code/dependencies and stop here when rejected (FR-006, FR-028; SC-005)

**Checkpoint**: Phase I is authorized only by a valid accepted decision. A local
demo, partial pass, or softened threshold is not authorization.

---

## Phase 3: Foundational Phase I Model

**Purpose**: Replace machine/workspace conflation and define shared authority.

- [X] T023 Write clean shared/dedicated/workspace-bound record and old-alpha rejection tests in `internal/environment/environment_test.go` (FR-026, FR-036; SC-014, SC-020)
- [X] T024 Replace the environment record/spec schema and validators without a dual reader in `internal/environment/environment.go` (FR-003, FR-026, FR-036)
- [X] T025 [P] Write stable slot and field-level machine compatibility inclusion/exclusion tests in `internal/manager/run_environment_test.go` (FR-001, FR-002, FR-038)
- [X] T026 [P] Write key permissions, atomic creation, HMAC identity, replacement and collision tests in `internal/workspaceattach/identity_test.go` (FR-005; SC-012)
- [X] T027 Implement root identity, private key, workspace ID, attachment, relation and redacted projection models in `internal/workspaceattach/model.go`, `internal/workspaceattach/identity.go`, and `internal/workspaceattach/status.go` (FR-005, FR-018)
- [X] T028 Implement canonical machine, boot, environment-service, and session configuration descriptors and digests in `internal/environment/configuration.go` and `internal/manager/runtime_configuration.go` (FR-002, FR-020, FR-038)
- [X] T029 [P] Write machine-activation/workspace-attach/session-execution separation tests in `internal/backend/backend_test.go` and `internal/backend/lima/lima_test.go` (FR-029; SC-017)
- [X] T030 Split backend contracts while preserving explicit dedicated/workspace-bound static mappings in `internal/backend/backend.go` and `internal/backend/lima/lima.go` (FR-029, FR-036)
- [X] T031 [P] Add typed recovery records and real next actions for mode, drift, root, transport, TCC, overload and cleanup failures in `internal/recovery/registry.go` (FR-021, FR-033, FR-035; SC-019)
- [X] T032 [P] Write closed resource-catalog topology and invalid dependency/probe tests in `internal/lifecycle/catalog_test.go` (FR-013, FR-014)
- [X] T033 Add selected `workspace.host-provider`, `workspace.guest-view` and only-if-real `workspace.environment-service` descriptors in `internal/lifecycle/catalog.go` (FR-013)
- [X] T034 Add strict attachment/environment/session summary schemas and update daemon/API schemas in `schemas/environment-summary.schema.json`, `schemas/workspace-attachment.schema.json`, and `schemas/daemon-status.schema.json` (FR-025)
- [X] T035 Register stable `035.*` proof requirements and registry JSON output in `internal/productevidence/registry.go` (SC-015)
- [X] T036 Add a production-source drift guard that rejects shared-mode reads of environment-level workspace fields across `internal/manager`, `internal/daemon`, `internal/hostcap`, and `internal/backend` (FR-017, FR-039)
- [X] T037 Implement the selected transport-neutral provider/limit interfaces in `internal/workspaceattach/contract.go` and `internal/workspaceattach/limits.go` (FR-031, FR-032)
- [X] T038 Resolve only selected helper dependencies through Go tooling, update helper/package manifests, and run `go mod tidy` for `go.mod` and `go.sum` (FR-031)

**Checkpoint**: The clean model compiles and all foundational tests fail closed,
but shared selection is not enabled until User Story 1 is complete.

---

## Phase 4: User Story 1 - Reuse One Warm Machine Across Projects (P1) MVP

**Goal**: Two compatible projects select one automatic Lima machine and receive
separate attachments.

**Independent Test**: Hold project A, start project B, and prove one environment,
instance and boot identity with two correct workspace IDs/views.

- [X] T039 [P] [US1] Write simultaneous first-run, cross-project stable-slot, included drift and excluded session-change tests in `internal/manager/run_environment_shared_test.go` (FR-001, FR-002, FR-038; SC-001)
- [X] T040 [P] [US1] Write mode-matrix and named-project mismatch tests in `internal/manager/run_environment_mode_test.go` (FR-003, FR-036, FR-040; SC-013, SC-020)
- [X] T041 [P] [US1] Write shared Lima config tests proving no static/dummy/broad/raw-path workspace mount in `internal/backend/lima/shared_machine_test.go` (FR-009, FR-029, FR-030; SC-017)
- [X] T042 [US1] Implement stable profile slot selection and explicit compatibility drift in `internal/manager/run_environment.go` (FR-001, FR-002, FR-038)
- [X] T043 [US1] Implement explicit shared/dedicated/workspace-bound creation and validation in `internal/manager/run_environment.go` and `internal/environment/environment.go` (FR-003, FR-036, FR-040)
- [X] T044 [US1] Build shared Lima machine activation with no selected workspace input in `internal/backend/lima/lima.go` (FR-009, FR-029; SC-017)
- [X] T045 [US1] Add canonical Manager workspace attach plan/apply and immutable session binding in `internal/manager/run_workspace.go` (FR-005, FR-012, FR-029)
- [X] T046 [US1] Make the 034 daemon session worker the sole attach owner with no CLI/embedded fallback in `internal/daemon/session_server.go` and `internal/daemon/sessions.go` (FR-012)
- [X] T047 [US1] Move provider/view/supervisor activation behind the authenticated concrete ready callback in `internal/manager/run_apply.go` and `internal/backend/backend.go` (FR-014)
- [X] T048 [US1] Scope runtime receipts, owner records and session directories to machine or immutable attachment facts in `internal/manager/run_session.go` and `internal/manager/run_runtime.go` (FR-039)
- [X] T049 [US1] Preserve environment-network service ownership and session-secret separation in `internal/manager/run_dataplane.go` (FR-020)
- [X] T050 [US1] Add a two-project one-machine daemon smoke with instance/boot/view assertions in `scripts/test-shared-workspace-smoke.sh` (SC-001, SC-002)

**Checkpoint**: One shared machine can admit two attachments; filesystem and
isolation promotion still depend on User Stories 2 and 3.

---

## Phase 5: User Story 2 - Collaborate Through A Live Exact Workspace (P1)

**Goal**: Ordinary development I/O is live, correct, bounded and fast.

**Independent Test**: Run the complete operation, durability, lock, watcher and
performance fixture in both directions against the selected transport.

- [X] T051 [P] [US2] Convert the accepted operation matrix into regression tests in `internal/workspaceattach/filesystem_test.go` (FR-008, FR-021, FR-022, FR-037; SC-003, SC-004)
- [X] T052 [P] [US2] Write logical/physical path, shell navigation, Git safety and external worktree preflight tests in `internal/backend/lima/workspace_path_test.go` (FR-004, FR-035, FR-041; SC-019, SC-023)
- [X] T053 [US2] Implement the accepted direct workspace provider in `internal/backend/lima/workspace.go` and selected `internal/workspaceattach/` transport files (FR-007, FR-008, FR-021, FR-031)
- [X] T054 [US2] Implement explicit handles, flush/fsync, lock ownership, cancellation, disconnect and truthful errno behavior required by the selected transport in `internal/workspaceattach/provider.go` (FR-021, FR-031, FR-037)
- [X] T055 [US2] Implement measured cache/notification policy and atomic-save invalidation in `internal/workspaceattach/cache.go` (FR-022; SC-003)
- [X] T056 [US2] Bind every operation to the captured root identity and reject root replacement/rename switching in `internal/workspaceattach/root.go` (FR-005, FR-007)
- [X] T057 [US2] Construct the session-private physical root and protected logical `/workspace` entry in `internal/backend/lima/session_view.go` (FR-004, FR-010, FR-041)
- [X] T058 [US2] Generate only verified logical/physical Git safe-directory entries and typed external-metadata guidance in `internal/manager/run_workspace.go` (FR-035, FR-041)
- [X] T059 [US2] Enforce synthetic target ownership and stable unsupported ownership/device errors without host chown in `internal/workspaceattach/provider.go` (FR-037)
- [X] T060 [US2] Package, verify, repair and uninstall every selected transport helper/driver in `internal/helperbin/helperbin.go`, `internal/packagekit/`, and `scripts/package-local.sh` (FR-031, FR-033)
- [X] T061 [US2] Add real correctness, watcher and fixed performance lanes in `scripts/lib/gate2-shared-workspace-performance.sh`, and rerun `scripts/test-hostfs-write-overlay-smoke.sh` to prove staged HostFS behavior is unchanged (SC-003, SC-004, SC-005, SC-011)

**Checkpoint**: The selected project behaves as a live development filesystem,
not an overlay or sync surface.

---

## Phase 6: User Story 3 - Confine Concurrent Views (P1)

**Goal**: Exact-root authority and truthful same/nested/disjoint semantics hold
for concurrent ordinary non-root sessions.

**Independent Test**: Probe paths, mounts, protocol, `/proc`, symlinks and root
replacement from disjoint, same and nested fixtures.

- [X] T062 [P] [US3] Write same/nested/disjoint classification and asymmetric authority tests in `internal/workspaceattach/relation_test.go` (FR-007, FR-027; SC-002)
- [X] T063 [P] [US3] Write traversal, ancestor-symlink, rename, case/Unicode alias, reserved-root and root-replacement adversarial tests in `internal/workspaceattach/root_test.go` (FR-007; SC-004)
- [X] T064 [P] [US3] Write broad-mount, guessed opaque key, staging-root, guest control and sibling protocol probes in `internal/backend/lima/workspace_isolation_test.go` (FR-009, FR-010, FR-030; SC-002, SC-017)
- [X] T065 [US3] Implement canonical same/nested/disjoint relations and non-authoritative notices in `internal/workspaceattach/relation.go` (FR-025, FR-027)
- [X] T066 [US3] Enforce descriptor/root-handle-relative traversal and rename safety in `internal/workspaceattach/root.go` (FR-007)
- [X] T067 [US3] Remove candidate staging/control visibility before non-root target readiness in `internal/backend/lima/session_view.go` (FR-010, FR-030)
- [X] T068 [US3] Enforce per-session/global limits, fairness and teardown-reserved capacity in `internal/workspaceattach/limits.go` (FR-032; SC-021)
- [X] T069 [US3] Reference-count same-root concrete providers while retaining independent bindings/handles/locks in `internal/manager/run_workspace.go` (FR-011, FR-031)
- [X] T070 [US3] Add real disjoint/same/nested ordinary-target probes to `scripts/lib/gate2-shared-workspace.sh` (SC-002, SC-006)
- [X] T071 [US3] Add explicit shared-kernel, ancestor-root and guest-root non-claim assertions to docs-truth tests in `scripts/test-doc-truth.sh` (FR-027, FR-034)

**Checkpoint**: Supported non-root isolation claims are proved without claiming
separate VM walls.

---

## Phase 7: User Story 4 - Stop Only After Every Dependency Releases (P1)

**Goal**: Workspace cleanup composes with 036 and never stops a live sibling.

**Independent Test**: Release sessions/providers/bridge in varied order, attach
during grace/stop, and restart after ambiguous cleanup.

- [X] T072 [P] [US4] Write planned-before-effect, ready-barrier, topology, dependent cleanup and same-root release tests in `internal/manager/run_workspace_lifecycle_test.go` (FR-013, FR-014, FR-015)
- [X] T073 [P] [US4] Write grace cancellation, stop-in-flight and exact-incarnation race tests in `internal/lifecycle/workspace_attach_test.go` (FR-023, FR-024; SC-007, SC-008)
- [X] T074 [P] [US4] Write daemon restart/provider absent/unproved/no-readoption tests in `internal/daemon/workspace_reconcile_test.go` (FR-016; SC-009)
- [X] T075 [US4] Register and commit the selected provider/view/service subgraph before authority in `internal/manager/run_workspace.go` (FR-013, FR-014)
- [X] T076 [US4] Implement target-revoke, handle-flush, view-unmount, provider-release and proof-ordered cleanup in `internal/manager/run_workspace.go` (FR-011, FR-016)
- [X] T077 [US4] Add selected transport absence/unproved probes to lifecycle reconciliation in `internal/daemon/lifecycle.go` (FR-016)
- [X] T078 [US4] Route attach exclusively through existing 036 grace/stop serialization in `internal/lifecycle/registry.go` and `internal/manager/run_workspace.go` (FR-015, FR-023, FR-024)
- [X] T079 [US4] Preserve sibling network, HostFS, terminal, bridge and open-handle usability during one view release in `internal/manager/run_apply.go` (FR-011; SC-006)
- [X] T080 [US4] Extend lifecycle model/replay coverage with workspace provider/view transitions in `internal/lifecycle/modelcheck.go` (FR-013, FR-016)
- [X] T081 [US4] Add real sibling-detach, bridge-pin, final-grace, stop and restart recovery scenarios in `scripts/lib/gate2-shared-workspace.sh` (SC-006, SC-007, SC-008, SC-009)

**Checkpoint**: Final stop remains a 036 outcome and no attachment path owns an
alternate timer or stop command.

---

## Phase 8: User Story 5 - Understand Sharing And Recover Safely (P2)

**Goal**: Every product surface distinguishes one machine from multiple views
and provides truthful, redacted recovery.

**Independent Test**: Inspect CLI/JSON/browser/TUI/doctor/events/audit/export
with two views and injected host/control sentinels.

- [X] T082 [P] [US5] Write machine/view summary, profile-scope and no-last-workspace contract tests in `internal/manager/manager_workspace_test.go` (FR-025, FR-034; SC-016)
- [X] T083 [P] [US5] Write event/reducer schema and unknown-kind drift tests in `internal/liveconsole/workspace_catalog_test.go` (FR-025)
- [X] T084 [P] [US5] Write true host-root/key/token injection and local/export boundary tests in `internal/workspaceattach/redaction_test.go` (FR-018, FR-019; SC-012)
- [X] T085 [US5] Implement machine-scoped environment and attachment-scoped session summaries in `internal/manager/manager.go` and `internal/manager/api.go` (FR-025, FR-034)
- [X] T086 [US5] Publish redacted workspace lifecycle events/status through `internal/daemon/status.go`, `internal/daemon/server.go`, and `internal/liveconsole/catalog.go` (FR-018, FR-025)
- [X] T087 [US5] Update live-console reducers and panels to render one machine with multiple scoped views in `internal/liveconsole/reducer.go` and `internal/manager/server.go` (FR-025; SC-016)
- [X] T088 [US5] Add real browser action/state E2E for two workspace views in `scripts/test-ui-e2e.sh` (FR-025; SC-016)
- [X] T089 [US5] Add real PTY/TUI rendering and event-stream E2E for two workspace views in `test/e2e/sessionpty/` and `scripts/test-ui-e2e.sh` (FR-025; SC-016)
- [X] T090 [US5] Update CLI environment/session list, inspect and explain output with dedicated/separate-profile guidance in `internal/app/app.go` (FR-003, FR-025, FR-034; SC-018)
- [X] T091 [US5] Add doctor checks for transport, root identity, TCC principals, mode drift, external metadata and lifecycle blockers in `internal/doctor/report.go` (FR-033, FR-035; SC-019)
- [X] T092 [US5] Resolve broker and host-app workspace resources only through the immutable attachment in `internal/manager/run_dataplane.go`, `internal/hostcap/projection.go`, and `internal/manager/host_app_resource.go` (FR-017, FR-039; SC-010)

**Checkpoint**: Product state is usable and truthful without turning display
labels, history or lifecycle metadata into authority.

---

## Phase 9: Polish, Evidence And Promotion

**Purpose**: Close docs, release gates and adversarial verification.

- [X] T093 [P] Update design-only machine/attachment architecture and exact selected transport in `docs/architecture-principles.md` and `docs/privacy-run-design.md` without claiming promotion before T102 (FR-029, FR-034)
- [X] T094 [P] Stage gated claim/non-claim text in `docs/threat-model.md` and `docs/claim-boundaries.md` while retaining the current implemented claim until T102 (FR-007, FR-018, FR-027, FR-034)
- [X] T095 [P] Stage pending platform/mode/transport support and first-run escape hatches in `docs/support-matrix.md` and `docs/first-run-alpha.md` without marking shared mode supported before T102 (FR-003, FR-035, FR-040; SC-018, SC-022)
- [X] T096 [P] Update session-bound projection and machine/view UI contracts in `docs/host-capability-projection.md` and `docs/tui-webui-experience.md` (FR-017, FR-025)
- [X] T097 Update Gate 0, real Lima and performance evidence requirements in `docs/privacy-run-test-plan.md` (SC-005, SC-015)
- [X] T098 Add deterministic shared-workspace smoke and all local contract/race/schema/redaction checks to `scripts/test-gate0.sh` (SC-015)
- [X] T099 Implement real release-shaped macOS arm64 Lima evidence producer with clean helper/driver resolution in `scripts/test-shared-workspace-lima-e2e.sh` (SC-001-SC-013, SC-016-SC-023)
- [X] T100 Emit and validate stable 035 product-evidence manifests, raw-sample and artifact digests in `internal/productevidence/` and `dist/product-evidence/035/` (SC-015)
- [X] T101 Run `scripts/test-gate0.sh` and fix every failure without weakening assertions or changing accepted thresholds
- [X] T102 Run `scripts/test-shared-workspace-lima-e2e.sh` on the exact candidate and independently verify one instance/boot, evidence schema, artifact digests and honest dirty state
- [X] T103 Execute every scenario in `specs/035-shared-default-vm-cross-workspace/quickstart.md` and retain bounded evidence references
- [X] T104 Run `go build ./...`, `go vet ./...`, `gofmt -l internal cmd`, `git diff --check`, and `go test ./...`; resolve module metadata only with `go mod tidy`
- [X] T105 Run markdownlint across changed specs/docs and verify route, event, schema, recovery, lifecycle and proof registries have no drift
- [X] T106 Finalize gated claims/support docs, update `docs/STATUS.md`, and set `specs/035-shared-default-vm-cross-workspace/spec.md` to Implemented only after clean Gate 0 and real Lima evidence prove all 41 FR and 23 SC

---

## Dependencies And Execution Order

### Phase Dependencies

- Setup T001-T005 has no product behavior and starts immediately.
- Phase R T006-T022 depends on Setup and blocks all later work.
- Foundational T023-T038 requires an accepted T022 artifact and the losing
  candidate removed.
- US1 T039-T050 requires Foundational and enables shared selection only at its
  checkpoint.
- US2 T051-T061 and US3 T062-T071 both require the US1 attachment path; their
  tests and isolated components may proceed in parallel.
- US4 T072-T081 requires the selected provider from US2 and attachment topology
  from US1.
- US5 T082-T092 requires stable machine/view models; UI test work may proceed in
  parallel after schemas settle.
- Promotion T093-T106 requires every desired story; claims/status remain old
  until T102 passes cleanly.

### User Story Dependencies

- **US1**: Stable shared slot and attach skeleton; first product increment.
- **US2**: Depends on US1 attachment, independently proves live filesystem.
- **US3**: Depends on US1 attachment, independently proves exact-root boundary.
- **US4**: Depends on US1 plus selected provider, independently proves release
  and stop ordering.
- **US5**: Depends on machine/view model, independently proves operator truth,
  recovery and redaction.

### Parallel Opportunities

- T002-T004 can run in parallel.
- VZ T008-T011, Portal T012-T015 and path identity T016-T018 can run in parallel
  after T006/T007, but T019-T022 consume all results serially.
- Foundational tests T023/T025/T026/T029/T031/T032 are separate files and can be
  written in parallel before implementations.
- US2 filesystem/path tests and US3 relation/root tests can run in parallel.
- US5 Manager, event and redaction tests can run in parallel.
- Documentation T093-T096 can run in parallel only after behavior is stable;
  `STATUS.md` remains serial and last.

## Implementation Strategy

1. Complete Setup and Phase R only.
2. Stop and inspect the binary decision. Do not continue on `rejected`.
3. On `accepted`, land the clean foundational model without enabling shared
   selection.
4. Complete US1 and prove one machine/two attachments locally.
5. Complete US2 and US3; do not promote filesystem or isolation from one alone.
6. Complete US4 and prove 036 remains the sole stop authority.
7. Complete US5 and runtime UI/redaction evidence.
8. Run clean installed-package real Lima promotion, then update claims/status.

## Format Validation

All tasks use `- [ ] TNNN [P?] [US?] description with file path`. IDs are
continuous T001-T106. Setup, Phase R, Foundational and Polish tasks have no story
label; user-story tasks use the matching `[US1]` through `[US5]` label.

---

## Phase 10: Convergence - Workspace Path Identity And Release Evidence

**Purpose**: Close the post-implementation path-identity gaps discovered while
reviewing the logical `/workspace` alias against the opaque per-attachment
physical root. This phase is additive: it does not weaken the accepted Portal
or shared-machine design and keeps performance measurement separate.

- [X] T107 [US1] Make path-identity planning accept only the production `wrk_` plus 64-hex workspace identity, replace short synthetic IDs in `internal/workspaceattach/path_identity_probe.go`, `internal/workspaceattach/path_identity_probe_test.go`, `internal/backend/lima/workspace_path_test.go`, and `scripts/test-workspace-path-identity-lima.sh`, and prove the old length guard fails against a real identity (FR-005, FR-041; SC-023)
- [X] T108 [US2] Add a fail-closed structured guest-path alias resolver and tests proving `/workspace` and the exact attachment physical root select the same relative object while traversal, malformed IDs, host paths, and guessed sibling roots are rejected in `internal/workspaceattach/` (FR-004, FR-007, FR-039; SC-002, SC-003, SC-004)
- [X] T109 [US2] Extend actual-session tests with logical/physical dev+inode equality, bidirectional create/read/write/rename/chmod/fsync/delete, nested `cd`, `pwd -L`, `pwd -P`, `realpath`, subprocess inheritance, same-root reuse, and different-root denial in `internal/backend/lima/` and `scripts/lib/gate2-shared-workspace.sh` (FR-004, FR-007, FR-041; SC-002, SC-003, SC-004, SC-023)
- [X] T110 [US5] Bind audit normalization and broker/Host App path projection to the immutable attachment identity so exact logical and physical aliases produce one structured workspace-relative path without generic string replacement; add positive, sibling-denial, malformed, and mutation-proof tests in `cmd/hideout-observer/`, `internal/broker/`, `internal/manager/`, and `internal/workspaceattach/` (FR-017, FR-019, FR-039; SC-010, SC-023)
- [X] T111 [US2] Replace the shared `getcwd` pseudo-agent fixture with a truthful Bash, Git, Node, Python, Go, Claude, and Codex behavior matrix: require distinct trust/history/cache/socket keys for stateful physical-cwd consumers, stable keys for the same root, and explicitly classify Go's logical `$PWD` module path without fabricating a distinct project-state claim in `scripts/test-workspace-path-identity-lima.sh` and the real product gate (FR-041; SC-023)
- [X] T112 Split stale path-correctness assertions out of `scripts/lib/gate2-shared-workspace-product-performance.sh`, run them through the installed candidate's actual session view in a resumable non-performance stage, and retain a negative fixture proving the judge rejects a missing alias, sibling exposure, or divergent inode (SC-003, SC-004, SC-015, SC-023)
- [X] T113 Wire the independent non-performance workspace-path stage into `scripts/test-shared-workspace-lima-e2e.sh`, its release-evidence manifest, and `docs/privacy-run-test-plan.md` without making successful performance measurement a prerequisite for retaining correctness evidence (SC-015)
- [X] T114 Run focused Go tests, Linux cross-vet, script lint/checks, the non-performance real Lima workspace-path stage, and adversarial mutation/negative-fixture checks; record fresh-eyes evidence and update `docs/STATUS.md`, `docs/DEBT.md`, and the 035 quickstart only from retained artifacts (SC-015, SC-023)

**Checkpoint**: A release candidate may claim workspace path identity only when
the installed product proves logical/physical file equivalence, project-state
separation, sibling denial, and attachment-bound audit/projection behavior. The
performance gate remains an independent promotion input.
