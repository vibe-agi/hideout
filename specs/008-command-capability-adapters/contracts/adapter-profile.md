# Contract: Adapter Profile Configuration

<!-- markdownlint-disable MD013 -->

## Profile Shape

The exact JSON field names may be finalized during implementation, but the
profile model must represent this structure:

```json
{
  "commandAdapters": {
    "adapters": {
      "root-sensitive": {
        "enabled": true,
        "path": "adapters/root-sensitive.js",
        "digest": "sha256:012345...",
        "entrypoint": "decideCommandAdapter",
        "commands": ["sudo", "apt", "mount", "iptables", "systemctl"],
        "allowedProposalCapabilities": ["guest.privilege.plan"],
        "description": "Built-in root-sensitive intent capture"
      }
    }
  }
}
```

## Validation Rules

- Adapter ID is required and unique within the profile.
- `path` is required for local adapters.
- `digest` is required before enablement.
- `entrypoint` is required and must be a simple identifier.
- `commands` must be non-empty for enabled adapters.
- Command names must be simple command symbols, not paths.
- A command symbol can have only one owner across host-open command proxy and
  adapters.
- `allowedProposalCapabilities` must contain only known generic capability
  names.
- Disabled adapters may exist but do not own command symbols at runtime.

## Digest Pinning

- Digest is calculated over the exact artifact bytes.
- Runtime verifies the artifact digest before script evaluation.
- Digest mismatch fails closed and emits adapter evidence.
- `refresh-digest` is an explicit Manager plan/apply operation and must show
  old and new digest values.

## Command Ownership

Default behavior:

- `open` and `xdg-open` remain owned by existing host-open command proxy.
- Adapter config cannot shadow them unless the operator explicitly changes
  ownership through typed plan/apply.

Failure cases:

- Duplicate ownership rejects the profile before runtime.
- A registered command with a missing or disabled adapter denies before target
  execution.

## Built-In Adapter Materialization

The built-in root-sensitive adapter may be represented as Go-embedded adapter
source or as a generated local profile artifact. In both cases the compiled
profile view must include adapter ID, digest, command matches, and allowed
proposal capabilities so runtime evidence is identical to local adapters.
