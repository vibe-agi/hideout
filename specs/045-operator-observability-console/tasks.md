# Tasks: Operator Observability Console

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from
`/specs/045-operator-observability-console/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`,
`contracts/`, and `quickstart.md`

**Tests**: Required. This feature changes profile, secret, network, guest
privilege, lifecycle, evidence, local API, TUI, WebUI, and release authority.
Authority tests are written first and must demonstrate failure before guarded
implementation is accepted.

**Organization**: Tasks are grouped by user story. Shared contracts and
authority primitives are foundational; each story retains an independent
acceptance path.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Pin reproducible dependencies, artifact generation, schemas, and
gate locations before changing behavior.

- [x] T001 Add pinned Bubble Tea v2, Bubbles v2, Lip Gloss v2, and cilium/ebpf dependencies and module checksums in `go.mod` and `go.sum`
- [x] T002 [P] Add dependency provenance and license inventory entries for the new Go and embedded BPF artifacts in `THIRD_PARTY_NOTICES.md` and `docs/dependencies.md`
- [x] T003 [P] Add JSON schemas for profile projections, operations, activity records, coverage intervals, observer frames, operator snapshots, and daemon event v2 in `schemas/`
- [x] T004 Add reproducible CO-RE compile/embed generation commands, pinned tool versions, and stale-output checks in `internal/workloadobs/collector/bpf/generate.go` and `scripts/gates/generated.sh`
- [x] T005 [P] Add deterministic workload, DNS, HTTP, SOCKS, credential-canary, PID-reuse, and event-loss fixture definitions in `internal/workloadobs/testdata/`
- [x] T006 [P] Add TLC configuration placeholders and shared constants for configuration, secret, and observation models in `formal/cfg/`
- [x] T007 Record the exact Gate 0, model, privacy, network, terminal, browser, lifecycle, real-Lima, packaging, mutation, and performance commands in `specs/045-operator-observability-console/gate-matrix.md`
- [x] T008 Add CI checks that reject stale generated BPF/schema artifacts and missing dependency licenses in `.github/workflows/ci.yml` and `scripts/gates/`

---

## Phase 2: Foundational Authority and Projection

**Purpose**: Establish shared identities, CAS/idempotency, redaction, coverage,
and event contracts that every client and observer path depends on.

**Critical**: No story may claim completion before these tasks pass.

### Failing tests and formal assertions

- [x] T009 [P] Add canonical profile digest, revision migration, out-of-band edit, and stale-CAS failing tests in `internal/manager/profile_projection_test.go`
- [x] T010 [P] Add operation-ID mismatch, response-loss replay, torn-record recovery, and duplicate-effect failing tests in `internal/manager/operation_store_test.go`
- [x] T011 [P] Add exact activity-owner, execution-identity, coverage-interval, and cross-owner rejection failing tests in `internal/workloadobs/types/model_test.go`
- [x] T012 [P] Add pre-persistence canary, URI userinfo, split-flag, query/auth field, token, truncation, and redaction-failure tests in `internal/workloadobs/redact/redact_test.go`
- [x] T013 [P] Add daemon instance/sequence gap, unknown optional event, terminal event, and stale read-only reducer tests in `internal/liveconsole/reducer_v2_test.go`
- [x] T014 [P] Add initial TLA+ invariants for stale plans, operation uniqueness, owner isolation, no false Available coverage, and exact cleanup in `formal/OperatorConfiguration.tla` and `formal/WorkloadObservation.tla`

### Shared implementation

- [x] T015 Implement strict versioned canonical JSON hashing and domain separators for plans and projections in `internal/manager/canonical.go`
- [x] T016 Implement `ProfileProjection`, atomic revision sidecar migration, digest drift detection, and store locking in `internal/manager/profile_projection.go` and `internal/profile/store.go`
- [x] T017 Implement bounded durable `Operation` types, phase validation, atomic persistence, lookup, pruning, and idempotency binding in `internal/manager/operation.go` and `internal/manager/operation_store.go`
- [x] T018 Implement the closed typed-change registry, draft validation, canonical diff/effect/blocker models, and plan digest in `internal/manager/profile_transaction_types.go`
- [x] T019 [P] Define the no-read public secret store interface, reference metadata, generation, and typed unavailable provider in `internal/secrets/store.go` and `internal/secrets/unsupported.go`
- [x] T020 [P] Implement exact reusable/disposable activity owners, workload boundaries, execution IDs, activity records, subjects, and validators in `internal/workloadobs/types/`
- [x] T021 Implement deterministic activity redaction with bounded values and fail-closed results in `internal/workloadobs/redact/`
- [x] T022 Implement append-only subsystem coverage intervals, reason registry, loss transitions, and query helpers in `internal/workloadobs/coverage/`
- [x] T023 Extend live-console seed/state/events with daemon instance, credential generation, operations, transitions, activity, coverage, risk, and capability projections in `internal/liveconsole/model.go`
- [x] T024 Upgrade the live-console reducer to event v2 instance/sequence semantics while preserving v1 read-only compatibility in `internal/liveconsole/reducer.go`
- [x] T025 Add optional stable API error details, new route inventory patterns, strict request limits, and sensitive-route no-store/no-body-log metadata in `internal/manager/api.go` and `internal/manager/routes.go`
- [x] T026 Complete the initial TLC configurations and Go schema/refinement fixtures for the foundational invariants in `formal/cfg/` and `internal/manager/formal_refinement_test.go`

