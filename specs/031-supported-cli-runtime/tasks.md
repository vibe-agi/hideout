# Tasks: Supported CLI Runtime

<!-- markdownlint-disable MD013 MD060 -->

**Input**: Design documents from `specs/031-supported-cli-runtime/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`, `contracts/`, `quickstart.md`

**Tests**: Required. This feature changes environment identity, backend
execution order, package artifacts, recovery, evidence, and real network/backend
claims. Test tasks precede implementation tasks and include positive,
fail-closed, redaction, and real-backend proof.

**Organization**: Tasks are grouped by user story. The separately published
runtime asset is not complete merely because local/unit tests pass.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Establish schemas, package locations, image-build inputs, and proof
identifiers without changing product behavior.

- [X] T001 [P] Add strict runtime catalog and verification schemas in `schemas/runtime-catalog.schema.json` and `schemas/runtime-verification.schema.json`
- [X] T002 [P] Create catalog/contract source skeleton and Go package layout in `internal/runtimecatalog/catalog.json`, `internal/runtimecatalog/contract.json`, and `internal/runtimecatalog/doc.go`
- [X] T003 [P] Create verification package layout in `internal/runtimeverify/doc.go`
- [X] T004 [P] Create reviewed image-build input layout in `runtime/developer-standard/README.md`, `runtime/developer-standard/packages.txt`, and `runtime/developer-standard/sources.lock.json`
- [X] T005 [P] Add runtime catalog/contract package locations to `scripts/package-local.sh` and package kind definitions in `internal/packagekit/manifest.go`
- [X] T006 Register both new schemas and a runtime smoke entrypoint in `scripts/test-gate0.sh`
- [X] T007 [P] Register stable 031 proof IDs and artifact policies in `internal/productevidence/runtime.go`, `internal/productevidence/aggregate.go`, and `internal/productevidence/registry_test.go`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Implement immutable catalog truth, additive provenance, host-only
receipts, recovery, build mechanics, and short-root image testing shared by all
stories.

**CRITICAL**: No user story work begins until this phase is complete.

- [X] T008 [P] Write failing catalog parse/resolution/safety tests for duplicate IDs, unknown fields, moving URLs, architecture ambiguity, shell-like probes, withdrawn revisions, and deterministic provenance in `internal/runtimecatalog/catalog_test.go` (FR-001, FR-002, FR-004, FR-006; SC-002, SC-013)
- [X] T009 Implement embedded catalog/contract parsing, strict validation, host-tuple resolution, digesting, and inspection views in `internal/runtimecatalog/catalog.go` and `internal/runtimecatalog/contract.go`
- [X] T010 [P] Write failing additive profile/environment provenance and no-inference tests in `internal/profile/profile_test.go` and `internal/environment/environment_test.go` (FR-003, FR-005, FR-011; SC-010)
- [X] T011 Implement optional `RuntimeSelection` and `RuntimeProvenance` records with clone/validation support in `internal/profile/profile.go`, `internal/environment/environment.go`, and `schemas/profile.schema.json`
- [X] T012 [P] Write failing receipt integrity, atomic storage, mismatch, stopped-status, bounded-output, and redaction tests in `internal/runtimeverify/model_test.go`, `internal/runtimeverify/store_test.go`, and `internal/runtimeverify/status_test.go` (FR-008, FR-010, FR-011, FR-017)
- [X] T013 Implement verification models, 0600 atomic environment-side receipt store, shared status builder, and redaction in `internal/runtimeverify/model.go`, `internal/runtimeverify/store.go`, and `internal/runtimeverify/status.go`
- [X] T014 [P] Add registry-backed runtime recovery records and typo-rejection tests in `internal/recovery/registry.go`, `internal/recovery/registry_test.go`, and `internal/doctor/report_test.go` (FR-004, FR-009, FR-015; SC-007)
- [X] T015 [P] Write production-route inventory tests for runtime catalog/status/verify routes in `internal/manager/api_routes_test.go` and `internal/daemon/routes_test.go`
- [X] T016 Fix declared-image smoke to use a short `/tmp` Lima root and assert cleanup in `scripts/test-env-image.sh` (FR-019)
- [X] T017 [P] Write source-lock, package-list, secret-fixture, native-architecture, and output-contract tests in `runtime/developer-standard/test-build.sh`
- [X] T018 Implement candidate build, resize, customization, cleanup, inventory, SBOM-status, checksum, provenance, and offline verification in `runtime/developer-standard/build.sh` and `runtime/developer-standard/verify-image.sh`
- [X] T019 Add a native Linux arm64 candidate-build workflow and explicit non-promoting artifact upload in `.github/workflows/runtime-developer-standard.yml`
- [X] T020 Add package-copy/checksum/schema parity tests for embedded and installed runtime metadata in `internal/packagekit/packagekit_test.go` and `scripts/test-package-smoke.sh`

