# Contract: Live Event Catalog

<!-- markdownlint-disable MD013 -->

## Catalog Row Shape

Each row describes one reducer-visible event kind or an explicit producer remap.

```json
{
  "kind": "environment",
  "producerKind": "environment",
  "source": "manager.Core.emitOperation",
  "requiredFields": ["id"],
  "redaction": "control-plane-stripped",
  "goReducer": true,
  "jsReducer": true,
  "panels": ["environments"]
}
```

Optional fields:

- `remapTo`: producer kind remaps to another event kind.
- `seedOnly`: row is seeded into the live model but has no live producer.
- `testOnly`: row exists only for reducer/schema forward-compatibility tests.

## Producer Coverage

- Every daemon event-bus production case must have a catalog row.
- Every default/remap case must be explicit and tested.
- A new producer kind that falls through an undocumented default remap fails.

## Reducer Coverage

- Every `liveconsole.Apply` branch must have a catalog row.
- Every WebUI-relevant event kind must have JavaScript reducer coverage.
- Unknown event kinds remain forward-compatible and ignored while sequence state
  stays coherent.

## Redaction

Rows with `control-plane-stripped` require tests that inject realistic
control-plane-looking values and prove they are absent from public model/output.

## Panels

Panels consuming live events must be mapped to event kinds. A panel with no live
producer is allowed only when explicitly marked seed-only/non-live.
