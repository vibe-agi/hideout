# Tasks: Community Host-App Recipes

<!-- markdownlint-disable MD013 MD060 -->

**Input**: Design documents from `specs/032-community-host-app-recipes/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`,
`contracts/`, `quickstart.md`

**Tests**: This feature crosses package trust, command ownership, HostFS, host
application identity, decisions, audit, and real host effects. Tests precede
implementation in every story; source/static grep alone is not completion.

## Phase 1: Setup

**Purpose**: Establish package, schema, and proof ownership without changing
runtime authority.

- [X] T001 Create package skeletons and ownership docs in `internal/packsnapshot/doc.go` and `internal/hostapppack/doc.go`
- [X] T002 [P] Add strict schemas in `schemas/host-app-pack.schema.json`, `schemas/host-app-pack-registry.schema.json`, `schemas/host-app-enablement.schema.json`, and `schemas/host-app-inspection.schema.json`
- [X] T003 [P] Add built-in pack and Core safety-profile data scaffolds in `internal/hostcap/recipes/builtin-vscode.json` and `internal/hostcap/recipes/safety-profiles.json`
- [X] T004 [P] Register 032 proof IDs and exact artifact policies in `internal/productevidence/host_app_pack.go`, `internal/productevidence/registry.go`, and `internal/productevidence/registry_test.go`
- [X] T005 Add new schemas and host-app pack smoke entrypoint checks to `scripts/test-gate0.sh`
- [X] T006 Validate empty scaffolds, JSON schemas, proof registry, and markdown in `scripts/test-host-app-pack-smoke.sh`

---

## Phase 2: Foundational - Immutable Source, Models, And Registries

**Purpose**: Build the authority-free intake and strict data model required by
all user stories.

**Blocking**: No user story begins until this phase is complete.

- [X] T007 [P] Write bounded local/git snapshot tests for symlinks, special files, size/count limits, hook/filter/submodule isolation, exact commit, mutation, and digest stability in `internal/packsnapshot/source_test.go`
- [X] T008 Implement immutable local/git acquisition, isolated git invocation, tree digest, and atomic snapshot publication in `internal/packsnapshot/source.go`
- [X] T009 Refactor adapter-pack intake to use `internal/packsnapshot` without changing 011 digests/lifecycle in `internal/adapterpack/source.go` and `internal/adapterpack/source_test.go`
- [X] T010 [P] Write strict host-app manifest tests for unknown fields, identifiers, bundle basenames, relative executable paths, bounded grammar, capability/result/resource allowlists, scripts/hooks, count/byte limits, and ANSI/OSC/control-character package prose in `internal/hostapppack/manifest_test.go`
- [X] T011 Implement manifest entities, strict decode, cross-reference validation, and canonical normalization in `internal/hostapppack/types.go` and `internal/hostapppack/manifest.go`
- [X] T012 [P] Write permission-fingerprint mutation matrix over every authority-bearing field and docs/tests-only controls in `internal/hostapppack/fingerprint_test.go`
- [X] T013 Implement canonical permission fingerprint and bounded permission diff in `internal/hostapppack/fingerprint.go`
- [X] T014 [P] Write registry/store tests for private modes, file locking, atomic revisions, source re-digest, installed/revoked states, profile enablement states, and crash/no-partial behavior in `internal/hostapppack/store_test.go`
- [X] T015 Implement registry, immutable revision, test-result, enablement, tombstone, and store-lock persistence in `internal/hostapppack/store.go`
- [X] T016 [P] Write JSON Schema parity tests for manifest, registry, enablement, and inspection examples in `internal/hostapppack/schema_test.go`
- [X] T017 Add model/schema golden fixtures and reject schema/model drift in `internal/hostapppack/testdata/`
- [X] T018 [P] Write reserved-command and explicit owner-replacement tests in `internal/cmdproxy/hostapp_registry_test.go`
- [X] T019 Implement the Core-owned reserved command catalog and deterministic collision planner in `internal/cmdproxy/hostapp_registry.go`
- [X] T020 [P] Add typed recovery registry cases for source, digest, conflict, app identity, safety, permission review, portal, revoke, and new-run failures in `internal/recovery/registry.go` and `internal/recovery/registry_test.go`
- [X] T021 Add host-app lifecycle audit action values and schema coverage in `schemas/audit-event.schema.json` and `internal/audit/audit_test.go`
- [X] T022 Add bounded inspection and 64-command compilation performance tests in `internal/hostapppack/performance_test.go`, run all foundational package/schema/source tests, and require zero mutation of profile/runtime authority before enablement in `scripts/test-host-app-pack-smoke.sh`

