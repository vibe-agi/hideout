<!-- markdownlint-disable MD013 -->

# Implementation Plan: Unified Named Environments With Declared Base Image

**Branch**: `003-unified-named-environments` | **Date**: 2026-07-06 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/003-unified-named-environments/spec.md`

## Summary

Unify Hideout on one reusable environment model: every reusable environment is
named, explicitly via `env create` or automatically via a deterministic
per-(profile, workspace) name; the most-recently-used fingerprint selection is
removed. Every reusable environment carries a declared base image (disk-image
URL with explicit sha256 digest, or built-in template name), pinned at
creation by validation — no network resolution. The pinned image declaration
is immutable environment data; URL digest mismatch is a boot-time verification
failure, and built-in template mapping changes are represented by backend
configuration version. Use-time drift comparison covers backend configuration
version and pinned workspace. Expected-command declarations are evaluated live
as diagnostics and are not identity. Existing environment records are
invalidated by a record-version bump with clean-and-recreate guidance; no
migration and no compatibility layer, per the clean-change principle for an
unreleased product.

## Technical Context

**Language/Version**: Go 1.25.0 plus existing POSIX shell test/gate scripts.

**Primary Dependencies**: Existing packages only — `internal/environment`
(store, `Spec`, `Record` with an existing `Version` field),
`internal/manager` (`run_environment.go` selection, `environment_lifecycle.go`
stop/clean, summaries, API), `internal/backend/lima` (generated lima.yaml;
base template currently hardcoded at `lima.go:398`), `internal/profile` and
`schemas/profile.schema.json` (new `environment.baseImage` field),
`internal/app` (CLI). No new third-party dependencies; no registry client.

**Storage**: Environment records under the existing store
(`environments/`), JSON with a bumped record version. Profile JSON/schema
gains one field. No new stores, caches, or image artifacts owned by Hideout
(image download/cache stays inside Lima's own mechanism).

**Testing**: `go test ./...`, targeted environment/manager/app/profile/schema
tests, `scripts/test-gate0.sh`, and a real Lima gate variant proving declared
image boot, wrong-digest fail-closed, and recreate recovery.

**Target Platform**: macOS host with Lima as the primary backend; native
backend follows the same record model with no VM lifecycle.

**Project Type**: Local privacy-runner CLI with Manager Core, typed profile
state, schemas, and documentation/spec artifacts.

**Performance Goals**: `env create` is local validation plus record write (no
guest boot, no network); run-time drift checking adds only record comparison
to existing selection.

**Constraints**: No image building, caching services, or credential handling;
references embedding credentials rejected; no OCI registry semantics; no
daemon; no dynamic mounts; `default` reserved; no record migration.

**Scale/Scope**: One slice across environment store, run selection, Lima
config generation, profile/schema, CLI, Manager summaries/lifecycle, and
docs/tests. Single-operator machine scale; environment counts in the tens.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

- **Privacy Boundary**: PASS. Touches environment lifecycle and backend
  preparation input; adds no host reach-back. Fail-closed cases: invalid or
  digest-less image references at create, digest mismatch or unpullable image
  at boot, backend/workspace drift at use, reserved/colliding names, dangerous
  workspace roots, destructive commands on running guests without force, and
  prior-version records on any touch.
- **Typed Authority**: PASS. Environment create/recreate/remove are Manager
  plan/apply operations executed by Go; CLI/TUI/WebUI share them. No script
  or config gains execution authority; the image declaration is data compiled
  by Go into backend preparation.
- **Workspace And Policy**: PASS. Workspace pinning reuses the existing
  dangerous-root guard and high-risk override; HostFS semantics unchanged
  except a new non-blocking shadowed-rule warning; deny precedence untouched.
- **Generality And Provider Scope**: PASS. Image declaration is a generic
  reference (URL+digest or built-in template name). No registry product or
  image ecosystem becomes Core semantics; the built-in default becomes an
  explicit profile value, removing a backend hardcode.
- **Evidence And Redaction**: PASS. Create/recreate/remove and drift
  rejections are audited; `env list`/`inspect` and run output name the
  environment; image references are operator data recorded verbatim;
  control-plane redaction unchanged; embedded-credential references rejected
  at validation.
- **Backend And Distribution**: PASS. Lima consumes the declaration through
  its existing images mechanism with digest verification; native is a weak
  harness with the same record model and no VM; no new helper artifacts; no
  InitTask scripts.
- **Gates**: Gate 0 plus package tests for model/naming/drift/validation; the
  Lima gate variant for image boot, wrong-digest failure, and recreate
  recovery;
  `--quick` stays green throughout.
- **Status And Docs**: `docs/STATUS.md` (environment model and identity
  wording), `docs/privacy-run-design.md` (environment chapter:
  named model implemented, MRU removed, identity formula updated per
  clarification), `docs/privacy-run-test-plan.md` (change-to-gate row and
  Lima gate variant), `README.md`/`README.zh-CN.md` (env commands replace
  `hideout list`), `docs/backend-capability-matrix.md` already lists the
  image row (verify wording only).

**Pre-design result**: PASS. No constitution violation or complexity
exception is required.

## Project Structure

### Documentation (this feature)

```text
specs/003-unified-named-environments/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── environment-model.md
└── tasks.md        # generated by /speckit-tasks after this plan
```

### Source Code (repository root)

```text
internal/
├── environment/        # Record/Spec: name, image fields, version bump, name index
├── manager/            # run_environment selection rewrite, lifecycle ops, summaries, API
├── backend/
│   └── lima/           # generated lima.yaml images from declaration; remove hardcoded base
├── profile/            # environment.baseImage field + validation
└── app/                # env command family, run --env, list removal, shadow warning surface

schemas/
└── profile.schema.json # environment.baseImage

docs/
├── STATUS.md
├── privacy-run-design.md
├── privacy-run-test-plan.md
README.md / README.zh-CN.md

scripts/                # Lima gate variant for image boot + drift + recreate
```

**Structure Decision**: Stay inside existing packages; no new top-level
package. The environment store owns naming and identity; Manager owns
lifecycle operations; the backend consumes the compiled image declaration.

## Phase 0: Research

See [research.md](research.md).

## Phase 1: Design

See [data-model.md](data-model.md),
[contracts/environment-model.md](contracts/environment-model.md), and
[quickstart.md](quickstart.md).

## Post-Design Constitution Check

- **Privacy Boundary**: PASS. Contracts define fail-closed behavior for every
  invalid input class before backend preparation; drift never auto-rebuilds.
- **Typed Authority**: PASS. The data model stores declarations and identity
  facts; all mutations go through Manager plan/apply; the image declaration
  never becomes an execution hook.
- **Workspace And Policy**: PASS. Pinning and the shadow warning reuse
  existing guards; no authority broadening.
- **Generality And Provider Scope**: PASS. Contracts accept only generic
  reference forms; OCI and builders are explicitly out of scope.
- **Evidence And Redaction**: PASS. Contracts enumerate audit events and
  verbatim-user-data handling; no new secrets exist in this slice.
- **Backend And Distribution**: PASS. Only Lima's existing image mechanism is
  used; native carries records without VM lifecycle.
- **Gates**: PASS. Quickstart maps every story to unit/schema/Gate 0/Lima
  gate evidence.
- **Status And Docs**: PASS. Doc updates are enumerated in the check above
  and carried into tasks.

## Complexity Tracking

No constitution violations or exceptional complexity are required.