**Checkpoint**: Synthetic catalogs and receipts are trustworthy; the image build
can create a candidate but no product preview claim exists yet.

---

## Phase 3: User Story 1 - Start With A Dependable Developer Runtime (Priority: P1) MVP

**Goal**: Explicitly select one immutable runtime, create a pinned environment,
observe the real guest baseline, and expose honest preview status.

**Independent Test**: From an empty store on macOS arm64, select
`developer-standard`, create/boot the environment without typing an image URL,
execute every baseline command as non-root/no-sudo, and inspect matching
provenance/status.

### Tests For User Story 1

- [X] T021 [P] [US1] Write failing init and environment selection plan/apply tests for explicit runtime, `--runtime`/`--image` conflict, unsupported host, withdrawn revision, catalog drift, insufficient/indeterminate disk before `limactl start`, and zero false-ready records in `internal/inittask/inittask_test.go`, `internal/manager/environment_lifecycle_test.go`, and `internal/app/app_test.go` (FR-001, FR-003, FR-004, FR-005; SC-001, SC-002, SC-007)
- [X] T022 [P] [US1] Write failing runtime catalog CLI and Manager route tests in `internal/app/app_test.go`, `internal/manager/api_test.go`, and `internal/manager/api_routes_test.go` (FR-010)
- [X] T023 [P] [US1] Write failing target PATH tests proving shims-first, durable `$HOME/.local/bin`, system paths, and no host PATH in `internal/backend/lima/lima_test.go` (FR-013, FR-016, FR-020; SC-011)
- [X] T024 [P] [US1] Write failing Lima runtime-probe tests for bounded direct argv, target identity, boundary block, baseline degradation, cancellation, output limits, and unchanged exact command check in `internal/backend/lima/runtime_test.go` (FR-007, FR-008, FR-009, FR-012, FR-017; SC-003, SC-005)
- [X] T025 [P] [US1] Write failing Manager run integration tests for receipt binding, visible degradation, unrelated-command continuation, aggregate audit, and no target side effect after boundary failure in `internal/manager/run_apply_test.go` (FR-008, FR-010, FR-011, FR-017)
- [X] T026 [P] [US1] Write runtime-image acceptance smoke for real tools, sizes, privilege, wrong digest, and a runtime Gate 2 mode that fails if `prepare_guest_node` or any guest package installation runs in `scripts/test-runtime-lima.sh` and `scripts/test-gate2-lima.sh` (FR-007, FR-012, FR-018, FR-019; SC-003, SC-004, SC-008, SC-012)

### Implementation For User Story 1

