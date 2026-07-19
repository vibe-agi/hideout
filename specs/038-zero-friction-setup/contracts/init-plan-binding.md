# Contract: Init Plan Binding

## Purpose

Bind every setup and advanced init mutation to the exact Manager plan reviewed
by the operator, while preserving a thin local confirmation UI.

## Transport

The existing authenticated Manager resources remain:

```text
POST /api/v1/init/plan
POST /api/v1/init/apply
```

No daemon-specific setup route is added. Both resources remain inside the
Manager parity-locked API surface and use the same token, host, and origin
checks as other Manager operations.

## Plan Request

`init/plan` accepts a versioned Init Service Request. For `mode=setup`, any
non-default option or unknown field is rejected. For `mode=init`, existing
advanced choices remain available.

The response contains:

```json
{
  "version": "hideout.manager-api/v1",
  "resource": "init/plan",
  "data": {
    "request": {},
    "review": {
      "version": "hideout.init-review/v1",
      "planVersion": "hideout.init-plan/v1",
      "planDigest": "<64-hex>",
      "state": "fresh",
      "requiresConfirmation": true
    },
    "plan": {},
    "observation": {}
  },
  "errors": []
}
```

Unknown state, unsupported runtime, malformed profile, or unsafe store
placement fails closed. A valid existing profile returns `state=ready` and
`requiresConfirmation=false` from pure reads.

## Apply Request

`init/apply` accepts the exact prepared object plus a confirmation:

```json
{
  "prepared": {},
  "confirmation": {
    "reviewVersion": "hideout.init-review/v1",
    "planDigest": "<64-hex>",
    "confirmed": true
  }
}
```

An options-only apply request is invalid. The API never silently replans and
applies a different plan.

## Apply Algorithm

Manager must execute these steps in order:

1. Decode with unknown-field rejection and validate all versions.
2. Validate the prepared object's internal canonical digest.
3. Normalize and validate the target profile name.
4. Enter the single Manager lock-owning apply method and acquire the existing
   store-rooted profile mutation lock exactly once.
5. Re-observe profile, package, runtime, and prerequisite state while locked.
6. Rebuild the semantic current plan without replacing reviewed generated
   values.
7. Compare state, plan version, and canonical digest.
8. Validate local confirmation against the current digest.
9. Invoke a private lock-assuming typed InitTask apply helper while retaining
   the lock; never reacquire the same profile lock recursively.
10. Report observed applied/skipped effects and deterministic audit evidence.

Any mismatch before step 9 returns a stable stale-plan error and performs zero
profile, identity, onboarding-evidence, VM, or runtime effects.

## Concurrency Contract

If another process creates or changes the profile while the operator reviews,
only the first lock holder whose prepared observation still matches may apply.
A later apply must not load the newly created profile and continue mutating it.

## Generated Values

Incidental timestamps and random presentation values are excluded from the
canonical projection. Authority-relevant generated values are retained from
the prepared plan. Revalidation must not regenerate them and then declare its
own plan stale.

## Ready Contract

For `state=ready`:

- the client sends no apply request;
- Manager does not call `LoadOrInit`;
- profile bytes, metadata, identity files, onboarding evidence, audit, and
  directory mtimes remain unchanged; and
- output describes the effective current posture without calling valid
  customization damage.

## Failure Contract

Daemon startup, build mismatch, readiness, authentication, profile lock,
decode, validation, drift, and apply failures never fall back to embedded
Manager authority. Errors carry one concise reason and one runnable recovery
action where the central recovery registry covers the condition.
