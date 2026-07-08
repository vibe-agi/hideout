# Data Model: Release Hardening And Compatibility Matrix

<!-- markdownlint-disable MD013 -->

## Support Matrix

Versioned product support contract owned by Go Core.

Fields:

- `schema`: constant `hideout.support-matrix/v1`
- `version`: human-readable matrix version, initially `2026-07-alpha`
- `generatedBy`: `hideout`
- `entries`: ordered list of Support Entry
- `nonClaims`: ordered list of Non-Claim

Validation:

- Must contain at least one entry for macOS arm64, Linux amd64, Linux aarch64,
  Lima backend, native backend, HostFS write overlay, DNS mediation, privilege
  separation, adapter ABI, package/install, doctor/export schemas, and release
  gates.
- All entries must use closed support levels.
- Any non-first-class entry must include non-empty `reason` and `guidance`.

## Support Entry

One row of support status.

Fields:

- `area`: `platform`, `backend`, `feature`, `schema`, `abi`, `gate`, `helper`, or
  `non-claim`
- `subject`: stable identifier, for example `darwin/arm64`, `backend/native`,
  `dns-mediation`, or `adapter-pack/v1`
- `level`: `first-class`, `supported`, `degraded`, `unsupported`, or
  `gate-required`
- `reason`: concise explanation
- `guidance`: operator action or next step
- `requiredGates`: zero or more gate IDs
- `evidence`: zero or more evidence source identifiers

Invariants:

- `backend/native` is always `degraded` for isolation claims.
- `platform/darwin/arm64` is the only first-class alpha platform in v1.
- `dns-mediation` and `hostfs-write-overlay` require real gate evidence for
  release-candidate readiness.

## Non-Claim

Explicit unsupported promise that must survive docs cleanup.

Fields:

- `id`: stable identifier
- `summary`: statement of what Hideout does not claim
- `appliesTo`: matrix subjects affected by the non-claim
- `guidance`: how operators should reason about the gap

Required v1 non-claims:

- Guest-root containment remains out of scope unless a later real boundary lands.
- Workspace write blocking/DLP is not enforced; workspace is audited at most.
- Native backend is not isolation evidence.
- Public marketplace trust is out of scope.
- Browser security beyond existing host-open boundary is out of scope.
- Unsupported platforms have no release support promise.

## Release Readiness Run

Machine-readable summary of one readiness command.

Fields:

- `schema`: constant `hideout.release-readiness/v1`
- `generatedAt`: UTC timestamp
- `mode`: `local-fast` or `release-candidate`
- `evidenceClass`: `local-fast` or `real-gate`
- `releaseReady`: boolean
- `status`: `passed`, `failed`, or `not-release`
- `commit`: git commit or `unknown`
- `platform`: `{os, arch}`
- `matrix`: `{schema, version}`
- `commands`: ordered command result summaries
- `gates`: ordered gate evidence summaries
- `nonClaims`: copied non-claim IDs
- `redaction`: `{mode: "control-plane"}`

Invariants:

- `mode=local-fast` always sets `releaseReady=false` and `status=not-release`.
- `mode=release-candidate` must set `releaseReady=false` if Gate 2 or Gate 3
  evidence is missing when those gates are required by the matrix.
- Raw logs, proxy URLs, tokens, generated machine IDs, and hidden helper
  credential paths are never embedded.

## Gate Evidence Summary

One gate or smoke check result in a readiness run.

Fields:

- `id`: stable gate ID
- `required`: boolean
- `status`: `passed`, `failed`, `missing`, `skipped`, or `not-run`
- `evidencePath`: optional redacted reference
- `summary`: redacted concise text

Validation:

- Required release-candidate gates cannot be `missing`, `skipped`, or `not-run`
  when `releaseReady=true`.

## Compatibility Fixture

Synthetic or real sample data for current alpha schemas and ABIs.

Fields:

- `family`: profile, package manifest, adapter pack, doctor report, export
  artifact, decision, notice, HostFS write decision, HostFS write event,
  onboarding evidence, daemon status, daemon event, live console seed, run
  result, init plan, or init audit
- `version`: schema/ABI version string
- `expectation`: `accepted` or `rejected`
- `guidance`: error or migration guidance for rejected fixtures

Invariants:

- Every family in FR-011 has one accepted fixture and one unknown-version
  rejected fixture.
- Rejected fixtures must fail before mutation, enablement, or apply.

## Stale Claim Finding

Result of a docs/matrix drift scan.

Fields:

- `id`: stable finding ID
- `path`: documentation or script path
- `status`: `pass`, `warn`, or `error`
- `summary`: redacted summary
- `expected`: matrix-derived expectation
- `actual`: current claim or matched text

Validation:

- Gate0 fails on any `error`.
- Historical spec trail may be `warn` or ignored only when the text is clearly
  marked historical, superseded, or deferred.