- [X] T027 [US1] Extend typed init/profile planning and CLI parsing with explicit runtime selection in `internal/inittask/inittask.go`, `internal/app/app.go`, and `schemas/init-plan.schema.json`
- [X] T028 [US1] Extend environment create planning/apply with resolved runtime provenance, `--runtime`/`--image` mutual exclusion, and pre-download free-space fail-closed checks in `internal/manager/environment_lifecycle.go`, `internal/manager/run_environment.go`, and `internal/app/app.go`
- [X] T029 [US1] Implement catalog list/inspect Core methods, Manager routes, daemon inventory, API schema, and CLI rendering in `internal/manager/runtime.go`, `internal/manager/api.go`, `internal/manager/routes.go`, `internal/daemon/routes.go`, `schemas/manager-api.schema.json`, and `internal/app/app.go`
- [X] T030 [US1] Add the durable guest user executable prefix without importing host PATH in `internal/backend/lima/lima.go`
- [X] T031 [US1] Implement generic runtime contract and result types plus Lima target-identity observation before network/HostFS setup in `internal/backend/runtime.go`, `internal/backend/backend.go`, and `internal/backend/lima/runtime.go`
- [X] T032 [US1] Wire validated contract, privilege-bound receipt sink, warning/audit/notice, and existing exact-command recovery through `internal/manager/run_apply.go` and `internal/manager/run_environment.go`
- [X] T033 [US1] Build a clean `developer-standard` candidate and record candidate artifacts under the ignored `dist/runtime/` path using `runtime/developer-standard/build.sh`
- [X] T034 [US1] Review candidate inventory, licenses, source retention, SBOM status, secret scan, size, QCOW2 check, and clean boot report; fix build inputs until `runtime/developer-standard/verify-image.sh` passes (FR-002, FR-017, FR-019; SC-004, SC-009, SC-012)
- [X] T035 [US1] Promote the reviewed candidate to a retained versioned HTTPS release asset, re-download it on a clean path, verify SHA-256, and record promotion evidence in `dist/runtime/promotion.json`
- [X] T036 [US1] Bind the promoted URL/digests and exact contract in `internal/runtimecatalog/catalog.json` and `internal/runtimecatalog/contract.json`; keep `runtime/developer-standard/sources.lock.json` byte-identical to the build input and bind its candidate-provenance digest rather than introducing a circular output lock
- [X] T037 [US1] Run `scripts/test-runtime-lima.sh` against the promoted artifact and write `031.runtime-real-image` and `031.runtime-baseline` product evidence with the registered typed runtime-provenance binding (FR-018, FR-019; SC-003, SC-004, SC-008, SC-012)

**Checkpoint**: US1 is independently usable only after T035-T037. A local
candidate or synthetic catalog does not satisfy the MVP.

---

## Phase 4: User Story 2 - Install A Real Agent Through The Privacy Network (Priority: P2)

**Goal**: Install one exact real agent package from an empty cache into the
durable target prefix through mediated DNS/tun2socks and execute its version.

**Independent Test**: In the promoted runtime with an empty npm cache, install
`@openai/codex@0.144.1`, execute `codex --version`, and prove the registry path
used mediated DNS/HTTPS with no proxy credential exposure.

### Tests For User Story 2

- [X] T038 [P] [US2] Add exact Codex version, npm integrity, arm64 optional-package, empty-cache, target-owner, and no-sudo assertions to `scripts/test-runtime-agent-install.sh` (FR-013, FR-016; SC-006)
- [X] T039 [P] [US2] Add deterministic network-denied, DNS-failed, registry-failed, and unwritable-prefix recovery fixtures in `internal/app/runtime_recovery_test.go` and `scripts/test-runtime-smoke.sh` (FR-015; SC-007)
- [X] T040 [P] [US2] Add target/public-evidence secret scans using true proxy/control-plane credential fixtures in `scripts/test-runtime-agent-install.sh` (FR-014, FR-017; SC-009)

### Implementation For User Story 2

- [X] T041 [US2] Add provider-neutral runtime install failure classification at the CLI/gate boundary without package guessing in `internal/app/runtime_recovery.go` and `internal/recovery/registry.go`
- [X] T042 [US2] Add the canonical non-root `$HOME/.local` Codex install/version command and explicit no-login note to `README.md`, `README.zh-CN.md`, and `docs/first-run-alpha.md`
- [X] T043 [US2] Integrate clean-cache real agent installation into the existing controlled proxy/DoH gate in `scripts/test-gate3-hidden-proxy.sh` without weakening connected-subnet reverse proof
- [X] T044 [US2] Run the direct real-agent smoke and privacy Gate 3 against the same promoted runtime digest, then emit `031.runtime-agent-install` and `031.runtime-agent-privacy` evidence (FR-013, FR-014, FR-018; SC-006, SC-008, SC-009)

