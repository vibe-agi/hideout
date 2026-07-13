# Data Model: Public Alpha Release Channel

## Model Principles

- Product version, source commit, package-tree identity, outer archive digest,
  runtime artifact, and evidence digest are distinct values.
- An inner artifact never embeds its own outer digest.
- Package-declared signing facts are descriptive; only independently observed
  signing and notarization facts can satisfy release gates.
- Candidate evidence and post-public evidence are different lifecycle stages.
- Every public file reference is relative, bounded, regular, present, and
  digest matched.
- Unpublished legacy internal shapes are rejected rather than becoming a
  permanent compatibility surface.

## 1. Package Identity

Identifies exact release bytes without overloading any field.

| Field | Type | Rules |
| --- | --- | --- |
| `name` | string | Exactly `hideout` for the product package |
| `productVersion` | string | Canonical SemVer prerelease without leading `v` |
| `sourceCommit` | string | Exactly 40 lowercase hexadecimal characters |
| `artifactSHA256` | string | Exactly 64 lowercase hexadecimal characters |
| `hostOS` | string | `darwin` for 033 |
| `hostArch` | string | `arm64` for 033 |

`artifactSHA256` exists only in outer release/evidence inputs. An extracted
package root can supply every other field but cannot reconstruct this value.

## 2. Package Manifest V1

Schema: `hideout.package-manifest/v1`

Fields:

- `schema`
- `builtAt`
- `release`: product version, `alpha` or `developer-preview` channel, exact
  tag
- `source`: repository, full commit, dirty=false
- `build`: workflow/ref identity and reproducible target facts
- `target`: host OS/architecture and Linux guest architecture
- `runtime`: selected catalog release plus the checked package-file digest of
  `runtime/catalog.json`
- `signingSummary`: expected mode plus descriptive per-host-binary observation
- existing `layout`, `files`, and `migration` inventory

Validation rules:

- Public-alpha manifests require `channel=alpha`, a matching `v<version>` tag,
  full clean commit, macOS arm64 target, and signing mode
  `developer-id-observed`.
- Developer previews require a distinct prerelease identity and
  `channel=developer-preview`; they cannot use the final public-alpha tag.
- Every path is relative, unique, non-symlinked, and checksum covered.
- `LICENSE`, `THIRD_PARTY_NOTICES.md`, `SECURITY.md`, package README, runtime
  catalog, schemas, host binaries, and guest helpers are package-manifest
  entries.
- The manifest does not contain the outer archive digest, notarization
  acceptance, release token, or local path.

## 3. Package Install State V1

Schema: `hideout.package-install-state/v1`

Retains:

- install time, prefix, and store root;
- package manifest schema and product release identity excluding archive
  digest;
- installed file/directory ownership;
- migration range; and
- proven obsolete files.

State transitions:

```text
absent -> installed-v1 -> same-version-reinstalled
                    \-> later-compatible-upgrade
                    \-> normally-uninstalled (store retained)
                    \-> explicitly-purged
```

Compatibility:

- Pre-033 unpublished install-state shapes fail with explicit reinstall
  guidance; they are not treated as a public N-1 compatibility promise.
- A same-version install is idempotent.
- Once a second public alpha exists, N-1 package install state is a required
  real fixture.
- Unsupported downgrade or durable-state schema fails closed with an exact
  recovery record; it never mutates first and diagnoses later.

## 4. Product Evidence Manifest V1

Schema: `hideout.product-hardening-evidence/v1`

`packageIdentity` uses the explicit six-field Package Identity.
Release-candidate evidence requires it; supporting local evidence may omit it
only when its registry requirement has no package freshness policy.

Freshness comparisons:

- `same-commit`: compare full source commit.
- `same-package`: compare name, product version, archive digest, and target.
- `same-commit-and-package`: require both.
- `exact-real`: additionally compare runtime artifact/build identity.

Manifest notes never supply or override freshness identity.

## 5. Release Readiness V1

Schema: `hideout.release-readiness/v1`

Extends readiness with:

- `sourceCommit`
- `package`: exact Package Identity
- `runtime`: expected retained runtime identity
- `candidateStatus`
- registered command/gate rows
- support matrix reference and required non-claims
- deterministic redaction status

`releaseReady=true` requires:

- clean local checks;
- canonical package v1 verification;
- required candidate proofs satisfied;
- exact Gate 2/Gate 3 runtime build with distinct managed environment IDs;
- exact package digest match;
- observed signing/notarization pass for public-alpha mode; and
- no `failed`, `missing`, `stale`, `not-run`, native substitute, or redaction
  failure row.

