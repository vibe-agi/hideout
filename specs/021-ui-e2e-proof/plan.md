# Implementation Plan: UI E2E Proof

<!-- markdownlint-disable MD013 -->

**Branch**: `021-ui-e2e-proof` | **Date**: 2026-07-09 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/021-ui-e2e-proof/spec.md`

## Summary

021 adds a local UI proof layer over the existing daemon-served WebUI and
`hideout tui` surfaces. It does not redesign the UI or add product authority.
The implementation establishes `hideout.product-hardening-evidence/v1`, then
uses targeted browser and PTY evidence to prove the current operator console is
visible, live, authenticated, and not secretly polling while a daemon event
stream is healthy.

The browser proof opens the served WebUI in a real local browser context,
observes console panels, applies a live event to visible DOM state, performs a
low-risk notice acknowledgement round trip through the existing Manager route,
and records redacted artifacts. The terminal proof launches the real
`hideout tui` command under a pseudo-terminal or equivalent terminal harness and
captures visible state changes from daemon events. Missing browser or PTY
prerequisites produce explicit `not-run` evidence and cannot satisfy 021
completion by themselves.

## Technical Context

**Language/Version**: Go 1.25.0 for product code, schemas, scripts, and test
harness glue. Browser automation uses a test-only Node CDP driver with local
Chrome/Chromium when available, but it is not a product runtime dependency.

**Primary Dependencies**: Existing `internal/daemon`, `internal/manager`,
`internal/liveconsole`, decision/notice routes, schema validator, shell smokes,
and JSON schema tooling. New dependencies are limited to a test-only browser
driver and a PTY/terminal harness or platform tool used by the proof runner.

**Storage**: Local JSON evidence manifests plus relative artifact references
under a temporary or caller-provided evidence directory. No durable product state
is introduced beyond existing daemon/Manager state used by the proof fixture.

**Testing**: `go test ./...`, targeted UI E2E proof script, schema validation for
the product-hardening evidence manifest, markdownlint for docs/specs, and Gate 0
integration that records `not-run` rather than pass when optional local UI E2E
dependencies are unavailable.

**Target Platform**: macOS arm64 first-class local alpha path; Linux supported
for the same proof where browser/PTY dependencies are available. Native backend
remains a weak local harness and is not isolation evidence.

**Project Type**: Go CLI plus local daemon, WebUI, TUI, JSON schemas, shell
smoke scripts, and docs.

**Performance Goals**: Proof runs should be deterministic and bounded for local
developer use. Healthy-stream assertions must observe zero hidden overview/audit
polling during the proof window.

**Constraints**: No new HostFS, network, backend, adapter, export/share,
browser-control, remote approval, or product UI framework authority. Skipped
browser/PTY prerequisites are `not-run`, not pass. All artifacts must pass
control-plane redaction before covering a claim.

**Scale/Scope**: Single local operator, one daemon/WebUI fixture, one browser
page, one TUI process, and one evidence manifest per proof run.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Touches UI observation, daemon event subscription,
  existing notice acknowledgement, test fixture lifecycle, and evidence files.
  Unsupported browser/PTY/daemon prerequisites fail as `not-run` or failure
  evidence; wrong tokens, hidden polling, redaction failure, or action failures
  prevent covered claims.
- **Typed Authority**: The only required action is existing Manager-owned notice
  acknowledgement through the current authenticated route and Go validator.
  Browser automation proposes no host mutation, HostFS apply, adapter approval,
  export release, or new authority.
- **Workspace And Policy**: Does not alter workspace mounts, HostFS grants,
  passthrough mounts, env policy, proxy secrets, profile state, or high-risk
  overrides. Test fixtures may create temporary stores and notices only.
- **Generality And Provider Scope**: Node/CDP browser and `script(1)` terminal
  mechanisms are explicitly test providers, not product semantics. The feature
  proves current Hideout WebUI/TUI surfaces and does not choose a future UI
  framework.
- **Evidence And Redaction**: Adds a product-hardening evidence manifest,
  screenshots or DOM summaries, terminal captures, event summaries, and command
  summaries. Redaction must remove tokens, claim tokens, proxy secrets,
  generated machine ids, hidden runtime credential paths, and raw staged HostFS
  content before artifacts cover claims.
- **Backend And Distribution**: Uses local daemon/WebUI/TUI only. No real Lima,
  DNS, HostFS data-plane, package, or release-candidate evidence is introduced.
  Browser/PTY tools are proof prerequisites, not helper artifacts.
- **Gates**: Gate 0 covers schemas, docs, unit/contract checks, and local smoke
  behavior. The targeted UI E2E proof lane must actually execute before marking
  021 complete; when Gate 0 runs on a host without browser/PTY dependencies it
  records `not-run` rather than a pass.
- **Status And Docs**: Update `docs/privacy-run-test-plan.md`,
  `docs/tui-webui-experience.md`, and `docs/STATUS.md` to describe UI E2E proof
  and its boundaries. Do not claim release readiness from local UI E2E alone.

**Initial Constitution Result**: PASS. No authority expansion; evidence and
not-run semantics are explicit.

## Project Structure

### Documentation (this feature)

```text
specs/021-ui-e2e-proof/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── product-hardening-evidence.md
│   └── ui-e2e-proof.md
└── tasks.md
```

### Source Code (repository root)

```text
schemas/
└── product-hardening-evidence.schema.json