**Checkpoint**: Profile revisions and operation IDs are durable; no secret or
activity authority bypasses Manager; stale/gapped clients can be read-only.

---

## Phase 3: User Story 1 - Understand a Live Run at a Glance (P1, MVP)

**Goal**: Replace the ANSI metrics dump with a readable live HUD that shows the
active workload, connection, coverage, risk/blocker, activity, and next action.

**Independent Test**: Start healthy, idle, failed, and concurrent-session
fixtures; use `hideout tui` and `--once` to identify all primary facts on one
screen, select a session, drill into details, and observe honest stale mode.

### US1 tests

- [x] T027 [P] [US1] Add operator-snapshot route contract tests for healthy, idle, concurrent, blocked, and scoped sessions in `internal/manager/operator_snapshot_api_test.go`
- [x] T028 [P] [US1] Add Bubble Tea model tests for resize, focus, session selection, detail drill-down, event burst, and stale mutation disablement in `internal/tui/model_test.go`
- [x] T029 [P] [US1] Add plain/Unicode/no-color/narrow/idle/error golden render tests in `internal/tui/render/golden_test.go` and `internal/tui/testdata/`
- [x] T030 [P] [US1] Add PTY tests for alternate-screen entry/exit, signal/panic restoration, keyboard navigation, non-TTY recovery, and `--once` in `internal/app/tui_pty_test.go`

### US1 implementation

- [x] T031 [US1] Implement one authoritative `OperatorSnapshot` builder with profile/session/activity scoping and next-action references in `internal/manager/operator_snapshot.go`
- [x] T032 [US1] Serve `GET /api/v1/operator/snapshot` and register parity metadata in `internal/manager/api.go` and `internal/manager/routes.go`
- [x] T033 [US1] Publish bounded profile/session/activity/coverage/risk/operation event-v2 deltas with daemon instance identity in `internal/daemon/events.go`
- [x] T034 [US1] Implement the Bubble Tea application model, typed messages, subscriptions, resize handling, frame coalescing, and modal stack in `internal/tui/model.go`
- [x] T035 [P] [US1] Implement accessible header, status bar, tabs, table, viewport, filter, empty-state, and detail primitives in `internal/tui/components/`
- [x] T036 [US1] Implement the one-screen Overview layout and progressive diagnostic detail in `internal/tui/render/overview.go`
- [x] T037 [US1] Implement recent activity, coverage, risk, blocker, and operation preview panels in `internal/tui/render/activity.go`
- [x] T038 [US1] Implement concurrent-session selector scoping that clears all prior-session rows before applying the new snapshot in `internal/tui/components/session_selector.go`
- [x] T039 [US1] Implement LIVE/IDLE LIVE/STALE/DISCONNECTED/CREDENTIAL EXPIRED/DAEMONLESS headers and read-only recovery actions in `internal/tui/render/health.go`
- [x] T040 [US1] Replace `app.tui` ANSI refresh with Bubble Tea interactive mode while preserving deterministic `--once` in `internal/app/app.go` and `internal/app/tui.go`

**Checkpoint**: US1 works without workload deep-history implementation by
rendering current supported evidence and explicit unavailable capabilities.

---

## Phase 4: User Story 2 - Investigate What a Workload Did (P1)

**Goal**: Attribute every supported descendant execution and its file,
connection, and DNS metadata to the correct session with explainable risks and
honest uncertainty.

**Independent Test**: Run the deterministic concurrent workload with rapid
children, re-parenting, PID reuse, file mutations, DNS, proxy/direct
connections, and unrelated guest noise; verify zero cross-attribution and all
expected evidence or explicit reduced coverage.

### US2 tests

- [x] T041 [P] [US2] Add cgroup creation, atomic target placement, descendant inheritance, escape attempt, PID reuse, and exact cleanup tests in `cmd/hideout-session-supervisor/cgroup_linux_test.go`
- [x] T042 [P] [US2] Add observer handshake, identity binding, CRC/schema/bounds, duplicate/gap/restart, backpressure, and target-auth refusal tests in `internal/sessionwire/observer_test.go`
- [x] T043 [P] [US2] Add fork/exec/exit/argv/cwd/parent/fast-exit/refork attribution fixtures in `internal/workloadobs/collector/process_test.go`
- [x] T044 [P] [US2] Add open/read/write/mmap/create/truncate/rename/unlink/metadata/hardlink/symlink/path-race aggregation fixtures in `internal/workloadobs/collector/file_test.go`
- [x] T045 [P] [US2] Add connect4/connect6/UDP/TCP/DNS/cache/literal-IP/shared-IP/encrypted-DNS/proxy mediator correlation fixtures in `internal/workloadobs/collector/network_test.go`
- [x] T046 [P] [US2] Add aggregation isolation, destructive-event preservation, deterministic risk rule, confidence, and policy-separation tests in `internal/workloadobs/aggregate/aggregate_test.go` and `internal/workloadobs/risk/risk_test.go`
- [x] T047 [P] [US2] Add exact-owner cursor/filter/pagination/execution-tree and pruned-gap API contract tests in `internal/manager/activity_api_test.go`
- [x] T048 [US2] Add a real Lima two-session/PID-reuse/unrelated-noise observation gate that initially fails in `scripts/gates/workload-observation-lima.sh`

### US2 implementation

