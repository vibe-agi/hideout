---
description: "Task list for Host Capability Projection (030)"
---

# Tasks: Host Capability Projection

<!-- markdownlint-disable MD013 MD024 MD060 -->

**Input**: Design documents from `specs/030-host-capability-projection/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md

**Tests**: REQUIRED — this feature touches Hideout authority (a new typed host capability, workspace path model, decision-center grant, audit). Every authority slice has a positive test and a fail-closed/redaction test. TDD: write the failing test before the implementation in each pair.

**Scope discipline**: implement ONLY `host.app.open-resource` + the `code` recipe. adb / AppleScript / result-streaming are design-ready registry entries that MUST fail closed if dispatched — do NOT implement them.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: parallelizable (different files, no incomplete dependency)
- **[Story]**: US1–US4; Setup/Foundational/Polish carry no story label

---

## Phase 1: Setup

- [X] T001 Create `internal/hostcap/` and `internal/hostcap/appopen/` and `internal/cmdgrammar/` package directories with `doc.go` describing each package's authority role (Core-owned registry/provider vs authority-free grammar).
- [X] T002 [P] Add `schemas/capability-descriptor.schema.json` and `schemas/open-resource-intent.schema.json` skeletons (draft per `contracts/capability-registry.md` and `data-model.md`), registered in the schema-validate list used by `cmd/hideout-schema-validate`.
- [X] T003 [P] Add `projection.*` recovery-code constants stub to `internal/recovery/registry.go` (codes from research D10; `domain.subject.detail` convention) — values only, wired to findings in later tasks.

---

## Phase 2: Foundational (blocking prerequisites for all user stories)

**Goal**: The Core projection machinery US1/US3/US4 all sit on. No user-visible flow yet.

### Capability registry & descriptor

- [X] T004 [P] Test `internal/hostcap/registry_test.go`: registry has unique ids, known enums, resolvable `providerRef`; `host.app.open-resource` present as `implemented`; adb/applescript present as `design-ready`; a `design-ready` descriptor reached at dispatch fails closed.
- [X] T005 Implement `internal/hostcap/descriptor.go` (`CapabilityDescriptor`, enums: RiskClass/ResultPolicy/DecisionPolicy/LifecyclePolicy/Status/ResourceKind) per `data-model.md`.
- [X] T006 Implement `internal/hostcap/registry.go`: static package-owned registry, lookup, `Validate()`; NO runtime registration API exported. Include the v1 descriptors from `contracts/capability-registry.md` (host.app.open-resource implemented; adb/applescript design-ready).

### App-identity registry (Core/package-owned)

- [X] T007 [P] Test `internal/hostcap/appid_test.go`: `vscode` resolves on darwin; a binary-path/bundle-id supplied as `AppRef` is rejected; unresolved app → `projection.app.absent`; resolver drift → `projection.app.identity-drift`.
- [X] T008 Implement `internal/hostcap/appid.go`: `AppIdentityEntry`, package-owned `vscode`→fixed macOS Applications bundle resolver with code-signature verification immediately before launch + safe/trusted `LaunchProfile`; never use ambient `PATH`; referenced by id only.

### OpenResourceIntent decode + validation

- [X] T009 [P] Test `internal/hostcap/intent_test.go`: strict decode rejects unknown fields; `AppRef` must resolve; each `ResourceRef` must be workspace-kind; `Location` ints positive; `WindowMode` in enum; a host path present in intent is rejected.
- [X] T010 Implement `internal/hostcap/intent.go`: `OpenResourceIntent`, `ResourceRef`, strict JSON decode, field validators. Host path never accepted from the intent.

### `code-open-v1` grammar (authority-free)

- [X] T011 [P] Test `internal/cmdgrammar/code_test.go`: each accepted form in `contracts/code-open-grammar.md` maps to the exact intent; `-g file:line:col` split structurally; unknown flag → refusal (`projection.flag.unrecognized`); no grammar output contains a host path or raw argv.
- [X] T012 Implement `internal/cmdgrammar/code.go`: declarative `code-open-v1` grammar → `OpenResourceIntent` proposal (positional workspace resource, `-g`, `-n`, `-r`, `unknownFlags: deny`).

### Workspace ResourceRef mapping (host path Core-only)

- [X] T013 [P] Test `internal/manager/hostcap_projection_test.go` (mapping part): a workspace `ResourceRef` resolves to a host path under the session `HostRoot`; `..`-escape and guest-only paths refuse (`projection.path.no-host-mapping`); symlink escape rechecked (reuse `host.open` resolution); host path never returned to the caller-facing struct.
- [X] T014 Implement the workspace ResourceRef → host path resolver in `internal/manager` (reuse the `internal/broker` `host.open` workspace resolution + `EvalSymlinks` escape recheck; extend `run_plan.go` `ResolveWorkspaceMapping` context so only Core holds the host path).

### `host.app.open-resource` provider

- [X] T015 [P] Test `internal/hostcap/openresource_test.go`: provider re-decodes+validates intent, resolves appRef, maps resource, enforces mode, launches via an injected launcher fake; result policy `none` (no host bytes to guest); de-dup/rate-limit identical `(appRef,target,windowMode)`; every failure refuses without fallback.
- [X] T016 Implement `internal/hostcap/openresource.go`: the `host.app.open-resource` provider per `contracts/capability-registry.md` (steps 1–7), launcher injected as an interface.

### VS Code launcher (host-side, safe/trusted)

- [X] T017 [P] Test `internal/hostcap/appopen/vscode_test.go`: safe-mode argv includes isolated `--user-data-dir` + `--disable-extensions` and does NOT include `--disable-workspace-trust`; auto-tasks-off flag present; trusted-mode argv uses operator config; `-g file:line:col` rendered correctly; argv built by Go from the typed intent, never from raw guest argv.
- [X] T018 Implement `internal/hostcap/appopen/vscode.go`: build safe/trusted VS Code argv from the validated intent + `LaunchProfile`.

### Broker action + cmdproxy binding

- [X] T019 [P] Test `internal/broker/broker_appopen_test.go`: `host.app.open-resource` action strict-decodes intent, rejects out-of-allowlist args, delegates to the provider, returns `{outcome}`/typed error with no host path/username/token; provider-unavailable refuses without fallback.
- [X] T020 Implement broker `host.app.open-resource` action in `internal/broker/broker.go` (route by `Action`, reuse the session `HostRoot` context; args allowlist `action`/`command`/`intent`).
- [X] T021 [P] Test `internal/cmdproxy/cmdproxy_test.go` (binding part): a `code` `Registration` binds to `host.app.open-resource` with grammar `code-open-v1`, owner `host-app-projection`; unknown command name at broker → `projection.command.unbound`; an installed `code` shim exclusively owns the name (no delegation on failure).
- [X] T022 Implement the `code` `CommandBinding`/`Registration` in `internal/cmdproxy/cmdproxy.go` and expose a `ProjectionRegistry()` that adds `code` alongside `open`/`xdg-open`.

### Wiring + recovery codes

- [X] T023 Wire the projection registry + provider into the run data plane: `internal/manager/run_dataplane.go` registers the `code` binding and the `host.app.open-resource` provider per run, bound to session/profile (LifecyclePolicy), and installs the guest `code` shim.
- [X] T024 [P] Complete `internal/recovery/registry.go` `projection.*` codes with messages/next-steps and wire them into the broker/provider failure paths; test `internal/recovery/registry_test.go` covers presence + human/JSON parity.
- [X] T025 Fill `schemas/capability-descriptor.schema.json` and `schemas/open-resource-intent.schema.json` to validate the real structs; test in `internal/hostcap` that emitted descriptors/intents validate against the schemas and that unknown fields fail.

**Checkpoint**: `go test ./internal/hostcap/... ./internal/cmdgrammar/... ./internal/cmdproxy/... ./internal/broker/... ./internal/manager/...` green; no host path/username/token in any projection struct or event.

---

## Phase 3: User Story 1 — `code .` opens a workspace resource in the host IDE (Priority: P1) 🎯 MVP

**Goal**: In a guest with no `code` binary, `code .` / `code <path>` / `code -g file:line:col` open the mapped host workspace in safe-mode VS Code; out-of-scope/malformed invocations fail closed.

**Independent test**: run the three accepted forms in a real guest → correct host resource opens in constrained VS Code; run the fail-closed forms → typed refusal, nothing opens, no fallback; guest/output/audit never carry the host path.

### Tests (write first)

- [X] T026 [P] [US1] Integration test `internal/manager/hostcap_projection_us1_test.go`: end-to-end (with a fake launcher + fake broker transport) proves `code .`, `code src/x`, `code -g src/x:12:3` reach the provider and launch the correct host target in safe mode; audit `ide.open` has mode `safe`, no host path.
- [X] T027 [P] [US1] Fail-closed test in the same file: guest-only path, workspace escape, unknown flag, absent app, unavailable provider each refuse with the expected `projection.*` code and never delegate to host exec or a shadowed guest binary.
- [X] T028 [P] [US1] Redaction test: across all US1 flows, guest response, adapter/grammar output, intent, `ide.open` event, and exported evidence contain no host absolute path, username, home, token, or raw argv.

### Implementation

- [X] T029 [US1] Guest `code` shim passthrough: ensure `cmd/hideout-shim` (or the existing shim mechanism) forwards `code` argv to the broker `host.app.open-resource` action via the grammar; the shim carries no host path.
- [X] T030 [US1] Boundary Summary / audit: emit `ide.open` on each open with `{command, capability, mode, workspaceIdentity, relativeTarget, outcome}` (no host path), through the existing audit + redaction path.
- [X] T031 [US1] De-dup/rate-limit identical opens within a short window; test an agent-style burst produces bounded host launches.
- [X] T032 [US1] End-to-end wiring check: `hideout run --profile <p> -- code .` maps guest → host, launches safe-mode VS Code (fake in unit, real in Gate 2), returns outcome to guest.

**Checkpoint**: US1 independently testable at Gate 0 with fakes; real launch deferred to Gate 2 (Phase 7).

---

## Phase 4: User Story 2 — guest without host-username/path disclosure (Priority: P2)

**Goal**: privacy/hardened profiles default `pathMode=alias` (guest `/workspace`); no host username/home synthesized into workspace path / identity env / git / verified mount metadata; scoped claim + non-claim; three-channel verification with self-test + preserve control.

**Independent test**: create a privacy profile → guest `pwd` under `/workspace`; three channels free of host username/home on real backend; preserve control exposes it; detector self-tests fire.

### Tests (write first)

- [X] T033 [P] [US2] Test `internal/profiletemplate/template_test.go`: newly created `privacy` and `hardened` profiles default `Workspace.PathMode="alias"`; `dev`/`debug` keep `preserve`; templates do not claim path privacy while inheriting `preserve`.
- [X] T034 [P] [US2] Test `internal/manager/run_environment_test.go` (drift): flipping `pathMode` on an existing environment changes resolved `GuestWorkspace` → workspace drift axis fail-closed recreate; no silent remap; no new identity input / record-version bump.
- [X] T035 [P] [US2] Test the three-channel detector harness (Go unit for the assertion logic that the Gate 2 script drives): each channel detector matches a deliberately-present host username/home (self-test), and reports absence only when truly absent.

### Implementation

- [X] T036 [US2] Set `privacy`/`hardened` default `pathMode=alias` in `internal/profiletemplate/template.go`; leave `profile.Default` `preserve` but ensure privacy/hardened templates override; do not silently migrate existing profiles.
- [X] T037 [US2] Ensure the projection ResourceRef path model (Phase 2) is consistent under alias: guest sees `/workspace`, Core resolves host path; add the doctor/onboarding host-path-disclosure warning when `preserve` is active on a privacy/hardened profile.
- [X] T038 [US2] Add the three-channel verification harness helpers (identity env incl. account-home vs process-HOME distinction; workspace namespace; mount metadata via `/proc/mounts`,`/proc/self/mountinfo`,`mount`,`findmnt`) as reusable assertion code the Gate 2 lane invokes; include per-channel detector self-test and a `preserve`-mode positive control fixture.

**Checkpoint**: alias default + drift + detector harness testable at Gate 0; real three-channel proof in Gate 2 (Phase 7).

---

## Phase 5: User Story 3 — safe/trusted-host-ide mode (Priority: P3)

**Goal**: safe mode is the default; `trusted-host-ide` is an explicit, revocable operator grant through the decision center, persisted in guest-unreachable control-plane state.

**Independent test**: trusted denied without grant; grant → operator config; revoke → next launch denied; explicit safe selection restores safe launches; safe-mode folder-open task marker never written.

### Tests (write first)

- [X] T039 [P] [US3] Test `internal/manager/hostcap_ide_mode_test.go`: without a grant, trusted requested → `projection.mode.trusted-denied` and no host launch; grant via decision center → mode `trusted-host-ide`; revoke → next launch denied; explicit safe selection restores safe launch; mode/grant state not influenced by anything written to the workspace.
- [X] T040 [P] [US3] Test `internal/hostcap/appopen/vscode_test.go` (safe-mode behavior): safe argv never contains `--disable-workspace-trust`; a folder-open task fixture marker is not written in safe mode (verified structurally via the argv/flags that disable auto-tasks + extensions).

### Implementation

- [X] T041 [US3] Implement requested `IdeMode` as per-profile control-plane state and `host-app.trusted-ide` authority through the existing decision center (`internal/decision`, `internal/manager`): claim/approve/deny/timeout/revoke/reopen, with every live grant bound to run/session/profile/workspace/environment/subject under `~/.hideout` — never the workspace. Invalidate on profile/environment identity change or ended/unprovable ownership.
- [X] T042 [US3] Enforce mode in the provider (Phase 2 `openresource.go`): `safe` default-allow-audited; `trusted-host-ide` requires live grant else `projection.mode.trusted-denied`.
- [X] T043 [US3] CLI/Manager surface for requesting/inspecting the trusted-ide grant (reuse `hideout decision ...` + a `hideout profile ide-mode` read/plan/apply); no per-invocation prompt for safe mode.

---

## Phase 6: User Story 4 — inspect projected capabilities (Priority: P3)

**Goal**: operator can see projected capabilities, bindings, active mode, and PATH shadow order, with host paths kept internal to Core.

**Independent test**: `doctor --feature projection --format json` lists bindings, capability, mode, shadow order; no host path/secret.

### Tests (write first)

- [X] T044 [P] [US4] Test `internal/doctor/report_test.go` (projection feature): `--feature projection` reports projected capabilities, `code` binding, active mode, and PATH shadow order (whether a real guest `code` is shadowed + order); output has no host absolute path or secret; human/JSON parity.

### Implementation

- [X] T045 [US4] Implement `doctor --feature projection` in `internal/doctor` reading the projection registry + binding + IdeMode + PATH shadow order; register the feature in the doctor feature list.
- [X] T046 [US4] Add a `ProjectionInspection` read model to `internal/manager` surfaced to doctor (and available to TUI/WebUI later), host paths Core-internal.

---

## Phase 7: Polish, Gates & Docs (cross-cutting)

### Evidence & Gate 0

- [X] T047 [P] Add 030 proof ids + claim ids to `internal/productevidence` (e.g. `030.projection.gate0.mechanics`, `030.projection.real-gate2.code-open`, `030.projection.real-gate2.privacy-three-channel`, `030.projection.real-gate2.trusted-grant`, `030.projection.real-gate2.not-run`, `030.projection.docs.claim-boundary`); test registry coverage.
- [X] T048 Add `scripts/test-host-capability-projection-smoke.sh` (Gate 0 mechanics): registry validation, grammar map/deny, intent strict-decode, broker allowlist + provider-unavailable no-fallback, redaction scan, recovery-code parity; wire into `scripts/test-gate0.sh`.

### Real backend (operator-run, not-run-honest)

- [X] T049 Extend `scripts/test-gate2-lima.sh` with the projection lane: (a) `code .`/`code -g` open the correct host workspace in safe mode; (b) fail-closed forms refuse without fallback; (c) safe-mode folder-open task marker absent, `--disable-workspace-trust` never used; (d) trusted request denied → grant succeeds → revoke denies → explicit safe selection restores safe; (e) three-channel username-hiding with per-channel detector self-test and preserve-mode positive control. Record `not-run` honestly when Lima/VS Code prerequisites absent; never satisfy the guest-visible/privacy claims with fixtures.
- [X] T050 Extend the Gate 3 lane (`scripts/` privacy gate) to re-verify the privacy assertions (proxy env absent, mediated DNS, connected-subnet blocked, enforced privilege) with the projected/aliased environment; record provenance.

### Docs (truth + claims)

- [X] T051 [P] New `docs/host-capability-projection.md`: the projection model (registry, capability, recipe, ResourceRef, safe/trusted mode), the four invariants, and the design-ready adb/AppleScript/result-streaming boundary.
- [X] T052 [P] Update `docs/threat-model.md`: the scoped username-hiding positive claim + adjacent non-claim (no "hides your identity"); the projection escape non-claims (no host-IDE protection from malicious workspace; safe mode disarms auto-exec by default; guest-writable workspace opened in host app is surfaced).
- [X] T053 [P] Update `docs/STATUS.md` (new implemented row with honest real-Lima evidence provenance + dirty), `docs/privacy-run-test-plan.md` (Gate 2/Gate 3 lanes), `docs/claim-boundaries.md` (030 claim → proof ids → real Gate 2), and `README.md`/`README.zh-CN.md` (`code .` workflow, safe-mode default, alias privacy note).
- [X] T054 [P] Update `docs/command-examples.json` with parseable projection examples once the CLI spelling is final; run the doc-truth smoke.

### Final verification

- [X] T055 Run `go build ./... && go vet ./... && gofmt -l . && go test ./...` and `scripts/test-gate0.sh`; confirm green, markdownlint 0 errors, `git diff --check` clean.
- [X] T056 Adversarial self-check: re-run the exploit forms (escape, guest-only, unknown flag, provider-unavailable, trusted-without-grant, workspace-written mode marker) and confirm each fails closed; confirm no host path/username/token in any guest-facing or exported surface (grep the evidence).

---

## Dependencies & execution order

- **Setup (T001–T003)** → **Foundational (T004–T025)** → user stories.
- **US1 (T026–T032)** depends only on Foundational. It is the MVP.
- **US2 (T033–T038)** depends on Foundational (ResourceRef/alias); independent of US1's flow otherwise.
- **US3 (T039–T043)** depends on Foundational + the provider (T016) mode hook.
- **US4 (T044–T046)** depends on Foundational (registry/binding/IdeMode).
- **Polish/Gates (T047–T056)** after the stories they cover; T049/T050 are operator-run real-backend.

## Parallel opportunities

- Within Foundational, the test tasks T004/T007/T009/T011/T013/T015/T017/T019/T021 are `[P]` (different files) and can be written first as a TDD batch before their implementations.
- US2/US3/US4 phases are largely independent of each other and can proceed in parallel once Foundational is done.
- Docs tasks T051–T054 are `[P]`.

## MVP scope

**US1 (Phase 3) on top of Foundational (Phase 2)** is the MVP: `code .` works in a guest that has no `code`, safe-mode, fail-closed, no host-path leak. US2 (privacy alias) is the strongly-recommended next slice because US1's ResourceRef model already assumes the aliased view.

## Implementation strategy

TDD per pair (test → impl). Deliver Foundational + US1 first as the demoable MVP, then US2 (privacy), then US3 (trusted mode) and US4 (inspection). Keep the registry designed for adb/AppleScript/result-streaming but implement none of them; the design-ready entries must fail closed if dispatched (T004/T006). Real-backend claims (T049/T050) are operator-run and recorded `not-run` honestly until executed on real Lima.

---

## Phase 8: Convergence

- [X] T057 CRITICAL replace ambient `exec.LookPath("code")` app resolution with a Core/package-owned canonical macOS VS Code identity resolver that rejects hostile/workspace-writable PATH candidates and verifies the resolved app/launcher identity immediately before use; add a hostile-PATH executable test per FR-008, Constitution I/II, and SC-002 (contradicts)
- [X] T058 CRITICAL add a generated-shim TDD integration test and fix `shimEnv` so the materialized `code` shim explicitly selects `host.app.open-resource` on native and Lima instead of falling through to `host.open`; assert the real broker action/grammar/provider path and no fallback per US1/AC1, FR-002, FR-006, and SC-001/SC-002 (contradicts)
- [X] T059 separate requested IDE mode from authority and implement `host-app.trusted-ide` through the existing decision-center claim/approve/deny/timeout/revoke lifecycle, binding the live grant to session/profile/workspace/environment/subject and invalidating stale identity; remove the profile mode file as its own grant per FR-010, FR-012, and SC-006 (contradicts)
- [X] T060 add projection-specific audit TDD through the real broker request with non-empty argv, then emit action `ide.open` without raw argv/host path/username/token despite shared broker metadata restoration; cover local audit and exported evidence per FR-018, Constitution IV, and SC-003 (contradicts)
- [X] T061 define and implement an owned host-app launch lifecycle: verify safe-mode state before launch, prevent broker-context cancellation from killing the launcher handoff, wait/release child resources correctly, and clean or durably own isolated VS Code user-data state without cross-launch trust carryover; prove folder-open auto-task absence behaviorally per FR-011, FR-013, Constitution V, and SC-001/SC-006 (partial)
- [X] T062 harden `code-open-v1` TDD so v1 accepts exactly one resource, requires a positive line for `-g`, parses colon-bearing paths from the right, rejects goto-plus-positional/no-target forms outside the contract, and keeps Core validation aligned per FR-002, FR-005, and SC-001/SC-002 (partial)
- [X] T063 make open dedup transactional and location-aware: include line/column in the key, record suppression only after a successful launch reservation/outcome, and allow immediate retry after launch failure; add concurrent and failure tests per US1/AC2 and SC-001 (partial)
- [X] T064 implement the Manager `ProjectionInspection` production read model and make doctor consume it with honest pass/warn/error/unknown states for profile, environment, binding, app/provider readiness, requested mode, live grant, and observed-vs-policy PATH shadow facts; reject the current static false-pass per FR-019 and SC-007 (contradicts)
- [X] T065 restore documentation and evidence truth: keep guest-visible open, safe auto-task absence, trusted grant/revoke, and three-channel username privacy pending/not-run until real gates execute; after Gate 2/Gate 3 pass, replace pending text only with provenance-bound positive claims and retain the dirty/non-release caveat; add doc-truth checks per FR-017, Constitution IV, and SC-008 (contradicts)
