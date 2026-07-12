# Implementation Plan: Packaging Sweep

<!-- markdownlint-disable MD013 -->

**Branch**: `017-packaging-sweep` | **Date**: 2026-07-09 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/017-packaging-sweep/spec.md`

## Summary

Tighten the alpha package lifecycle built in 013 so upgrade, verify, repair,
and uninstall are supportable for external alpha users. The package remains
tarball/install-script based and the existing `internal/packagekit` package
stays the Go-owned lifecycle boundary. 017 adds obsolete package-owned file
detection, report-first repair, stronger migration compatibility checks, honest
external prerequisite reporting for `tun2socks`, and package smoke coverage for
those paths.

## Technical Context

**Language/Version**: Go 1.25 module plus POSIX shell smoke/package wrappers.

**Primary Dependencies**: Standard library filesystem, checksum, JSON, and
process utilities already used by `internal/packagekit`; existing markdown,
schema, and smoke tooling.

**Storage**: Filesystem package artifact, install prefix, installed-state
manifest under the prefix, durable Hideout store, and survivor purge audit.

**Testing**: `go test ./...`, targeted `internal/packagekit` and
`internal/app` tests, package smoke, Gate 0, markdownlint, schema validation,
`gofmt -l`, `go vet ./...`, and `git diff --check`.

**Target Platform**: macOS host first-class; Linux amd64/aarch64 smoke for
installed artifact discovery where available. Windows remains out of scope.

**Project Type**: CLI/local control-plane product with helper binaries and
package lifecycle commands.

**Performance Goals**: Package verify, obsolete scan, and repair planning
complete in under 2 seconds for the alpha package file count on a normal local
filesystem.

**Constraints**: No Homebrew/signing/notarization release bar; no auto-update;
no package metadata shell execution; no automatic obsolete-file deletion during
upgrade; no durable state removal without explicit purge; `tun2socks` remains
an external prerequisite in v1.

**Scale/Scope**: One local operator, one package root, one install prefix, and
hundreds of package-owned files at most.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Touches install/runtime lifecycle and helper/prereq
  reporting only. It fails closed on missing manifests, unprovable ownership,
  unsupported migration range, path escape, helper/schema/script mismatch, and
  ambiguous repair targets before mutation or success claims.
- **Typed Authority**: `hideout package install|verify|repair|uninstall` are
  Go-owned lifecycle operations under `internal/packagekit` and `internal/app`
  dispatch. No JavaScript/config authority participates.
- **Workspace And Policy**: Does not alter workspace mounts, HostFS, env
  policy, proxy secrets, profile policy, or run authority. Durable store state
  is preserved by default and removed only by explicit purge.
- **Generality And Provider Scope**: Generic alpha package lifecycle. Homebrew,
  signing, notarization, Windows packaging, and vendored `tun2socks` are
  deferred provider/distribution surfaces.
- **Evidence And Redaction**: Package verify/upgrade/repair/uninstall output,
  package lifecycle audit, survivor purge audit, package smoke, and Gate 0
  prove behavior. These surfaces must not leak Hideout control-plane secrets.
- **Backend And Distribution**: Tightens helper artifact discovery and
  installed package state. It does not change backend capabilities or first-run
  InitTask repair behavior.
- **Gates**: Gate 0 plus package smoke. No real Gate 2/3 required because this
  feature does not change isolation, HostFS data-plane, DNS mediation, or
  backend runtime behavior.
- **Status And Docs**: Update package docs, `docs/STATUS.md` if status wording
  changes, and `docs/privacy-run-test-plan.md`/Gate 0 references if smoke scope
  changes.

Post-design re-check: PASS. The design keeps package mutation in Go, makes
repair explicit, preserves durable state, does not broaden runtime authority,
and adds Gate 0 package smoke coverage without new isolation claims.

## Project Structure

### Documentation (this feature)

```text
specs/017-packaging-sweep/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── package-command.md
│   ├── package-lifecycle.md
│   └── package-prerequisites.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/packagekit/
├── install.go             # upgrade compatibility and obsolete detection
├── verify.go              # installed/artifact verification and stale-file failures
├── repair.go              # explicit obsolete package-owned file repair
├── manifest.go            # migration and installed-state data model
├── audit.go               # lifecycle audit and survivor purge evidence
└── packagekit_test.go     # package lifecycle unit tests

internal/app/app.go        # package CLI dispatch and output
internal/app/app_test.go   # CLI/package command tests
scripts/test-package-smoke.sh
scripts/test-gate0.sh
schemas/package-manifest.schema.json
docs/
README.md
```

**Structure Decision**: Continue using `internal/packagekit` as the focused
Go-owned package lifecycle boundary. Add repair and stale-file planning there
instead of expanding `internal/app/app.go`; `internal/app` remains CLI parsing
and presentation.

## Complexity Tracking

No constitutional violations. No new project, authority layer, or provider
integration is introduced.