- [x] T049 [US2] Implement Hideout-owned non-delegated cgroup-v2 leaf creation, FD validation, empty/removal proof, and capability probe in `cmd/hideout-session-supervisor/cgroup_linux.go`
- [x] T050 [US2] Atomically place the non-root target in its session leaf with `UseCgroupFD/CgroupFD`, keeping supervisor and observer outside it in `cmd/hideout-session-supervisor/process_linux.go`
- [x] T051 [US2] Extend supervisor start/ready/completion messages with exact activity owner, cgroup identity, observer generation, and coverage summary in `internal/sessionwire/control.go`
- [x] T052 [US2] Implement fixed `hideout-observer` startup, capability probing, session registration, heartbeat, and shutdown in `cmd/hideout-observer/`
- [x] T053 [US2] Add embedded CO-RE program sources, generated objects, helper manifest entries, and digest verification in `internal/workloadobs/collector/bpf/` and `internal/helperbin/`
- [x] T054 [US2] Implement process fork/exec/exit kernel programs and userland execution-tree normalization in `internal/workloadobs/collector/process/`
- [x] T055 [US2] Implement supported file tracing/LSM hooks, authoritative file identity, path reconstruction, mmap, destructive operations, and byte counters in `internal/workloadobs/collector/file/`
- [x] T056 [US2] Implement fanotify file fallback with explicit merged/overflow/mmap limitations and Partial coverage in `internal/workloadobs/collector/fanotify/`
- [x] T057 [US2] Implement cgroup socket connect/sendmsg and skb socket-cookie correlation for process-to-IP/port/route evidence in `internal/workloadobs/collector/network/`
- [x] T058 [US2] Implement bounded DNS/proxy metadata parsing, TTL-aware same-execution correlation, and exact/inferred/unknown reasons in `internal/workloadobs/collector/dns/`
- [x] T059 [US2] Implement the authenticated bounded guest observer transport separate from PTY control in `internal/sessionwire/observer.go` and `internal/backend/lima/observer_stream.go`
- [x] T060 [US2] Register the boundary before target-ready, ingest and validate observations, account for all loss, and close coverage on observer/session exit in `internal/daemon/activity_service.go` and `internal/daemon/sessions.go`
- [x] T061 [US2] Implement per-execution/process/file/network/DNS aggregation with documented windows and no cross-coverage merging in `internal/workloadobs/aggregate/`
- [x] T062 [US2] Implement versioned explainable risk rules, evidence references, severity/confidence, and allowed-vs-violation separation in `internal/workloadobs/risk/`
- [x] T063 [US2] Implement exact-owner activity query, execution tree, filter, cursor, and summary services in `internal/workloadobs/query/` and `internal/manager/activity_service.go`
- [x] T064 [US2] Serve activity summary/events/executions/coverage/risks routes with strict owner resolution in `internal/manager/api.go` and `internal/manager/routes.go`
- [x] T065 [US2] Connect Activity tabs, filters, correlated detail, and risk evidence to Manager queries in `internal/tui/render/activity.go` and `internal/tui/model.go`
- [x] T066 [US2] Add scriptable `hideout activity summary|events|executions|coverage|risks` commands and JSON/human parity in `internal/app/activity.go` and `internal/app/app.go`

**Checkpoint**: The supported Lima reference workload proves correct
attribution; every unsupported or lost interval is visibly reduced.

---

## Phase 5: User Story 3 - Change Configuration Safely (P1)

**Goal**: Give CLI and TUI one reviewable CAS/idempotent configuration and
secret flow, including online proxy/DNS transition without daemon restart.

**Independent Test**: Edit network, secret, profile environment, and lifecycle
settings while another client races; inject validation/activation/response
loss and verify stale rejection, no duplicate effect, rollback, and read-only
stale UI.

### US3 tests and models

- [x] T067 [P] [US3] Complete TLA+ stale-client/CAS/operation-response-loss and secret stage/activate/rollback safety/liveness scenarios in `formal/OperatorConfiguration.tla`, `formal/SecretTransition.tla`, and `formal/cfg/`
- [x] T068 [P] [US3] Add concurrent client, stale plan, conflicting key, operation retry, crash boundary, and immutable-session-snapshot tests in `internal/manager/profile_transaction_test.go`
- [x] T069 [P] [US3] Add in-memory and real macOS Keychain set/rotate/delete/locked/missing/crash-reconcile tests with canaries in `internal/secrets/keychain_test.go` and `scripts/gates/keychain-real.sh`
- [x] T070 [P] [US3] Add direct/proxy/DNS stage/probe/activate/drain/rollback/existing-connection/blocked-session tests in `internal/manager/network_transition_test.go`
- [x] T071 [P] [US3] Add old typed-route adapter and new transaction/secret API plan/apply parity tests in `internal/manager/profile_transaction_api_test.go`
- [x] T072 [P] [US3] Add TUI edit/cancel/review/confirm/apply/stale/secret-mask/response-loss PTY tests in `internal/tui/modal/config_test.go` and `internal/app/tui_config_pty_test.go`

### US3 implementation

