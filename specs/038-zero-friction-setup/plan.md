# Implementation Plan: Zero-Friction Setup

**Branch**: `038-zero-friction-setup` (worktree: `master`) | **Date**:
2026-07-19 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from
`/specs/038-zero-friction-setup/spec.md`

## Summary

Add `hideout setup` as one interactive, fixed projection of the existing init
authority. The command prepares the supported `default` + `dev` + Lima +
direct + `developer-standard` posture, discloses its boundary, asks once with
a default-no prompt, and writes configuration without starting a VM or
downloading the runtime.

The implementation first replaces the split init behavior with a Manager-owned
`InitService`: both `setup` and advanced `init` use authenticated daemon HTTP,
receive a versioned prepared review, and submit that exact prepared plan for
apply. One lock-owning Manager method takes the existing store-rooted profile
mutation lock, performs fresh observations inside the lock, rejects drift, and
only then invokes a private lock-assuming InitTask helper. A pure-read readiness
classifier keeps repeated
setup from calling mutation-oriented `LoadOrInit`. The current package/PTY and
first-run evidence harness gains an additive direct/setup lane while retaining
the privacy lane.

## Technical Context

**Language/Version**: Go 1.25.x; POSIX shell for package and evidence lanes

**Primary Dependencies**: Go standard library HTTP/JSON/crypto packages,
`golang.org/x/sys/unix` for existing store locks, existing daemon, Manager,
InitTask, profile-template, runtime-catalog, package, and product-evidence
packages; no new runtime dependency

**Storage**: Existing private file store under `~/.hideout`, profile JSON,
onboarding/audit evidence, daemon Unix socket and lock files; no new persisted
schema

**Testing**: `go test`, packaged shell smoke, PTY execution, macOS arm64 Lima
Gate 2, schema validation, markdownlint, docs-truth, and product-evidence
evaluation

**Target Platform**: Public alpha support target macOS arm64 with Lima and a
Linux arm64 guest; native remains a weak local test harness only

**Project Type**: Single Go CLI plus local authenticated daemon and packaged
Linux helpers

**Performance Goals**: Setup completes without a VM or runtime transfer; daemon
cold-start is bounded by the existing readiness contract; first-run waits emit
the current bounded heartbeat and never fabricate byte or percentage progress

**Constraints**: Default-no local confirmation; no setup flags; no embedded
mutation fallback; exact reviewed-plan binding; existing valid profiles are
strictly read-only; direct networking is disclosed as non-private; real claims
require installed-package and Lima evidence

**Scale/Scope**: One local operator, concurrent CLI processes, one fixed setup
projection, all existing advanced init choices, two first-run evidence lanes,
and one exact agent compatibility fixture

## Constitution Check

*GATE: PASS before research; PASS after Phase 1 design.*

- **Privacy Boundary - PASS/PASS**: Setup changes profile, runtime-selection,
  backend, and network configuration only through existing InitTasks. It grants
  no HostFS, host-app, endpoint, adapter, or decision authority. Unsupported,
  malformed, stale, or unprovable state fails before mutation. Direct network
  is visibly non-private.
- **Typed Authority - PASS/PASS**: `InitService` owns preparation,
  re-observation, digest validation, and single-lock application in Go Manager
  Core. The
  CLI only renders and confirms; the daemon authenticates and transports. No
  JavaScript or configuration executes authority.
- **Workspace And Policy - PASS/PASS**: Setup fixes the existing alias-mode
  read/write workspace at `/workspace`; files outside it remain governed by
  existing HostFS policy. It adds no mount, passthrough, deny override, or
  secret-bearing policy.
- **Generality And Provider Scope - PASS/PASS**: Lima, Homebrew, the retained
  developer runtime, and the exact Codex package are named alpha providers or
  compatibility fixtures. They are not promoted into generic Core semantics.
- **Evidence And Redaction - PASS/PASS**: Existing deterministic redaction
  applies to review, result, audit, and manifests. Stable 038 proof IDs separate
  local PTY evidence from real Lima evidence and preserve `not-run` honestly.
- **Backend And Distribution - PASS/PASS**: Package install remains
  `--skip-init`; setup uses typed InitTasks. Real isolation and agent claims
  require the packaged macOS binary and Lima. Native cannot satisfy them.
- **Gates - PASS/PASS**: Gate 0 covers parser, parity, binding, read-only state,
  cancellation, daemon failure, redaction, and docs. Package PTY covers the UI.
  Real Gate 2 covers Lima, runtime, identity, workspace, audit, reuse, and agent
  install/run.
- **Status And Docs - PASS/PASS**: README, Chinese README, first-run,
  distribution, status, support matrix, control-plane, claim-boundary,
  privacy-design/test-plan, CLI help, formula caveats, and docs-truth move only
  after their evidence exists.

Principle V's lifecycle requirement is met directly: setup separates local
configuration readiness from VM/runtime lifecycle, and the subsequent run uses
the existing exact-provenance environment lifecycle. No constitutional
exception or complexity waiver is required.

## Project Structure

### Documentation (this feature)

```text
specs/038-zero-friction-setup/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── init-plan-binding.md
│   ├── setup-cli.md
│   └── setup-evidence.md
├── checklists/
│   └── requirements.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/
├── app/
│   ├── app.go                    # setup/init UI and dispatch
│   └── init_client.go            # authenticated typed daemon client
├── daemon/
│   └── autostart.go              # bounded local control-service readiness
├── manager/
│   ├── api.go                    # bound init plan/apply wire contract
│   ├── init_service.go           # prepare/revalidate/apply authority
│   ├── manager.go                # existing InitTask authority entrypoint
│   └── profile_lock.go           # store-rooted cross-process serialization
├── inittask/
│   └── inittask.go               # existing typed effects and audit
├── profile/
│   └── profile.go                # strict pure Load and existing defaults
├── profiletemplate/
│   └── template.go               # existing dev projection
├── backend/lima/
│   └── lima.go                   # honest runtime wait and heartbeat
└── productevidence/
    └── registry.go               # stable 038 proof registration

packaging/homebrew/hideout.rb
schemas/manager-api.schema.json
scripts/test-first-run-e2e.sh
scripts/test-runtime-agent-install.sh
scripts/test-first-run-docs-smoke.sh
scripts/test-gate0.sh

README.md
README.zh-CN.md
docs/
```

The published formula in
`/Users/null/Code/github/vibe-agi/homebrew-tap/Formula/hideout.rb` is a separate
release-synchronized repository. Source and published copies receive a parity
check so caveats and helper inventory cannot drift.

**Structure Decision**: Extend existing packages along their current ownership
boundaries. Manager receives the only new service abstraction because it
removes a real security split between CLI and API plan/apply behavior. Setup
does not receive a separate profile writer, persisted format, route family, or
package harness.

## Complexity Tracking

No constitutional violations or justified exceptions.