Readiness is candidate evidence. It never claims anonymous public download.

## 6. Public Release Manifest

Schema: `hideout.public-release/v1`

Fields:

- release identity: version, channel, tag, full source, target, generated time;
- project license and third-party notice identity;
- exact package and evidence asset metadata;
- package-manifest and readiness digests;
- observed signing identity and notarization submission;
- retained runtime catalog/artifact identity;
- support-matrix version and major non-claim IDs;
- exact checksums filename and release asset allowlist.

The release manifest is immutable candidate input. It cannot contain its own
digest. `SHA256SUMS` supplies that external digest.

## 7. Signing And Notarization Observation

Signing observation fields:

- status: `developer-id-verified`, `developer-preview-unsigned`, or `failed`;
- observed Team ID and leaf common name;
- observation time and host platform;
- per-Mach-O package-relative path, code identifier, CDHash, secure timestamp,
  hardened-runtime observation, and strict verification result.

Notarization observation fields:

- status: `accepted`, `rejected`, `not-run`, or `not-applicable-preview`;
- submission UUID;
- private ZIP envelope SHA-256;
- accepted/rejected observation time;
- ticket mode `online` for v1;
- staple status `not-applicable-tar-gz`.

No credential, keychain name, issuer secret, password, or local submission path
is retained.

## 8. Public Evidence Bundle

Schema: `hideout.public-evidence-bundle/v1`

The bundle manifest contains:

- candidate identity excluding the enclosing bundle digest;
- runtime expectation;
- readiness path/digest/status;
- required proof IDs and source manifest paths;
- bounded artifact inventory with kind, path, size, digest, and redaction
  status; and
- bundle-level checksum filename.

Validation rules:

- Maximum uncompressed content: 64 MiB.
- Regular files/directories only; no symlinks, hard links, devices, absolute
  paths, `..`, duplicate normalized paths, or trailing data.
- Every reference is declared and digest matched.
- Proof feature/ID, claims, mode, class, target, package identity, runtime, and
  artifacts match the authoritative registry.
- Raw audit logs and unselected gate output are excluded.

## 9. Publication Receipt

Schema: `hideout.publication-receipt/v1`

Fields:

- release ID, version, tag, full commit, immutable=true, prerelease=true;
- public URL and publication time;
- each allowlisted asset's ID, basename, byte count, API digest, downloaded
  digest, and anonymous-download result;
- package signing/notarization verification result after download;
- GitHub release-attestation observation when available;
- overall `public-verified` or `failed` status; and
- redaction status.

The receipt is workflow evidence and post-public docs input. It is not added to
the immutable release asset set. Failed receipts cannot update public docs.

## 10. Published Release Inventory

Schema: `hideout.published-releases/v1`

Contains zero or more immutable public release summaries and at most one
`current` version. Each summary records:

- version/channel/maturity;
- tag, commit, target, and public release URL;
- package/evidence URLs and digests;
- runtime catalog release;
- readiness and publication receipt identity;
- publication time;
- license; and
- required support/non-claim IDs.

Only a validated `public-verified` receipt may add an entry. Candidate package
docs do not consume a future entry.

## 11. Compatibility Decision

Fields:

- operation: install, reinstall, upgrade, downgrade, repair, uninstall, purge;
- installed and candidate product versions and schema versions;
- status: allowed, denied, or needs-explicit-purge;
- stable recovery code, reason, hint, next actions, and evidence refs.

Rules:

- Version ordering is SemVer-aware and prerelease-aware.
- Unsupported downgrade is denied before mutation.
- Normal uninstall never implies purge.
- Unknown schemas are denied with export/recreate guidance.

## Lifecycle

```text
source-tagged
  -> package-tree-built
  -> host-binaries-signed
  -> notarization-accepted
  -> archive-frozen
  -> candidate-gates-passed
  -> draft-uploaded
  -> independently-real-proved
  -> manually-approved
  -> public-verifying
  -> public-verified
```

Failure transitions:

- Before publication: remain draft and failed; no public claim.
- After immutable publication: bounded anonymous-verification retry. If it
  still fails, emit no public-verified receipt, do not update the current
  inventory or public docs, record an incident, and consume the version. The
  immutable prerelease may remain visible, but Hideout never endorses it as
  the current alpha and never replaces its assets or reuses its tag.
- Missing Developer ID: optionally create a separately versioned developer
  preview; never enter `public-alpha` states.
