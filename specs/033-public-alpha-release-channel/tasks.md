<!-- markdownlint-disable MD013 MD024 -->

# Tasks: Public Alpha Release Channel

**Input**: Design documents from
`/specs/033-public-alpha-release-channel/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`,
`contracts/`, `quickstart.md`

**Tests**: Release identity, evidence, signing observations, package ownership,
and public claims cross authority and evidence boundaries. Tests are required
before implementation for every user story.

**Organization**: Tasks are grouped by user story so each story can be
implemented and independently validated.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel because it changes different files and has no
  dependency on unfinished work in the same phase.
- **[Story]**: User story owning the task.
- Every task includes concrete file paths.

## Phase 1: Setup

**Purpose**: Establish the legal, package, schema, and release package layout.

- [X] T001 Add the official Apache-2.0 text to `LICENSE`
- [X] T002 Create the typed release-domain package skeleton in `internal/releasechannel/doc.go`
- [X] T003 [P] Add public release, evidence bundle, publication receipt, and published inventory schema skeletons in `schemas/public-release.schema.json`, `schemas/public-evidence-bundle.schema.json`, `schemas/publication-receipt.schema.json`, and `schemas/published-release-inventory.schema.json`
- [X] T004 [P] Add the candidate release inventory skeleton in `releases/current.json` and release-notes template in `.github/release-promotions/README.md`

---

## Phase 2: Foundational Identity And Evidence

**Purpose**: Introduce one canonical first-release product, source, archive,
and evidence identity without manufacturing compatibility to unpublished
internal artifacts.

**Critical**: This phase blocks every user story.

- [X] T005 [P] Write canonical package manifest/install-state identity and explicit unpublished-legacy rejection tests in `internal/packagekit/manifest_test.go` and `internal/packagekit/install_state_test.go`
- [X] T006 Implement strict canonical package manifest/install-state readers and writers in `internal/packagekit/manifest.go`, `internal/packagekit/install_state.go`, and `internal/packagekit/verify.go`
- [X] T007 [P] Write explicit PackageIdentity freshness and target-evaluation tests in `internal/productevidence/manifest_test.go` and `internal/productevidence/evaluate_test.go`
- [X] T008 Implement canonical product-evidence identity and `public-release` target composition in `internal/productevidence/manifest.go`, `internal/productevidence/evaluate.go`, and `internal/productevidence/registry.go`
- [X] T009 [P] Write canonical release-readiness identity, stale archive, and no-outer-digest fallback tests in `internal/releasecompat/readiness_test.go`
- [X] T010 Implement canonical release-readiness package/runtime/signing identity in `internal/releasecompat/readiness.go`
- [X] T011 [P] Write strict release identity, asset-set, path-containment, and redaction model tests in `internal/releasechannel/release_test.go` and `internal/releasechannel/bundle_test.go`
- [X] T012 Implement shared release identity, strict JSON decoding, artifact hashing, containment, and bounded archive primitives in `internal/releasechannel/identity.go`, `internal/releasechannel/json.go`, and `internal/releasechannel/artifact.go`
- [X] T013 [P] Add stable release and package compatibility recovery records with executable next actions in `internal/recovery/registry.go` and tests in `internal/recovery/registry_test.go`
- [X] T014 Replace unpublished legacy package, evidence, and readiness shapes with one strict canonical v1 in `schemas/package-manifest.schema.json`, `schemas/product-hardening-evidence.schema.json`, and `schemas/release-readiness.schema.json`
- [X] T015 Register all new and updated schemas in `scripts/test-gate0.sh` and schema contract tests under `internal/schemautil/`
- [X] T016 Export the proof registry as machine-readable JSON and register all seven `033.release.*` proof IDs and claim mappings in `internal/productevidence/registry.go`, `internal/productevidence/catalog.go`, and `docs/claim-boundaries.md`

**Checkpoint**: Explicit identity and evidence models are strict, migrated, and
shared by all later stories.

---

## Phase 3: User Story 1 - Install One Official Alpha Package (P1) MVP

**Goal**: Install and inspect one versioned package without Go, source state,
profile creation, daemon startup, or hidden prerequisite installation.