**Checkpoint**: A community source can be snapshotted and inspected as strict
data, but no guest command or host effect is enabled.

---

## Phase 3: User Story 1 - Add And Use A Familiar Host App (Priority: P1)

**Goal**: One guided add flow enables one exact safe recipe for future runs and
routes its guest command through the existing generic host-app provider.

**Independent Test**: Add a local test recipe to one profile, start a new run,
invoke its workspace command, and observe the expected app effect with no app
identity/path in guest intent and no impact on another profile or old session.

### Tests For User Story 1

- [X] T023 [P] [US1] Write application-root tests for basename-only expansion, canonical containment, ancestor ownership/write posture, workspace/HostFS/tmp/source/store overlap, executable containment, launch-time replacement, and descriptor-safe bounded unsigned bundle-tree digest traversal including mutation, special files, and escaping links in `internal/hostcap/appidentity_test.go`
- [X] T024 [P] [US1] Write signed identity tests proving package expectations only narrow Core-observed TeamID/bundle/code identity and package self-requirements cannot authenticate in `internal/hostcap/appidentity_darwin_test.go`
- [X] T025 [P] [US1] Write Core safety-profile tests for identity compatibility, required/forbidden argv and settings as one effect, isolated qualified-app/run state, and unknown-app no-safe behavior in `internal/hostcap/appopen/safety_test.go`
- [X] T026 [P] [US1] Write generic declarative grammar tests for aliases, one resource, goto line/column, windows, unknown flags, bounds, and output with no app/binding/capability/host-path/raw-argv fields in `internal/cmdgrammar/openresource_test.go`
- [X] T027 [P] [US1] Write app-add plan/apply tests for read-only plan, Core-derived plain-language review, local and exact-commit Git source drift, exact digest acceptance, cancellation, atomic install+enable, install-only, profile scope, and old-session next action in `internal/manager/host_app_packs_test.go`
- [X] T028 [P] [US1] Write CLI/Manager parity tests for local and exact-commit Git add plus list/inspect/validate/test/enable fields, TTY/non-interactive confirmation, and shared sanitized untrusted package text in `internal/app/host_app_test.go` and `internal/manager/api_test.go`
- [X] T029 [P] [US1] Write broker tests that require registered command ownership, derive qualified app and resource kind from immutable binding plus live session mapping, reject forged app/binding/capability/result/resource-kind/host-path/unknown fields, and never fall back in `internal/broker/hostapp_test.go`
- [X] T030 [P] [US1] Write two-recipe provider tests proving one generic resolver/renderer/provider path and independent removal in `internal/hostcap/openresource_test.go`

### Implementation For User Story 1

