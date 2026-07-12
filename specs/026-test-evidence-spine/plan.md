# Implementation Plan: Test And Evidence Spine

<!-- markdownlint-disable MD013 -->

**Branch**: `026-test-evidence-spine` | **Date**: 2026-07-09 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/026-test-evidence-spine/spec.md`

## Summary

Replace the current per-feature required proof slices with a small Go-owned
proof requirement registry and evaluator. Existing 021-025 product-hardening
manifests remain schema-compatible; `stale` is an evaluator result, not a new
proof status. Shell gates, docs truth, and release readiness consume a
deterministic JSON view derived from the same registry so required proof IDs are
not duplicated across Go and shell.

## Technical Context

**Language/Version**: Go 1.x project code plus Bash smoke scripts.

**Primary Dependencies**: Existing standard-library Go packages, existing
`internal/productevidence`, `internal/releasecompat`, and shell smoke scripts.
No new third-party dependency.

**Storage**: JSON evidence manifests on disk. No durable store migration.

**Testing**: `go test` for registry/evaluator/schema fixtures; existing shell
smoke for docs truth and product-hardening compatibility; Gate 0 for final
integration.

**Target Platform**: Local development and Gate 0 on supported alpha hosts.
No new backend-specific runtime path.

**Project Type**: Go CLI/control-plane repository with shell gates and docs.

**Performance Goals**: Registry/evaluator checks are small in-memory
operations. Gate 0 must not materially slow down; no real backend dependency.

**Constraints**: Keep `hideout.product-hardening-evidence/v1` proof status
values stable (`passed`, `failed`, `not-run`). Avoid broad test framework or
script rewrites. Keep local product-hardening proof distinct from release
readiness.

**Scale/Scope**: Existing 021-025 product-hardening proof IDs plus room for
future features to register proof requirements without new per-feature slices.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: No runtime authority is added. The feature touches only
  evidence validation, docs truth, and release readiness reporting. It fails
  closed on missing, failed, not-run, stale, redaction-failed, or artifact-bad
  evidence before any pass claim.
- **Typed Authority**: No Manager plan/apply operation or host provider is
  introduced. Go Core owns proof registry, evaluator, artifact checks, and JSON
  view generation.
- **Workspace And Policy**: No workspace, HostFS, env policy, proxy secret, or
  profile state change.
- **Generality And Provider Scope**: Generic Hideout evidence infrastructure,
  not a provider or backend-specific behavior.
- **Evidence And Redaction**: Evaluator output and registry JSON are redacted
  and never expose raw control-plane material. Existing manifest validation and
  product evidence redaction rules remain authoritative.
- **Backend And Distribution**: No helper binary, backend requirement, or
  package change. Native remains weak harness; real gates remain separate.
- **Gates**: Gate 0 must run registry/evaluator validation. No new Gate 2/Gate
  3 proof is introduced.
- **Status And Docs**: Update `docs/privacy-run-test-plan.md`,
  `docs/STATUS.md`, and `docs/claim-boundaries.md` if implementation changes
  current evidence/status wording. Update docs truth smoke to consume registry
  JSON instead of a duplicated proof list.

## Project Structure

### Documentation (this feature)

```text
specs/026-test-evidence-spine/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── proof-registry.md
│   └── proof-evaluation.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/productevidence/
├── aggregate.go              # existing aggregation/completion callers
├── claims.go                 # existing proof/claim constants
├── manifest.go               # existing manifest schema validation
├── registry.go               # new/updated Go-owned proof registry
├── evaluate.go               # new evaluator and result model
├── registry_test.go          # registry completeness/determinism tests
├── evaluate_test.go          # passed/missing/failed/stale/artifact fixtures
└── schema_test.go            # JSON view/schema compatibility checks

internal/releasecompat/
├── readiness.go              # supporting-evidence integration point
└── readiness_test.go         # local vs real-gate separation tests

scripts/
├── test-doc-truth-smoke.sh   # consume registry JSON/helper
└── test-gate0.sh             # include 026 validation

docs/
├── claim-boundaries.md
├── privacy-run-test-plan.md
└── STATUS.md
```

**Structure Decision**: Keep 026 in existing `internal/productevidence` because
that package already owns product-hardening manifest validation, required proof
IDs, aggregation, and schema tests. Release readiness consumes evaluator output
as supporting evidence from `internal/releasecompat`; shell gates consume a
deterministic registry JSON view generated by Go.

## Complexity Tracking

No constitution violation or extra complexity exception. The only new
abstraction is a registry/evaluator that removes existing duplicated
per-feature proof lists and makes failure diagnostics stronger.

## Phase 0: Research Summary

Research output is in [research.md](research.md). Decisions:

- Keep proof manifest schema stable.
- Model `stale` as evaluator output.
- Add a Go-owned proof registry with deterministic JSON view.
- Add evaluator artifact checks for existence and digest mismatch.
- Integrate release readiness only as supporting local proof context; real
  Gate 2/Gate 3 evidence remains mandatory.

## Phase 1: Design Summary

Design output:

- [data-model.md](data-model.md)
- [contracts/proof-registry.md](contracts/proof-registry.md)
- [contracts/proof-evaluation.md](contracts/proof-evaluation.md)
- [quickstart.md](quickstart.md)

## Constitution Check (Post-Design)

- **Privacy Boundary**: PASS. No runtime authority added; validation fails
  closed on invalid evidence.
- **Typed Authority**: PASS. Registry/evaluator are Go-owned and shell only
  consumes deterministic output.
- **Workspace And Policy**: PASS. No user policy or workspace mutation.
- **Generality And Provider Scope**: PASS. Generic evidence infrastructure only.
- **Evidence And Redaction**: PASS. Redaction failures invalidate proofs and
  registry/evaluator output is redacted.
- **Backend And Distribution**: PASS. No helper or backend change.
- **Gates**: PASS. Gate 0 coverage only; real gates remain required for
  isolation claims.
- **Status And Docs**: PASS when docs/test-plan/status and docs truth smoke are
  updated to reference 026.
