# Quickstart: Profile Templates And Onboarding

<!-- markdownlint-disable MD013 -->

## Scenario 1: Privacy Non-Interactive Onboarding

Command:

```sh
hideout init \
  --profile alpha-privacy \
  --template privacy \
  --backend lima \
  --network tun2socks \
  --proxy-secret env:HIDEOUT_PROXY_URL \
  --mediated-resolver 1.1.1.1 \
  --no-input
```

Expected:

- profile validates;
- network mode is `tun2socks`;
- proxy env is not visible to the guest;
- mediated resolver is recorded as an IP;
- HostFS grants are empty;
- command adapters are empty;
- onboarding evidence exists and has no control-plane secret matches.

Covers: FR-001, FR-002, FR-003, FR-004, FR-009, FR-010, FR-014, FR-015,
SC-001, SC-002, SC-007.

## Scenario 2: Missing Non-Interactive Choices Fail Closed

Command:

```sh
hideout init --no-input
```

Expected:

- non-zero exit;
- message lists missing explicit template/profile/backend/network choices;
- no profile file;
- no onboarding evidence file.

Covers: FR-011, FR-013, SC-005.

## Scenario 3: Hardened Enforced Succeeds

Command:

```sh
hideout init \
  --profile alpha-hardened \
  --template hardened \
  --backend lima \
  --network tun2socks \
  --proxy-secret env:HIDEOUT_PROXY_URL \
  --mediated-resolver 1.1.1.1 \
  --privilege-status enforced \
  --no-input
```

Expected:

- profile and evidence are created;
- evidence privilege status is `enforced`;
- metadata/evidence label the effective posture as `hardened`.

Covers: FR-005, SC-003.

## Scenario 4: Hardened Degraded Fails Closed

Command:

```sh
hideout init \
  --profile alpha-hardened \
  --template hardened \
  --backend lima \
  --network tun2socks \
  --proxy-secret env:HIDEOUT_PROXY_URL \
  --mediated-resolver 1.1.1.1 \
  --privilege-status degraded \
  --no-input
```

Expected:

- non-zero exit;
- no hardened profile;
- guidance explains recreate/base-image/privilege separation.

Covers: FR-005, FR-006, SC-003.

## Scenario 5: Explicit Hardened Degraded Fallback

Command:

```sh
hideout init \
  --profile alpha-hardened-degraded \
  --template hardened \
  --backend lima \
  --network tun2socks \
  --proxy-secret env:HIDEOUT_PROXY_URL \
  --mediated-resolver 1.1.1.1 \
  --privilege-status degraded \
  --allow-degraded-template \
  --no-input
```

Expected:

- profile is created;
- metadata or name contains degraded;
- evidence states hardened was not achieved.

Covers: FR-006, SC-004.

## Scenario 6: Dev And Debug Are Weaker By Design

Commands:

```sh
hideout init --profile alpha-dev --template dev --backend native --network direct --no-input
hideout init --profile alpha-debug --template debug --backend native --network direct --no-input
```

Expected:

- both profiles validate;
- evidence labels weak/local development posture;
- no privacy or hardened claims.

Covers: FR-007, FR-008, SC-001.

## Scenario 7: Interactive Cancellation

Run `hideout init` in a simulated TTY, answer no at confirmation.

Expected:

- prompt recommends `privacy`;
- prompt names HostFS and adapter-pack defaults;
- no profile and no evidence summary are written.

Covers: FR-012, FR-013, SC-006.

## Scenario 8: Existing Profile Collision

Create a profile, then run onboarding with the same profile name.

Expected:

- fail closed before mutation;
- no replacement;
- error points to explicit future recreate/replace flow.

Covers: FR-016.

## Scenario 9: Gate 0 Smoke

Run:

```sh
scripts/test-onboarding-smoke.sh
scripts/test-gate0.sh
```

Expected:

- all four templates validate;
- evidence schema validates;
- no default HostFS grants or adapter pack bindings;
- docs include recommended and advanced commands.

Covers: FR-017, SC-008, SC-009.
