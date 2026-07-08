# Contract: Compatibility Fixtures

<!-- markdownlint-disable MD013 -->

## Fixture Scope

The compatibility smoke covers the current alpha families named in FR-011:

- profile schema
- package manifest
- adapter-pack manifest
- adapter-pack registry
- doctor report
- export artifact
- decision record
- notice record
- HostFS write decision
- HostFS write event
- onboarding evidence
- daemon status
- daemon event
- live console seed
- run result
- init plan
- init audit

## Accepted Fixture Rule

Each family must have one accepted current-version fixture. The test must use
the production parser, schema validator, or schema constant that the product
uses for that family.

## Rejected Fixture Rule

Each family must have one unknown-major fixture. The validator must reject it
before mutation, enablement, or apply. A rejected fixture must produce explicit
guidance such as recreate, upgrade, or unsupported version.

## Unknown Version Rule

Unknown major versions are not tolerated as warnings. They fail closed unless a
future migration feature explicitly adds support and tests.

## Redaction Rule

Fixtures that exercise report/export/readiness surfaces must include
control-plane-shaped values and assert the emitted result does not contain raw
values.

## Gate0 Role

Gate0 runs a smoke-sized compatibility check. Broader historical migration tests
may be added later, but current alpha release readiness requires at least the
accepted/rejected pair for every family above.