- [x] T073 [US3] Implement plan/apply CAS, canonical replan-under-lock, commutative mutation keys, and projection commit in `internal/manager/profile_transaction.go`
- [x] T074 [US3] Implement provider effect ownership, durable phase checkpoints, idempotent result replay, and terminal event publication in `internal/manager/operation_service.go`
- [x] T075 [US3] Implement Security.framework generic-password envelopes, generation/operation recovery, memory clearing, and typed Keychain errors in `internal/secrets/keychain_darwin.go`
- [x] T076 [US3] Implement daemon-owned secret metadata/plan/apply/delete/resolve services without a public read-value path in `internal/manager/secret_service.go` and `internal/daemon/secret_service.go`
- [x] T077 [US3] Serve strict secret list/plan/apply no-store routes and zero sensitive buffers on every exit in `internal/manager/api.go` and `internal/manager/routes.go`
- [x] T078 [US3] Add `hideout secret set|rotate|delete|status|list` with TTY/stdin input, review, confirmation, and no argv value in `internal/app/secret.go`
- [x] T079 [US3] Retain startup environment secret resolution as one-release read-only fallback with deprecation reason and safe recovery command in `internal/network/network.go` and `internal/app/help.go`
- [x] T080 [US3] Implement network candidate stage, non-secret validation, atomic new-connection activation, proof, rollback, and exact blockers in `internal/manager/network_transition.go`
- [x] T081 [US3] Bind accepted connections to route/secret generation and preserve them across live pointer changes in `internal/network/gateway.go`
- [x] T082 [P] [US3] Implement strict typed-change handlers for network, profile env, HostFS, command proxy/adapter, and activity retention in `internal/manager/profile_changes/`
- [x] T083 [US3] Adapt existing profile-specific plan/apply routes and CLI commands to the shared transaction service without breaking output compatibility in `internal/manager/api.go` and `internal/app/app.go`
- [x] T084 [US3] Implement Config view desired/effective/transition/scope fields and capability-driven editors in `internal/tui/render/config.go`
- [x] T085 [US3] Implement draft/plan/review/confirm/apply/terminal modal state machine, high-risk typed confirmation, and secret input clearing in `internal/tui/modal/config.go`
- [x] T086 [US3] Route environment stop/clean controls through their existing typed plan/apply authority and show active blockers in `internal/tui/modal/lifecycle.go`
- [x] T087 [US3] Implement Operations view with durable ID lookup, phase/effects/evidence/result/recovery, and response-loss resume in `internal/tui/render/operations.go`
- [x] T088 [US3] Add Go refinement traces matching configuration and secret models and execute all new TLC configurations in `internal/manager/profile_transaction_refinement_test.go` and `scripts/gates/formal.sh`

**Checkpoint**: The reported proxy-secret failure is fixed through
`hideout secret set`; daemon and VM stay running for healthy eligible changes.

---

## Phase 6: User Story 4 - Find the Supported Operation Quickly (P1)

**Goal**: Make first-run and recovery tasks discoverable with progressive,
task-oriented help while retaining a complete advanced inventory.

**Independent Test**: With no external docs, find setup, first run, secret,
proxy, TUI/WebUI, readiness, activity, stop/clean, update/uninstall, and support
paths using at most two help invocations per task.

### US4 tests

- [x] T089 [P] [US4] Add catalog uniqueness, handler/route/flag/help coverage, stability/audience, alias, and stale-entry tests in `internal/app/command_catalog_test.go`
- [x] T090 [P] [US4] Add primary/contextual/all/terminal-width/no-color golden help and two-invocation journey tests in `internal/app/help_golden_test.go` and `internal/app/testdata/help/`
- [x] T091 [P] [US4] Add missing-secret, desired-vs-effective, pending-next-attach, stale-client, unsupported-capability, and unknown-command recovery tests in `internal/app/error_guidance_test.go`

### US4 implementation

- [x] T092 [US4] Implement one declarative command/action catalog with task group, purpose, syntax, flags, examples, prerequisites, effects, safety, recovery, audience, and stability in `internal/app/command_catalog.go`
- [x] T093 [US4] Replace the top-level switch inventory with catalog dispatch adapters while preserving existing command parsers and exit behavior in `internal/app/app.go`
- [x] T094 [US4] Render concise primary help as a supported operator journey with common copyable tasks in `internal/app/help.go`
- [x] T095 [US4] Render contextual command help with purpose, syntax, examples, prerequisites, effect scope, safety, recovery, and next commands from the catalog in `internal/app/help.go`
- [x] T096 [US4] Render grouped searchable `hideout help all`, retain `help --all` as a named compatibility alias, and separate stable/advanced/lab commands in `internal/app/help.go`
- [x] T097 [US4] Map typed Manager and parser errors to stable user guidance and safe catalog-backed next commands in `internal/app/guidance.go`
- [x] T098 [US4] Render catalog-backed contextual Help and key overlays in the TUI without duplicating strings in `internal/tui/render/help.go`
- [x] T099 [US4] Add catalog-backed help/search to the local browser console and link each action to canonical CLI syntax in `internal/daemon/uiweb.go`
- [x] T100 [US4] Add English and Simplified Chinese operator journey, proxy-secret migration, update/uninstall, and troubleshooting pages in `docs/user-guide.md` and `docs/user-guide.zh-CN.md`

**Checkpoint**: Help explains that exporting a secret after daemon start cannot
modify the daemon environment and directs users to the live secret workflow.

---

## Phase 7: User Story 5 - Trust Coverage, Privacy, and Retention (P1)

**Goal**: Make every evidence gap explicit, retain bounded host-private
activity under the exact lifecycle owner, delete it safely, and redact known
credentials before persistence.

**Independent Test**: Exercise supported/degraded/loss/restart/quota/recreate/
export cases with unique canaries; verify coverage transitions, 0600 ownership,
exact deletion, local path visibility, and zero known-secret matches.

### US5 tests and models

