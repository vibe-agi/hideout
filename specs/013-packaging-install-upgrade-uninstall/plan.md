# Implementation Plan: Packaging, Install, Upgrade, And Uninstall

<!-- markdownlint-disable MD013 -->

**Branch**: `013-packaging-install-upgrade-uninstall` | **Date**: 2026-07-09 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/013-packaging-install-upgrade-uninstall/spec.md`

## Summary

Promote the existing local package smoke path into an alpha packaging product
path. The package remains tarball/install-script based, but install/upgrade,
verify, and uninstall become typed `hideout package` operations backed by a
package artifact manifest plus an installed-state manifest that records the
actual prefix, installed file checksums, helper/schema/script inventory, and
migration range. The shell installer becomes a thin wrapper over the packaged
`hideout package install` command.

## Technical Context

**Language/Version**: Go 1.25 module plus POSIX shell packaging wrappers.

**Primary Dependencies**: Standard library for filesystem/copy/checksum/tarball
semantics already used in the repo; existing JSON schema validator in tests.

**Storage**: Filesystem package artifact, install prefix, and durable Hideout
store. Installed package state lives under the install prefix, not inside the
durable store.

**Testing**: `go test ./...`, targeted `internal/app` package tests, package
smoke, Gate 0, markdownlint, jq/schema validation, `git diff --check`.

**Target Platform**: macOS host first-class; Linux amd64/aarch64 package smoke
coverage for installed CLI/helper/schema discovery. Windows out of scope.

**Project Type**: CLI/local control-plane product with helper binaries.

**Performance Goals**: Package verify and uninstall dry-run complete in under
2 seconds for the alpha package file count on a normal local filesystem.

**Constraints**: No Homebrew/signing/notarization as release bar; no
auto-update daemon; no arbitrary package metadata shell execution; no durable
state removal without explicit purge; packages are not relocatable after
install.

**Scale/Scope**: One local operator, one install prefix per package operation,
hundreds of package-owned files at most.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: Touches install/runtime lifecycle and helper/schema
  discovery. It fails closed on missing helpers, checksum mismatch, unsupported
  target, incompatible migration range, ambiguous prefix, unowned uninstall,
  or manifest parse failure before package mutation.
- **Typed Authority**: `hideout package install|verify|uninstall` are Go-owned
  operations. The shell installer only dispatches to the packaged Go command
  and does not interpret package authority.
- **Workspace And Policy**: Does not alter workspace, HostFS, env policy,
  proxy secrets, or profile authority. Durable store state is preserved by
  default and removed only by explicit purge.
- **Generality And Provider Scope**: Generic alpha tarball packaging path.
  Homebrew remains a development/smoke artifact, not a release bar.
- **Evidence And Redaction**: Package manifests, verify output, smoke logs, and
  optional package audit records show what happened. Manifests must not include
  control-plane tokens, proxy secret values, UI tokens, generated machine ids,
  or hidden runtime credential paths.
- **Backend And Distribution**: Includes existing helper binaries and schemas.
  First-run repair remains typed InitTask via `hideout init`/`doctor`, not
  package metadata scripts.
- **Gates**: Gate 0 plus package smoke. No real Lima Gate 2/3 needed because
  no isolation behavior changes.
- **Status And Docs**: Update README, docs/README, docs/STATUS,
  docs/privacy-run-test-plan, and package docs to make installed commands the
  primary path.

Post-design re-check: PASS. The design keeps authority in Go, leaves package
metadata declarative, preserves durable state by default, and adds Gate 0
package smoke coverage without making new isolation claims.

## Project Structure

### Documentation (this feature)

```text
specs/013-packaging-install-upgrade-uninstall/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── package-command.md
│   ├── package-manifest.md
│   └── package-smoke.md
└── tasks.md
```

### Source Code (repository root)

```text
internal/app/app.go                 # package CLI dispatch
internal/app/app_test.go            # CLI/package unit tests
internal/packagekit/                # package manifest/install/verify/uninstall
schemas/package-manifest.schema.json
scripts/package-local.sh
scripts/test-package-smoke.sh
scripts/test-gate0.sh
packaging/install-package.sh
docs/
README.md
```

**Structure Decision**: Add `internal/packagekit` so manifest parsing,
checksum validation, install, upgrade, and uninstall are tested independently
from CLI flag parsing. `internal/app` remains the user-facing command layer.

## Complexity Tracking

No constitutional violations. The new package is justified because package
lifecycle code would otherwise expand `internal/app/app.go` with filesystem
copy/checksum/uninstall logic that needs focused tests.