- [X] T031 [US1] Implement Core application-root resolution and signed observed identity model with pre-launch revalidation in `internal/hostcap/appidentity.go` and `internal/hostcap/appidentity_darwin.go`
- [X] T032 [US1] Implement Core-owned versioned safety profiles and combined argv/settings/state verification in `internal/hostcap/appopen/safety.go`, `internal/hostcap/appopen/render.go`, and `internal/hostcap/appopen/safestate.go`
- [X] T033 [US1] Replace code-specific parsing with strict declarative `open-resource-v1` grammar and unbound intent in `internal/cmdgrammar/openresource.go` and `schemas/open-resource-intent.schema.json`
- [X] T034 [US1] Implement installed+built-in qualified app catalog, immutable run binding, and runtime re-digest in `internal/hostcap/appregistry.go` and `internal/hostcap/binding.go`
- [X] T035 [US1] Implement Manager app add/validate/test/list/inspect/enable plan/apply and atomic snapshot+enablement flow in `internal/manager/host_app_packs.go`
- [X] T036 [US1] Add production-owned Manager routes and strict request/response shapes for app lifecycle in `internal/manager/routes.go`, `internal/manager/api.go`, and `schemas/manager-api.schema.json`
- [X] T037 [US1] Implement `hideout app add/list/inspect/validate/test/enable` as Manager Core consumers with layered review and confirmation in `internal/app/app.go`
- [X] T038 [US1] Compile exact profile app bindings into command registrations and future-run shims with explicit old-session behavior in `internal/manager/host_app_catalog.go`, `internal/manager/run_dataplane.go`, and `internal/cmdproxy/cmdproxy.go`
- [X] T039 [US1] Make broker host-app dispatch validate immutable command/binding ownership and pass only internally bound intent/provider identity in `internal/broker/hostapp.go` and `internal/broker/broker.go`
- [X] T040 [US1] Refactor `host.app.open-resource` to consume a bound app and Core safety decision rather than guest appRef in `internal/hostcap/openresource.go` and `internal/hostcap/projection.go`
- [X] T041 [US1] Convert VS Code identity, launch, grammar, and safety data to `builtin.vscode` pack and remove `CodeAppRef`, `CodeRegistration`, direct `ResolveApp("vscode")`, and `vscode-user-data` production special cases in `internal/hostcap/recipes/`, `internal/cmdgrammar/`, `internal/cmdproxy/`, and `internal/manager/`
- [X] T042 [US1] Keep `profile host-app-mode` only as a typed compatibility alias to the built-in binding while generic inspection contains no app-specific branch in `internal/app/app.go` and `internal/manager/projection_inspection.go`
- [X] T043 [US1] Emit install/validate/test/add/enable and launch/refusal audit from validated snapshot/binding facts in `internal/hostapppack/evidence.go` and `internal/manager/host_app_packs.go`
- [X] T044 [US1] Run US1 tests and emit `032.host-app-pack.gate0.lifecycle` plus `032.host-app-pack.gate0.binding` evidence from `scripts/test-host-app-pack-smoke.sh`

**Checkpoint**: A safe external recipe works for one future run/profile through
one generic provider; no app-specific runtime path or fallback remains.

---

## Phase 4: User Story 2 - Understand, Approve, And Revoke Risk (Priority: P2)

**Goal**: Core-derived identity/safety/permission facts remain visible and
elevated authority is exact, default-deny, update-sensitive, and revocable.

**Independent Test**: Compare verified/unverified/absent/drifted states, approve
one elevated binding, prove cross-app denial, update permission suspension, and
disable/revoke with retained audit.

### Tests For User Story 2

- [X] T045 [P] [US2] Write explicit unsigned-app trust tests for exact canonical/`bundle-tree-v1` content digest, descriptor-stable traversal, unverified labels, ask-each-run-only access, change/retrust, and zero package self-verification in `internal/hostcap/appidentity_test.go`
- [X] T046 [P] [US2] Write app-scoped decision concurrency/lifecycle tests across app, pack revision, binding, command, session, profile, workspace, environment, identity, timeout, owner loss, disable, update, and revoke in `internal/manager/host_app_decisions_test.go`
- [X] T047 [P] [US2] Write update permission-diff tests for every fingerprint field, no silent inheritance, unchanged-permission exact revision selection, and rollback/no-partial failure in `internal/manager/host_app_packs_test.go`
- [X] T048 [P] [US2] Write inspection parity tests for verified/unverified/absent/drifted/unsupported, safety, shadow/conflict, source/test, permission, grant, outcome, recovery, and bounded untrusted hint fields with ANSI/OSC/control-sequence injection in `internal/manager/projection_inspection_test.go`, `internal/app/host_app_test.go`, and `internal/doctor/report_test.go`
- [X] T049 [P] [US2] Write redaction tests injecting real control credential shapes, raw host/executable paths, username, repository credential, and raw argv into lifecycle/runtime/evidence paths in `internal/hostapppack/evidence_test.go` and `internal/broker/hostapp_test.go`
- [X] T050 [P] [US2] Write disable/revoke/remove race and no-fallback tests with running immutable sessions, retained audit, owned-file deletion, unrelated preservation, and tombstones in `internal/manager/host_app_packs_test.go`