**Checkpoint**: The real agent install works through the product privacy path;
interactive login remains explicitly out of scope.

---

## Phase 5: User Story 3 - Understand Runtime And Command Readiness (Priority: P2)

**Goal**: Provide one authoritative runtime status and recovery model across CLI,
doctor, Manager, environment inspection, and run behavior.

**Independent Test**: Remove one baseline command from a disposable real guest,
then verify all surfaces agree on failed status while an unrelated command may
run and the missing exact command cannot.

### Tests For User Story 3

- [X] T045 [P] [US3] Write failing runtime verify plan/apply tests for environment locking, no target command, no repair, stopped/start behavior, receipt replacement, cancellation, and provenance mismatch in `internal/manager/runtime_test.go` (FR-008, FR-010, FR-011)
- [X] T046 [P] [US3] Write failing CLI/doctor/Manager parity tests for every status and recovery field in `internal/app/app_test.go`, `internal/doctor/report_test.go`, and `internal/manager/api_test.go` (FR-010, FR-011, FR-015; SC-007, SC-010)
- [X] T047 [P] [US3] Write mutable-guest drift and exact-command no-execution regression tests in `scripts/test-runtime-lima.sh` and `internal/manager/run_apply_test.go` (FR-008, FR-009; SC-005)

### Implementation For User Story 3

- [X] T048 [US3] Implement typed runtime verify plan/apply Core methods and API routes in `internal/manager/runtime.go`, `internal/manager/api.go`, and `internal/manager/routes.go`
- [X] T049 [US3] Implement `hideout runtime verify --env`, runtime fields in environment inspection, and machine/human renderers in `internal/app/app.go`
- [X] T050 [US3] Add doctor `--feature runtime` from the shared status/recovery builder in `internal/app/app.go` and `internal/doctor/report.go`
- [X] T051 [US3] Add runtime status/provenance to Manager overview and bounded Boundary Summary in `internal/manager/manager.go` and `internal/manager/boundary_summary.go`
- [X] T052 [US3] Map the existing `backend.CommandNotFoundError` to `runtime.command.missing` only for catalog-selected environments in `internal/manager/run_apply.go` and `internal/app/app.go`
- [X] T053 [US3] Run the drift/readiness matrix and emit `031.runtime-readiness-parity` evidence from `scripts/test-runtime-lima.sh`

**Checkpoint**: Readiness is actual-guest truth, not catalog or stale receipt
truth, and every operator surface agrees.

---

## Phase 6: User Story 4 - Preserve Existing Security And Evidence Boundaries (Priority: P3)

**Goal**: Prove runtime convenience changes guest data only and all promoted
boundary claims remain true on the exact image.

**Independent Test**: Run complete real Gate 2 and Gate 3 against the promoted
revision and verify matching image/provenance plus zero authority delta and
secret leakage.

### Tests For User Story 4

- [X] T054 [P] [US4] Add effective-policy before/after comparison covering HostFS, network, endpoint, host-app, command-proxy, scripts, and privilege in `internal/manager/runtime_test.go` (FR-020; SC-011)
- [X] T055 [P] [US4] Add malformed receipt, native false-green, custom-image false-promotion, and injected credential fixtures in `internal/runtimeverify/status_test.go` and `internal/productevidence/redaction_test.go` (FR-017, FR-018; SC-009, SC-010)
- [X] T056 [P] [US4] Extend release/product-evidence evaluator tests to require exact runtime revision, image digest, a valid per-proof environment ID, candidate commit/dirty state, Gate 2, and Gate 3 while allowing the two independent real gates to use distinct environment IDs in `internal/productevidence/evaluate_test.go` and `internal/releasecompat/readiness_test.go` (FR-018; SC-008)

