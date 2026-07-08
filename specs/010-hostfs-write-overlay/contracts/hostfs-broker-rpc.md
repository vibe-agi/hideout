# Contract: HostFS Broker RPC

<!-- markdownlint-disable MD013 -->

## Scope

The guest `hideout-hostfsd` FUSE daemon sends HostFS RPC to the host broker. 010 extends this stream from read-only requests to staged write-class requests. The broker validates session token and routes to the HostFS service; it does not directly mutate host files.

## Existing Read Actions

Existing actions remain compatible:

```text
host.fs.stat
host.fs.read
host.fs.list
```

## Write-Class Actions

New write-class requests use explicit actions:

```text
host.fs.write.create
host.fs.write.replace
host.fs.write.append
host.fs.write.truncate
host.fs.write.mkdir
host.fs.write.delete
host.fs.write.rename
host.fs.write.chmod
host.fs.write.chown
```

## Request Envelope

Common fields:

```json
{
  "id": "broker-request-id",
  "subject": "hostfs:daemon",
  "action": "host.fs.write.replace",
  "args": {
    "path": "/Users/alice/file.txt",
    "destinationPath": "",
    "offset": 0,
    "dataBase64": "aGVsbG8=",
    "truncate": false,
    "mode": "0644",
    "uid": 501,
    "gid": 20
  }
}
```

Rules:

- `command` and `argv` remain forbidden for HostFS RPC;
- only action-appropriate args are accepted;
- content payloads are streamed or chunked when needed; complete files are not required in memory;
- unknown args fail closed;
- unsupported write-class actions return read-only/unsupported denial, not host mutation.

## Response Envelope

Successful staging:

```json
{
  "decision": "allow",
  "status": "ok",
  "exitCode": 0,
  "data": {
    "operationId": "hfwop_123",
    "decisionId": "hfwdec_123",
    "staged": true,
    "hostChanged": false
  }
}
```

Denied staging:

```json
{
  "decision": "deny",
  "status": "denied",
  "exitCode": 126,
  "stderr": "hostfs operation denied"
}
```

The guest may treat successful staging as filesystem success for its overlay view. Host apply remains separate.

## Audit

Broker audit for write-class requests includes:

- operation kind;
- requested path and destination path when applicable;
- matched policy effect, rule id, and source;
- `staged=true|false`;
- `hostChanged=false`;
- operation id and decision id when staged;
- deny/conflict/unsupported reason.

Audit never includes overlay object paths, claim tokens, broker tokens, daemon tokens, or setup credentials.