**Independent Test**: Run the package contract and clean-install harness in a
fresh HOME with `--skip-init`, then inspect version, package, and doctor output.

### Tests

- [X] T017 [P] [US1] Write binary identity JSON and version mismatch tests in `internal/app/app_test.go`
- [X] T018 [P] [US1] Write canonical package archive, license/notices inventory, full-commit, and no-profile install tests in `internal/packagekit/package_test.go`
- [X] T019 [P] [US1] Create a clean-install E2E harness with no Go/source/developer PATH in `scripts/test-public-alpha-clean-install.sh`

### Implementation

- [X] T020 [US1] Add `hideout version --json` with canonical product version, full source commit, build time, host OS, and host architecture in `internal/app/app.go`
- [X] T021 [US1] Embed full 40-hex commit and explicit product version/channel into binaries, and add separate `stage-tree` and `finalize` modes in `scripts/install-local.sh` and `scripts/package-local.sh`
- [X] T022 [US1] Stage package content, then generate the canonical package manifest and final archive only after signing while including `LICENSE`, notices, security guidance, runtime catalog, schemas, binaries, and helpers in `scripts/package-local.sh`
- [X] T023 [US1] Preserve install/profile separation and expose typed prerequisite recovery during packaged install in `internal/packagekit/install.go`, `internal/app/app.go`, and package `install.sh`
- [X] T024 [P] [US1] Document exact versioned download, checksum, install, verify, and doctor commands in `docs/distribution-bootstrap.md` and `docs/first-run-alpha.md`
- [X] T025 [US1] Wire the clean-install harness into `scripts/test-gate0.sh` as a local contract lane that does not claim real Lima success

**Checkpoint**: A package can be inspected and installed independently of the
source tree and profile lifecycle.

---

## Phase 4: User Story 2 - Prove The Download Is The Tested Candidate (P1)

**Goal**: Bind public bytes to signing, notarization, real gates, exact runtime,
registered proofs, and anonymous post-public download.

**Independent Test**: Validate the four-asset release set and evidence bundle,
then mutate every identity and gate input and prove release readiness fails.

### Tests

- [X] T026 [P] [US2] Write public release manifest and exact asset-set mutation tests in `internal/releasechannel/release_test.go`
- [X] T027 [P] [US2] Write evidence bundle size, link, traversal, digest, proof, redaction, and local-path rejection tests in `internal/releasechannel/bundle_test.go`
- [X] T028 [P] [US2] Write signing and notarization observation tests for Developer ID, timestamp, hardened runtime, accepted online result, preview status, and credential redaction in `internal/releasechannel/signing_test.go`
- [X] T029 [P] [US2] Write publication receipt and anonymous-download identity tests in `internal/releasechannel/receipt_test.go`
- [X] T030 [P] [US2] Add no-publish workflow fault fixtures for failed gate, absent approval, partial asset set, digest drift, and rebuild attempts in `scripts/test-public-alpha-release.sh`

### Implementation

- [X] T031 [US2] Implement strict public release manifest and four-asset allowlist validation in `internal/releasechannel/release.go`
- [X] T032 [US2] Implement bounded evidence bundle build/validate with registered proofs and existing export decisions in `internal/releasechannel/bundle.go`
- [X] T033 [US2] Implement independently observed signing/notarization models and macOS observers in `internal/releasechannel/signing.go` and `internal/releasechannel/signing_darwin.go`
- [X] T034 [US2] Implement publication receipt validation, immutable-release observation, and anonymous download digest checks in `internal/releasechannel/receipt.go`
- [X] T035 [US2] Add `hideout support release validate`, `validate-signing`, and `validate-notarization` commands in `internal/app/app.go`
- [X] T036 [US2] Extend `hideout support readiness` to accept exact package archive, signing, notarization, and public evidence inputs in `internal/app/app.go`
- [X] T037 [US2] Stage the candidate tree once, sign it, finalize its manifest/archive without mutating signed files, observe and notarize the same finalized frozen tree, retain a draft, and upload no credential-bearing logs in `.github/workflows/hideout-alpha-candidate.yml`
- [X] T038 [US2] Add independent real-mac candidate proof ingestion, protected approval, immutable publication, anonymous verification, and a receipt-bound inventory/docs pull request in `.github/workflows/hideout-alpha-promote.yml`
- [X] T039 [US2] Implement the exact candidate/real-gate evidence runner and cleanup report in `scripts/test-public-alpha-candidate.sh`
- [X] T040 [US2] Generate and validate `SHA256SUMS`, release manifest, evidence archive, and publication receipt in `scripts/test-public-alpha-release.sh`