### Implementation For User Story 4

- [X] T057 [US4] Bind runtime fields into Gate 2/Gate 3 manifests and release evidence without accepting native/local fixtures in `scripts/lib/gate-result.sh`, `scripts/test-gate2-lima.sh`, `scripts/test-gate3-hidden-proxy.sh`, and `internal/productevidence/runtime.go`
- [X] T058 [US4] Run complete real Gate 2 against the promoted digest with guest package preparation disabled, including HostFS visibility/read/write, projection, privilege, cleanup, and environment lifecycle; emit `031.runtime-boundary-regression`
- [X] T059 [US4] Run complete real Gate 3 against that same digest and assert DoH forward proof, connected-subnet reverse block, agent registry HTTPS, and enforced privilege
- [X] T060 [US4] Aggregate exact-image Gate 2/Gate 3/product evidence and prove absent, stale, dirty, mismatched, native, or failed evidence cannot produce preview readiness in `internal/productevidence/evaluate.go` and `internal/releasecompat/readiness.go`

**Checkpoint**: Runtime convenience has real-backend evidence and no broadened
authority; preview still does not mean supported or release-ready.

---

## Phase 7: Polish And Cross-Cutting Concerns

**Purpose**: Distribution, claim discipline, docs, and final verification.

- [X] T061 [P] Update runtime design, current status, gate topology, and preview/non-claims in `docs/privacy-run-design.md`, `docs/STATUS.md`, `docs/privacy-run-test-plan.md`, `docs/threat-model.md`, and `docs/claim-boundaries.md`
- [X] T062 [P] Update package/docs indexes and product quickstart in `docs/README.md`, `README.md`, and `README.zh-CN.md`
- [X] T063 [P] Add runtime catalog, verification, build provenance, and promotion examples to `docs/command-examples.json` and documentation-truth tests in `scripts/test-doc-truth-smoke.sh`
- [X] T064 Add runtime schemas/catalog/contracts/build inputs to package layout, install/repair/uninstall verification, and stale-file handling in `scripts/package-local.sh`, `internal/packagekit/verify.go`, and `internal/packagekit/repair.go`
- [X] T065 Add `scripts/test-runtime-smoke.sh` to Gate 0 and prove synthetic catalogs cannot satisfy real-image proof
- [X] T066 Run `go build ./...`, `go vet ./...`, `gofmt -l internal cmd`, `git diff --check`, `go test ./...`, markdownlint, package smoke, runtime smoke, and `scripts/test-gate0.sh`
- [ ] T067 Execute all 12 scenarios in `specs/031-supported-cli-runtime/quickstart.md` and record exact outputs/evidence references
- [X] T068 Perform an adversarial implementation review for false-green catalog status, mutable-guest drift, target-writable evidence, hidden first-boot provisioning, secret-bearing image state, wrong-architecture fallback, and evidence fixation; resolve every Blocking/High/Medium finding
- [ ] T069 Re-run the complete static battery and required real Gate 2/Gate 3 after review fixes; update evidence digests and source state honestly
- [ ] T070 Mark `specs/031-supported-cli-runtime/spec.md` Implemented and all tasks complete only when the retained asset, 88 tasks, real gates, package/docs truth, and completion audit are all proven

---

## Dependencies And Execution Order

### Phase Dependencies

- Setup has no dependency.
- Foundational depends on Setup and blocks every story.
- US1 depends on Foundational and on external promotion for its final checkpoint.
- US2 and US3 depend on US1's promoted runtime; their unit fixtures can be
  written earlier but their independent tests cannot pass before US1.
- US4 depends on US1-US3 because it validates the integrated product.
- Polish depends on all stories.

### User Story Dependencies

- **US1 (P1)**: Catalog, selection, real image, baseline; MVP.
- **US2 (P2)**: Uses US1 runtime and existing privacy network; no new package
  provider.
