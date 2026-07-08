# Contract: Manager Command Adapter Plan/Apply

<!-- markdownlint-disable MD013 -->

## Operations

Manager exposes typed plan/apply operations for command adapters. CLI is the
first consumer; WebUI/TUI may display the same plan and recent decisions.

Supported operations:

- `add-local`
- `enable`
- `disable`
- `refresh-digest`
- `remove`
- `list`
- `recent-decisions`

## Plan Request

```json
{
  "profile": "default",
  "operation": "add-local",
  "adapterId": "root-sensitive",
  "path": "adapters/root-sensitive.js",
  "entrypoint": "decideCommandAdapter",
  "commands": ["sudo", "apt", "iptables"],
  "allowedProposalCapabilities": ["guest.privilege.plan"]
}
```

## Plan Response

```json
{
  "version": "hideout.command-adapter-plan/v1",
  "profile": "default",
  "operation": "add-local",
  "adapterId": "root-sensitive",
  "digest": "sha256:...",
  "commandsBefore": ["open", "xdg-open"],
  "commandsAfter": ["apt", "iptables", "open", "sudo", "xdg-open"],
  "allowedProposalCapabilities": ["guest.privilege.plan"],
  "status": "pending",
  "warnings": [
    "root-sensitive adapter captures command intent only until 009 is enforced"
  ],
  "changed": true
}
```

## Apply Rules

- Apply requires a valid plan version.
- Apply recomputes the plan under the profile mutation lock.
- Apply rejects profile drift, command ownership drift, and digest drift unless
  the operation is explicitly `refresh-digest`.
- Apply writes through the profile store, not through raw file mutation.
- Apply emits local audit with control-plane redaction.

## API Surface

The exact HTTP route may follow existing Manager route naming, but it must be a
typed API and not a raw profile writer. It must be covered by route inventory
or manager API tests if added to `/api/v1/`.

Minimum local surfaces:

- CLI commands under `hideout profile command-adapter ...`.
- Manager Core methods for plan/apply/list/recent decisions.
- WebUI/TUI read surfaces showing configured adapters and recent decisions.

## Fail-Closed Cases

- Missing artifact.
- Artifact digest mismatch.
- Duplicate command ownership.
- Unknown proposal capability.
- Adapter output schema violation.
- Attempt to enable an adapter without explicit operator action.
- Attempt to use plan/apply to broaden authority beyond declared capabilities.
