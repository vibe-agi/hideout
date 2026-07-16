# Contract: Canonical Manager Run Service

<!-- markdownlint-disable MD013 MD060 -->

## Single Entry Point

The daemon session worker and Manager HTTP run apply must invoke one Manager
run service. It owns:

1. strict request validation;
2. run planning and environment selection;
3. drift revalidation and confirmation binding;
4. session/runtime/audit creation;
5. per-run broker, HostFS, network, endpoint, and host-capability providers;
6. backend activation and target execution;
7. target completion and ordered cleanup;
8. redacted result and recovery mapping.

The CLI may parse flags and display the review, but cannot perform these steps
for an executable run.

The HTTP adapter is non-renewable: it periodically revalidates the credential
bound to the request and cancels after rotation grace. It may not convert a
stale HTTP request into an indefinitely authorized run. Renewable or terminal
clients use the daemon session socket.

## Request Parity

The structured request represents every current executable CLI option:

- profile/backend/network/proxy/resolver selection;
- host and guest workspace;
- named environment, ephemeral identity, remove-after-run, and weak-isolation
  acknowledgement;
- argv and run-scoped public environment;
- audit selection and verbose result;
- HostFS grants/denies and profile-grant suppression;
- preview endpoint requests;
- terminal mode and descriptor.

Unknown fields and contradictory combinations fail. Secret references are
resolved only in Manager/Core.

## Confirmation Binding

- The daemon sends a deterministic review containing a plan version/digest.
- Acceptance refers to that exact digest.
- Manager revalidates mutable state before apply.
- A changed/stale plan returns a new review or fails; old acceptance never
  approves changed authority.
- Non-interactive missing confirmation defaults to deny.

## Ownership

- The daemon registers a worker before target authority becomes usable.
- The environment transition lock is released before target lifetime.
- One owner record and kernel lock bind the worker to its session.
- Same-workspace sibling workers use distinct runtime/provider/supervisor state.
- Explicit environment stop takes the transition lock and refuses any live or
  unproved worker/owner.

## Result

- Target stdout/stderr/terminal data are stream data, not part of the control
  result.
- The final result includes session/environment identity, exact completion,
  audit/boundary summary, and cleanup outcome.
- Status, events, doctor, and audit derive lifecycle from the same worker and
  owner model.
- No result exposes operator token, broker token, proxy value, setup credential,
  raw HostFS handle, SSH credential, or guest supervisor control path.
