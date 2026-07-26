# Research: Ordinary User Release

## Decision 1: Target a self-service prerelease, not GA

**Decision**: Preserve the professional individual macOS arm64 audience and
prerelease maturity. The release removes avoidable operator assembly and
maintainer-facing UX from the primary journey, but it does not add unattended
updates, a support SLA, guest-root containment, workspace DLP, or unsupported
platform claims.

**Rationale**: The repository already has a published supervised alpha contract
and strong real-backend evidence. The remaining gap is self-service delivery,
not evidence for the broader guarantees a GA label would imply.

**Alternatives considered**:

- Promote directly to beta/GA: rejected because runtime freshness, automatic
  update policy, cross-platform coverage, and a maintenance SLA remain explicit
  non-claims.
- Keep the release “supervised” without changing UX: rejected because the user
  explicitly requested an ordinary-user release convergence version.

## Decision 2: Make primary help concise and preserve a complete index

**Decision**: `hideout help` and no-argument invocation render a short supported
journey. `hideout help --all` renders the complete current command inventory.
`hideout help <topic>` and `<topic> --help` provide contextual help for the
ordinary-user journey.

**Rationale**: The current top-level help mixes setup, routine operations,
advanced profile internals, build helpers, and laboratory probes. Removing
commands would break discoverability; separating views preserves compatibility
while making the intended path obvious.

**Alternatives considered**:

- Delete advanced commands from help: rejected because experienced operators
  still need a complete supported index.
- Add a second onboarding binary: rejected because it would create another
  product entry point and distribution artifact.
- Leave help unchanged and rely on documentation: rejected because first-run
  commands must be discoverable without opening repository docs.

## Decision 3: Project concise doctor output from the existing report

**Decision**: Keep `doctor.Report` and JSON output authoritative. Default light
human output renders a concise readiness projection. `--verbose`,
`--level deep`, or explicit `--feature` selection renders the complete current
human report.

**Rationale**: The current default prints every passing subsystem and internal
fields. The report already contains stable statuses, recovery codes, reasons,
hints, and next actions. A renderer can select the user-actionable subset
without recomputing any check.

**Alternatives considered**:

- Add a separate `status` checker: rejected because it would duplicate doctor
  facts and could drift.
- Change JSON to match the shorter output: rejected because automation and
  evidence require the complete structured report.
- Hide all warnings: rejected because missing setup and degraded states must
  remain visible.

## Decision 4: Add a bounded JSON support report, not a raw log archive

**Decision**: `hideout support report --out <path>` writes one strict
`hideout.support-report/v1` JSON artifact. It contains binary identity,
support-matrix identity, applicable package verification, a redacted light
doctor report, recovery guidance, collection status, and provenance. It does
not include raw audit records or workspace files.

**Rationale**: A JSON artifact is inspectable, schema-validatable, bounded, and
compatible with the existing export/redaction model. A tar archive encourages
unbounded file collection and makes path/symlink handling harder to audit.

**Alternatives considered**:

- Zip the entire log/store directory: rejected because it would leak secrets,
  host paths, and user data.
- Reuse only `doctor --evidence-out`: rejected because it omits binary/package
  identity and does not provide a stable support handoff contract.
- Upload automatically: rejected because external transmission requires
  separate authority and privacy design.

## Decision 5: Pin and build `tun2socks` v2.6.0 as a package-owned guest helper

**Decision**: Build `github.com/xjasonlyu/tun2socks/v2` v2.6.0 for the package's
Linux guest architecture from an isolated checked-in Go module. Add the helper
and its manifest to the Hideout package, copy the upstream MIT license, and
record the separate dependency graph in third-party notices.

**Rationale**: Existing Gate 3 and first-run fixtures already build and exercise
v2.6.0. Upstream identifies v2.6.0 as a stable tagged module under the MIT
license and publishes Linux arm64/amd64 assets. Keeping the tested pin avoids
changing network behavior while closing the hidden-helper assembly gap.

**Alternatives considered**:

- Upgrade immediately to the newer v2.7.0: rejected for this slice because the
  current architecture and real evidence are pinned to v2.6.0; a version bump
  requires its own compatibility and network-gate review.
- Download an upstream binary during first run: rejected because runtime
  network fetch and mutable upstream availability would undermine package
  identity.
- Require Homebrew or the operator to install the helper: rejected because the
  binary executes inside Hideout's guest network boundary and is a product
  dependency.
- Import the upstream command into the Core module: rejected because it would
  enlarge the trusted Core dependency graph and would not preserve executable
  provenance separation.

## Decision 6: Preserve explicit development override semantics

**Decision**: A valid explicit helper override may continue to take precedence
for source development and gates, but diagnostics and evidence label it as an
override. An installed release without an override resolves its package-owned
helper. Missing/invalid override data never falls through invisibly.

**Rationale**: Existing gates need controlled fixtures, while ordinary package
claims must bind to package bytes. Provenance, not only executable presence,
determines what claim an observation can satisfy.

**Alternatives considered**:

- Remove overrides: rejected because it harms development, negative fixtures,
  and backend testing.
- Silently fall back from a broken explicit override to the package helper:
  rejected because an explicit selection must fail closed.

## Decision 7: Keep updates provider-owned and make guidance explicit

**Decision**: Homebrew users update, repair, and uninstall through Homebrew.
Standalone users use the existing package verification, repair, install, and
uninstall surfaces. This slice improves contextual help, package status, and
documentation; it does not add an auto-update daemon or mutate Homebrew Cellar
files independently.

**Rationale**: The existing package lifecycle already proves migration-range
checks, state preservation, repair, and purge. Duplicating package-manager
ownership would create conflicting writers.

**Alternatives considered**:

- Background automatic updates: rejected because it adds network authority,
  signing policy, rollback scheduling, and lifecycle complexity outside this
  prerelease.
- A generic “self-update” that overwrites the running binary: rejected because
  it conflicts with Homebrew ownership and signed-package provenance.

## Decision 8: Add one exact-package ordinary-user acceptance proof

**Decision**: Add an acceptance runner that binds the new help, concise doctor,
support report, packaged helper, package lifecycle, first run, and required UI
journeys to one retained candidate digest. It produces registered product
evidence consumed by existing release readiness.

**Rationale**: Individual local tests can all pass against different builds and
still fail to prove the bytes users download. The existing 033 publication
model already knows how to bind evidence to a candidate.

**Alternatives considered**:

- Treat Gate 0 plus old feature receipts as sufficient: rejected because new
  package content and user-facing behavior must be observed on the new exact
  candidate.
- Create an independent release system: rejected because it would duplicate
  033 release identity, signing, notarization, and receipt contracts.

## Evidence consulted

- Current repository implementation and tests at HEAD on 2026-07-26.
- Existing feature contracts 013, 024, 033, 038, and 040-043.
- Upstream `xjasonlyu/tun2socks` v2.6.0 release and module metadata: tagged
  stable module, MIT license, Linux release targets.
- Current repository `docs/DEBT.md`, which already makes helper packaging due
  before privacy mode is recommended to real users.
