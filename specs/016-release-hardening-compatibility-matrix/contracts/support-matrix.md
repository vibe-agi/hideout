# Contract: Support Matrix

<!-- markdownlint-disable MD013 -->

## CLI Surface

```text
hideout support matrix [--json]
hideout version
hideout doctor [--backend native|lima|auto] [--json]
```

Requirements:

- `hideout support matrix --json` emits `hideout.support-matrix/v1` JSON.
- `hideout support matrix` emits a stable human-readable summary.
- `hideout version` preserves existing lines and appends compact matrix/support
  lines.
- `hideout doctor` includes a `support-matrix` finding for the requested or
  default backend.

## Matrix JSON Shape

Top-level fields:

- `schema`
- `version`
- `generatedBy`
- `entries`
- `nonClaims`

Entry fields:

- `area`
- `subject`
- `level`
- `reason`
- `guidance`
- `requiredGates`
- `evidence`

Non-claim fields:

- `id`
- `summary`
- `appliesTo`
- `guidance`

Closed support levels:

- `first-class`
- `supported`
- `degraded`
- `unsupported`
- `gate-required`

## Required Rows

- `platform/darwin/arm64`: first-class
- `platform/linux/amd64`: supported
- `platform/linux/arm64`: supported
- `backend/lima`: first-class or supported for isolation claims
- `backend/native`: degraded
- `feature/dns-mediation`: gate-required with Gate 3
- `feature/hostfs-write-overlay`: gate-required with Gate 2
- `feature/guest-privilege-separation`: gate-required or supported with real
  privilege evidence
- `abi/command-adapter/v1`: supported
- `abi/adapter-pack/v1`: supported
- `schema/profile/v1`: supported
- `schema/doctor-report/v1`: supported
- `schema/export-artifact/v1`: supported
- `gate/release-candidate`: gate-required

## Fail-Closed Rules

- Missing required rows fail matrix validation.
- Unknown support level fails matrix validation.
- Non-first-class rows without reason and guidance fail validation.
- Current host/backend lookup returns `unsupported` when no row matches.

## Redaction

Matrix output must not include operator-local store roots, proxy URLs, tokens,
generated machine IDs, helper credential paths, or claim tokens.
