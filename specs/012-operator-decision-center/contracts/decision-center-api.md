# Contract: Decision Center Manager API And CLI

<!-- markdownlint-disable MD013 -->

## Manager API Resources

All routes require the existing local Manager token and return the existing
Manager API envelope.

```text
GET  /api/v1/decisions
GET  /api/v1/decisions/{id}
POST /api/v1/decisions/{id}/claim
POST /api/v1/decisions/{id}/approve
POST /api/v1/decisions/{id}/deny
GET  /api/v1/notices
GET  /api/v1/notices/{id}
POST /api/v1/notices/{id}/ack
```

Query filters:

- `kind`;
- `state`;
- `profile`;
- `session`;
- `severity` for notices;
- `includeTerminal=true` for terminal decisions.

## Claim Request

```json
{
  "expectedVersion": "hideout.decision/v1",
  "surface": "cli"
}
```

Claim response is the only response that may include a claim token:

```json
{
  "version": "hideout.decision-claim/v1",
  "decisionId": "dec_...",
  "state": "claimed",
  "claimToken": "claim_...",
  "claimExpiresAt": "2026-07-08T00:01:30Z"
}
```

## Approve Request

```json
{
  "expectedVersion": "hideout.decision/v1",
  "claimToken": "claim_...",
  "reason": "operator-approved"
}
```

## Deny Request

```json
{
  "expectedVersion": "hideout.decision/v1",
  "claimToken": "claim_...",
  "reason": "operator-denied"
}
```

Kinds may map approve/deny onto provider-specific words such as apply/discard,
but the generic response must include the generic terminal status and provider
result.

## Watch/Event Contract

Daemon event payloads:

```json
{
  "kind": "decision",
  "phase": "updated",
  "payload": {
    "id": "dec_...",
    "recordClass": "actionable",
    "state": "claimed"
  }
}
```

```json
{
  "kind": "notice",
  "phase": "updated",
  "payload": {
    "id": "not_...",
    "recordClass": "informational",
    "status": "degraded",
    "acknowledged": false
  }
}
```

Events are refresh signals plus redacted summaries. They are not authority and
must not carry claim tokens.

## CLI Commands

```text
hideout decision list [--kind <kind>] [--state <state>] [--include-terminal]
hideout decision inspect <decision-id>
hideout decision claim [--surface cli] <decision-id>
hideout decision approve --claim-token <token> <decision-id>
hideout decision deny --claim-token <token> [--reason <reason>] <decision-id>
hideout decision watch
hideout notice list [--kind <kind>] [--severity <level>]
hideout notice inspect <notice-id>
hideout notice ack [--surface cli] <notice-id>
```

Existing HostFS write CLI may remain as compatibility commands backed by the
same generic store.

## Failure Contract

- Unknown kind: fail closed.
- Missing/expired/wrong claim token: fail closed.
- Provider rejects apply: fail closed and preserve provider result.
- Redaction failure: fail closed before output.
- Audit write failure: fail closed before authority.
- Notice ack for missing notice: fail closed without provider effects.
