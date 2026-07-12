# Contract: Console Actions

<!-- markdownlint-disable MD013 -->

## Allowed Actions

Console v1 may expose only these operator-triggered actions:

- claim a decision through the existing Manager decision claim route;
- resolve a claimed decision through the existing Manager decision resolve route;
- acknowledge a notice through the existing Manager notice ack route;
- refresh the console model;
- explicitly run local light doctor and show/cache the resulting report.

## Prohibited Actions

Console v1 MUST NOT add:

- package repair/apply;
- automatic doctor repair;
- environment clean/stop beyond existing surfaces;
- HostFS write mutation outside decision resolve;
- backend start/stop beyond existing typed routes;
- profile policy editing;
- marketplace or adapter-pack trust changes.

## Failure Behavior

- Stale claim token: show stale-token/denied state and do not retry with ambient authority.
- Expired decision: show timeout/default-deny state.
- Already claimed decision: show denied/claimed state.
- Notice ack failure: show error state and leave notice visible.
- Doctor run failure: show doctor error finding; do not auto-fix.

## Audit And Evidence

Actions rely on existing Manager/decision/notice/provider audit. The console
does not invent separate audit events except ordinary WebUI/daemon request
refusal evidence already implemented by 006.
