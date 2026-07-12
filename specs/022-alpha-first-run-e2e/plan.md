# Implementation Plan: Alpha First-Run E2E

**Branch**: `022-alpha-first-run-e2e` | **Date**: 2026-07-09 |
**Spec**: [spec.md](spec.md)

**Input**: Feature specification from
`/specs/022-alpha-first-run-e2e/spec.md`

## Summary

022 proves package first-run mechanics from a package artifact to one
successful command. All lanes install from the package with `--skip-init`.
Local-fast verifies the installed package, initializes one weak/dev profile,
runs a low-risk command with the installed binary, captures audit and Boundary
evidence, and writes stable `hideout.product-hardening-evidence/v1` proof
entries. Real Lima/privacy first-run proof is explicit and prerequisite-gated.

## Technical Context

**Language/Version**: Go 1.24+ repository code, POSIX shell, jq for smoke
assertions, existing Node only where 021 UI proof already requires it.

**Primary Dependencies**: Existing `internal/packagekit`,
`internal/productevidence`, `internal/app`, `internal/doctor`,
`internal/manager`, package scripts, schema validator, and docs smoke scripts.

**Storage**: Temporary install prefix, temporary `HIDEOUT_STORE_ROOT`, package
install state, profile store, local audit logs, and product-hardening evidence
JSON.

**Testing**: `go test ./...`, `scripts/test-gate0.sh`, new
`scripts/test-first-run-e2e.sh`, package smoke, first-run docs smoke, schema
validation, markdownlint, and targeted fixture modes.

**Target Platform**: macOS/Linux local operator machines. Local-fast mode works
without Lima. Real-backend mode targets the same first-class Lima/privacy path
documented in `docs/first-run-alpha.md` and may be `not-run` when prerequisites
are unavailable.

**Project Type**: Go CLI/daemon/local WebUI/TUI project with shell-based E2E
smoke gates and JSON evidence artifacts.

**Performance Goals**: Local-fast first-run E2E should complete within normal
Gate 0 smoke budget. Real-backend mode may be slower and is explicitly optional
unless requested by a release or manual proof run.

**Constraints**: No new authority, no dependency installer, no template
expansion, no public release claim. Local-fast cannot use the privacy template
with native/direct; it uses a weak/dev profile and labels it honestly. Package
verification, duplicate init, unsafe path, stale package, missing prereq,
audit/Boundary absence, and redaction failure all block pass claims.

**Scale/Scope**: One package artifact, one install prefix, one store, one
profile, one low-risk command, one evidence manifest per run. Fixture modes
cover representative failure classes instead of a full release matrix.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: What host, filesystem, network, backend, script,
  endpoint, environment, or lifecycle authority does this feature touch? How
  does it fail closed when unsupported, ambiguous, or denied?
  - Touches package install/verify, first-run init/profile lifecycle, local
    run, audit/Boundary observation, doctor/support checks, docs, and evidence.
    It does not add HostFS, network, JS, daemon, browser, or backend authority.
    It fails closed on package mismatch, duplicate init, missing prereq,
    unsafe paths, stale package state, missing audit/Boundary evidence, and
    redaction failure.
- **Typed Authority**: Which Manager plan/apply operation, Go validator, and
  Go-owned provider execute the authority? If JavaScript/config participates,
  what facts can it see and what proposal shape can Go independently validate?
  - Existing typed init and run paths execute authority. Package verification
    is Go-owned in `internal/packagekit`; evidence validation is Go-owned in
    `internal/productevidence`. No JavaScript/config decision point is added.
- **Workspace And Policy**: Does the feature alter workspace mounts, HostFS,
  passthrough mounts, env policy, proxy secrets, or profile state? How are
  denies, reserved roots, user-authored grants, and high-risk overrides handled?
  - Alters profile state only through typed `hideout init`. Uses a dedicated
    temp workspace. Does not create HostFS grants or change env policy beyond
    the selected existing template.
- **Generality And Provider Scope**: Does the feature introduce a specific
  agent, package manager, browser, editor, proxy port, backend quirk, or test
  fixture? If yes, is it explicitly scoped as a provider, example, lab/smoke
  fixture, or operator-local setup rather than a generic product default?
  - Local-fast/native is a weak smoke fixture. Lima/privacy is the first-class
    alpha path, but real proof is explicit and prerequisite-gated. No provider
    becomes generic semantics.
