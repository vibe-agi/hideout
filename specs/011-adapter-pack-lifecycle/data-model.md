# Data Model: Adapter Pack Lifecycle And Local Registry

<!-- markdownlint-disable MD013 -->

## Adapter Pack

A lifecycle-managed collection of adapter metadata, constrained JavaScript
source, and deterministic test vectors.

Fields:

- `id`: stable pack id unique in the store-wide registry.
- `version`: pack-authored version string.
- `description`: local display text.
- `source`: pack source descriptor.
- `manifestDigest`: digest of the normalized manifest.
- `sourceDigest`: digest of the locked source tree.
- `adapters`: one or more adapter definitions exported by the pack.
- `tests`: deterministic test vectors.
- `state`: `installed`, `candidate`, `disabled`, `revoked`, or `invalid`.
- `evidenceRefs`: audit/export references for lifecycle events.

Rules:

- Installing a pack does not grant runtime authority.
- Pack id/version cannot override built-in adapter identity.
- Pack-authored metadata is descriptive and cannot declare new authority types.

## Pack Source

The origin of an installed pack.

Fields:

- `kind`: `local` or `git`.
- `path`: local source path for local packs.
- `url`: git URL for git packs.
- `commit`: exact commit id for git packs.
- `fetchedAt`: local timestamp for evidence.

Rules:

- Git source must use an exact commit id, not a branch, tag, or ambiguous ref.
- Recursive submodule checkout is disabled by default.
- Source intake does not enable the pack.

## Pack Lock

The immutable identity of one installed revision.

Fields:

- `packId`
- `revisionId`: Hideout-minted stable revision id.
- `version`
- `source`
- `manifestDigest`
- `sourceDigest`
- `fileDigests`
- `createdAt`
- `testResultId`
- `validationStatus`

Rules:

- Profile bindings pin `revisionId`, not latest pack id.
- Digest mismatch makes the revision unusable until reinstalled or upgraded.
- A new source creates a new candidate revision.

## Pack Adapter Definition

One adapter entry exported by a pack.

Fields:

- `adapterId`: adapter id within the pack.
- `entrypoint`: adapter function name.
- `commands`: command symbols the adapter can own when enabled.
- `allowedProposalCapabilities`: proposal capabilities the profile may allow.
- `description`: display text.

Rules:

- Command ownership is not active until profile enable binding.
- Capabilities are upper bounds; profile binding may allow a subset.
- Adapter output still uses the 008 ABI.

## Pack Test Vector

Pack-authored deterministic input/output fixture.

Fields:

- `id`
- `adapterId`
- `context`: command adapter context fixture.
- `expectedOutcome`
- `expectedReasonContains`
- `expectedProposalCapability`
- `forbiddenEvidenceSubstrings`

Rules:

- Tests run without filesystem, network, process, timer, host mutation, profile
  mutation, backend handles, broker tokens, or raw authority access.
- Passing tests do not override Core validation.
- Missing tests prevent enablement.

## Pack Test Result

Hideout-owned result from running pack test vectors.

Fields:

- `id`
- `packId`
- `revisionId`
- `status`: `passed`, `failed`, or `blocked`.
- `total`
- `passed`
- `failed`
- `failures`
- `coreValidationStatus`
- `evidenceRef`

Rules:

- Enablement requires status `passed` and Core validation `passed`.
- Test output is redacted before audit/export.

## Pack Registry Entry

Store-wide state for one pack id.

Fields:

- `packId`
- `activeRevisionId`: most recent installed revision for inspection, not a
  profile authority pointer.
- `revisions`
- `state`
- `builtIn`: true only for Core-owned built-in metadata entries.
- `evidenceRefs`

Rules:

- Revoked registry entry causes every profile binding to fail closed.
- Built-in entries are read-only and cannot be upgraded or revoked through pack
  registry mutation.

## Profile Enable Binding

Profile-level authority edge that enables one adapter revision for one profile.

Fields:

- `packId`
- `revisionId`
- `adapterId`
- `commands`
- `allowedProposalCapabilities`
- `enabled`
- `boundAt`
- `evidenceRef`

Rules:

- Binding must pin a revision id.
- Binding commands cannot conflict with command proxy or another enabled pack.
- Disable removes runtime ownership without deleting registry state.
- Re-enable is required to move to a new pack revision.

## Built-In Adapter Metadata

Read-only pack-compatible description of a Core-owned adapter.

Fields:

- `id`
- `version`
- `digest`
- `commands`
- `allowedProposalCapabilities`
- `description`
- `nonClaims`

Rules:

- Built-ins can be listed, inspected, and tested.
- Built-ins cannot be overwritten by registry install/upgrade.

## Pack Lifecycle Evidence

Audit/export details for pack operations.

Fields:

- `event`: install, validate, test, enable, disable, upgrade, revoke, mismatch,
  runtime-selection, or failure.
- `packId`
- `revisionId`
- `profile`
- `adapterId`
- `status`
- `reason`
- `digestSummary`
- `testSummary`
- `redactionStatus`

Rules:

- Evidence never includes broker/UI tokens, `HIDEOUT_SECRET_*` backing values,
  generated machine ids, raw control-plane paths, or hidden store paths.
- Pack-authored text is local operator data and is exported only through the
  export/share boundary.
