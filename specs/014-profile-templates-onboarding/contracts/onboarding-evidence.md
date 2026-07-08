# Contract: Onboarding Evidence

<!-- markdownlint-disable MD013 -->

## File

`profiles/<profile>/onboarding-evidence.json`

## Schema

Version: `hideout.onboarding-evidence/v1`

Required fields:

- `version`
- `time`
- `profile`
- `template`
- `effectivePosture`
- `backend`
- `network`
- `hostfsPosture`
- `adapterPackPosture`
- `privilege`
- `warnings`
- `nonClaims`
- `profilePath`
- `nextSteps`

Conditionally required:

- `proxySecretRef` when network is `tun2socks`;
- `mediatedResolver` when network is `tun2socks`;
- `initAuditPath` when init apply wrote audit.

## Redaction

Before writing, free-text evidence fields pass deterministic control-plane
redaction. Evidence must not contain:

- raw proxy secret values;
- broker tokens;
- UI tokens;
- `HIDEOUT_SECRET_*` values or backing names;
- generated machine ids;
- hidden runtime credential paths.

Proxy secret refs may be recorded as refs, not resolved secret values.

## Failure And Cancellation

- Missing choices: no evidence file.
- Existing profile collision: no evidence file.
- Hardened degraded or unknown without explicit fallback: no evidence file.
- Confirmation refusal/cancellation: no evidence file.
- Evidence write failure: init apply returns failure and must not print success.

## Smoke Assertions

Gate 0 onboarding smoke validates:

- evidence schema;
- zero control-plane secret pattern matches;
- `privacy` evidence contains zero HostFS grants and zero adapter-pack posture;
- hardened degraded fallback evidence is visibly marked degraded.
