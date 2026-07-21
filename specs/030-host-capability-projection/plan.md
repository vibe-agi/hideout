# Implementation Plan: Host Capability Projection

<!-- markdownlint-disable MD013 MD060 -->

**Branch**: `030-host-capability-projection` | **Date**: 2026-07-10 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/030-host-capability-projection/spec.md`

## Summary

Project one authorized host capability into the guest as a command the CLI already knows. V1 delivers the `code` recipe end-to-end: inside a guest that has no `code` binary, `code .` / `code <path>` / `code -g <file>:<line>:<column>` open the mapped host workspace in a constrained (safe-mode) VS Code, through a typed, audited, fail-closed brokered route, without the guest ever learning the host absolute path or username.

Technical approach: add a Core-owned static capability registry (`CapabilityDescriptor`), a generic `host.app.open-resource` capability provider, a Core/package-owned app-identity registry (`appRef` → host binary/bundle by platform), and a `code` command binding whose declarative grammar parses guest argv into an app-agnostic `OpenResourceIntent` that Go re-decodes and field-validates. Workspace resources cross every guest/adapter/event boundary as a workspace-scoped `ResourceRef` (guest `/workspace/...` + relative path); only Core resolves the host path via the session-bound mapping — the same one-directional invariant that also hides the host username under `pathMode=alias`. Safe mode opens VS Code with an isolated `--user-data-dir`, `--disable-extensions`, auto-tasks not run, and Workspace Trust kept on. `trusted-host-app` is an explicit, revocable operator grant through the decision center. The registry is designed to accommodate adb, AppleScript templates, and result-streaming, but none of those is implemented in v1.

This reuses existing infrastructure: command-proxy `Registration` model (`internal/cmdproxy`), broker action routing and workspace-file resolution with symlink-escape recheck (`internal/broker/broker.go`), `pathMode` alias mapping (`internal/manager/run_plan.go`), synthetic identity (`internal/backend/lima`), operator decision center (`internal/decision`, `internal/manager`), command-adapter proposal outcomes (`internal/cmdadapter`), environment drift model with `GuestWorkspace` already a drift axis (`internal/manager/run_environment.go`), and the policy action set (`internal/policy/policy.go`).

## Technical Context

**Language/Version**: Go (repository toolchain; matches existing `internal/*` packages).

**Primary Dependencies**: Existing internal packages only — `cmdproxy`, `broker`, `hostopen`, `cmdadapter`, `decision`, `manager`, `environment`, `profile`, `backend/lima`, `policy`, `audit`, `recovery`, `productevidence`. Guest-side: existing host command shim (`cmd/hideout-shim`). No new third-party dependency.

**Storage**: Control-plane store (`~/.hideout`, guest-unreachable) for the capability registry data (package-owned static), profile `pathMode`, and the `trusted-host-app` grant record (via the existing decision store). No new database.

**Testing**: `go test` (Gate 0 unit/mechanics), plus real macOS arm64 Lima Gate 2 and Gate 3 obligations via `scripts/test-*.sh`. TDD: contract/unit tests precede implementation for each slice.

**Target Platform**: macOS arm64 first-class host, Linux guest via Lima. VS Code as the single implemented recipe. Linux host / other editors are out of v1 scope.

**Project Type**: Single Go project (CLI + local control plane + guest helpers). Uses the existing `internal/` + `cmd/` + `scripts/` + `schemas/` + `docs/` layout.

**Performance Goals**: `code .` opens with no perceptible added latency beyond a normal host `code` launch; repeated identical invocations are de-duplicated/rate-limited so an agent cannot flood host windows. No steady-state polling introduced.

**Constraints**: Fail-closed everywhere; no generic fallback to host execution or a shadowed guest binary; host absolute path / username / tokens / raw argv never cross to guest, adapter, intent, event, or export; safe mode never uses `--disable-workspace-trust`; adb/AppleScript/result-streaming designed but not implemented; do not exceed v1 scope.

**Scale/Scope**: One capability family, one recipe, four user stories. New Go packages roughly: `internal/hostcap` (registry + descriptors + `host.app.open-resource` provider + app-identity registry), `internal/hostcap/appopen` (VS Code safe/trusted launcher), a `code` command binding + grammar (in `cmdproxy` or a new `internal/cmdgrammar`), broker action handler, manager wiring + inspection, doctor feature, and one real-Lima gate lane. Estimated a focused slice, not a mega-batch.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Touches a new typed host capability (`host.app.open-resource`) reached through the existing broker; the workspace path model (`pathMode` alias); the operator decision surface; profile/environment lifecycle; audit; doctor. Fails closed on: unbound projected name, unavailable/failed provider, unknown flag, no-host-mapping path, absent/drifted app, stale symlink target, missing trusted-mode grant. No new ambient host reach-back path; nothing falls back to host execution. **Aligned with Principle I.**
- **Typed Authority**: Authority executes only in the Go `host.app.open-resource` provider after Go field-validates a re-decoded `OpenResourceIntent`. A declarative/JS command adapter may only parse argv and propose a structured intent; it never passes raw argv to the host and carries zero host authority. The app-identity registry is Core/package-owned; untrusted layers reference `appRef` by stable id only. **Aligned with Principle II** (editors are a recipe, not Core semantics).
- **Workspace And Policy**: Defaults new privacy/hardened profiles to `pathMode=alias`; does not alter HostFS/env policy/proxy secrets; existing profiles/environments are never silently changed; a `pathMode` change enters the existing drift model (workspace axis) requiring explicit recreate. Safe mode is a low-risk default-allowed capability; trusted mode is an explicit revocable operator grant. **Aligned with Principle III.**
- **Generality And Provider Scope**: Core owns a generic registry + `host.app.open-resource` capability + app-identity registry. `code`/VS Code is a **recipe** (binding + grammar + registered app id). `cursor`/`zed` are future recipes over the same capability. adb/AppleScript/result-streaming are design-ready only. No specific editor becomes Core semantics.
- **Evidence And Redaction**: Typed `ide.open` projection audit; doctor/inspection of projected capabilities, bindings, mode, PATH shadow order; three-channel privacy verification is real-backend evidence with per-channel detector self-test + preserve-mode positive control. Host path/username/tokens/raw argv never appear in any guest-facing or exported surface. **Aligned with Principle IV.**
- **Backend And Distribution**: Reuses existing broker/opener; VS Code launch is a host-side provider (no guest package install). App-identity registry ships as package-owned static data. Native backend is a mechanics-only harness; guest-visible and privacy claims require real Lima. No new InitTask needed for v1 (profile default change + capability registration are typed plan/apply, not scripts). **Aligned with Principle V.**
- **Gates**: Gate 0 for registry/grammar/intent/no-fallback/redaction/schema. Real Lima Gate 2 for the guest-visible `code .` open, safe-mode auto-exec-did-not-run, trusted-mode grant/revoke, and three-channel username-hiding (with control + self-test). Real Gate 3 privacy assertions must still pass with the projected/aliased environment.
- **Status And Docs**: Update `docs/STATUS.md` (new implemented row, honest real-Lima evidence provenance), `docs/threat-model.md` (scoped username-hiding claim + adjacent non-claim; projection escape non-claims), a projection design doc (new or in an existing design doc), `docs/privacy-run-test-plan.md` (Gate 2/Gate 3 lanes), README (the `code .` workflow, safe-mode default), and the claim-boundaries + productevidence registry.

**Result**: PASS. No constitutional violations; no Complexity Tracking entries required.

## Project Structure

### Documentation (this feature)

```text
specs/030-host-capability-projection/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── capability-registry.md
│   ├── code-open-grammar.md
│   ├── broker-app-open-action.md
│   └── host-app-mode-and-grant.md
└── tasks.md             # Phase 2 output (/speckit-tasks)
```

### Source Code (repository root)

```text
internal/
├── hostcap/                     # NEW: Core-owned capability registry + provider
│   ├── registry.go              #   CapabilityDescriptor, static registry, lookups
│   ├── descriptor.go            #   descriptor types (riskClass, resultPolicy, ...)
│   ├── appid.go                 #   Core/package-owned appRef -> host binary/bundle
│   ├── intent.go                #   OpenResourceIntent decode + field validation
│   ├── openresource.go          #   host.app.open-resource provider
│   └── appopen/
│       └── vscode.go            #   safe/trusted VS Code launcher (host-side)
├── cmdproxy/                    # EXTEND: add `code` Registration + binding model
│   └── cmdproxy.go              #   code binding -> host.app.open-resource action
├── cmdgrammar/                  # NEW: declarative command grammar -> intent
│   └── code.go                  #   code-open-v1 grammar (positional + -g/-n/-r flags)
├── broker/
│   └── broker.go                # EXTEND: route host.app.open-resource, ResourceRef
├── manager/
│   ├── run_plan.go              # EXTEND: workspace ResourceRef mapping (Core-only host path)
│   ├── hostcap_projection.go    # NEW: projection wiring, inspection, host-app-mode grant
│   └── run_dataplane.go         # EXTEND: register code binding + provider per run
├── profiletemplate/
│   └── template.go              # EXTEND: privacy/hardened default pathMode=alias
├── recovery/
│   └── registry.go              # EXTEND: projection.* recovery codes
├── productevidence/
│   └── claims.go                # EXTEND: 030 proof ids + claim ids
└── doctor/
    └── ...                      # EXTEND: `doctor --feature projection`

cmd/
└── hideout-shim/                # EXTEND (if needed): code shim passthrough to broker

schemas/
├── capability-descriptor.schema.json     # NEW
└── open-resource-intent.schema.json      # NEW

scripts/
├── test-host-capability-projection-smoke.sh  # NEW: Gate 0 mechanics
└── test-gate2-lima.sh                         # EXTEND: code . + safe-mode + privacy

docs/
├── host-capability-projection.md   # NEW design doc (discover vs open vs future)
├── STATUS.md / threat-model.md / privacy-run-test-plan.md / README*  # EXTEND
```

**Structure Decision**: Single Go project, extending the existing `internal/` layout. The one new authority family lives in a new `internal/hostcap` package so the capability registry, provider, app-identity registry, and intent validation are Core-owned and isolated from the untrusted grammar/adapter layer. Command grammar (`internal/cmdgrammar`) is a separate, authority-free parsing layer. Everything else extends existing packages (cmdproxy, broker, manager, profiletemplate, doctor, recovery, productevidence).

## Complexity Tracking

No constitutional violations. No entries required.
