# Data Model: Alpha First-Run E2E

## FirstRunEvidence

Product-hardening manifest entries for 022.

Fields:

- `version`: `hideout.product-hardening-evidence/v1`.
- `featureId`: `022-alpha-first-run-e2e`.
- `proofId`: stable proof identifier.
- `mode`: existing product-evidence mode, usually `local-fast`, `real-gate`,
  `docs`, `schema`, or `unit`.
- `status`: `passed`, `failed`, or `not-run`.
- `commandSummary`: redacted human summary of the command or scenario.
- `coveredClaims`: spec/docs/status/quickstart claims covered by the proof.
- `prerequisites`: named prerequisite statuses.
- `artifacts`: redacted logs, manifests, docs reports, or event summaries.
- `redactionStatus`: `passed`, `failed`, or `not-run`.

Validation:

- No proof may pass when a required prerequisite is missing.
- `local-fast` proofs must include weak/native/dev-only notes.
- Real backend proofs must use `real-gate` or an equivalent real mode and must
  not pass through native fallback.

## PackageUnderTest

The package artifact or staging directory used for install.

Fields:

- `packageRoot`: artifact root after extraction.
- `installPrefix`: temp install prefix.
- `storeRoot`: temp `HIDEOUT_STORE_ROOT`.
- `manifestPath`: package or installed manifest path.
- `gitCommit`: package manifest commit.
- `dirty`: package dirty flag when available.
- `helperInventory`: package-owned helper list and external prerequisites.

Validation:

- Package root and install prefix must be clean real paths.
- Verification must happen before first-run pass evidence.
- Stale package-owned files block success until explicit repair.

## InstallContext

Runtime context for the first-run proof.

Fields:

- `prefix`: install prefix.
- `path`: PATH value that exposes the installed binary.
- `storeRoot`: durable store used by the installed binary.
- `workspace`: dedicated workspace.
- `proofMode`: `local-fast` or `real-backend`.
- `backend`: `native`, `lima`, or other selected backend.
- `networkMode`: `direct` or `tun2socks`.
- `proxySecretRef`: optional proxy secret reference, not raw secret.
- `mediatedResolver`: optional mediated DNS resolver.

Validation:

- Local-fast native context is weak/dev-only.
- Real-backend context must not silently fall back to native.
- Workspace must not be host home, Hideout store, or a reserved root.

## FirstRunProfile

The profile created by the proof lane's explicit init step.

Fields:

- `name`: profile name, default `default`.
- `template`: expected template, `dev` for local-fast or `privacy` for real
  backend.
- `backend`: selected backend.
- `networkMode`: selected network mode.
- `createdBy`: documented init step.

Validation:

- The profile must be created exactly once.
- Pre-existing profile or partial profile state fails the clean first-run proof.
- Installer-created default profile is avoided by `--skip-init`.
- Local-fast profile is weak/dev-only and cannot claim privacy posture.

## ProofMode

Classification for the proof lane.

States:

- `local-fast`: native/dev harness proof.
- `real-backend`: real Lima/privacy proof requested.
- `not-run`: prerequisites absent.
- `failed`: command or validation failed.

Transitions:

- `local-fast` may pass only with weak/dev-only notes.
- `real-backend` may pass only after the real backend executes.
- Missing real prerequisites transition to `not-run`, not `passed`.

## PrerequisiteFinding

Structured diagnostic for a blocked or degraded first-run path.

Fields:

- `name`: prerequisite identifier.
- `status`: `available`, `missing`, or `skipped`.
- `severity`: `error`, `warning`, or `info`.
- `reason`: observed fact.
- `nextAction`: operator-facing recovery hint.

Validation:

- Error findings block pass evidence.
- Missing optional real-backend prerequisites may produce `not-run`.
- Findings must not contain raw control-plane material.

## ArtifactRef

Reference to proof output.

Fields:

- `kind`: `manifest`, `log`, `docs-report`, `event-summary`, or existing
  product-evidence artifact kind.
- `path`: path relative to the evidence output directory when possible.
- `sha256`: optional checksum.
- `redactionStatus`: redaction result.
- `description`: short purpose.

Validation:

- Artifact paths must not require host secret roots to interpret.
- Redaction failure blocks pass evidence for that artifact.