- **US3 (P2)**: Uses US1 catalog/receipts; independently validates drift/status.
- **US4 (P3)**: Cross-feature real-backend evidence after US1-US3.

### Parallel Opportunities

- T001-T004 and T007 use separate files.
- T008, T010, T012, T014, T015, and T017 are independent failing-test tasks.
- US1 failing tests T021-T026 can be authored in parallel.
- US2 tests T038-T040, US3 tests T045-T047, and US4 tests T054-T056 use
  separate files.
- Docs T061-T063 can run in parallel after behavior stabilizes.
- Real image build/promotion/gates are intentionally serial.

## Parallel Example: User Story 1

```text
T021: init/environment selection tests
T022: catalog CLI/Manager tests
T023: target PATH tests
T024: Lima probe tests
T025: Manager run integration tests
T026: real runtime acceptance script
```

## Implementation Strategy

### MVP First

1. Complete T001-T020.
2. Complete T021-T032 with synthetic test catalogs.
3. Build/review/promote the real image in T033-T036.
4. Complete T037 and independently validate US1.
5. Do not call the local/synthetic state an MVP before T035-T037.

### Incremental Delivery

1. US1: dependable runtime and real baseline.
2. US2: real agent installation through privacy networking.
3. US3: current readiness and recovery parity.
4. US4: integrated boundary proof.
5. Polish: package, docs, review, full evidence.

## Notes

- `[P]` means different files and no incomplete dependency.
- Every completed task must be marked `[X]`; do not bulk-mark unverified work.
- Dirty development evidence is useful but cannot satisfy promotion or final
  preview proof.
- GitHub/release credentials are an external prerequisite only for T035; all
  earlier engineering work remains executable without them.
- Long real-image and Gate commands require progress updates before execution.

## Phase 8: Convergence

**Purpose**: Close adversarial-review gaps that allowed local or asserted state
to masquerade as exact real-runtime readiness.