internal/
├── productevidence/        # manifest model, writer, redaction, schema helpers
├── manager/                # targeted notice/WebUI test hooks only if needed
├── daemon/                 # test fixture wiring only if needed
└── app/                    # TUI PTY test seams only if needed

scripts/
└── test-ui-e2e.sh          # local proof runner, writes evidence manifest

test/
└── e2e/
    ├── webui/              # Node/CDP browser driver assets
    └── tui/                # script(1)/terminal harness assets

docs/
├── privacy-run-test-plan.md
├── tui-webui-experience.md
└── STATUS.md
```

**Structure Decision**: Use a small Go-owned `internal/productevidence` package
for manifest shape and redaction so 022-025 reuse the same proof vocabulary.
Keep browser and PTY drivers under `test/e2e` or the smoke script path because
they are verification infrastructure, not product code. Do not place
browser-control or terminal automation under product Manager/daemon packages
unless a narrow test seam is required and default product behavior remains nil.

## Complexity Tracking

No constitution violations. The new evidence package is justified because
021-025 need one stable proof manifest; ad hoc script output would recreate the
claim-mapping gap this series is meant to close.

## Phase 0: Research

Research resolves provider and proof-boundary choices before implementation:

- evidence manifest ownership and relation to 016 release readiness;
- browser proof provider and missing-prerequisite behavior;
- required low-risk browser action;
- daemon versus fixture server split;
- TUI PTY harness and deterministic event seam boundary;
- hidden-polling measurement;
- artifact redaction and canary injection;
- Gate 0 placement and targeted completion requirements.

## Phase 1: Design

Design artifacts specify:

- `hideout.product-hardening-evidence/v1` manifest entities and JSON contract;
- UI E2E proof contract for browser and terminal lanes;
- redaction and not-run semantics;
- quickstart scenarios mapping every FR/SC to a proof, unit test, or explicit
  out-of-scope boundary.

## Post-Design Constitution Re-Check

- **Privacy Boundary**: PASS. Design uses existing daemon/WebUI/TUI and notice
  acknowledgement only; every unsupported condition is failed or `not-run`.
- **Typed Authority**: PASS. No new action provider; notice ack remains the only
  required authenticated action.
- **Workspace And Policy**: PASS. No workspace, HostFS, policy, or profile grant
  changes.
- **Generality And Provider Scope**: PASS. Browser and PTY are test providers,
  explicitly not product capabilities.
- **Evidence And Redaction**: PASS. Manifest contract includes stable proof ids,
  claim mapping, artifacts, prerequisite status, and redaction status.
- **Backend And Distribution**: PASS. Local UI proof is separated from real
  backend/release evidence.
- **Gates**: PASS. Gate 0 can record `not-run`; 021 completion requires targeted
  executed proof lanes.
- **Status And Docs**: PASS. Docs updates are limited to test-plan and UI proof
  truth, without overclaiming release readiness.
