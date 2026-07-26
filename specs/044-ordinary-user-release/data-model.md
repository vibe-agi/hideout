# Data Model: Ordinary User Release

## 1. Help View

A non-persisted projection of registered CLI commands.

Fields:

- `mode`: `primary`, `contextual`, or `all`
- `topic`: optional normalized command topic
- `journeyCommands`: ordered ordinary-user commands
- `advancedCommands`: complete registered advanced command index
- `claimNotices`: direct-network, platform, workspace, and maturity notices

Rules:

- Primary help contains only ordinary-user journey commands plus a pointer to
  the complete index.
- All-mode contains every registered command.
- Contextual help performs no state reads or writes.
- Unknown topics fail with the unknown topic and a runnable help command.

## 2. Readiness Summary

A non-persisted projection of an authoritative doctor report.

Fields:

- `state`: `ready`, `attention`, or `blocked`
- `profile`
- `backend`
- `networkPosture`
- `boundarySummary`
- `actionableFindings`
- `nextCommands`
- `detailHint`

Relationships:

- Derives from exactly one `hideout.doctor-report/v1`.
- Never performs checks and never changes finding status.
- Each actionable item retains the source finding ID and recovery code when
  available.

State derivation:

```text
any required error                     -> blocked
no required error + actionable warning -> attention
otherwise                              -> ready
```

Warnings that describe maintainer-only release evidence are not presented as
ordinary-user repair actions, but remain in detailed and JSON output.

## 3. Support Report

A shareable, versioned JSON artifact.

Fields:

- `schema`: `hideout.support-report/v1`
- `generatedAt`
- `product`: version, source commit, build time, host OS/architecture
- `support`: support-matrix schema/version and current platform/backend levels
- `package`: applicability, package identity, verification state, finding
- `doctor`: redacted `hideout.doctor-report/v1`
- `recovery`: unique recovery codes and safe next actions
- `collection`: per-section `collected`, `not-applicable`, or `failed`
- `redaction`: mode and excluded data classes
- `provenance`: local command and output contract, without raw host paths

Validation rules:

- Maximum serialized size: 1 MiB.
- Exactly one supported schema value.
- No unknown fields.
- No raw audit body, workspace content, proxy value, environment backing name,
  token, generated machine ID, or raw host-user path.
- Package failure is represented as a finding, not as successful verification.
- Output path must be explicit, clean, regular-file-bound, beneath a safe
  parent, and written atomically with mode `0600`.

## 4. Packaged Privacy Helper

The distributable provenance record for the guest `tun2socks` executable.

Fields:

- `command`: `tun2socks`
- `upstreamModule`: `github.com/xjasonlyu/tun2socks/v2`
- `upstreamVersion`: `v2.6.0`
- `license`: `MIT`
- `targetOS`: `linux`
- `targetArch`
- `artifact`
- `sha256`
- `buildMode`: source-built pinned module
- `packageOwned`
- `override`: absent or explicit development path

Rules:

- Package-owned artifacts are present in the package manifest and executable.
- The helper manifest digest equals the actual artifact digest.
- The upstream license is present in the package.
- Explicit override paths must be clean regular executable files for the
  expected guest platform and are labeled development evidence.
- An invalid explicit override fails closed and does not silently fall through.

## 5. Installed Package Guidance

A non-authoritative presentation derived from verified package state and the
documented installation provider.

Fields:

- `installationKind`: `homebrew`, `standalone`, or `development`
- `integrity`: `verified`, `damaged`, `not-applicable`, or `unknown`
- `version`
- `prefix`
- `storePolicy`: `preserved` or `purge-explicit`
- `updateCommand`
- `repairCommand`
- `uninstallCommand`
- `purgeCommand`

Rules:

- Homebrew guidance never writes Cellar files through Hideout.
- Standalone guidance names the exact prefix.
- Unknown installation kind does not guess a destructive command.
- Durable state is preserved unless explicit purge is selected.

## 6. Ordinary User Acceptance Evidence

A product-evidence record bound to one retained candidate.

Fields:

- `featureId`: `044-ordinary-user-release`
- `candidate`: version, commit, package digest, manifest digest, clean/public
  status
- `journeys`: install, help, setup, doctor, support, first-run, privacy,
  upgrade, repair, uninstall, UI
- `realGates`: Gate 2 and Gate 3 evidence identities
- `releaseChecks`: signing, notarization, anonymous download, publication
- `redactionStatus`
- `cleanupStatus`
- `status`: `passed`, `failed`, or `not-run`

Rules:

- Every journey references the same candidate digest.
- Required `not-run` is never equivalent to passed.
- Rebuilt bytes invalidate all earlier candidate-bound observations.
- Publication requires a clean public commit, passing real gates, signature,
  notarization, anonymous identity, and receipt.