- [X] T071 Require an explicit trusted runtime family/expectation and all registered 031 real proofs before `releaseReady`; reject minimal fabricated or arbitrary non-native gate JSON in `internal/releasecompat/readiness.go`, `internal/app/app.go`, and tests per FR-018/SC-008 (contradicts)
- [X] T072 Bind runtime proof freshness to promoted catalog/package commit, exact artifact digests, clean source state, and existing digest-verified artifacts rather than caller notes or current-checkout overrides in `internal/productevidence/`, `internal/releasecompat/`, and tests per FR-018/FR-019/SC-008 (contradicts)
- [X] T073 Observe and bind the actual Lima instance, image origin/digest, guest architecture, and boot identity; reject reused or replaced instances whose observed identity cannot prove the selected revision in `internal/backend/lima/`, `internal/manager/run_runtime.go`, `internal/runtimeverify/`, and tests per FR-004/FR-008/SC-003 (partial)
- [X] T074 Invalidate historical receipts before every ordinary run/probe and after failed, canceled, cleaned-up, or boot-changed execution; make status freshness session/boot-bound and add post-target-drift regressions in `internal/manager/` and `internal/runtimeverify/` per FR-008/FR-010/SC-005 (contradicts)
- [X] T075 Revalidate current catalog membership, withdrawal, release URL/digest, architecture, and contract identity in runtime status and run attachment so removed or changed revisions cannot remain preview-ready in `internal/manager/runtime.go`, `internal/manager/run_runtime.go`, and tests per FR-005/FR-010/SC-010 (partial)
- [X] T076 Add a v1 promotion validator requiring the supported macOS arm64 Lima/VZ tuple and the complete named developer baseline; keep generic parsing from being mistaken for promotable support in `internal/runtimecatalog/`, schemas, and tests per FR-001/FR-007/SC-003 (partial)
- [X] T077 Make image verification fail closed on incomplete inspection and scan all SSH host keys, machine identity/cloud residue, private-key material, target/root agent auth state, and credential caches across retained filesystem roots with adversarial fixtures in `runtime/developer-standard/` per FR-002/FR-017/SC-009 (partial)
- [X] T078 Replace the false `runtime_guest_provisioning=not-run` claim with truthful evidence that distinguishes required Go-owned Hideout system bootstrap from prohibited package/tool provisioning, and pin both paths in Gate tests in `scripts/test-gate2-lima.sh` per FR-007/FR-018/SC-011 (contradicts)
- [X] T079 Suppress trusted runtime readiness whenever an unsafe workspace overlaps the Hideout store or can write receipt authority, even after explicit workspace override; add tamper regressions in `internal/manager/` and `internal/runtimeverify/` per Constitution I/IV and FR-010 (partial)
- [X] T080 Make Gate 0 exercise the actual embedded runtime catalog/contract and report an empty catalog as explicitly unpromoted rather than a usable passing runtime in `scripts/test-runtime-smoke.sh` and catalog CLI tests per FR-001/SC-002 (contradicts)
- [X] T081 Bind readiness to an independently observed active image identity rather than copying the expected Lima config URL/digest into the observation; reject replaced or mutated runtime disks in `internal/backend/lima/`, runtime receipts, and adversarial tests per FR-004/FR-008/SC-003 (contradicts)
- [X] T082 Consume build provenance as an authoritative runtime evidence input so a dirty image cannot be relabeled clean from another checkout, and bind Gate manifests to its exact commit/image digest in `internal/productevidence/`, `scripts/lib/gate-result.sh`, and tests per FR-018/FR-019/SC-008 (contradicts)
- [X] T083 Require one canonical candidate commit across promoted runtime, verified package, all required proof feature/claim identities, and release evidence; reject self-attested redaction and unknown fields with real secret fixtures in `internal/productevidence/`, `internal/releasecompat/`, and tests per FR-017/FR-018/SC-008/SC-009 (contradicts)
- [X] T084 Make offline image verification cover all retained executable/state roots and binary content, require executable/versioned baseline tools, strengthen the agent no-auth scan, and make runtime smoke execute behavioral catalog/promotion checks in `runtime/developer-standard/`, `internal/runtimecatalog/`, and `scripts/test-runtime-*.sh` per FR-002/FR-007/FR-017/SC-003/SC-009 (contradicts)
- [X] T085 Keep the runtime boundary contract active under unsafe-workspace override while suppressing only trusted receipt persistence, and preserve authoritative failed verification status/failed IDs without stale running-state inference in `internal/manager/run_runtime.go`, `internal/manager/runtime.go`, and tests per FR-008/FR-010/SC-005 (contradicts)
- [X] T086 Make the canonical phase/release lane run runtime-enabled Gate 2 and Gate 3 in independent managed environments against one exact runtime artifact and candidate, explicitly verify after ordinary runs, and pass trusted runtime/package/product evidence inputs to readiness in `scripts/test-phase1.sh`, `scripts/test-gate2-lima.sh`, `scripts/test-gate3-hidden-proxy.sh`, and `scripts/test-release-readiness.sh` per FR-018/SC-008 (contradicts)
- [X] T087 Reject vacuous externally supplied release-readiness JSON unless it contains exact local/product command outcomes and required Gate 2/Gate 3 rows in both Go validation and `schemas/release-readiness.schema.json` per FR-018/SC-008 (contradicts)
- [ ] T088 Re-run the complete 031 adversarial/static/real-gate battery after T081-T087, refresh clean provenance and evidence digests, and do not restore Implemented status until all external gates pass

## Phase 9: Provenance Convergence

**Purpose**: Remove the circular release identity assumption discovered while
producing a promoted runtime and a package that embeds its final catalog.

- [X] T089 Separate the promoted runtime image build identity from the verified Hideout package candidate identity throughout runtime bindings, product evidence, release readiness, schemas, scripts, contracts, and adversarial tests; require exact clean binding for both identities without requiring the image-producing commit to equal the later package commit per FR-018/FR-019/SC-008 (contradicts)
