# Implementation Plan: Projection Readiness Proof

<!-- markdownlint-disable MD013 -->

**Branch**: `043-projection-readiness-proof` | **Date**: 2026-07-23 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/043-projection-readiness-proof/spec.md`

## Summary

Close the projected-command first-run race by extending the existing
pre-target session-runtime visibility barrier to the complete immutable
projection catalog compiled by Manager. Manager materializes every exact shim
and dispatcher, writes a strict session-local readiness manifest last, and
binds its catalog digest into the reviewed run and prepared backend session.
Lima then mounts the exact session view, validates the manifest and every
regular non-symlink executable before the fixed supervisor may report ready.
Manager validates the resulting catalog digest before committing target launch.

The implementation reuses the existing two-second bounded session-view poll and
supervisor ready/commit boundary. Missing files may retry only before target
commit; malformed, foreign, stale, symlinked, or digest-mismatched state fails
immediately. Ordinary guest commands are never retried after launch and no
ambient guest or host fallback is introduced.

The slice also closes the stale 030 acceptance ledger with direct current
proofs, corrects the drifted capability-descriptor schema, and adds a strict
clean exact-package producer/evaluator. One retained evidence set re-proves the
new first-attempt contract plus the existing 030 built-in projection, 032
external pack, and a newly registered 039 durable-grant proof. Alias privacy is
promoted only when the matching clean real privacy gate passes.

## Technical Context

**Language/Version**: Go 1.25.0; POSIX shell for release gates; JSON Schema 2020-12 for public/evidence contracts

**Primary Dependencies**: existing `internal/cmdproxy`, `internal/hostcap`, `internal/hostapppack`, `internal/manager`, `internal/backend/lima`, `internal/daemon`, `internal/sessionwire`, `internal/productevidence`, `internal/releasecompat`; `github.com/santhosh-tekuri/jsonschema/v6`; Lima and the package-owned Linux helpers

**Storage**: private per-session runtime directory and readiness manifest under the existing environment runtime tree; host-local audit JSONL; ignored retained release-evidence directory; no new user configuration or durable authority database

**Testing**: Go unit/integration tests, `go test -race`, shell syntax/lint, schema validation, mutation-red assertions, evidence negative fixtures, full Gate 0, aggregate real Lima Gate 2, clean exact-package 043 Gate 2, and conditional matching Gate 3 privacy proof

**Target Platform**: promoted path is macOS arm64 with Lima and an aarch64 guest; native and fake runners prove mechanics only

**Project Type**: Go CLI/daemon/Manager monorepo with package-owned Linux helpers and shell release gates

**Performance Goals**: readiness p95 at most two seconds once the exact guest session view is available; pre-commit cancellation at most two seconds; 100% first-attempt success across at least 10 fresh environments and 30 warm new sessions

**Constraints**: complete session catalog, exact session/environment/snapshot/instance/boot identity, manifest written last, regular non-symlink executable entries, no target retry, no fallback, no host-path or credential evidence, no new CLI/config/capability/provider

**Scale/Scope**: up to the existing bounded projected-command catalog (Core commands plus at most 64 host-app commands), concurrent disjoint sessions, four historical 030 debt dispositions, four strict real proof families, and closed negative-fixture inventories

## Constitution Check

*GATE: Passed before Phase 0 research and re-checked after Phase 1 design.*

- **Privacy Boundary**: The feature touches projected command files, the
  guest session mount, backend/supervisor readiness, host-app broker admission,
  and evidence. A missing, late, foreign, stale, symlinked, malformed, or
  digest-mismatched entry stops before `SupervisorCommit`; cancellation and
  timeout launch no target or host effect.
- **Typed Authority**: Manager still compiles the authoritative command
  registry and host-app binding catalog. The Lima backend proves guest
  availability but receives no host-app authority. Existing Go broker/provider
  validation remains the only host-effect path. Shell checks observe bounded
  files inside the exact session view and cannot add a command or grant.
- **Workspace And Policy**: Workspace mounts, HostFS rules, profile grants,
  deny precedence, trusted grant semantics, and high-risk overrides are
  unchanged. The catalog digest becomes part of reviewed run truth; it does not
  broaden policy.
- **Generality And Provider Scope**: Readiness applies to the generic
  projected-command catalog. VS Code and the external pack remain named real
  fixtures/providers. Lima implements the promoted session-view proof but the
  product contract is not defined as a Lima command or editor behavior.
- **Evidence And Redaction**: Structured readiness evidence contains status,
  reason, public command names, catalog digest, counts, and bounded duration.
  It omits host paths, private runtime paths, usernames, tokens, raw argv,
  machine IDs, grant contents, and application private state. New semantic
  judges receive unknown-field and false-green fixtures.
- **Backend And Distribution**: No new helper or runtime image is introduced.
  The existing packaged shim, supervisor, Workspace Portal, HostFSD, and DNS
  stub are exact-package inputs to real gates. Native remains a weak mechanics
  harness; no InitTask or setup behavior changes.
- **Gates**: Gate 0 covers exact catalog/manifest construction, session-view
  validation, broker mismatch, template defaults, drift, schema parity,
  lifecycle activation, cancellation, races, mutation proofs, evaluator
  negative fixtures, docs truth, and aggregate local suites. Clean Gate 2
  proves fresh/warm/concurrent first attempts and the 030/032/039 real flows.
  Matching Gate 3 is required only to promote alias privacy provenance.
- **Status And Docs**: Update `docs/STATUS.md`, `docs/DEBT.md`,
  `docs/claim-boundaries.md`, `docs/privacy-run-design.md`,
  `docs/privacy-run-test-plan.md`, `docs/threat-model.md`, projection/recipe
  subsystem docs, and the 039 spec status only after the corresponding strict
  evidence passes.

### Post-Design Re-check

The design still passes all principles. The readiness manifest is a completion
marker and integrity expectation, not authority; it is derived from Manager's
already validated catalog, written before backend execution, and checked only
inside the exact boot/session view. The supervisor commit remains the sole
target side-effect boundary. Existing broker/provider checks independently
revalidate the command, binding, grant, resource, and application identity even
after readiness succeeds. No Complexity Tracking exception is required.

## Project Structure

### Documentation (this feature)

```text
specs/043-projection-readiness-proof/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── adversarial-report.md
├── checklists/
│   └── requirements.md
├── contracts/
│   ├── projection-readiness.md
│   └── observability-and-evidence.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/
├── backend/
│   ├── backend.go                         # immutable readiness expectation/proof fields
│   └── lima/
│       ├── lima.go                        # preserve Manager catalog through Prepare
│       ├── session_view.go                # exact bounded manifest/entry barrier
│       ├── session_stream.go              # pre-commit cancellation and ready proof
│       └── *_test.go
├── broker/
│   ├── hostapp.go                         # exact registry-before-binding admission
│   └── hostapp_test.go                    # deliberately inconsistent registry fixture
├── hostcap/
│   ├── descriptor.go                     # public descriptor JSON shape
│   └── registry_test.go                  # real schema parity and unknown fields
├── manager/
│   ├── projection_readiness.go            # catalog snapshot/manifest/digest
│   ├── run_dataplane.go                   # materialize manifest last
│   ├── run_plan.go                        # bind reviewed catalog digest
│   ├── run_apply.go                       # recompile/compare/bind proof
│   └── *_test.go
├── profiletemplate/
│   └── template_test.go                   # direct four-template alias assertion
├── daemon/
│   └── session_server_test.go             # no Started frame before readiness
├── productevidence/
│   ├── projection_readiness.go            # strict 043/030/032/039 evaluator
│   ├── registry.go                        # exact package/runtime requirements
│   └── projection_readiness_test.go       # closed negative fixtures
└── releasecompat/
    ├── matrix.go                          # 039/043 support requirements
    └── readiness_test.go                  # semantic fixture builder

