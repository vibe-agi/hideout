# Implementation Plan: Ordinary User Release

**Branch**: `044-ordinary-user-release` | **Date**: 2026-07-26 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/044-ordinary-user-release/spec.md`

## Summary

Converge the existing public-alpha install, setup, doctor, package lifecycle,
privacy network, UI, and release-evidence paths into one self-service macOS
arm64 prerelease. The implementation will:

1. split concise first-run help from the complete advanced command index;
2. add a concise default doctor projection while preserving detailed and JSON
   evidence;
3. add one bounded redacted support-report command and schema;
4. build, attribute, package, verify, and use a pinned Linux guest
   `tun2socks` helper;
5. make update, repair, uninstall, and purge guidance explicit for Homebrew and
   standalone users; and
6. add an exact-package ordinary-user acceptance proof that feeds the existing
   release-readiness and publication chain.

The slice reuses Manager facts, doctor findings, package manifests, InitTask
repair, export redaction, and the 033 release identity. It introduces no new
host authority and no fallback from privacy to direct networking.

## Technical Context

**Language/Version**: Go 1.25.0; Bash compatible with macOS Bash 3.2 for release
and gate scripts; JSON Schema for persisted/shareable contracts

**Primary Dependencies**: Existing Go standard library and repository modules;
Lima as the supported backend; `github.com/xjasonlyu/tun2socks/v2` pinned at
v2.6.0 in an isolated helper-build module; existing audit, doctor, export,
helper discovery, packagekit, releasecompat, and releasechannel packages

**Storage**: Existing profile/store/package files; one explicit support-report
JSON output; package manifest entries and third-party notice material; no new
ambient database or mutable service

**Testing**: `go test`, race tests for changed stateful packages, JSON Schema
validation, shell contract/smoke tests, package smoke, docs truth, Gate 0,
exact-package macOS arm64 Gate 2 and Gate 3, required UI E2E, signing,
notarization, anonymous download, publication receipt

**Target Platform**: Host `darwin/arm64`; guest helper `linux/arm64`; existing
narrower Linux source/package behavior must not regress but is not promoted by
this release

**Project Type**: Local security CLI plus resident daemon role, guest helper
binaries, package/install scripts, local TUI/WebUI, and release evidence

**Performance Goals**: Concise help renders immediately; light doctor retains
its current bounded local probe behavior and renders no more than 20 non-blank
summary lines for a healthy installation; support report is bounded to 1 MiB
and completes within the light-doctor probe budget; no new first-run network
fetch beyond the declared runtime

**Constraints**: Fail closed; direct remains default and is explicitly
non-private; privacy helper is package-owned but upstream proxy remains
operator-owned; no helper download at runtime; no raw audit/workspace content
in support output; no source-tree commands in ordinary-user guidance; one
retained candidate must be tested and then signed/notarized without rebuild

**Scale/Scope**: One professional individual operator, one local installation,
one default profile and selected project per first-run journey; existing
concurrent-session and shared-default-VM semantics remain unchanged

## Constitution Check

*Pre-research gate: PASS. Post-design gate: PASS.*

- **Privacy Boundary**: The slice touches package ownership, diagnostic/export
  presentation, privacy helper supply, network setup prerequisites, and
  uninstall lifecycle. Missing or mismatched artifacts, unsafe support output
  destinations, absent proxy/DNS prerequisites, and missing real evidence stop
  the affected action. Privacy never becomes direct networking implicitly.
- **Typed Authority**: Existing Manager setup/init, doctor builders, InitTask
  repair, network plans, packagekit validators, and release validators remain
  authoritative. Concise help has no authority. Concise doctor output is a
  projection of `doctor.Report`. Support collection validates Go-owned facts
  and writes only through a Go-owned bounded provider. The guest helper is
  copied through the existing Manager network-helper path after Go-owned
  resolution and package verification.
- **Workspace And Policy**: No workspace mount, HostFS grant, environment
  allowlist, proxy-secret storage, or profile authorization rule changes.
  Support output excludes workspace content and raw host paths. The proxy value
  remains host-only; profiles retain only the existing secret reference and
  resolver.
- **Generality And Provider Scope**: Homebrew and Lima are named release
  providers. `tun2socks` v2.6.0 is a pinned implementation dependency for the
  existing generic privacy-network contract, not a new generic provider
  surface. VS Code, one agent CLI, and the Gate 3 proxy remain examples or
  compatibility fixtures only.
- **Evidence And Redaction**: Full doctor JSON remains authoritative. The
  concise view consumes the same report. The new support report has a strict
  schema, deterministic redaction, a size bound, negative fixtures, and no raw
  audit payload. Package/helper identity, exact candidate digest, Gate 2/3,
  UI, signing, notarization, and publication receipt feed the existing product
  evidence model.
- **Backend And Distribution**: Native remains a weak harness. The package owns
  every supported guest helper, including the pinned privacy helper. Setup and
  repair remain typed InitTask operations; packaging scripts build immutable
  artifacts but do not execute runtime authority.
- **Gates**: Gate 0 covers CLI contracts, schema, package manifest, support
  redaction, help/doctor mutation proofs, package lifecycle, and documentation.
  Clean exact-package Gate 2 covers the default first run, lifecycle,
  projection, and required UI path. Clean exact-package Gate 3 covers privacy
  helper resolution, gateway/proxy/DNS forwarding, redaction, and privilege
  evidence. Release readiness, signing, notarization, anonymous download, and
  publication receipt remain mandatory.
- **Status And Docs**: Update `README.md`, `README.zh-CN.md`,
  `docs/first-run-alpha.md`, `docs/distribution-bootstrap.md`,
  `docs/support-matrix.md`, `docs/claim-boundaries.md`, `docs/STATUS.md`,
  `docs/privacy-run-test-plan.md`, `docs/DEBT.md`, `CHANGELOG.md`, package
  caveats, and release notes/inventory when the candidate is frozen.

## Project Structure

### Documentation (this feature)

```text
specs/044-ordinary-user-release/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── checklists/
│   └── requirements.md
├── contracts/
│   ├── cli-journey.md
│   ├── package-privacy-helper.md
│   ├── support-report.md
│   └── ordinary-user-acceptance.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/app/
├── app.go                         # command dispatch; existing doctor wiring
├── help.go                        # concise/contextual/full help command
└── support_report.go              # support report CLI only

