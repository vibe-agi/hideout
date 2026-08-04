# Contract: Manager Migration API

## Scope and transport

Migration extends the existing authenticated local Manager API under
`/api/v1/`. It follows the existing `resource/plan` and `resource/apply` shape and
returns the standard versioned API envelope:

```json
{
  "version": "<manager-api-version>",
  "resource": "migration/import/plan",
  "data": {},
  "errors": []
}
```

The existing bearer-token, expiry, loopback-listener, Host, and Origin checks
remain mandatory. Browser responses use `Cache-Control: no-store`. Migration does
not add a remote daemon API.

JavaScript is a presentation client only. Every request is decoded with unknown
fields rejected, converted to typed Go values, validated against current Manager
and provider facts, and authorized by an immutable confirmed plan.

## Resources

### `GET migration/capabilities`

Returns no authority and requires no passphrase.

```json
{
  "bundleReadVersions": [1],
  "bundleWriteVersion": 1,
  "exportModes": ["config"],
  "fullState": {
    "available": false,
    "backend": "lima",
    "providerVersion": "",
    "reasonCode": "migration.provider.compatibility_unproved"
  },
  "limits": {
    "environments": 32,
    "logicalBytes": 4398046511104,
    "payloadRecords": 1048576,
    "chunkBytes": 4194304
  }
}
```

`fullState.available` is true only after the runtime provider proves its supported
Lima version/layout, package helper, disk graph access, staging root, and required
free-space checks. A binary's claim alone is insufficient.

### `POST migration/secret-input`

Creates an in-memory, one-shot handle for bundle key derivation.

Request body is decoded by a dedicated size-limited handler and excluded from
request tracing:

```json
{
  "purpose": "export-create",
  "bundlePath": "/absolute/path/dev.hideout-migration",
  "passphrase": "<never logged>",
  "confirmation": "<required for export-create>"
}
```

`purpose` is `export-create`, `export-resume`, `inspect`, or `import`. For read
purposes, the handler authenticates the header before issuing a handle. Response:

```json
{
  "handle": "opaque-memory-handle",
  "purpose": "import",
  "bundleID": "random-bundle-id",
  "expiresAt": "2026-08-02T12:00:00Z",
  "usesRemaining": 1
}
```

The handle is bound to authenticated client session, purpose, stable bundle file
identity when applicable, and a short TTL. It is neither durable nor returned by
any snapshot. Daemon restart, expiry, use, bundle replacement, or client-token
change invalidates it. The response never reveals whether failure was a wrong
passphrase or authenticated-header corruption.

### `POST migration/export/plan`

Request:

```json
{
  "schema": "hideout.migration-export-request.v1",
  "mode": "full",
  "environmentNames": ["dev"],
  "includeSecretRefs": [],
  "outputPath": "/absolute/path/dev.hideout-migration"
}
```

The Manager resolves source revisions, environment/disk closure, stopped state,
provider capability, excluded data classes, output conflicts, selected secret
exportability, estimated logical/allocated bytes, and required free space.

Response data is an immutable review plan:

```json
{
  "schema": "hideout.migration-export-plan.v1",
  "planID": "...",
  "planDigest": "...",
  "baseRevisions": {},
  "mode": "full",
  "environments": [],
  "diskGraph": {},
  "sourceInventoryDigest": "sha256:...",
  "selectedSecrets": [],
  "excludedClasses": [],
  "warnings": [],
  "effects": [],
  "confirmationText": "..."
}
```

The plan does not contain a passphrase, secret value, master key, snapshot path,
or raw provider error.

### `POST migration/export/apply`

Request:

```json
{
  "plan": {},
  "confirmation": {
    "planDigest": "...",
    "acceptedWarningCodes": []
  },
  "secretInputHandle": "opaque-memory-handle",
  "idempotencyKey": "client-generated-random-value"
}
```

The Manager revalidates plan schema/digest, base revisions, stopped proof,
capability, claims, output nonexistence, confirmation, and secret handle. It then
creates a durable operation and returns without requiring the HTTP request to stay
open:

```json
{
  "operationID": "...",
  "state": "claiming",
  "next": "hideout migrate status <operation-id>"
}
```

The same authenticated client and idempotency key returns the same operation.
Changing the plan under the same key is a conflict.

### `POST migration/import/inspect`

Request:

```json
{
  "bundlePath": "/absolute/path/dev.hideout-migration",
  "secretInputHandle": "opaque-memory-handle"
}
```

Returns a redacted `BundleInspection` with bundle/format/version, source product
and backend compatibility, environment names, disk logical/allocated estimates,
included/excluded classes, secret references/value presence, required capabilities,
and warnings. It creates no draft, claims, secret entries, profile, environment,
or backend object.

URLs with userinfo are redacted; selected secret values and guest file content are
never returned.

### `POST migration/import/plan`

Request:

```json
{
  "schema": "hideout.migration-import-draft.v1",
  "bundlePath": "/absolute/path/dev.hideout-migration",
  "secretInputHandle": "opaque-memory-handle",
  "selectedEnvironmentRefs": ["bundle-env-1"],
  "nameMappings": [
    {"sourceRef": "bundle-env-1", "destinationName": "dev-clone"}
  ],
  "workspaceMappings": [],
  "secretMappings": [],
  "identityPolicies": [
    {"sourceRef": "bundle-env-1", "policy": "safe-clone"}
  ],
  "authorityDecisions": []
}
```

Returns either a complete immutable plan or a draft review with typed blockers.
The API never silently fills an authority-bearing destination value.

```json
{
  "schema": "hideout.migration-import-plan.v1",
  "planID": "...",
  "planDigest": "...",
  "bundlePath": "/absolute/path/dev.hideout-migration",
  "bundleBinding": {},
  "baseRevisions": {},
  "compatibility": {},
  "objects": [],
  "environmentActions": [],
  "identityActions": [],
  "workspaceActions": [],
  "authorityActions": [],
  "disabledProposals": [],
  "riskAcknowledgements": [],
  "effects": [],
  "blockers": []
}
```

`blockers` must be empty before apply. A Safe Clone plan contains only the policy,
not its not-yet-generated identity values. Exact Guest Restore adds a typed
acknowledgement describing the source-retirement/collision non-guarantee.

### `POST migration/import/apply`

Request has the same plan/confirmation/secret-handle/idempotency envelope as
export apply. Confirmation contains exact accepted risk codes and approved
authority proposal IDs. `--yes` or a generic Boolean cannot synthesize these.

Apply re-authenticates the sealed bundle binding, checks current revisions and
claims, and creates a fresh operation. Reapplying the same plan with a different
idempotency key is intentionally another import and therefore creates new Hideout,
backend, and Safe Clone guest identities.

### `GET migration/operations`

Returns bounded summaries, newest first. Filters may include `kind`, `state`, and
`bundleID`; unknown filters are rejected.

### `GET migration/operations/{id}`

Returns a redacted operation snapshot:

```json
{
  "operationID": "...",
  "kind": "import",
  "state": "materializing",
  "phaseStartedAt": "...",
  "progress": {
    "completedLogicalBytes": 0,
    "totalLogicalBytes": 107374182400,
    "completedEncodedBytes": 0,
    "componentsComplete": 0,
    "componentsTotal": 3,
    "currentComponent": "root disk for dev"
  },
  "recovery": {
    "required": false,
    "allowedActions": []
  },
  "warnings": [],
  "lastErrorCode": ""
}
```

The projection is suitable for CLI, TUI, and WebUI. It omits source/destination
secret values, credentials, keys, raw disk paths, passphrases, helper requests,
and unredacted provider stderr.

### Operation actions

```text
POST migration/operations/{id}/resume
POST migration/operations/{id}/cancel
POST migration/operations/{id}/recover
```

`resume` requires a new purpose-bound secret-input handle when bundle keys are no
longer in memory. `cancel` contains `retainPartial: true|false` for export and has
no destructive default. `recover` contains exactly one Manager-advertised action,
for example `finish` or `rollback`, plus the inspected operation revision. The
Manager rejects actions not present in the current recovery projection.

## Concurrency and revision contract

- Plan includes all relevant Manager record revisions and provider capability
  revision.
- Apply fails with a conflict if any base revision or stable bundle binding changed.
- Claims are acquired in deterministic order before effects.
- At most one nonterminal operation claims a destination name, backend object,
  source snapshot object, disk object, secret destination, or final output path.
- Status reads are lock-free with respect to long I/O and return the last durable
  snapshot.
- Cancellation is cooperative at authenticated record/effect boundaries.
- Daemon shutdown persists a recoverable state; it does not discard a worker or
  report success prematurely.

## HTTP and product error mapping

| Condition | HTTP status | Stable product code |
| --- | ---: | --- |
| Malformed/unknown field | 400 | `migration.request.invalid` |
| Authentication/token failure | 401/403 | Existing Manager auth code |
| Missing resource | 404 | `migration.operation.not_found` |
| Revision/name/claim/output conflict | 409 | `migration.plan.stale` |
| Unsupported provider/layout | 422 | `migration.capability.unavailable` |
| Expired/used secret handle | 422 | `migration.secret_input.required` |
| Provider unavailable | 503 | `migration.provider.unavailable` |

Long-running apply returns HTTP success with an operation whose later product
state may be `recoverable-failure`; transport success is not product completion.

## Audit events

Representative events:

- `migration.export.planned`, `.started`, `.checkpoint`, `.sealed`, `.cancelled`,
  `.failed`, `.recovered`;
- `migration.import.inspected`, `.planned`, `.confirmed`, `.started`, `.adopted`,
  `.committed`, `.rolled_back`, `.failed`, `.recovered`;
- `migration.authority.approved` and `migration.identity_policy.selected`.

Events include operation/bundle opaque IDs, phase, counts, policy enum, proposal
class/ID, stable result code, and timestamps. They do not include secret values,
passphrases, cryptographic key material, raw payload, credentials, or unredacted
authority-bearing values.
