# Contract: Route Inventory

<!-- markdownlint-disable MD013 -->

## Manager Routes

Manager routes are `/api/v1/<resource>` paths served by the existing Manager
API handler. The route inventory must be production-derived:

- either dispatch uses the same table as the inventory;
- or dispatch exposes a production recognizer that tests and docs use.

The inventory contract is:

```json
{
  "method": "GET",
  "path": "/api/v1/overview",
  "resource": "overview",
  "class": "manager-api",
  "owner": "internal/manager.API"
}
```

Rules:

- Inventory-only route fails validation.
- Recognized-but-uninventoried route fails validation.
- Unknown route remains rejected as unknown Manager API resource.
- POST member routes such as decision/notice members are represented by
  patterns where necessary, but their recognizer behavior must be tested.

## Daemon-Specific Routes

Daemon routes are local control-plane endpoints outside `/api/v1`.

Expected classes include:

- daemon status;
- daemon event stream;
- daemon stop;
- daemon background submission;
- loopback WebUI root, when served by daemon.

Rules:

- Daemon-specific endpoints are never counted as Manager routes.
- Daemon endpoints still use existing operator authentication.
- A daemon endpoint added without inventory fails validation.

## UI Action Routes

WebUI/TUI actions are not new authority. They must target existing Manager or
daemon routes.

Rules:

- Tests observe runtime requests or shared runtime action descriptors.
- Static source grep is not proof.
- Unknown path, unsupported method, or wrong route class fails validation.
- Browser EventSource/query-token behavior remains specific to `/daemon/events`;
  normal action routes must not bypass existing token handling.
