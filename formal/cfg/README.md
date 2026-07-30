# Feature 045 TLC configurations

This directory contains the bounded model inputs for the operator
observability console. `shared-constants.json` is the machine-readable source
of truth used by refinement tests and configuration validation. The `.cfg`
files spell out the same finite values for TLC.

`OperatorConfiguration.cfg`, `OperatorConfigurationLiveness.cfg`,
`RequestWorkflowLiveness.cfg`, `SecretTransition.cfg`, and
`WorkloadObservation.cfg` are active and have matching modules and property
declarations. The operator and request models deliberately separate their
full concurrent safety spaces from weak-fair single-operation/request crash,
lease, rollback, and cleanup progress spaces. Configuration drift is checked by
`internal/manager/formal_refinement_test.go` and
`internal/manager/profile_transaction_refinement_test.go`. The complete
configuration, secret, route, and observation production traces are part of
the consolidated `scripts/gates/formal.sh` release gate.

`formal/inventory.json` is the fail-closed repository inventory for every
root and feature configuration, its module, and every Go refinement test. The
formal gate rejects an unlisted configuration/module or refinement test before
running TLC, and `scripts/gates/formal-verify.sh` rechecks source and log
digests before accepting the local evidence.

The bounds are deliberately small but include:

- two concurrent clients and operation IDs for stale-CAS, disconnect, response
  loss, exact replay, and mismatch rejection;
- two secret generations and live connections for stage/probe failure,
  activation, post-activation rollback, old-connection preservation, and
  response recovery;
- reusable and disposable owners, parent and reused-PID process identities,
  event loss, restart, retention, and cleanup for observation.
