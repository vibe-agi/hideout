# Contract: Evidence And Readiness

## Proof Registry

033 adds these stable IDs:

- `033.release.package-identity` (`release-candidate`): canonical package, full
  commit, target, and exact archive digest.
- `033.release.signing-notarization` (`release-candidate`): independent
  Developer ID and accepted online notarization.
- `033.release.clean-install` (`release-candidate`): install and run without
  source, Go, prior store, or developer `PATH` fallback.
- `033.release.real-gate-binding` (`release-candidate`): exact package/runtime
  binding across real Gate 2 and Gate 3.
- `033.release.docs-candidate-truth` (`release-candidate`): candidate docs make
  no public-availability claim.
- `033.release.public-download` (`public-release`): anonymous exact-byte
  download of immutable public assets.
- `033.release.docs-public-truth` (`public-release`): public docs derive from
  the verified publication receipt.

The `public-release` evaluation target includes every `release-candidate`
requirement plus its two post-public requirements. Other targets retain their
existing behavior.

Every requirement declares feature ID, layer, required target, freshness,
artifact policy, runtime policy, required mode/class, and exact claim IDs.
Shell may emit only registered IDs.

## Evidence Bundle

Schema: `hideout.public-evidence-bundle/v1`

Required content:

```text
bundle-manifest.json
candidate-identity.json
release-readiness.json
package/package-manifest.json
package/verify.json
signing/observation.json
notarization/observation.json
runtime/build-provenance.json
gates/gate2.json
gates/gate3.json
proofs/*.json
artifacts/<only registry-required files>
proof-registry.json
SHA256SUMS
```

The bundle manifest records every file except itself and `SHA256SUMS` through a
non-recursive inventory; `SHA256SUMS` covers all regular content including the
bundle manifest. The outer release manifest records the compressed bundle
digest.

Validation rejects:

- more than 64 MiB uncompressed content;
- symlink/hard-link/device/path traversal or duplicate path;
- missing, undeclared, extra, or digest-invalid artifact;
- unregistered/duplicate proof or missing required claim;
- a user-data artifact that lacks a passed export/redaction decision;
- dirty, stale, local-only, native-only, `not-run`, or mismatched package/runtime
  identity; and
- known control-plane material or local absolute developer paths.

## Canonical Readiness V1

Candidate readiness binds:

```text
sourceCommit
package.productVersion
package.artifactSHA256
package.target
runtime artifact/build identity
Gate 2 environment ID
Gate 3 environment ID
signing observation digest
notarization observation digest
proof-registry version
support-matrix version
```

Gate 2 and Gate 3 must use the same runtime artifact/build but different
managed environment IDs. Public download is not a candidate-readiness input.

## Post-Public Evaluation

Publication verification supplies:

- validated candidate readiness and release manifest;
- GitHub release ID/tag/commit/prerelease/immutable observations;
- exact API asset names, sizes, and `sha256:` digests;
- anonymously downloaded bytes and local SHA-256 values;
- package extraction/verification and signing observation from downloaded
  bytes; and
- public docs inventory generated from the resulting receipt.

Only a satisfied `public-release` report may set receipt status to
`public-verified` or update `releases/current.json`.

## Redaction

- Signing keys, certificates, passwords, keychain profile names, App Store
  Connect values, GitHub tokens, proxy URLs, daemon/broker/claim tokens, local
  roots, and workspace content are forbidden in public output.
- Submission UUID, Team ID, signing common name, code identity, public release
  URL, package digest, and runtime digest are permitted provenance.
- Negative fixtures inject representative credential forms and local paths;
  tests must prove refusal/redaction in model, human, JSON, logs, receipt, and
  bundle output.
