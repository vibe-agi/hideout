# Contract: Adapter Pack Manifest

<!-- markdownlint-disable MD013 -->

## Manifest Shape

```json
{
  "schemaVersion": "hideout.adapter-pack/v1",
  "id": "example.pack",
  "version": "1.0.0",
  "description": "Example command adapters",
  "adapters": [
    {
      "id": "example-tool",
      "entrypoint": "decideCommandAdapter",
      "script": "adapters/example-tool.js",
      "commands": ["example-tool"],
      "allowedProposalCapabilities": ["host.fs.write.plan"],
      "description": "Classifies example-tool write requests"
    }
  ],
  "tests": [
    {
      "id": "denies-unsafe-command",
      "adapterId": "example-tool",
      "context": {
        "command": {
          "name": "example-tool",
          "argv": ["example-tool", "--unsafe"],
          "cwd": "/workspace"
        }
      },
      "expect": {
        "outcome": "deny",
        "reasonContains": "unsafe"
      }
    }
  ]
}
```

## Validation Rules

- `schemaVersion` must be `hideout.adapter-pack/v1`.
- `id` is required and must be unique in the store registry unless installing a
  new candidate revision for the same pack.
- `version` is required.
- Every adapter id is unique within the pack.
- Every adapter script path must stay inside the locked source tree.
- Every command symbol must be simple and cannot contain path separators.
- Every requested capability must be a known generic proposal capability.
- Every test vector must name an adapter in the pack.
- A pack without tests may be installed but cannot be enabled.

## Non-Authority Rules

- Manifest fields are data, not authority.
- Manifest cannot add new outcome names.
- Manifest cannot grant HostFS write apply, privilege setup, host execution,
  endpoint, network, profile mutation, or marketplace trust authority.
- Manifest cannot override built-in adapter metadata.
