# Data Model: Supported CLI Runtime

<!-- markdownlint-disable MD013 MD060 -->

## Runtime Catalog

Package-owned immutable document.

| Field | Type | Rules |
|-------|------|-------|
| `schema` | string | `hideout.runtime-catalog/v1` |
| `catalogRelease` | string | Stable release identifier; never `latest` |
| `generatedAt` | RFC 3339 | Informational build time |
| `families` | array | Exactly one v1 family; unique IDs |

Unknown fields fail validation.

## Runtime Family

| Field | Type | Rules |
|-------|------|-------|
| `id` | string | V1: `developer-standard` |
| `displayName` | string | Operator-facing name |
| `maturity` | enum | V1: `preview` |
| `currentRevision` | string | References one revision in this family |
| `revisions` | array | Immutable revision records |

## Runtime Revision

| Field | Type | Rules |
|-------|------|-------|
| `id` | string | Version-like immutable ID |
| `contractId` | string | Stable contract identity |
| `contractDigest` | string | SHA-256 of canonical contract bytes |
| `artifacts` | array | One v1 macOS arm64/Linux aarch64 artifact |
| `status` | enum | `preview`, `withdrawn`; never `supported` in v1 |
| `reviewedAt` | RFC 3339 | Artifact acceptance time |

Withdrawn revisions stay parseable for old provenance but cannot create a new
environment.

## Runtime Artifact

| Field | Type | Rules |
|-------|------|-------|
| `hostOS` | string | V1 `darwin` |
| `hostArch` | string | V1 `arm64` |
| `guestArch` | string | V1 `aarch64` |
| `format` | string | `qcow2` |
| `location` | URL | Version-addressed HTTPS `.qcow2`; no user info or fragment |
| `sha256` | string | 64 lowercase hex characters |
| `downloadBytes` | integer | Positive, at most 4 GiB |
| `virtualBytes` | integer | Positive, at most 16 GiB |
| `supplyMode` | enum | V1 `hideout-built` |
| `source` | object | Base URL/digests, build commit, lock digest, license review |
| `packageInventoryDigest` | string | SHA-256 of sorted inventory |
| `sbom` | object | `available` plus digest/ref, or honest `unavailable` |

The concrete environment image declaration is derived as
`location + "#sha256:" + sha256`.

## Runtime Contract

| Field | Type | Rules |
|-------|------|-------|
| `schema` | string | `hideout.runtime-contract/v1` |
| `id` | string | Matches revision `contractId` |
| `observations` | array | Unique stable observation IDs |

### Runtime Observation

| Field | Type | Rules |
|-------|------|-------|
| `id` | string | Stable bounded identifier |
| `class` | enum | `boundary` or `baseline` |
| `command` | string | Simple command name, no slash or whitespace |
| `versionArgs` | string array | Bounded direct argv; no shell/control chars |
| `outputPattern` | string | Bounded anchored RE2 pattern or empty |
| `description` | string | Human-readable catalog fact |

The contract cannot express environment variables, working-directory changes,
shell source, redirection, download, package install, or host authority.

## Runtime Selection

Operator request used during plan/apply.

| Field | Type | Rules |
|-------|------|-------|
| `family` | string | Required when runtime selected |
| `revision` | string | Empty means catalog `currentRevision`; plan records result |
| `hostOS` | string | Observed by Core, not user-controlled in apply |
| `hostArch` | string | Observed by Core, not user-controlled in apply |

`runtime` and custom `imageRef` are mutually exclusive.

## Runtime Provenance

Additive immutable object stored in profile/environment records.

| Field | Type | Rules |
|-------|------|-------|
| `family` | string | Catalog family |
| `revision` | string | Exact revision |
| `catalogRelease` | string | Exact package catalog |
| `contractId` | string | Exact contract |
| `contractDigest` | string | SHA-256 |
| `artifactLocation` | string | HTTPS source, no credential |
| `artifactSHA256` | string | Exact image digest |
| `packageInventoryDigest` | string | Catalog-bound digest observed inside the active guest |
| `downloadBytes` | integer | Immutable catalog download size for pre-start disk checks |
| `virtualBytes` | integer | Immutable catalog virtual size for pre-start disk checks |
| `hostOS` | string | `darwin` |
| `hostArch` | string | `arm64` |
| `guestArch` | string | `aarch64` |
| `maturity` | string | `preview` |

An environment with no provenance is `custom/unverified` even when its image
string happens to equal a catalog artifact.

## Runtime Verification Receipt

Host-only mutable observation written atomically as mode 0600.

| Field | Type | Rules |
|-------|------|-------|
| `schema` | string | `hideout.runtime-verification/v1` |
| `environmentId` | string | Existing environment owner |
| `imageRef` | string | Must equal environment record |
| `provenance` | object | Exact environment provenance copy |
| `contractDigest` | string | Must match provenance |
| `observedAt` | RFC 3339 | Current run time |
| `backend` | string | V1 `lima` |
| `backendReal` | boolean | Required true for preview-ready |
| `running` | boolean | Observation was from live guest |
| `privilegeStatus` | string | `enforced`, `degraded`, or `unknown` |
| `status` | enum | `preview-ready`, `preview-failed`, `custom/unverified`, `unknown` |
| `results` | array | One result per contract observation |
| `failedIds` | string array | Sorted unique failures |
| `recovery` | object | Optional shared recovery record |

The receipt's instance object also carries the observed active-build identity:
the SHA-256 of `/etc/hideout/package-inventory.txt`. It must equal provenance.
This identifies the selected build inside the running guest; it is not a full
mutable-disk hash and does not claim containment after guest-root compromise.

Every real-gate binding is environment-owned and therefore carries the exact
environment ID it observed. Cross-gate comparison requires each ID to be valid
and non-empty, and Gate 2 and Gate 3 deliberately use distinct disposable
environments. Equality is required for immutable runtime and candidate fields,
not for the two mutable environment identities.

### Observation Result

| Field | Type | Rules |
|-------|------|-------|
| `id` | string | Contract observation ID |
| `class` | string | Contract class |
| `command` | string | Command name only |
| `present` | boolean | Current guest observation |
| `versionOutput` | string | UTF-8, control-stripped, at most 512 bytes |
| `matched` | boolean | Pattern match result when configured |
| `reason` | string | Bounded typed reason; no raw shell error |

## Runtime Status View

Derived read model shared by CLI, doctor, Manager, and Boundary Summary.

| Status | Meaning |
|--------|---------|
| `preview-ready` | Live real Lima observation passed contract and privilege requirement |
| `preview-failed` | Provenance exists but current observation failed |
| `custom/unverified` | No catalog provenance; no preview claim |
| `unknown` | Provenance exists but no trustworthy observation can be read |
| `not-running` | Guest is stopped; last receipt is context, not current success |

## State Transitions

```text
runtime selector
  -> catalog resolve
  -> plan with concrete artifact + provenance
  -> environment create
  -> unknown / not-running
  -> guest start + actual observations
     -> preview-ready
     -> preview-failed

custom image
  -> environment create
  -> custom/unverified
```

Catalog update never transitions an existing environment. Stopping a ready
guest renders `not-running`; it does not delete the last receipt or present it
as current.
