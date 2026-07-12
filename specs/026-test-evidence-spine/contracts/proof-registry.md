# Contract: Proof Registry

<!-- markdownlint-disable MD013 -->

## Purpose

Expose one Go-owned registry of product proof requirements for Go tests, shell
gates, docs truth, and release-readiness checks.

## JSON Shape

```json
{
  "schema": "hideout.proof-registry/v1",
  "requirements": [
    {
      "featureId": "021-ui-e2e-proof",
      "proofId": "021.webui.browser.console",
      "layer": "product-hardening",
      "requiredFor": "targeted-completion",
      "freshnessPolicy": "same-commit",
      "claimIds": ["021.FR-001"],
      "artifactPolicy": "exists-and-digest-if-supplied"
    }
  ]
}
```

## Rules

- Output must be deterministic.
- `requirements` must be sorted by `featureId`, then `proofId`.
- `proofId` must be unique.
- Shell scripts must consume this JSON view or a Go helper derived from the same
  registry. They must not duplicate the 021-025 required-proof list.
- Unknown enum values fail registry validation.
- The registry view contains proof metadata only; it must not include raw
  secrets, token values, machine IDs, or hidden control-plane paths.

## Compatibility

Existing product-hardening manifests remain unchanged. Registry JSON is a new
view over required proofs, not a replacement for
`hideout.product-hardening-evidence/v1`.