**Checkpoint**: A human approval cannot publish bytes unless every machine gate
and exact-byte check passes.

---

## Phase 5: User Story 3 - Reach A First Successful Lima Run (P2)

**Goal**: Reach one direct Lima run from the installed package while preserving
the separate real privacy Gate 3 claim.

**Independent Test**: Use a fresh packaged install and exact retained runtime to
run `pwd` in direct mode, then run Gate 3 separately with no direct fallback.

### Tests

- [X] T041 [P] [US3] Extend clean-install E2E to assert explicit init, non-root Lima/direct `pwd`, exact runtime identity, and no privacy claim in `scripts/test-public-alpha-clean-install.sh`
- [X] T042 [P] [US3] Add exact package/runtime binding and distinct Gate 2/Gate 3 environment tests in `internal/releasecompat/readiness_test.go`

### Implementation

- [X] T043 [US3] Record package archive identity and retained runtime identity in clean-install and real-gate evidence in `scripts/test-public-alpha-clean-install.sh` and `scripts/test-public-alpha-candidate.sh`
- [X] T044 [US3] Fail closed on privacy-profile prerequisite absence without direct fallback in `internal/app/app.go` and `internal/manager/run_plan.go`
- [X] T045 [P] [US3] Document direct first success, the separate approximately 1 GB runtime, privacy follow-up, agent install, and safe `code .` walkthrough in `docs/first-run-alpha.md`
- [X] T046 [US3] Add candidate assertions for the existing real Gate 2 and Gate 3 scripts to `scripts/test-public-alpha-candidate.sh`

**Checkpoint**: Packaged first success is real but cannot be misread as the
privacy-network proof.

---

## Phase 6: User Story 4 - Recover Or Remove Without Losing State (P2)

**Goal**: Reinstall, repair, uninstall, and migrate package state without
silently mutating durable operator data.

**Independent Test**: Reinstall, introduce package-owned drift, repair, normal
uninstall, explicit purge, v1 migration, and unsupported downgrade fixtures.

### Tests

- [X] T047 [P] [US4] Add unpublished-legacy rejection, same-version idempotence, downgrade refusal, and store-preservation tests in `internal/packagekit/install_test.go` and `internal/packagekit/migration_test.go`
- [X] T048 [P] [US4] Extend package smoke for packaged reinstall, dry-run repair, apply, normal uninstall, and purge audit in `scripts/test-package-smoke.sh`

### Implementation

- [X] T049 [US4] Implement canonical install-state handling, explicit unpublished-legacy rebuild guidance, and fail-before-mutation downgrade decisions in `internal/packagekit/migration.go` and `internal/packagekit/install.go`
- [X] T050 [US4] Keep repair and uninstall restricted to proven package-owned paths while preserving the store by default in `internal/packagekit/repair.go` and `internal/packagekit/uninstall.go`
- [X] T051 [US4] Render stable package migration/recovery records in CLI and doctor output in `internal/app/app.go` and `internal/doctor/report.go`
- [X] T052 [P] [US4] Document reinstall, repair, uninstall, purge, state preservation, and N-1 policy in `docs/distribution-bootstrap.md`

**Checkpoint**: Package lifecycle recovery is bounded and durable state remains
operator-owned.

---

## Phase 7: User Story 5 - Know What The Alpha Does Not Promise (P3)

**Goal**: Derive every public maturity, platform, and non-claim statement from
one verified published inventory.

**Independent Test**: Compare human and JSON support output with candidate and
public docs; removing a required entry, non-claim, proof, or receipt fails.

### Tests

- [X] T053 [P] [US5] Write published inventory strict-schema and candidate/public lifecycle tests in `internal/releasechannel/inventory_test.go`
- [X] T054 [P] [US5] Add support matrix human/JSON parity and required non-claim tests in `internal/releasecompat/matrix_test.go` and `internal/app/app_test.go`
- [X] T055 [P] [US5] Extend docs-truth smoke with candidate-publication and anonymous-receipt negative fixtures in `scripts/test-doc-truth-smoke.sh`

