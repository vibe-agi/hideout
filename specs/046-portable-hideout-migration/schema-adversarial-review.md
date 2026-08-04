# Adversarial Review: Migration Schemas

**Batch**: T001–T004 dependency and strict schema setup
**Date**: 2026-08-02
**Scope**: migration manifest, shared operator projection, plan, and adoption
request/receipt schemas

## Fresh-eyes findings

1. A migration plan must not persist the one-shot passphrase handle used by an
   API request. The immutable plan schema has no such field and rejects it as an
   unknown property.
2. Secret manifest entries can describe reference-only, selected-value, or
   non-exportable transfer. Selected values can only point to an encrypted
   component; the schema has no inline plaintext value field.
3. The adoption document is data-only. Its action vocabulary is closed, Safe
   Clone requires exactly both reset actions, and Exact Guest Restore permits
   only the preserve action. Command, script, mount, proxy, and path fields are
   not accepted.
4. Arrays and byte/count fields carry the v1 structural bounds before Go performs
   cross-record sums, graph closure, digest, canonical-order, and path checks.
5. The zstd module is pinned directly with both module and `go.mod` checksums.

Schemas alone cannot prove reference uniqueness, graph closure, aggregate sums,
identity equality, or approval correspondence. Those checks remain explicit
portable-core and Manager tasks rather than implicit schema claims.

## Red-green and mutation proof

The schema tests were added first and failed because all three schema resources
were absent. After implementation, representative manifest, export request/plan,
import draft/plan, and adoption request/receipt documents passed.

For the mutation proof, the manifest's top-level `additionalProperties` rule was
temporarily changed from `false` to `true`. The unchanged negative test failed:

```text
manifest accepted a passphrase field
```

The strict rule was restored and the same suite returned green.

## Negative fixtures

The tests reject:

- top-level passphrase data and inline secret values;
- more than 32 environments;
- a one-shot secret-input handle in an immutable plan;
- an import draft selecting more than 32 environments;
- executable command data in an adoption request; and
- Exact Guest Restore actions under a Safe Clone policy.

## Validation

```text
go test ./schemas -count=1
go mod verify
jq empty schemas/migration-*.schema.json
```

All commands passed after the mutation was restored.
