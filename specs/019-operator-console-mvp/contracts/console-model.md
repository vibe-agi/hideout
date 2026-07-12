# Contract: Console Model

<!-- markdownlint-disable MD013 -->

## Scope

The console model is a presentation model over existing Manager overview,
daemon event seed/state, decisions, notices, doctor status, package status, and
support matrix status.

It MUST NOT own authority.

## Required Panels

WebUI:

- Action Required
- Decisions
- Notices
- Doctor
- Package And Support
- Environments
- Background Operations
- HostFS Writes
- Stream Health

TUI compact:

- Action Required counts
- Decisions/notices summary
- Doctor/package/support summary
- Environments/background summary
- HostFS write summary
- Stream health

## Redaction

The model and all renderers MUST omit:

- claim tokens;
- provider-private refs;
- broker/UI tokens;
- proxy backing values;
- hidden runtime credential paths;
- raw HostFS staged content.

## Empty And Error States

Every required panel MUST define visible empty and error states. Empty panels
must be explicit, for example "No pending decisions", rather than omitted.

## Event Contract

Healthy daemon streams update panels through event payloads or explicit operator
refresh. Hidden steady-state overview/audit polling is prohibited while stream
health is live or idle-live.
