# Contract: Adapter Pack Registry And Lock

<!-- markdownlint-disable MD013 -->

## Registry Shape

```json
{
  "schemaVersion": "hideout.adapter-pack-registry/v1",
  "packs": {
    "example.pack": {
      "packId": "example.pack",
      "state": "installed",
      "activeRevisionId": "rev_012345",
      "revisions": {
        "rev_012345": {
          "revisionId": "rev_012345",
          "version": "1.0.0",
          "source": {
            "kind": "git",
            "url": "https://example.invalid/repo.git",
            "commit": "0123456789abcdef0123456789abcdef01234567"
          },
          "manifestDigest": "sha256:...",
          "sourceDigest": "sha256:...",
          "testResultId": "test_012345",
          "validationStatus": "passed"
        }
      }
    }
  }
}
```

## State Rules

- Registry is store-wide.
- `activeRevisionId` is for inspection only; profiles do not track it
  implicitly.
- Profile enable bindings must pin `revisionId`.
- Upgrade creates a new revision and test result.
- Revoke marks a pack unusable for runtime routing until the operator resolves
  profile bindings.
- Built-in adapter metadata entries are read-only and cannot be overwritten.

## Write Rules

- Registry writes are atomic.
- Registry is schema-validated after write.
- Registry write failure fails closed and does not change profile authority.
- Digest mismatch during read or runtime compile makes the revision unusable.

## Git Source Rules

- Git source must be an exact commit id.
- Floating branches, tags, symbolic refs, and ambiguous refs are rejected for
  enablement.
- Recursive submodule checkout is disabled by default.
- Local hook/filter configuration is not treated as authority.
- The checked-out tree digest must match the lock before tests or enablement.