internal/doctor/
├── report.go                      # authoritative findings
├── render.go                      # detailed/JSON rendering
└── summary.go                     # concise projection

internal/supportreport/
├── report.go                      # bounded report model and validation
├── collect.go                     # read-only fact collection
└── write.go                       # safe explicit output provider

internal/helperbin/
└── helperbin.go                   # packaged/development helper resolution

internal/packagekit/
├── manifest.go
├── prereq.go
└── packagekit_test.go

tools/tun2socks-build/
├── go.mod
└── go.sum

third_party/tun2socks/
└── LICENSE

schemas/
└── support-report.schema.json

scripts/
├── install-local.sh
├── package-local.sh
├── test-package-smoke.sh
├── test-ordinary-user-release.sh
└── test-gate0.sh

packaging/homebrew/
└── hideout.rb

docs/
├── first-run-alpha.md
├── distribution-bootstrap.md
├── support-matrix.md
├── claim-boundaries.md
├── privacy-run-test-plan.md
├── STATUS.md
└── DEBT.md
```

**Structure Decision**: Keep security and lifecycle authority in the existing
Go packages. Add new CLI commands in their own files. Keep concise doctor
rendering in `internal/doctor` so every caller projects the same report.
Isolate the third-party helper build graph from the trusted Core module and
make packaging own its output and notice. Extend existing package and gate
scripts instead of creating a second release pipeline.

## Complexity Tracking

No constitutional violations are required. The isolated helper-build module is
intentional supply-chain separation: importing an upstream command into the
trusted Core module would enlarge Core's dependency graph and still would not
produce the separately distributed guest executable.