### Implementation For User Story 2

- [X] T051 [US2] Implement exact-digest unverified app trust records and launch-time drift checks in `internal/hostapppack/trust.go` and `internal/hostcap/appidentity.go`
- [X] T052 [US2] Generalize host-app decision kind/provider identity and grant checker to exact app/binding/revision/run facts in `internal/decision/types.go`, `internal/manager/hostcap_projection.go`, and `internal/manager/decisions.go`
- [X] T053 [US2] Implement update plan/apply, permission diff, suspension, fresh acceptance, and exact revision switching in `internal/manager/host_app_packs.go`
- [X] T054 [US2] Implement disable, store-wide revoke, ownership-checked remove, runtime revocation check, tombstone, and retained evidence in `internal/manager/host_app_packs.go` and `internal/hostapppack/store.go`
- [X] T055 [US2] Replace single-app projection inspection with shared binding catalog/status/recovery model in `internal/manager/projection_inspection.go` and `internal/manager/manager.go`
- [X] T056 [US2] Add `hideout app update/disable/remove` plus advanced revoke and full human/JSON renderers in `internal/app/app.go`
- [X] T057 [US2] Add doctor host-app pack findings from shared inspection without auto-running package hints in `internal/app/app.go` and `internal/doctor/report.go`
- [X] T058 [US2] Extend audit, Boundary Summary, live-console event catalog, and export-safe summaries for generic app lifecycle/runtime outcomes in `internal/manager/boundary_summary.go`, `internal/daemon/events.go`, and `internal/liveconsole/catalog.go`
- [X] T059 [US2] Add stable typed recovery to CLI/Manager/doctor for every selected failure without parsing provider prose in `internal/recovery/registry.go` and `internal/app/app.go`
- [X] T060 [US2] Run US2 lifecycle/identity/safety/redaction tests and emit `032.host-app-pack.gate0.identity-safety` evidence in `scripts/test-host-app-pack-smoke.sh`

**Checkpoint**: Dangerous facts are visible; no trust crosses app/revision/run,
and disable/revoke stops routing while preserving evidence.

---

## Phase 5: User Story 3 - Open An Already-Authorized HostFS Resource (Priority: P2)

**Goal**: Host-app projection consumes existing active HostFS content authority
without creating a path-policy bypass.

**Independent Test**: Open one active authorized HostFS resource and reject
see-only, ungranted, denied, stale, ended, retargeted, or reserved variants.

### Tests For User Story 3

- [X] T061 [P] [US3] Write Core resource-resolver tests for workspace and HostFS portal ownership, content/tree authority, see-only denial, reserved roots, expiry, ended owner, symlink retarget, and no host-path output in `internal/manager/host_app_resource_test.go`
- [X] T062 [P] [US3] Write broker/provider integration tests proving HostFS authority is rechecked after decision approval and before launch in `internal/broker/hostapp_test.go` and `internal/hostcap/openresource_test.go`
- [X] T063 [P] [US3] Write audit/export fixtures for HostFS resource class and relative target with zero lower host path or portal/provider token in `internal/hostapppack/evidence_test.go`

### Implementation For User Story 3

- [X] T064 [US3] Replace workspace-only projection resolver with a host-path-free workspace/HostFS resource resolver in `internal/hostcap/openresource.go` and `internal/manager/host_app_resource.go`
- [X] T065 [US3] Add authoritative HostFS portal/content decision lookup and immediate canonical revalidation without creating grants in `internal/hostfs/service.go` and `internal/manager/host_app_resource.go`
- [X] T066 [US3] Bind HostFS resource authority and owner state into run-scoped app decisions and invalidate on portal/policy loss in `internal/manager/hostcap_projection.go`
- [X] T067 [US3] Extend open-resource schema/inspection/audit with `hostfs-portal` while omitting lower paths in `schemas/open-resource-intent.schema.json`, `schemas/host-app-inspection.schema.json`, and `internal/hostapppack/evidence.go`
- [X] T068 [US3] Add local HostFS resource-consumption positive/negative proof to `scripts/test-host-app-pack-smoke.sh`
- [X] T069 [US3] Add real workspace/HostFS portal projection assertions to `scripts/lib/gate2-projection.sh`