- **Evidence And Redaction**: What audit events, Boundary Summary fields,
  explain/doctor output, Manager API state, TUI/WebUI rendering, and redaction
  rules prove what happened without leaking secrets?
  - Uses package verify output, init output, run output, audit show output,
    `Hideout boundary:` output, doctor/support findings, and
    `hideout.product-hardening-evidence/v1`. Evidence is schema-validated and
    redaction-scanned with existing control-plane material checks.
- **Backend And Distribution**: Which backend capabilities and helper
  artifacts are required? Is native only a weak harness? Does first-run or
  repair use typed InitTasks instead of scripts?
  - Package helper inventory comes from package verify. `tun2socks` remains an
    external prerequisite. Native is weak/dev-only. First-run repair remains
    existing typed init/package/doctor behavior; no arbitrary repair shell is
    added.
- **Gates**: Which checks from `docs/privacy-run-test-plan.md` are required
  before merge? Gate 0 is required for docs/schemas/static contracts; backend,
  network, browser, HostFS, endpoint exposure, and dogfood claims require the
  relevant product gates.
  - Gate 0 is required. Local-fast first-run E2E is a Gate 0/local proof only.
    Real Lima/privacy proof mode may produce additional evidence but does not
    replace Gate 2/Gate 3 or release-readiness gates.
- **Status And Docs**: Which of `docs/STATUS.md`,
  `docs/privacy-run-design.md`, `docs/threat-model.md`,
  `docs/privacy-run-test-plan.md`, or subsystem docs must be updated?
  - Update `docs/first-run-alpha.md`, `docs/privacy-run-test-plan.md`,
    `docs/STATUS.md`, README/docs index references if needed, and 022
    quickstart/contracts. Claims/non-claims remain unchanged.

## Project Structure

### Documentation (this feature)

```text
specs/022-alpha-first-run-e2e/
├── plan.md              # This file (/speckit-plan command output)
├── research.md          # Phase 0 output (/speckit-plan command)
├── data-model.md        # Phase 1 output (/speckit-plan command)
├── quickstart.md        # Phase 1 output (/speckit-plan command)
├── contracts/           # Phase 1 output (/speckit-plan command)
└── tasks.md             # Phase 2 output
```

### Source Code (repository root)

```text
internal/
├── productevidence/      # 022 proof IDs, required proof aggregation, tests
├── packagekit/           # existing package verify/install/repair behavior
├── app/                  # existing CLI dispatch and first-run command outputs
└── manager/              # existing run/audit/Boundary surfaces

scripts/
├── test-first-run-e2e.sh # new local-fast/real-backend first-run proof runner
├── test-first-run-docs-smoke.sh
├── test-package-smoke.sh
└── test-gate0.sh

docs/
├── first-run-alpha.md
├── privacy-run-test-plan.md
└── STATUS.md

schemas/
└── product-hardening-evidence.schema.json
```

**Structure Decision**: Keep 022 as scripts plus small evidence-library
extensions over existing package/init/run surfaces. Do not create a new package
installer or first-run product subsystem.

## Complexity Tracking

> **Fill ONLY if Constitution Check has violations that must be justified**

No constitution violations. No new authority, helper, provider, or storage
subsystem is introduced.

## Post-Design Constitution Check

- **Privacy Boundary**: PASS. The design only proves existing package/init/run
  paths and refuses to translate local-fast results into real isolation claims.
- **Typed Authority**: PASS. Package verification, init, run, audit, Boundary,
  and evidence validation stay Go-owned.
- **Workspace And Policy**: PASS. The feature uses a dedicated temp workspace
  and existing profile/template semantics.
- **Generality And Provider Scope**: PASS. Native is labeled weak/dev-only;
  Lima/privacy proof is explicit and prerequisite-gated.
- **Evidence And Redaction**: PASS. Proof entries use the existing
  product-hardening evidence schema plus redaction checks.
- **Backend And Distribution**: PASS. Package helper integrity is verified;
  external prerequisites remain explicit.
- **Gates**: PASS. Gate 0 covers local proof and docs; real backend claims
  remain under their existing gates.
- **Status And Docs**: PASS. 022 updates first-run docs, status, and test plan
  without creating new claims.
