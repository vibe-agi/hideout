# Contract: Manager Adapter Pack Lifecycle

<!-- markdownlint-disable MD013 -->

## Operations

Manager exposes typed operations for adapter packs. CLI is the first consumer;
TUI/WebUI/daemon surfaces may list and inspect the same state.

Lifecycle operations:

- `adapter-pack/install`
- `adapter-pack/test`
- `adapter-pack/enable`
- `adapter-pack/disable`
- `adapter-pack/upgrade`
- `adapter-pack/revoke`
- `adapter-pack/list`
- `adapter-pack/inspect`

## Install Request

```json
{
  "source": {
    "kind": "git",
    "url": "https://example.invalid/repo.git",
    "commit": "0123456789abcdef0123456789abcdef01234567"
  }
}
```

## Enable Request

```json
{
  "profile": "default",
  "packId": "example.pack",
  "revisionId": "rev_012345",
  "adapterId": "example-tool",
  "commands": ["example-tool"],
  "allowedProposalCapabilities": ["host.fs.write.plan"]
}
```

## Plan Response

```json
{
  "version": "hideout.adapter-pack-plan/v1",
  "operation": "adapter-pack/enable",
  "profile": "default",
  "packId": "example.pack",
  "revisionId": "rev_012345",
  "adapterId": "example-tool",
  "status": "pending",
  "changed": true,
  "warnings": [],
  "validation": {
    "core": "passed",
    "tests": "passed"
  }
}
```

## Apply Rules

- Authority-changing operations use Manager plan/apply.
- Apply recomputes the plan under profile/registry mutation lock.
- Apply rejects registry drift, profile drift, digest drift, failed tests, and
  command ownership conflicts.
- Apply emits local audit with deterministic control-plane redaction.
- Read-only list/inspect operations do not mutate state.

## Compatibility

- Existing 008 profile-scoped local adapters remain readable.
- A registry-backed profile binding compiles to the existing runtime adapter
  shape before broker execution.
- Existing `hideout profile command-adapter ...` commands may remain for legacy
  local artifacts, but new pack lifecycle commands should be distinct enough to
  avoid implying that install grants runtime authority.

## Fail-Closed Cases

- Source is not local or exact-commit git.
- Git commit cannot be fetched or verified.
- Manifest schema validation fails.
- Digest lock mismatch.
- Missing or failed pack tests.
- Pack requests undeclared or unsupported capabilities.
- Profile binding conflicts with another command owner.
- Built-in adapter mutation is attempted.
- Registry/profile/audit write cannot be completed safely.