**Checkpoint**: The same recipe works with already-authorized HostFS content;
discover-only visibility and stale authority cannot launch a host app.

---

## Phase 6: User Story 4 - Contribute A Recipe Without Core Changes (Priority: P3)

**Goal**: Contributors can scaffold/validate/test/install ordinary recipes, and
maintainers can add built-ins as data.

**Independent Test**: Scaffold a second recipe, validate/test/install it without
Go edits, mutate source after install, and prove both built-in/external recipes
use the generic runtime path.

### Tests For User Story 4

- [X] T070 [P] [US4] Write deterministic scaffold golden tests and refusal to overwrite non-empty paths in `internal/hostapppack/scaffold_test.go`
- [X] T071 [P] [US4] Write quality-vector runner tests for pass/fail/not-run, strict expected unbound intent, no host/process/network/profile access, and no security-badge status in `internal/hostapppack/test_test.go`
- [X] T072 [P] [US4] Write end-to-end contributor CLI tests for init/validate/test/add with actionable schema/conflict/identity errors in `internal/app/host_app_test.go`

### Implementation For User Story 4

- [X] T073 [US4] Implement deterministic `hideout app init` scaffold in `internal/hostapppack/scaffold.go` and `internal/app/app.go`
- [X] T074 [US4] Implement authority-free package test vector runner and result persistence in `internal/hostapppack/test.go`
- [X] T075 [US4] Add contributor schema/reference documentation and two fixtures under `examples/host-app-packs/`
- [X] T076 [US4] Add package test/source mutation/no-Core-edit proof to `scripts/test-host-app-pack-smoke.sh`
- [X] T077 [US4] Prove built-in VS Code pack and external fixture share byte-equivalent inspection/binding shapes in `internal/hostapppack/builtin_test.go`

**Checkpoint**: Ordinary open-resource recipes are data contributions; new host
effects still require reviewed Core providers.

---

## Phase 7: Polish, Real Gate, And Cross-Cutting Verification

**Purpose**: Distribution, evidence, documentation truth, real host effect, and
adversarial completion audit.

