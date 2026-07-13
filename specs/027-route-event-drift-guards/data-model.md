# Data Model: Route And Event Drift Guards

<!-- markdownlint-disable MD013 -->

## Route Inventory Entry

- `method`: HTTP method.
- `path`: Full route path or path pattern.
- `resource`: Manager resource name when route class is `manager-api`.
- `class`: `manager-api` or `daemon-specific`.
- `owner`: Package/component that dispatches the route.
- `description`: Short human-readable purpose for diagnostics.

Validation:

- Manager entries must be under `/api/v1/`.
- Daemon-specific entries must not be under `/api/v1/`.
- Each inventory entry must be recognized by the production recognizer.
- Each production-recognized route must be present in the inventory.

## Route Recognizer

Production-owned helper that classifies a method/path pair.

- Input: method and path.
- Output: recognized/not-recognized, route class, resource, and allowed method.
- Must be usable by tests without constructing a full daemon or Manager server.
- Must avoid a test-only route list.

## UI Action Route

Runtime action target from WebUI or TUI.

- `surface`: `webui` or `tui`.
- `action`: logical action name.
- `method`: HTTP method.
- `path`: actual request path emitted at runtime.
- `class`: expected route class.
- `auth`: expected token/header/query behavior.

Validation:

- Runtime tests record actual requests.
- Covered action targets must be recognized by the route recognizer.
- Wrong class, unknown path, or unsupported method fails.

## Live Event Catalog Entry

- `kind`: liveconsole event kind consumed by reducers.
- `producerKind`: kind passed to the daemon event bus, when applicable.
- `source`: production source name, `seed-only`, or `test-only`.
- `remapTo`: event kind produced when producer kind remaps.
- `requiredFields`: payload fields required for schema/reducer correctness.
- `redaction`: `control-plane-stripped` or `none`.
- `goReducer`: whether Go reducer coverage is required.
- `jsReducer`: whether JavaScript reducer coverage is required.
- `panels`: live panels that depend on this event kind.

Validation:

- Production source rows must have producer coverage.
- Seed-only and test-only rows must be explicit.
- Every reducer branch must have a catalog row.
- Every producer kind must map to a catalog row or declared remap.
- Required fields must match event validation tests.

## Reducer Coverage Record

Evidence that a catalog entry is covered by a reducer test.

- `kind`: event kind.
- `go`: true when `liveconsole.Apply` test covers the event.
- `js`: true when the served WebUI reducer test covers the event.
- `unknownBehavior`: explicit behavior for unknown kinds.

Validation:

- WebUI-relevant kinds require both Go and JavaScript coverage.
- TUI-only or terminal-rendering rows require Go/TUI coverage.
- Unknown-kind behavior must remain aligned between Go and JavaScript where
  WebUI consumes the stream.