schemas/
├── capability-descriptor.schema.json
└── projection-readiness.schema.json

scripts/
├── test-projection-readiness-smoke.sh
├── test-projection-readiness-lima-e2e.sh
├── test-host-capability-projection-e2e.sh
├── test-host-app-pack-e2e.sh
├── lib/gate2-projection.sh
├── test-gate0.sh
├── test-gate2-lima.sh
└── test-gate3-hidden-proxy.sh

docs/
├── STATUS.md
├── DEBT.md
├── claim-boundaries.md
├── host-capability-projection.md
├── host-app-recipes.md
├── privacy-run-design.md
├── privacy-run-test-plan.md
└── threat-model.md
```

**Structure Decision**: Extend the existing Manager catalog, per-session
runtime, Lima session-view barrier, and supervisor ready/commit chain. The
manifest and catalog model live in a dedicated Manager file so the authority
boundary is reviewable; Lima consumes a backend-neutral expectation rather than
reconstructing profile state. Product evidence follows the strict 041/042
validator pattern instead of treating shell marker text as semantic proof.

## Implementation Phases

### Phase 0: Readiness And Evidence Research

1. Trace fresh, warm, shared, dedicated, and workspace-bound session views.
2. Freeze the final Manager catalog and pre-target side-effect boundary.
3. Re-verify all four 030 ledger observations against current implementation.
4. Map existing 030/032/039 real flows and release-evidence weaknesses.
5. Define strict artifact inventory, performance samples, and Gate 3 scope.

### Phase 1: Contracts And Data Model

1. Define the immutable catalog snapshot and last-written readiness manifest.
2. Define guest observation states, typed reasons, retryability, and proof.
3. Bind catalog digest into plan/apply, backend session, and supervisor commit.
4. Define structured audit and strict real evidence with closed check maps.
5. Record exact debt dispositions and promotion/non-promotion rules.

### Phase 2: Test-First Readiness Implementation

1. Add red fixtures for missing/late/foreign/symlinked/digest-drifted entries,
   pre-commit cancellation, catalog drift, no target/host effect, and ordinary
   command compatibility.
2. Build the Manager catalog snapshot and atomic manifest completion marker.
3. Carry the reviewed expectation through backend preparation without
   reconstructing profile-only state.
4. Extend the exact session-view barrier and authenticated ready proof.
5. Gate lifecycle activation and daemon `Started` on the validated proof.
6. Add typed redacted audit/recovery output and concurrency/race coverage.

### Phase 3: Historical Contract And Judge Closure

1. Add the deliberately inconsistent broker registry/binding negative fixture.
2. Add direct alias assertions for all four current templates.
3. Retain the existing pathMode recreate proof and unbound-intent schema proof.
4. Correct descriptor JSON tags/schema `residualPolicy` parity and unknown
   field rejection.
5. Register strict 043 and 039 proof IDs; strengthen 030/032 package/runtime
   policies and semantic validators.
6. Add implementation mutations and evaluator negative fixtures before green
   assertions are accepted.

### Phase 4: Clean Real Evidence And Convergence

1. Run targeted tests, races, schema/docs gates, and full Gate 0.
2. Build one clean exact package and run 10 fresh, 30 warm, and concurrent
   projected first attempts.
3. Re-prove built-in safe projection, external pack, and durable grant/revoke
   with exact host effects and zero fallback.
4. Run matching Gate 3 in an independent environment when privacy prerequisites
   are available; otherwise retain the dirty privacy non-promotion explicitly.
5. Retain exact hashes and adversarial mutation/negative-fixture results.
6. Update status/claims/debt only for proofs that passed, then analyze and
   converge every FR, SC, acceptance scenario, and task.

## Complexity Tracking

No constitution violations or justified exceptions.