- [X] T078 [P] Update package artifact/install/repair/uninstall checksums for schemas, built-in pack/safety data, and examples in `scripts/package-local.sh`, `internal/packagekit/verify.go`, and `scripts/test-package-smoke.sh`
- [X] T079 [P] Add the operator/contributor lifecycle guide in `docs/host-app-recipes.md` and update design, threat model, test topology, status, claim boundaries, docs index, command examples, and README ecosystem/non-claims in `docs/host-capability-projection.md`, `docs/privacy-run-design.md`, `docs/threat-model.md`, `docs/privacy-run-test-plan.md`, `docs/STATUS.md`, `docs/claim-boundaries.md`, `docs/README.md`, `docs/command-examples.json`, `README.md`, and `README.zh-CN.md`
- [X] T080 [P] Add all 032 proof IDs and community-pack overclaim patterns to `scripts/test-doc-truth-smoke.sh`
- [X] T081 [P] Add route/event/schema drift guards for every new Manager resource and lifecycle event in `internal/manager/api_routes_test.go` and `internal/liveconsole/catalog_test.go`
- [X] T082 Run complete Gate 0 host-app-pack smoke and record artifact-backed `032.host-app-pack.gate0.lifecycle`, `032.host-app-pack.gate0.binding`, and `032.host-app-pack.gate0.identity-safety` evidence from `scripts/test-host-app-pack-smoke.sh`
- [X] T083 Build an external local test pack that binds a second command to the installed VS Code app without rebuilding Hideout in `test/host-app-packs/gate2-external/`
- [X] T084 Add and run a distinct `scripts/test-host-app-pack-e2e.sh` real macOS arm64 Lima Gate 2 wrapper for built-in `code`, external command, workspace/HostFS mapping, safe/elevated exact scope, unsafe app refusal, old-session/new-run behavior, disable/revoke no fallback, and redaction; reuse shared 030 projection helpers but leave the 030 evidence entrypoint and proof semantics unchanged
- [X] T085 Emit artifact-backed `032.host-app-pack.real-gate2.external` evidence and prove native/local/package-self-test/not-run fixtures cannot satisfy it in `internal/productevidence/host_app_pack.go` and `scripts/test-host-app-pack-e2e.sh`
- [X] T086 Execute all 12 scenarios in `specs/032-community-host-app-recipes/quickstart.md` and retain exact output/evidence references
- [X] T087 Run `go build ./...`, `go vet ./...`, `gofmt -l internal cmd`, `git diff --check`, `go test ./...`, markdownlint, package smoke, host-app-pack smoke, doc-truth smoke, Gate 0, and real Gate 2
- [X] T088 Perform adversarial review for guest-writable app RCE, package self-attestation, argv/settings safe bypass, cross-binding app selection, source TOCTOU, permission-fingerprint omissions, reserved-command collision, HostFS fixation, fallback, redaction theater, and test fixation; resolve every Blocking/High/Medium finding
- [X] T089 Re-run complete static and real-gate battery after review fixes and update evidence digests/source state honestly
- [X] T090 Mark `specs/032-community-host-app-recipes/spec.md` Implemented and all tasks complete only after 101/101, external-pack real Gate 2, package/docs truth, and adversarial audit are proven

**Retained 032 receipts (dirty private-alpha source at `644e6b53daaa`)**:

- Gate 0: `.hideout-release-evidence/host-app-pack-gate0-20260712T034500Z-644e6b53daaa/product-hardening-evidence.json`, manifest SHA-256 `824bde55deb8b5ea67c8411028b25fb28f0da0255fecff8d61440fdcb08416dd`.
- Real Gate 2: `.hideout-release-evidence/host-app-pack-20260712T033608Z-644e6b53daaa/product-hardening-evidence.json`, manifest SHA-256 `a570514909514cd79d39493d58ec69e923bca39aa5f4ec31305181b68b536f83`, public log SHA-256 `52d8b695371d18fc06a24471b0c7ea2aaa06ff95f001f98d98b3ed26b46ad937`.

---

## Dependencies And Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: Starts immediately.
- **Foundational (Phase 2)**: Depends on Setup; blocks all stories.
- **US1 (Phase 3)**: Depends on Foundational; MVP and generic runtime path.
- **US2 (Phase 4)**: Depends on US1 binding/lifecycle, but its test files can be authored in parallel after Foundation.
- **US3 (Phase 5)**: Depends on US1 resource/provider path and US2 exact decision identity.
- **US4 (Phase 6)**: Depends on Foundation and US1 lifecycle; scaffold/test work can proceed parallel with US2.
- **Polish (Phase 7)**: Depends on all stories. Real Gate 2 and completion audit are mandatory.

### User Story Dependency Graph

```text
Setup -> Foundational -> US1 (MVP)
                         |-> US2 -> US3
                         |-> US4
US2 + US3 + US4 -> Polish + real Gate 2 + adversarial audit
```

### Parallel Opportunities

- T002-T004 can run in parallel after T001.
- T007, T010, T012, T014, T016, T018, and T020 target independent foundational test files.
- US1 test tasks T023-T030 are parallel after Foundation.
- US2 tests T045-T050 are parallel after the US1 model contracts stabilize.
- US3 tests T061-T063 and US4 tests T070-T072 are independent.
- Package/docs/drift work T078-T081 is parallel after story behavior freezes.

## Implementation Strategy

### MVP First

1. Complete Setup and Foundational source/model work.
2. Complete US1 with one safe external recipe, one profile, workspace resources,
   new-run shim compilation, immutable binding dispatch, and built-in VS Code
   migration.