- [x] T101 [P] [US5] Add exhaustive persisted/index/render/export/log canary scan tests for managed values, encodings, URI userinfo, auth fields, sensitive flags/query, and control tokens in `internal/workloadobs/redact/canary_test.go`
- [x] T102 [P] [US5] Add framed segment torn-write, CRC, seal/hash, corrupt quarantine, index rebuild, quota overshoot, prune-order, and coverage-gap tests in `internal/workloadobs/store/store_test.go`
- [x] T103 [P] [US5] Add reusable incarnation clean/delete/recreate and disposable cleanup exact-owner/no-neighbor-deletion tests in `internal/manager/activity_cleanup_test.go`
- [x] T104 [P] [US5] Add file/directory mode, symlink, traversal, cursor cross-owner, target user, and unauthenticated API access tests in `internal/workloadobs/store/security_test.go`
- [x] T105 [P] [US5] Add hook loss, ring overflow, observer/daemon restart, schema mismatch, path/actor ambiguity, encrypted DNS, and retention interval tests in `internal/workloadobs/coverage/coverage_test.go`
- [x] T106 [P] [US5] Add local-path visibility and reviewed export path/redaction/no-publication contract tests in `internal/manager/activity_export_test.go`
- [x] T107 [US5] Complete observation-loss/retention/exact-cleanup safety and weak-fair progress properties in `formal/WorkloadObservation.tla` and `formal/cfg/WorkloadObservation.cfg`

### US5 implementation

- [x] T108 [US5] Implement 0600 CRC-framed active segments, atomic sealed manifests/indexes, crash repair, and corrupt quarantine in `internal/workloadobs/store/`
- [x] T109 [US5] Implement bounded path/domain/IP/execution/time indexes and owner-bound opaque cursors in `internal/workloadobs/store/index.go`
- [x] T110 [US5] Implement 8 MiB active segment, measured/tunable per-owner and global quotas, oldest-sealed pruning, and explicit retention coverage gaps in `internal/workloadobs/store/retention.go`
- [x] T111 [US5] Enforce host-private directory/file modes, no-follow opens, exact-root validation, and target-unreadable placement in `internal/workloadobs/store/security.go`
- [x] T112 [US5] Register activity cleanup as a typed reusable environment lifecycle effect with exact absence proof in `internal/daemon/lifecycle.go` and `internal/manager/activity_cleanup.go`
- [x] T113 [US5] Delete disposable owner activity during terminal session cleanup and preserve custom legacy audit destinations in `internal/session/session.go` and `internal/daemon/sessions.go`
- [x] T114 [US5] Surface current/historical coverage intervals, drop counts, retained range, quota, and corruption/prune reasons in Manager/TUI in `internal/manager/operator_snapshot.go` and `internal/tui/render/coverage.go`
- [x] T115 [US5] Feed managed secret/control-token generations into immutable redaction snapshots, handle supported encodings/split fields, and degrade on failure in `internal/workloadobs/redact/snapshot.go`
- [x] T116 [US5] Integrate activity with existing reviewed evidence export plan/apply and stricter host-path policy in `internal/manager/activity_export.go`
- [x] T117 [US5] Add operator-visible retention settings and typed transaction changes in `internal/manager/profile_changes/activity_retention.go` and `internal/tui/render/config.go`
- [x] T118 [US5] Add privacy limitation, local path behavior, coverage non-claims, quota, and exact lifecycle ownership to doctor/support/boundary summaries in `internal/manager/doctor.go`, `internal/app/support.go`, and `docs/privacy-run-design.md`
- [x] T119 [US5] Run the real Lima loss/quota/cleanup/redaction gate and Go refinement traces, recording exact evidence in `scripts/gates/workload-privacy-lima.sh` and `.artifacts/045/`

**Checkpoint**: No affected interval can remain Available after known loss, and
clean/delete/recreate proves only the exact old owner is absent.

---

## Phase 8: User Story 7 - Recover Without False Success (P1)

**Goal**: Reconcile accepted configuration/lifecycle/decision work after
crashes or disconnects without duplicate destructive effects or green unknown
state.

**Independent Test**: Crash at every provider/evidence/publication boundary,
restart, retry the same operation ID, and verify terminal/resumable/rollback/
unproved state plus bounded claim release.

### US7 tests and models

- [x] T120 [P] [US7] Add table-driven crash points before/after claim, persist, stage, activate, proof, commit, event, and response in `internal/manager/operation_recovery_test.go`
- [x] T121 [P] [US7] Add stop/clean/delete duplicate-provider-call and stable terminal evidence tests in `internal/daemon/lifecycle_operation_test.go`
- [x] T122 [P] [US7] Add decision dialog disconnect, bounded lease expiry/release, takeover, and stale claimant tests in `internal/manager/decision_lease_test.go`
- [x] T123 [P] [US7] Add attach/reconcile/config/stop/cleanup conflict-owner visibility and no-damage tests in `internal/lifecycle/conflict_test.go`
- [x] T124 [P] [US7] Add weak-fair liveness, disconnect, crash, retry, rollback, claim release, and cleanup progress checks in `formal/OperatorConfiguration.tla`, `formal/RequestWorkflow.tla`, and their cfg files

### US7 implementation

