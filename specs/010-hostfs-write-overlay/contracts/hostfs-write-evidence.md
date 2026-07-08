# Contract: HostFS Write Evidence

<!-- markdownlint-disable MD013 -->

## Audit Actions

HostFS write overlay emits local audit events for:

```text
host.fs.overlay.stage
host.fs.overlay.deny
host.fs.overlay.pending
host.fs.overlay.claim
host.fs.overlay.apply
host.fs.overlay.discard
host.fs.overlay.timeout
host.fs.overlay.conflict
host.fs.overlay.cleanup
```

## Required Details

Stage:

- `operation`
- `path`
- `destinationPath` when applicable
- `ruleId`
- `source`
- `operationId`
- `decisionId`
- `hostChanged=false`
- `privilegeStatus`

Apply:

- `operation`
- `path`
- `destinationPath` when applicable
- `operationId`
- `decisionId`
- `decision`
- `status`
- `changedPaths`
- `conflictReason` when applicable
- `partialMutationPrevented` for failure paths
- `privilegeStatus`

Timeout:

- `operationId`
- `decisionId`
- `decision=deny`
- `reason=approval-timeout`
- `stagedDiscarded=true`

## Redaction

Control-plane values are stripped deterministically:

- broker tokens;
- UI and daemon tokens;
- claim tokens;
- `HIDEOUT_SECRET_*` backing values;
- generated machine IDs;
- setup credentials;
- overlay object paths;
- raw control-plane store paths.

User file paths, diffs, and previews are host-local operator data. They may appear in local audit, but export/share applies 005 user-data decisions.

## Live Events

Daemon/live-console events use an explicit HostFS write kind or payload fields that make these states visible:

- pending decision;
- claimed decision without claim token;
- applied decision;
- discarded decision;
- timeout;
- conflict;
- degraded/unknown privilege warning.

Events never carry claim tokens or overlay object paths.

## Export

Export/share artifacts include HostFS write evidence through existing audit export:

- control-plane strip is reasserted;
- fixed evidentiary fields are preserved;
- user data redaction choices apply to paths/previews;
- unresolved local overlay object paths are not exported as dangling references.