3. Validate no app-specific generic branch or fallback remains before adding
   elevated trust or HostFS.

### Incremental Delivery

1. **US1**: Low-friction safe recipe path through generic Core.
2. **US2**: Honest unverified/elevated trust, updates, decisions, inspection,
   disable/revoke, redaction.
3. **US3**: Existing HostFS authority consumption.
4. **US4**: Contributor scaffold and quality tests.
5. **Polish**: Package/docs truth, external real Gate 2, adversarial review.

No story is declared complete from source presence or static grep. Each
checkpoint requires the listed behavior tests; the feature requires retained
real host-effect evidence.

---

## Phase 8: Convergence

**Purpose**: Correct adversarial gaps found in Foundation and US1 work that was
already marked complete; these tasks are additive and do not replace the
unfinished story tasks above.

- [X] T091 Require a Core-owned trusted macOS signing requirement or platform trust verdict before classifying an app as `verified`; treat a bundle that only satisfies its own designated requirement as unverified and add real observer-command/classification regressions in `internal/hostcap/appidentity_darwin.go` and tests per FR-011/SC-004 (contradicts)
- [X] T092 Bind every Core safety-profile match to the exact reviewed executable-relative path and executable code identity, so another helper inside a legitimate signed bundle cannot inherit unrelated safe argv/settings semantics in `internal/hostcap/appopen/`, safety data, and tests per FR-013/FR-014/SC-005 (partial)
- [X] T093 Close resource-and-application check/use races at the final host launch boundary by revalidating canonical workspace/HostFS ownership and the exact executable identity immediately before effect, with replacement and symlink-retarget race tests in `internal/hostcap/` and broker/provider tests per FR-010/FR-023/SC-004/SC-010 (contradicts)
- [X] T094 Replace the structurally ambiguous tree-digest stream with a versioned length-framed encoding, bound exact Git intake before download with time/object/byte limits, count empty local directories, and add collision/resource-exhaustion regressions in `internal/packsnapshot/` per FR-002/FR-003/SC-009 (contradicts)
- [X] T095 Make host-app JSON Schemas and Go validation bidirectionally equivalent for executable paths and Core-produced bundle-tree identity values, including production-generated positive fixtures and differential negative cases in `schemas/host-app-*.schema.json` and `internal/hostapppack/schema_test.go` per FR-006/SC-012 (contradicts)
- [X] T096 Persist optional recipe-test `not-run` honestly and keep it quality-only rather than an authority prerequisite, while preserving passed/failed distinction in `internal/hostapppack/` and Manager add/enable tests per FR-031 (contradicts)
- [X] T097 Make enablement and runtime source consumption use one already-verified immutable revision snapshot rather than reopening mutable manifest files after digest validation; add concurrent replacement tests in `internal/hostapppack/store.go` per FR-002/SC-009 (partial)
- [X] T098 Enforce the 64 projected-command limit across the complete profile merge, not once per pack, and reject the sixty-fifth combined command with multi-pack tests in `internal/cmdproxy/hostapp_registry.go` per FR-006/FR-017 (partial)
- [X] T099 Re-run the complete static, race, package, documentation-truth, redaction, and real Gate 2 battery after T091-T098; mark the feature Implemented only when all 101 tasks and retained external-pack evidence pass per FR-033/SC-014 (partial)

## Phase 9: Convergence

**Purpose**: Remove the remaining application-specific parallel runtime path
found after local quickstart execution; this task is additive and does not
replace the unfinished real-gate work above.

- [X] T100 Derive built-in binding, safety, fingerprint, inspection, and shim grammar facts exclusively from the embedded host-app pack plus Core safety catalog; delete the legacy `apps.json`/`ResolveApp` registry and code-specific missing-binding fallback, and prove absent immutable binding metadata fails closed with no app-specific generic branch per FR-028/T041 (contradicts)
- [X] T101 Synchronize the Core capability descriptor with the unbound open-resource v2 schema and both authoritative resource classes (`workspace`, `hostfs-portal`), with registry drift tests, per FR-022/SC-012 (contradicts)