- [x] T125 [US7] Implement daemon-start operation ledger scan and provider-specific reconcile dispatch in `internal/daemon/operation_recovery.go`
- [x] T126 [US7] Reconcile Keychain/network effects by generation, route binding, probe evidence, and operation envelope rather than blind replay in `internal/manager/network_transition_recovery.go`
- [x] T127 [US7] Reconcile stop/clean/delete intent with repeated stable backend and exact metadata/activity absence evidence in `internal/daemon/lifecycle.go`
- [x] T128 [US7] Require terminal evidence predicates before committing succeeded/stopped/cleaned/deleted and persist `unproved` recovery otherwise in `internal/manager/operation_service.go`
- [x] T129 [US7] Implement visible bounded decision leases, disconnect release, expiry events, and authenticated takeover rules in `internal/manager/decision_service.go`
- [x] T130 [US7] Unify attach/reconcile/config/lifecycle mutation keys and return typed blocker owner/phase/recovery in `internal/lifecycle/coordinator.go`
- [x] T131 [US7] Re-seed daemon events and client projections after recovery without re-adopting unproved live resources in `internal/daemon/daemon.go` and `internal/liveconsole/seed.go`
- [x] T132 [US7] Render stopping/rolling-back/recovery-required/unproved states and existing-operation retry actions in TUI/CLI in `internal/tui/render/operations.go` and `internal/app/guidance.go`
- [x] T133 [US7] Add Go refinement traces and a crash-matrix gate with mutation proofs in `internal/manager/operation_recovery_refinement_test.go` and `scripts/gates/recovery.sh`
- [x] T134 [US7] Document failure boundaries, idempotent retry, leases, blockers, and manual recovery in `docs/recovery.md` and `docs/threat-model.md`

**Checkpoint**: A request, backend return, single observation, or disconnected
green screen is never sufficient terminal proof.

---

## Phase 9: User Story 6 - Investigate Deep History in the Browser (P2)

**Goal**: Provide richer local history/filter/correlation and the same
transaction authority in the browser without a second domain model.

**Independent Test**: Generate overlapping sessions and operations, filter on
every supported dimension, compare facts with CLI/TUI, apply one transaction,
then expire credentials and inject an event gap.

### US6 tests

- [x] T135 [P] [US6] Add compound owner/session/process/time/path/domain/IP/risk/coverage query and cursor tests in `internal/manager/activity_browser_api_test.go`
- [x] T136 [P] [US6] Add snapshot/event/detail parity fixtures shared by CLI, TUI, and browser in `internal/liveconsole/parity_test.go`
- [x] T137 [P] [US6] Add browser draft/review/confirm/apply/stale-plan/idempotent-response-loss/rollback tests in `internal/daemon/uiweb_transaction_test.go`
- [x] T138 [P] [US6] Add SSE gap, slow subscriber, credential expiry/rotation, instance restart, re-seed, and read-only tests in `internal/daemon/uiweb_events_test.go`

### US6 implementation

- [x] T139 [US6] Split the embedded browser console into typed static assets without adding authority outside Go Manager in `internal/daemon/uiweb.go` and `internal/daemon/uiweb_assets/`
- [x] T140 [US6] Implement browser overview, session timeline, execution tree, file/network/DNS activity, coverage, risk, and operation panels in `internal/daemon/uiweb_assets/`
- [x] T141 [US6] Implement bounded compound filters, owner-bound cursors, retained-range gaps, and detail correlation in `internal/daemon/uiweb_assets/activity.js`
- [x] T142 [US6] Implement desired/effective/transition configuration forms and canonical review/confirm/terminal operation dialogs in `internal/daemon/uiweb_assets/config.js`
- [x] T143 [US6] Implement one event-v2 reducer and snapshot re-seed path matching `liveconsole` semantics in `internal/daemon/uiweb_assets/state.js`
- [x] T144 [US6] Disable mutation on stale/disconnected/expired state, refresh credentials safely, and avoid polling while SSE is healthy in `internal/daemon/uiweb_assets/client.js`
- [x] T145 [US6] Add keyboard navigation, accessible state text, responsive history layout, control-string escaping, and bounded DOM updates in `internal/daemon/uiweb_assets/style.css` and `internal/daemon/uiweb_assets/app.js`
- [x] T146 [US6] Execute browser parity, auth, accessibility, stale, and performance journeys and record evidence in `scripts/gates/browser-console.sh` and `.artifacts/045/`

**Checkpoint**: Browser depth differs from TUI presentation, but all facts,
plans, operation identities, coverage, and success semantics are identical.

---

## Phase 10: User Story 8 - Produce One Verified Release Candidate (P1)

**Goal**: Bind all safety, UX, privacy, recovery, packaging, and performance
claims to one exact clean local candidate without publishing it.

**Independent Test**: Build once from a clean commit, install, execute every
ordinary/adversarial real-Lima journey, upgrade with disposable legacy data,
uninstall/reinstall, and verify the evidence manifest and candidate identity.

### Release judges and evidence