### Implementation

- [X] T056 [US5] Implement strict published inventory loading and receipt-derived updates in `internal/releasechannel/inventory.go` and `releases/current.json`
- [X] T057 [US5] Add public-alpha package, runtime, 029-032, signing, and unsupported-platform rows plus human non-claims in `internal/releasecompat/matrix.go` and `internal/app/app.go`
- [X] T058 [US5] Generate candidate-neutral docs and receipt-bound post-public inventory/docs changes, verify deterministic regeneration, and prepare the reviewable source update in `scripts/test-doc-truth-smoke.sh`
- [X] T059 [P] [US5] Update product overview, exact quickstart, alpha maturity, and non-claims in `README.md` and `README.zh-CN.md`
- [X] T060 [P] [US5] Reconcile implemented status, support scope, evidence provenance, and release channel in `docs/STATUS.md`, `docs/support-matrix.md`, and `docs/claim-boundaries.md`
- [X] T061 [P] [US5] Add release history and release-note contract in `CHANGELOG.md` and `.github/release-promotions/README.md`

**Checkpoint**: Candidate docs cannot claim publication, and public docs cannot
claim it without anonymous proof.

---

## Phase 8: User Story 6 - Report A Problem Without Publishing Secrets (P3)

**Goal**: Route normal feedback to bounded public issues and security reports
to a verified private channel using existing redacted export authority.

**Independent Test**: Exercise issue forms, repository API state, doctor/export
commands, and injected-secret fixtures without publishing sensitive material.

### Tests

- [X] T062 [P] [US6] Add repository trust and issue-form static/runtime checks to `scripts/test-public-alpha-release.sh`
- [X] T063 [P] [US6] Add injected credential and local-path refusal tests across support, evidence, receipt, and export surfaces in `internal/releasechannel/redaction_test.go`

### Implementation

- [X] T064 [P] [US6] Publish private vulnerability guidance and reporting expectations in `SECURITY.md`
- [X] T065 [P] [US6] Publish contribution, test, evidence, and disclosure guidance in `CONTRIBUTING.md`
- [X] T066 [P] [US6] Add bounded bug and security-routing forms in `.github/ISSUE_TEMPLATE/bug.yml`, `.github/ISSUE_TEMPLATE/config.yml`, and `.github/pull_request_template.md`
- [X] T067 [US6] Reuse doctor JSON and `audit export` as the official share path in `README.md`, `README.zh-CN.md`, `SECURITY.md`, and release notes
- [X] T068 [US6] Verify private vulnerability reporting through the GitHub API and reject docs-only inference in `scripts/test-public-alpha-release.sh`

**Checkpoint**: Public support asks for bounded facts, while security reports
have a private route and public artifacts contain no injected secret.

---

## Phase 9: Polish And Release Closure

**Purpose**: Complete legal inventory, workflow governance, gates, and the full
candidate/public validation battery.

- [X] T069 [P] Add reviewed direct-dependency attribution and runtime notice boundaries in `THIRD_PARTY_NOTICES.md`
- [X] T070 [P] Update distribution, privacy design, privacy test plan, and test evidence contracts in `docs/distribution-bootstrap.md`, `docs/privacy-run-design.md`, and `docs/privacy-run-test-plan.md`
- [X] T071 Add public-alpha proof and schema checks plus all local release smokes to `scripts/test-gate0.sh`
- [X] T072 Validate workflow YAML, strict schemas, shell syntax, executable recovery actions, and no credential output in `scripts/test-public-alpha-release.sh --contract-only`
- [X] T073 Enable and independently verify GitHub private vulnerability reporting and immutable release policy for `vibe-agi/hideout`
- [X] T074 Configure the protected `public-alpha` environment and document required reviewers and release credentials in `.github/release-promotions/README.md`
- [X] T075 Run `go build ./...`, `go vet ./...`, `gofmt -l internal cmd`, `git diff --check`, `go test ./...`, markdownlint, and `scripts/test-gate0.sh`
- [X] T076 Run `scripts/test-public-alpha-release.sh --no-publish` and verify zero public release, candidate-created Lima instance, browser, temp directory, and secret-bearing state
- [X] T077 On a clean exact candidate commit with Developer ID credentials, run clean-install, real Gate 2/3, signing, notarization, draft retention, protected promotion, anonymous redownload, receipt validation, and post-public docs truth through `.github/workflows/hideout-alpha-candidate.yml`, `.github/workflows/hideout-alpha-promote.yml`, and `.hideout-release-evidence/033-public-alpha/`

