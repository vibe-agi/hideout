# Implementation Plan: Profile Templates And Onboarding

<!-- markdownlint-disable MD013 -->

**Branch**: `014-profile-templates-onboarding` | **Date**: 2026-07-09 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/014-profile-templates-onboarding/spec.md`

## Summary

Add built-in first-run profile templates (`privacy`, `hardened`, `dev`, and
`debug`) to the existing `hideout init` typed init path. The implementation
extends the Go-owned InitTask plan/apply operation so template profile creation,
network selection, mediated DNS resolver selection, hardened privilege-fact
checks, interactive confirmation, and local onboarding evidence are one
fail-closed operation. The default recommendation is `privacy`; `hardened`
requires an enforced privilege fact; degraded fallback is explicit and visibly
marked. No template grants workspace-external HostFS access or installs adapter
packs by default.

## Technical Context

**Language/Version**: Go 1.25 module plus existing POSIX shell Gate 0 smoke.

**Primary Dependencies**: Existing `internal/profile`, `internal/inittask`,
`internal/manager`, `internal/app`, deterministic audit redaction, JSON schema
validation used by Gate 0.

**Storage**: Durable Hideout store. Profiles remain under
`profiles/<name>/profile.json`; onboarding evidence is a local JSON file under
the profile directory and init audit remains `init-audit.jsonl`.

**Testing**: `go test ./...`, targeted `internal/profiletemplate`,
`internal/inittask`, and `internal/app` tests, Gate 0 onboarding smoke,
markdownlint, `gofmt -l`, `go vet ./...`, `git diff --check`.

**Target Platform**: macOS and Linux hosts. Real Lima is not required because
014 creates profile/init state and consumes injected or observed privilege
facts; backend isolation behavior is unchanged.

**Project Type**: CLI/local control-plane product with typed Go plan/apply
operations and local evidence.

**Performance Goals**: Non-interactive onboarding plan/apply completes in under
1 second excluding existing helper compilation; template rendering does not
perform network I/O.

**Constraints**: No adapter pack installation by default; no workspace-external
HostFS grants by default; no automatic base-image download; no new isolation
claim beyond 009 privilege status; no silent hardened-to-privacy downgrade; no
interactive prompt in daemon background paths.

**Scale/Scope**: One local operator creating one profile per onboarding run.
Four built-in templates in v1.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Touches profile state, network profile defaults,
  mediated DNS resolver carrier, CLI prompt/confirmation, and local evidence.
  It fails closed on unknown template, missing non-interactive choices, existing
  profile collisions, hardened without enforced privilege status, ambiguous
  degraded fallback, invalid network/proxy/resolver combination, confirmation
  refusal, or evidence write failure before reporting success.
- **Typed Authority**: `manager.PlanInit`/`ApplyInit` and `inittask.PlanMachine`
  remain the Go-owned provider. Template definitions are static Go data. CLI
  prompts only select inputs; JavaScript/config has no role in template
  authority.
- **Workspace And Policy**: Alters profile state only. HostFS grants stay empty
  by default; adapter packs and command adapters stay empty unless the operator
  later enables them through 011. Existing deny/reserved-root rules remain in
  lower HostFS/profile code.
- **Generality And Provider Scope**: Templates are generic Hideout posture
  presets. Base-image and privilege guidance are generic facts, not tied to one
  package manager, editor, agent, or marketplace pack. Native remains labeled a
  weak development harness.
- **Evidence And Redaction**: Init audit, onboarding evidence JSON, CLI output,
  README/docs, and Gate 0 smoke prove selected template, backend, network,
  HostFS, adapter-pack, and privilege posture. Evidence passes deterministic
  control-plane redaction and never records raw proxy secret values, UI tokens,
  broker tokens, generated machine ids, or `HIDEOUT_SECRET_*` material.
- **Backend And Distribution**: Reuses existing init helper tasks for Lima
  helper discovery/build. No new helper binary and no package metadata script.
  First-run repair continues through typed InitTasks.
- **Gates**: Gate 0 plus targeted unit tests and onboarding smoke. Real Lima
  gates are not required because backend/network runtime isolation does not
  change.
- **Status And Docs**: Update README, docs/README, docs/STATUS,
  docs/privacy-run-test-plan, and first-run docs so packaged alpha users see
  the recommended onboarding path.

Post-design re-check: PASS. The design keeps profile mutation in Go-owned
InitTask plan/apply, makes hardened enforced-only, keeps weak modes labeled,
and adds evidence/smoke coverage without new isolation claims.

## Project Structure

### Documentation (this feature)

```text
specs/014-profile-templates-onboarding/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── onboarding-command.md
│   ├── onboarding-evidence.md
│   └── template-catalog.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/profiletemplate/           # built-in template catalog and rendering
internal/profiletemplate/*_test.go
internal/inittask/inittask.go       # typed plan/apply carries template inputs
internal/inittask/inittask_test.go
internal/manager/manager.go         # PlanInit/ApplyInit use extended options
internal/manager/api.go             # init API request parity
internal/app/app.go                 # hideout init flags, prompts, output
internal/app/app_test.go
schemas/onboarding-evidence.schema.json
scripts/test-onboarding-smoke.sh
scripts/test-gate0.sh
README.md
docs/
```

**Structure Decision**: Add `internal/profiletemplate` for template definitions,
validation, evidence summaries, and prompt text so `internal/app` handles only
CLI interaction. Keep durable mutation inside `internal/inittask` so 014 does
not create a second profile-writing path.

## Complexity Tracking

No constitutional violations. The new package is justified because template
catalog, privilege-fact decisions, evidence rendering, and interactive review
need focused tests and would otherwise bloat `internal/app/app.go` and
`internal/inittask/inittask.go`.