- [x] T147 [P] [US8] Add assertion-to-judge-to-mutation-proof inventory for every new authority, attribution, redaction, coverage, help, UI, recovery, and cleanup claim in `docs/release/045-claim-matrix.md`
- [x] T148 [P] [US8] Add negative fixtures that break each new judge and prove it fails before acceptance in `scripts/mutation/045/`
- [x] T149 [P] [US8] Extend dependency/license/vulnerability/advisory scanning to embedded BPF and distinguish reachable from unreachable advisories with explicit evidence in `scripts/gates/dependencies.sh`
- [x] T150 [P] [US8] Extend package/runtime manifests and helper digest/license verification for `hideout-observer` and UI assets in `internal/packagekit/`, `internal/helperbin/`, and `runtime/`
- [ ] T151 [US8] Run all TLC configurations and Go refinement suites with no invariant or liveness counterexample and record results in `scripts/gates/formal.sh` and `.artifacts/045/formal/`
- [ ] T152 [US8] Run full unit, race, fuzz/property, schema, generated, static, dependency, advisory, and mutation gates and record results in `scripts/gates/release-candidate.sh` and `.artifacts/045/local/`
- [ ] T153 [US8] Run real Lima concurrent observation, online proxy rotation, crash/retry, loss, cleanup, target-tamper, and attribution gates in `scripts/gates/release-candidate-lima.sh`
- [ ] T154 [US8] Run PTY/TUI and browser first-time, configuration, stale, recovery, parity, accessibility, and injection journeys in `scripts/gates/release-candidate-ui.sh`
- [ ] T155 [US8] Run privacy canary scan over stores, indexes, process listings, Keychain metadata output, APIs, UI, logs, exports, support, and evidence in `scripts/gates/release-candidate-privacy.sh`
- [ ] T156 [US8] Measure reference elapsed overhead, observer CPU/RSS, event/drop rate, daemon/TUI memory, render latency, query latency, quota overshoot, attach, and interactive freshness in `scripts/gates/release-candidate-performance.sh`
- [x] T157 [US8] Tune and freeze aggregation windows, storage quotas, and risk thresholds from measured evidence in `internal/workloadobs/defaults.go` and `docs/activity-observation.md`
- [ ] T158 [US8] Build an exact clean package and verify binary/helper/schema/UI/runtime digests and reproducible manifest in `scripts/release/build-candidate.sh`
- [ ] T159 [US8] Test clean install, old-version upgrade with intentionally discarded old data, Keychain migration guidance, current-version reinstall, and uninstall absence in `scripts/release/test-package-lifecycle.sh`
- [x] T160 [US8] Update product status, design, threat model, test plan, formal model catalog, privacy, retention, recovery, help, and supported coverage matrix in `docs/STATUS.md`, `docs/privacy-run-design.md`, `docs/threat-model.md`, `docs/privacy-run-test-plan.md`, and `docs/formal-models.md`
- [x] T161 [US8] Perform final source/security/UX code review, record severity/owner/resolution for every finding, and leave no required finding open in `docs/release/045-code-review.md`
- [ ] T162 [US8] Fix all required review, gate, dependency, advisory, performance, packaging, install, and documentation findings in their owning source files and rerun affected judges
- [ ] T163 [US8] Generate a signed-by-digest local evidence manifest binding commit, version, package, helpers, runtime, models, gates, limitations, and review in `scripts/release/collect-evidence.sh` and `.artifacts/045/evidence.json`
- [ ] T164 [US8] Install the exact local candidate on this machine, discard legacy Hideout data as authorized, run setup/secret/connect/run/TUI/WebUI/clean/update/uninstall smoke, and record results in `.artifacts/045/local-install/`
- [ ] T165 [US8] Verify no remote tag, GitHub Release, Homebrew commit/push, or package publication occurred and report the local candidate as ready-or-blocked in `docs/release/045-readiness.md`

**Checkpoint**: Candidate readiness requires every applicable result to be
fresh and passing; unsupported/reduced/not-run cannot substitute for a claim.

---

## Phase 11: Polish and Cross-Cutting Closure

**Purpose**: Remove drift, verify the written product path, and leave an exact
handoff.

- [ ] T166 [P] Run Go formatting, Markdown lint, shell/static lint, schema validation, generated checks, and `git diff --check`, fixing all feature-owned failures
- [ ] T167 [P] Validate every command and interaction in `specs/045-operator-observability-console/quickstart.md` against the installed candidate and update only documented final spellings
- [x] T168 [P] Audit all new logs/events/errors for internal jargon, raw secret, control-sequence, and false-success language in `internal/`, `cmd/`, and `docs/`
- [x] T169 Reconcile all 72 functional requirement identifiers (FR-001–FR-071 plus FR-035a) and 15 success criteria to implementation, tests, and evidence in `specs/045-operator-observability-console/checklists/acceptance.md`
- [x] T170 Record only genuinely deferred non-required work with owner, risk, trigger, and non-claim in `docs/DEBT.md`
- [ ] T171 Run the complete release-candidate orchestrator once more from the exact clean tree and verify the evidence manifest has no stale/reduced/not-run required entry in `.artifacts/045/`
- [ ] T172 Confirm the local install is the exact candidate, preserve no legacy data as authorized, and hand off publication as a separate explicitly authorized action in `docs/release/045-readiness.md`
- [x] T173 Fix the high-event observer shutdown deadline/reap defect, execute its Linux regression in the real-Lima observation lane, and retain an exact-clean passing receipt in `cmd/hideout-session-supervisor/`, `scripts/gates/workload-observation-lima.sh`, and `.artifacts/045/`
- [x] T174 Add fail-closed progressive Lima aggregation that revalidates and reuses only exact-commit passed lanes from the immediately preceding digest-bound aggregate in `scripts/gates/release-candidate-lima.sh`
- [x] T175 [US8] Register and independently validate the final Feature 045 release closure, including exact package/formal inventories, twelve-gate scope attribution, review closure, local install, and publication absence in `internal/productevidence/` and `scripts/release/collect-evidence.sh`
- [x] T176 [US8] Install and version-check mandatory ShellCheck tooling before Gate 0 in both release workflows and lock the workflow contract in `scripts/test-public-alpha-release.sh`
- [x] T177 [US8] Require the 045 closure before the first public-candidate VM and use a content-addressed signed-migration candidate summary rather than an incomplete package pointer in `scripts/test-public-alpha-candidate.sh`
- [x] T178 Make the generated-output gate's start semantics explicit with side-effect-free help, fail-fast unknown-option rejection, pinned-LLVM ownership, and a no-VM declaration in `scripts/gates/generated.sh`
- [x] T179 Reject malformed or duplicate additional product evidence and any caller-supplied unsigned Feature 046 proof before the public candidate's first VM in `scripts/test-public-alpha-candidate.sh`
- [x] T180 Add a zero-lane Gate 0 toolchain preflight, install/pin Lima 2.2.0 in both release workflows, and retain bounded missing-pass diagnostics from the migration inventory
- [x] T181 Split remote Gate 0 into required formal/non-formal shards, retain the aggregate required check, record per-model TLC timing/worker/run-review evidence, and provide a single-configuration diagnostic rerun that cannot claim full formal acceptance
- [x] T182 Recover formal run reviews across job-level hard stops, fail closed when the review is absent, and pin the hosted runner to the measured single-worker policy after the two-worker `WorkloadObservation` regression
- [x] T183 Remove the browser SSE credential-rotation subscriber-count race by proving stale unsubscription before fresh non-durable event publication
- [x] T184 Pin native Temurin 21 in every Gate 0 and signed-candidate macOS job, reject translated or wrong-major Java before test work, and bind the host/JVM architecture identity into formal evidence

