# Implementation Plan: HostFS Discoverable Namespace

<!-- markdownlint-disable MD013 -->

**Branch**: `029-hostfs-discoverable-namespace` | **Date**: 2026-07-10 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/029-hostfs-discoverable-namespace/spec.md`

## Summary

Add an explicit HostFS `discover` capability that lets an isolated target see
operator-selected names and coarse node kinds without receiving content or
write authority. `see:`, `see-dir:`, and `see-tree:` compile into the existing
per-root HostFS rule model; the HostFS service constructs a complete-or-error
synthetic namespace; an additive typed broker error record replaces stderr as
the errno authority; and eligible locked reads create a bounded, asynchronous
`hostfs.read` decision. Approval writes an exact-file, current-session grant
under the private session store, and the already-running broker recognizes it
on the target's next retry by synchronously checking that grant before denial.

The feature keeps legacy profiles unchanged unless they add an explicit
discover rule, preserves reserved-root and operation-specific deny precedence,
does not block a filesystem call while waiting for a person, and does not claim
that hidden predictable paths are unknowable. Gate 0 proves policy, lifecycle,
limits, migration, redaction, and schema behavior; real Lima Gate 2 proves the
guest-visible FUSE namespace, errno, cache convergence, and cross-process live
grant path.

## Technical Context

**Language/Version**: Go 1.25; POSIX shell for gates; embedded JavaScript only
for the existing WebUI reducer tests (no JavaScript authority)

**Primary Dependencies**: Go standard library; `github.com/hanwen/go-fuse/v2`
v2.10.1; `golang.org/x/sys` v0.46.0 for advisory file locking; existing
Manager, broker, decision, HostFS, profile-template, doctor, audit, and
product-evidence packages

**Storage**: Existing JSON profile files and decision store; append-only local
audit JSONL; new private per-session HostFS read state beneath
`$HIDEOUT_STORE_ROOT/sessions/<session>/hostfs-read/`, written atomically under
an advisory lock and removed with other ephemeral session authority

**Testing**: `go test ./...`, targeted package tests, JSON Schema validation,
shell smoke tests in Gate 0, docs truth/markdownlint, and real macOS arm64 Lima
Gate 2 through `scripts/test-gate2-lima.sh`

**Target Platform**: macOS arm64 host with a Linux Lima guest for product
claims; native remains a weak local harness; the guest helper remains the
packaged Linux `hideout-hostfsd`

**Project Type**: Single Go CLI/control-plane application with a packaged Linux
FUSE helper and local WebUI/TUI operator surfaces

**Performance Goals**: Hidden or locked HostFS calls return immediately within
the existing broker timeout; approved content succeeds on the next unchanged
retry; locked-to-granted stat presentation converges within one second; no
eager recursive indexing; at most four concurrent host enumeration calls per
session

**Constraints**: Successful listings are complete relative to the declared
visibility domain; directories are limited to 4096 entries and discovery to 32
relative path components; each session has at most eight pending read
decisions and eight newly created read decisions per rolling 60 seconds;
decision timeout is five minutes; an approved grant expires at session end or
24 hours after approval, whichever comes first; untrusted reason text is at
most 512 UTF-8 bytes

**Scale/Scope**: One professional operator on one machine, tens of concurrent
sessions at most, ordinary developer home trees, no multi-tenant policy,
marketplace authority, background index, or organization approval workflow

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

- **Privacy Boundary**: **PASS pre-design / PASS post-design.** The feature
  changes HostFS name visibility and exact-file session read authority. Reserved
  roots, discover denies, read denies, incomplete enumeration, malformed grant
  state, unknown errors, dead sessions, and unavailable host prerequisites fail
  closed before content is returned. Discovery is documented as metadata
  disclosure rather than a new containment claim.
- **Typed Authority**: **PASS pre-design / PASS post-design.** Go Core owns
  selector validation, visibility precedence, typed errno mapping, decision
  creation, claim/apply/deny/reopen, session liveness, grant persistence, and
  broker enforcement. CLI, daemon, TUI, and WebUI use the existing Manager API.
  JavaScript receives only redacted decision state and cannot mint a grant.
- **Workspace And Policy**: **PASS pre-design / PASS post-design.** Workspace
  remains a separate direct mount. Discover is an ordinary HostFS operation;
  deny and reserved-root rules win. Broad preset roots exclude the categorized
  credential/browser set, while explicit operator-authored exact content grants
  outside reserved roots keep their existing authority.
- **Generality And Provider Scope**: **PASS pre-design / PASS post-design.** The
  Core vocabulary is generic (`discover`, `hostfs.read`, typed errno). Desktop,
  Documents, Downloads, macOS TCC, and Lima appear only as preset, host
  prerequisite, or release-gate providers.
- **Evidence And Redaction**: **PASS pre-design / PASS post-design.** Decision,
  grant, suppression, and aggregate discovery facts use existing local audit
  and live events. File content, symlink targets, capability/claim tokens, and
  private grant paths are excluded. Exported evidence still crosses the 005
  export boundary. Stable 029 proof IDs distinguish local and real-gate proof.
- **Backend And Distribution**: **PASS pre-design / PASS post-design.** No new
  binary is introduced. The changed `hideout-hostfsd` remains in existing
  install/package manifests and checksum verification. Onboarding changes are
  typed InitTask/profile-template fields, not setup shell authority.
- **Gates**: **PASS pre-design / PASS post-design.** Gate 0 covers policy,
  provider, schema, migration, redaction, and docs. The feature cannot be marked
  Implemented or satisfy its real proof IDs until the 20 promoted assertions
  pass in real Lima Gate 2. Native/local-fast output cannot substitute.
- **Status And Docs**: **PASS pre-design / PASS post-design.** Completion updates
  `README.md`, `README.zh-CN.md`, `docs/STATUS.md`, `docs/threat-model.md`,
  `docs/claim-boundaries.md`, `docs/hostfs-overlay-design.md`,
  `docs/first-run-alpha.md`, `docs/privacy-run-test-plan.md`, and
  `docs/command-examples.json`. Status remains draft until real Gate 2 evidence
  exists.

No constitution violation or compiled-Go flexible product judgment is
introduced. The new Go logic is enforcement, validation, storage, and typed
provider behavior, which the constitution requires Core to own.

## Project Structure

### Documentation (this feature)

```text
specs/029-hostfs-discoverable-namespace/
|-- checklists/requirements.md
|-- contracts/
|   |-- broker-hostfs-error.md
|   |-- read-decision-api.md
|   |-- session-read-grant.md
|   `-- visibility-policy.md
|-- data-model.md
|-- plan.md
|-- quickstart.md
|-- research.md
|-- spec.md
`-- tasks.md
```

### Source Code (repository root)

```text
cmd/hideout-hostfsd/
|-- main_linux.go                 # typed errno mapping and bounded FUSE TTLs
`-- main_linux_test.go

internal/
|-- app/
|   `-- app.go                    # selector/migration/init/doctor/decision CLI
|-- broker/
|   |-- broker.go                 # additive typed response error
|   `-- hostfs.go                 # provider proposal/result mapping
|-- decision/
|   |-- store.go                  # atomic provider-aware reopen primitive
|   `-- types.go                  # hostfs.read kind and reopen action
|-- doctor/
|   `-- report.go                 # explicit HostFS probe findings
|-- hostfs/
|   |-- hostfs.go                 # discover op, scopes, precedence
|   |-- readgrant/                # private session grant/index persistence
|   |-- service.go                # coarse namespace and complete-or-error list
|   `-- *_test.go
|-- hostpathrisk/                 # one categorized sensitive-root catalog
|-- inittask/
|   `-- inittask.go               # visibility plan/review/evidence fields
|-- manager/
|   |-- decisions.go              # hostfs.read claim/apply/deny/reopen
|   |-- hostfs_read.go            # Go-owned decision provider and limits
|   |-- profile_hostfs.go         # typed legacy-list migration plan/apply
|   |-- run_dataplane.go          # owner lock and provider/grant wiring
|   |-- routes.go                 # reopen endpoint inventory
|   `-- server.go                 # redacted decision action rendering
|-- productevidence/
|   |-- claims.go
|   `-- registry.go               # stable 029 proof registry entries
|-- profile/
|   `-- profile.go                # discover/profile validation and migration load
|-- profiletemplate/
|   `-- template.go               # none/landmarks/home-tree expansion
`-- session/
    `-- session.go                # owner/read-state layout and cleanup

schemas/
|-- broker-envelope.schema.json
|-- decision-record.schema.json
|-- hostfs-read-grants.schema.json
|-- init-plan.schema.json
|-- onboarding-evidence.schema.json
|-- profile.schema.json
`-- run-plan.schema.json

scripts/
|-- test-gate0.sh
|-- test-gate2-lima.sh
|-- test-hostfs-visibility-e2e.sh
`-- test-doc-truth-smoke.sh
```

**Structure Decision**: Extend the current Go authority packages and existing
HostFS helper. `internal/hostfs/readgrant` isolates the cross-process artifact
format and locking from policy evaluation; `internal/hostpathrisk` removes the
existing sensitive-root duplication risk. No second application, service,
database, or helper binary is added.

## Complexity Tracking

No constitution violations require justification.
