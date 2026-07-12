# Contract: Typed HostFS Broker Error

<!-- markdownlint-disable MD013 -->

## Envelope

Broker responses gain an additive top-level `error` object:

```json
{
  "id": "req_123",
  "decision": "deny",
  "status": "denied",
  "exitCode": 126,
  "stderr": "hostfs read requires operator approval",
  "error": {
    "code": "hostfs.read.approval-required",
    "errno": "EACCES",
    "retryable": true,
    "decisionRef": "dec_hfr_abc123"
  }
}
```

HostFS failure responses require `error`. Non-HostFS responses may omit it.
`stderr` is optional human context and never determines errno.

## Schema

```text
error.code         required non-empty closed enum
error.errno        required closed enum
error.retryable    required boolean
error.decisionRef  optional public decision ID
error.retryAfterMs optional integer, 1..60000
```

`decisionRef` is not a claim token and cannot authorize any Manager action.
Capability tokens, claim tokens, private provider paths, file content, and
symlink targets are forbidden.

## V1 Code/Errno Matrix

| Code | Errno | Retryable | Decision reference |
| --- | --- | --- | --- |
| `hostfs.path.hidden` | `ENOENT` | false | forbidden |
| `hostfs.read.approval-required` | `EACCES` | true | required |
| `hostfs.read.denied` | `EACCES` | false | forbidden |
| `hostfs.read.request-limited` | `EACCES` | true | forbidden |
| `hostfs.directory.not-enumerable` | `EACCES` | false | forbidden |
| `hostfs.directory.incomplete` | `EOVERFLOW` | false | forbidden |
| `hostfs.write.unauthorized` | `EACCES` | false | forbidden |
| `hostfs.host.prerequisite-failed` | `EIO` | false | forbidden |
| `hostfs.operation.unsupported` | `EROFS` | false | forbidden |
| `broker.unavailable` | `EIO` | true | forbidden |

`retryAfterMs` is permitted only for `hostfs.read.request-limited` when Core
can compute the remaining rolling-window bound, or for `broker.unavailable`
when transport code has a truthful bound. Pending-cap refusal may omit it.

## Producer Rules

- Core validates the code/errno/retryability combination before serialization.
- Approval-required is emitted only after the provider returns a real
  pending/claimed decision.
- Explicit read deny, terminal deny/timeout, exact-directory readdir, and
  capacity refusal never emit a decision reference.
- Outside-domain, reserved, and discover-denied paths use hidden/ENOENT.
- Host prerequisite/TCC failure is EIO and cannot be represented as a Hideout
  approval request.
- Unsupported operation means the data plane does not implement the operation;
  missing authority for an implemented operation is not EROFS.

## Consumer Rules

The packaged Linux helper:

1. validates the complete typed record;
2. validates the exact code/errno pair against its closed table;
3. maps to the corresponding kernel errno;
4. maps a missing, malformed, unknown, or mismatched typed record to EIO;
5. never parses stderr to choose errno after the synchronized helper/server
   version is packaged.

FUSE presentation uses explicit TTLs:

- entry TTL: one second;
- attr TTL: one second;
- negative TTL: zero;
- `NullPermissions`: true;
- content open/read: broker authorized every time.

An old helper's fallback EIO is fail-closed degraded compatibility, not proof
of typed-error support.