---

## Dependencies and Execution Order

### Phase dependencies

- Phase 1 has no dependency.
- Phase 2 depends on Phase 1 and blocks all product stories.
- US1, US2, US3, and US4 can begin after Phase 2; their core packages are
  independently testable.
- US5 depends on US2 observation ingestion but can build redaction/store tests
  in parallel.
- US7 depends on the US3 operation/network path and US5 exact cleanup path.
- US6 depends on the shared projections plus US1, US2, US3, and US5 Manager
  contracts.
- US8 depends on every required story and gate implementation.
- Phase 11 depends on the exact US8 candidate.

### User-story dependency graph

```text
Setup -> Foundation
Foundation -> US1
Foundation -> US2 -> US5
Foundation -> US3 -> US7
Foundation -> US4
US1 + US2 + US3 + US5 -> US6
US1 + US2 + US3 + US4 + US5 + US6 + US7 -> US8 -> Closure
```

### Within each story

- Authority-changing tests and model assertions are written and observed
  failing before implementation.
- Types/validators precede services; services precede routes and clients.
- Positive paths, fail-closed paths, mutation proofs, and recovery evidence all
  pass before a story checkpoint.
- UI surfaces consume Manager contracts and never become direct providers.

## Parallel Opportunities

- Setup tasks T002, T003, T005, and T006 touch independent files after T001.
- Foundational tests T009-T014 can be authored in parallel; types T019-T020 can
  proceed independently after their tests.
- US1 tests T027-T030 and component task T035 are independent.
- US2 collector tests T041-T047 are independent; process, file, and network/DNS
  providers split after the shared observer skeleton.
- US3 tests T067-T072 are independent; typed change handlers T082 can split by
  handler package.
- US4 test tasks T089-T091 are independent.
- US5 tests T101-T106 are independent; store, redaction, cleanup, and export
  packages can progress independently after shared types.
- US7 tests T120-T124 are independent.
- US6 tests T135-T138 are independent.
- US8 claim, mutation, dependency, and package inventory tasks T147-T150 are
  independent before orchestration.

## Parallel Examples

### US1

```text
T027 operator snapshot contract tests
T028 Bubble Tea model tests
T029 renderer golden tests
T030 PTY/terminal restoration tests
```

### US2

```text
T043 process collector fixtures
T044 file collector fixtures
T045 network/DNS collector fixtures
T046 aggregation/risk fixtures
```

### US3

```text
T068 transaction concurrency tests
T069 Keychain provider tests
T070 network transition tests
T072 TUI modal/PTY tests
```

### US5

```text
T101 redaction canary scan
T102 segment/quota recovery
T103 exact lifecycle cleanup
T104 store/API access security
T105 coverage loss intervals
T106 reviewed export
```

## Implementation Strategy

### MVP first

1. Complete Setup and Foundation.
2. Complete US1 with existing evidence plus explicit unavailable placeholders.
3. Validate healthy, idle, concurrent, error, stale, narrow terminal, and
   `--once` paths independently.
4. Do not claim workload observability or editable configuration in the MVP.

### Incremental safety delivery

1. Land the cgroup and observer behind honest capability states.
2. Add process, file, network, DNS, aggregation, and risk one provider at a
   time; each provider remains reduced until its negative gate passes.
3. Land CAS/idempotency and Keychain before exposing edit controls.
4. Add retention/cleanup and crash recovery before enabling deep history.
5. Add WebUI depth only after Manager parity fixtures pass.
6. Freeze public claims only in the exact release-candidate evidence run.

### Publication boundary

All tasks may produce and install a local candidate. None authorizes a remote
tag, GitHub Release, Homebrew change/push, or other publication. That remains a
separate operator decision after T165/T174.

## Completion format audit

Every executable task above uses the required checkbox, sequential task ID,
optional parallel marker, story label where applicable, action, and exact file
path.

Setup/foundation/closure tasks intentionally omit story labels. Story tasks use
their original `US1` through `US8` labels even though priority ordering places
US7 before the P2 browser story.
