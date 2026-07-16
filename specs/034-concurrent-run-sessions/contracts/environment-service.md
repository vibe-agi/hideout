# Contract: Environment Shared Service

<!-- markdownlint-disable MD013 MD060 -->

## V1 Service

V1 applies this contract only to the existing environment-level network
runtime. Direct mode has no long-lived service. `tun2socks` and its DNS
mediation are VM-global and therefore shared by compatible sessions.

## Fingerprint

Core computes a SHA-256 over canonical fields:

- network mode and engine;
- mediated resolver;
- normalized local bypass hosts;
- proxy secret reference name;
- SHA-256 of the resolved proxy secret value;
- generated network contract version.

The raw secret and secret digest are control-plane data. Public status exposes
only `matching`, `conflict`, `absent`, or `unprovable`.

## Activation

Under the environment transition lock:

- no live owner/service: materialize and run setup once, verify it, persist a
  strict ready state, then admit the first owner;
- matching ready service and a live owner: admit without rerunning setup;
- different fingerprint: deny the new run before target execution;
- stale service with no live owners: run bounded cleanup, verify removal, then
  start the requested service;
- failed or unprovable cleanup: mark environment error and deny.

The proxy secret file is removed by successful setup and never copied into a
session summary.

## Cleanup

A session exit does not clean the service while a sibling owner is live. The
last finishing owner performs bounded cleanup and records the outcome before
closing its owner. A cleanup failure is visible and prevents a false `ready`
state.

No mutable integer is authoritative. The live owner registry supplies the
reference count.

## Future Services

Additional environment helpers may reuse this contract only after defining a
strict kind, configuration fingerprint, health probe, cleanup, redaction, and
real-backend gate. This contract does not create a generic service plugin.