---

## Dependencies And Execution Order

### Phase Dependencies

- Setup has no dependency.
- Foundational identity/evidence depends on Setup and blocks all stories.
- US1 and US2 can start after Foundational; together they are the public-alpha
  MVP.
- US3 depends on the packaged install from US1 and identity from US2.
- US4 depends on the canonical package/install-state model from Foundational and US1.
- US5 depends on release models from US2 but can develop against fixtures.
- US6 can start after Foundational and integrates with US2/US5 at closure.
- Polish depends on all selected stories.

### User Story Dependencies

- **US1 (P1)**: Foundational only.
- **US2 (P1)**: Foundational only; publication awaits US1 clean-install proof.
- **US3 (P2)**: US1 package plus US2 exact identity.
- **US4 (P2)**: Foundational package state plus US1 install path.
- **US5 (P3)**: US2 release/receipt models.
- **US6 (P3)**: Foundational export/redaction contracts; public links await US5.

### Parallel Opportunities

- Schema skeletons, inventory skeleton, and package skeleton are parallel.
- Package, evidence, readiness, release-model, and recovery tests are parallel
  in Foundational.
- Within each story, tasks marked `[P]` operate on separate files.
- US4 and US6 can proceed while US2 workflows and US3 real gates are developed.
- Documentation files split across US1, US3, US4, US5, and US6 can be drafted
  independently, then reconciled in Polish.

## Implementation Strategy

### MVP First

1. Complete Setup and Foundational.
2. Complete US1 to produce a cleanly installable package.
3. Complete US2 contract/no-publish lanes and prove every failure is closed.
4. Stop and validate the developer-preview candidate before using credentials.

### Incremental Closure

1. Add US3 first-success and real-gate binding.
2. Add US4 lifecycle recovery.
3. Add US5 inventory-driven truth.
4. Add US6 public/private feedback routing.
5. Run the full no-publish rehearsal.
6. Use Developer ID credentials and protected approval only after every local
   and real gate is green.

## Notes

- Tests for release authority are written before implementation.
- A passing local or hosted-CI lane cannot replace real macOS Gate 2/3,
  Developer ID signing, notarization, repository state, or anonymous download.
- Developer preview and no-publish lanes use distinct identities and cannot
  satisfy the public-alpha release target.
- T077 is the only task allowed to claim the public alpha actually exists.

---

## Phase 10: Convergence

**Purpose**: Close evidence-authority and cleanup gaps found by post-implementation
assessment before any credentialed publication run.

- [X] T078 Produce and strictly validate the canonical evidence-bundle core inventory, including a real package verification receipt and exact runtime build provenance, per FR-015 and FR-018 (partial)
- [X] T079 Require verifiable Go-owned export/redaction decisions for every user-data-bearing public evidence artifact and reject self-declared redaction-only fixtures per FR-016 (partial)
- [X] T080 Make hosted candidate cleanup fail closed with verified zero retained keychain, temporary root, and secret-bearing state plus a bounded cleanup receipt per FR-031 and SC-013 (partial)
- [X] T081 Reject macOS and POSIX candidate-local absolute paths, including private temporary roots, across public evidence and receipt validation with mutation tests per SC-010 (partial)
- [X] T082 Add a target-independent distribution verifier for exact macOS package bytes so Ubuntu promotion can validate package identity, signing, notarization, and evidence without weakening end-user unsupported-platform checks per FR-020 and SC-008 (contradicts)
- [X] T083 Validate the retained promotion context immediately after its single atomic write and add a contract guard that rejects duplicate/truncating writers before protected promotion per FR-020 and SC-008 (partial)
