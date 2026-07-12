# Runtime Catalog Contract

<!-- markdownlint-disable MD013 MD060 -->

## Purpose

The package-owned runtime catalog maps one operator-facing runtime name to one
reviewed immutable guest artifact. It is guest-domain data, not an ecosystem
authority or provisioning recipe.

## Source Of Truth

- Canonical source bytes live at `internal/runtimecatalog/catalog.json` and
  `internal/runtimecatalog/contract.json`.
- Go embeds those exact bytes.
- Packaging copies those exact bytes to `share/hideout/runtime/` and records
  their digests in the package manifest.
- Source, embedded, packaged, and schema-validated bytes must match. There is no
  store override, remote refresh, profile-provided catalog, or JS hook.

## V1 Catalog Shape

```json
{
  "schema": "hideout.runtime-catalog/v1",
  "catalogRelease": "2026.07.0",
  "generatedAt": "2026-07-11T00:00:00Z",
  "families": [
    {
      "id": "developer-standard",
      "displayName": "Developer Standard",
      "maturity": "preview",
      "currentRevision": "2026.07.0",
      "revisions": [
        {
          "id": "2026.07.0",
          "status": "preview",
          "contractId": "developer-standard/v1",
          "contractDigest": "sha256:<64-hex>",
          "reviewedAt": "2026-07-11T00:00:00Z",
          "artifacts": [
            {
              "hostOS": "darwin",
              "hostArch": "arm64",
              "guestArch": "aarch64",
              "format": "qcow2",
              "location": "https://<retained-versioned-asset>.qcow2",
              "sha256": "<64-hex>",
              "downloadBytes": 1,
              "virtualBytes": 1,
              "supplyMode": "hideout-built",
              "source": {
                "baseLocation": "https://<versioned-debian-base>.qcow2",
                "baseSHA512": "<128-hex>",
                "baseSHA256": "<64-hex>",
                "buildCommit": "<12-hex>",
                "sourceLockSHA256": "<64-hex>",
                "licenseReview": "reviewed"
              },
              "packageInventoryDigest": "sha256:<64-hex>",
              "sbom": {
                "available": false,
                "status": "unavailable-preview"
              }
            }
          ]
        }
      ]
    }
  ]
}
```

The example is structural, not a valid production catalog. Placeholder hosts,
zero/one-byte artifacts, missing digests, unknown keys, moving URLs, unsupported
architectures, duplicate IDs, or a current withdrawn revision fail validation.

## Resolution

Input:

```json
{
  "family": "developer-standard",
  "revision": "",
  "hostOS": "darwin",
  "hostArch": "arm64"
}
```

Output includes the resolved revision, artifact, concrete image declaration,
contract, and immutable `RuntimeProvenance`.

Rules:

1. Empty revision resolves only to that family's declared `currentRevision`.
2. Exactly one artifact must match the Core-observed host OS and architecture.
3. A withdrawn revision cannot create a new environment.
4. Location must be version-addressed HTTPS `.qcow2`; query/userinfo and moving
   aliases are rejected.
5. The concrete image declaration is parsed again by
   `environment.ParseImageDeclaration`.
6. No fallback to a template, custom image, other architecture, or older
   revision is allowed.

## Runtime Contract Safety

Each observation is direct argv, never shell source. Validators reject:

- commands containing slash, whitespace, glob, or control characters;
- shell interpreters as observation commands;
- `-c`, `--command`, redirection, substitutions, or environment assignment;
- more than four version args, an arg over 128 bytes, or output pattern over
  256 bytes;
- unknown class, duplicate ID/command-class pair, or unknown JSON fields.

Catalog/contract content can cause bounded guest observations only. It cannot
write state, install packages, access host resources, mutate policy, or request
credentials.

## Inspection

`hideout runtime list` and `hideout runtime inspect developer-standard` expose:

- family/revision/maturity;
- supported host and guest architectures;
- artifact location and abbreviated digest;
- compressed/virtual size;
- baseline commands and version observations;
- base source, inventory, SBOM, and license-review status;
- explicit preview non-claims.

Inspection does not download, boot, verify, or claim the current guest is ready.
