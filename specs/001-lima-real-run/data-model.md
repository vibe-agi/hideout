<!-- markdownlint-disable MD013 -->

# Data Model: Hideout Lima Real Run

## DogfoodRun

Represents one supervised Lima invocation of a configured target CLI.

### DogfoodRun Fields

- `sessionId`: Hideout session identifier created for the run.
- `environmentId`: reusable environment identifier used or created by the run.
- `backend`: backend used for the run; must be `lima` for dogfood evidence.
- `profile`: operator-authored profile name.
- `workspace`: sanitized workspace path accepted by workspace safety checks.
- `networkMode`: selected network policy, `direct` or privacy mode.
- `targetCommand`: configured target command, recorded without secret values.
- `auditPath`: session audit path.
- `boundarySummary`: structured summary derived from audit facts.
- `referenceResult`: workload result object.
- `cleanupState`: normal completion, interrupted, cleanup error, or preserved
  reusable environment state.

### DogfoodRun Validation Rules

- `backend=native` may exist only as wiring evidence and cannot satisfy dogfood
  isolation success.
- `workspace` must pass unsafe workspace guard before backend preparation.
- `auditPath` and `boundarySummary` must be present for completed runs.
- Secret values must not appear in `targetCommand`, `boundarySummary`, or
  routine evidence.

### DogfoodRun State Transitions

```text
planned -> rejected
planned -> preparing -> running -> completed -> summarized
planned -> preparing -> failed-closed
running -> interrupted -> cleanup -> summarized
```

## ReferenceWorkload

The deterministic useful-work fixture for this feature.

### ReferenceWorkload Fields

- `taskFile`: workspace file describing the small task.
- `inputFile`: optional workspace input read by the target.
- `outputFile`: expected workspace file to create or update.
- `expectedContent`: expected output content or marker.
- `successCheck`: guest/workspace check that verifies the output.
- `endpointURL`: operator-declared network endpoint.
- `networkExpectedStatus`: expected endpoint response or status marker.

### ReferenceWorkload Validation Rules

- `outputFile` must be inside the selected workspace.
- Any executable success check must run inside the guest/workspace context or
  be expressed as host-side artifact verification by the test harness.
- `endpointURL` must be provided by the operator/test harness and must not
  encode secrets.

## TargetCLI

The generic CLI exercised by the dogfood run.

### TargetCLI Fields

- `command`: command name available inside the guest.
- `arguments`: workload arguments supplied by the operator/test harness.
- `toolSupply`: profile-managed tool supply that makes the command available.
- `envInputs`: optional operator-selected environment variables exposed with
  normal run policy.

### TargetCLI Validation Rules

- Missing command or failed tool supply must fail closed.
- Core must not hardcode real product package names or commands.
- Runtime Hideout env, broker tokens, and proxy secret values must not be
  visible to the target.

## BoundaryActionSet

Known actions used to make SC-006 measurable.

### BoundaryActionSet Fields

- `hostOpenDeny`: a localhost/private host.open request expected to deny.
- `hostFSAllow`: an allowed HostFS read/list action, when configured.
- `hostFSDeny`: a denied or reserved-root HostFS action expected to deny.
- `networkSetup`: direct or privacy network setup event.
- `endpointExposure`: required preview/endpoint exposure event for this smoke.
- `sessionLifecycle`: session setup and end events.

### BoundaryActionSet Validation Rules

- The action set is fixed by the smoke script, not inferred from incidental
  target behavior.
- Every expected decision must appear in audit or Boundary Summary.
- Redacted evidence must show categories/counts, not secret endpoint details.

## RunEvidence

Operator-facing evidence for this feature.

### RunEvidence Fields

- `runOutput`: target stdout/stderr and minimal Hideout completion hints.
- `auditEvents`: redacted events filtered to the session.
- `boundarySummary`: structured capability counts and non-secret categories.
- `workspaceDiff`: host-visible expected file change.
- `networkResult`: declared endpoint reached or fail-closed reason.
- `backendEvidence`: Lima isolation evidence or native wiring-only label.

### RunEvidence Validation Rules

- Must be derived from authoritative runtime facts or deterministic test
  artifacts.
- Must omit proxy secrets, broker tokens, hidden endpoint internals, browser
  automation secrets, callback/open URL query secrets, and raw host file
  contents outside declared test artifacts.
