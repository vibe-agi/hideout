# Runtime Selection And Status Contract

<!-- markdownlint-disable MD013 MD060 -->

## CLI

```text
hideout runtime list [--json]
hideout runtime inspect <family> [--revision <id>] [--json]
hideout runtime verify --env <name> [--json]

hideout init ... [--runtime <family>] [--image <declaration>]
hideout env create <name> [--runtime <family>] [--image <declaration>]
```

`--runtime` and `--image` are mutually exclusive. V1 accepts only
`developer-standard` on macOS arm64 with Lima. Runtime selection is explicit;
no existing template/default is changed.

## Init And Environment Apply

The existing typed init and environment-create plans gain:

```json
{
  "runtimeSelection": {
    "family": "developer-standard",
    "requestedRevision": "",
    "resolvedRevision": "2026.07.0",
    "catalogRelease": "2026.07.0",
    "imageRef": "https://example.invalid/runtime.qcow2#sha256:<64-hex>",
    "contractDigest": "sha256:<64-hex>",
    "maturity": "preview"
  }
}
```

The plan contains no secret. Apply re-resolves and checks that package catalog
identity still matches the plan before writing profile/environment state.
Catalog drift between plan and apply fails closed.

## Manager Model

The Core owns typed methods for:

- catalog list/inspect;
- runtime selection resolution;
- environment runtime status;
- runtime verification plan/apply.

Daemon/API routing, when exposed, uses production route inventory and the same
Core methods. No CLI-only resolver, status recomputation, or WebUI-only policy is
allowed. V1 does not require new TUI/WebUI controls.

## Verification Plan

`runtime verify` plan is read/probe intent, not repair:

```json
{
  "schema": "hideout.runtime-verify-plan/v1",
  "environmentId": "env_...",
  "environmentName": "work",
  "backend": "lima",
  "imageRef": "https://...#sha256:...",
  "runtime": {
    "family": "developer-standard",
    "revision": "2026.07.0",
    "contractDigest": "sha256:..."
  },
  "effects": [
    "start or reuse the selected Lima guest",
    "observe declared commands and privilege state",
    "replace the host-only verification receipt"
  ]
}
```

Apply acquires the environment lock, uses the pinned environment image and
provenance, and runs the same backend observation used by `hideout run`. It does
not execute a user target, install, download packages, mutate the image, or
change profile/environment identity.

## Status Rules

| Environment facts | Rendered status |
|-------------------|-----------------|
| No runtime provenance | `custom/unverified` |
| Provenance, guest stopped | `not-running` plus last-observed timestamp/status |
| Provenance, no valid receipt | `unknown` |
| Live real receipt passes contract and privilege | `preview-ready` |
| Live receipt misses any observation or privilege requirement | `preview-failed` |
| Native/fixture observation | `unknown` or `custom/unverified`, never preview-ready |

Receipts whose environment ID, image ref, provenance, contract digest, or
backend do not match the current record are ignored and reported as invalid.

## Shared Output

CLI, doctor, Manager, environment inspection, and Boundary Summary share:

- family, revision, maturity, artifact digest;
- current status and whether the guest is running;
- observed/last-observed timestamps;
- failed observation IDs and classes;
- privilege status;
- stable recovery record;
- gate-required notes for preview claims.

Human output abbreviates image digests but JSON retains full values. Neither
surface prints proxy credentials, tokens, machine identity, builder paths,
target environment values, or package-manager cache contents.

## Recovery Codes

V1 adds registered codes for:

- `runtime.selection.unsupported`
- `runtime.catalog.invalid`
- `runtime.artifact.unavailable`
- `runtime.artifact.digest-mismatch`
- `runtime.disk.insufficient`
- `runtime.boundary.missing`
- `runtime.baseline.missing`
- `runtime.command.missing`
- `runtime.network.denied`
- `runtime.dns.failed`
- `runtime.registry.failed`
- `runtime.prefix.unwritable`

Each code has one observed reason, bounded hint, executable next action where
one exists, and docs reference. Codes do not guess a package for unknown tools.
