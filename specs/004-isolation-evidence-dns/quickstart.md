<!-- markdownlint-disable MD013 -->

# Quickstart: Validate Isolation Evidence And DNS Leak Closure

Validation guide for `004-isolation-evidence-dns`. Contracts live in
[contracts/dns-mediation.md](contracts/dns-mediation.md) and
[contracts/isolation-evidence.md](contracts/isolation-evidence.md); entity
shapes in [data-model.md](data-model.md).

## Preconditions

- Repository root. Unit/schema steps need neither Lima nor network.
- The DNS closure proof and the isolation-evidence gates need macOS/Lima and are
  operator-run.

## 1. Static And Unit Gates

```bash
go test ./...
scripts/test-gate0.sh
```

Expected: green. New unit coverage: the bootstrap generates the connected-subnet
bypass-route block and its cleanup rollback; the DNS policy string reflects
enforced behavior (no "not yet verified" wording); the controlled DNS listener
records queries and exposes a zero-query assertion; the release-dogfood schema
validates the new `isolationGates` and `environmentSnapshot` objects.

## 2. DNS Bypass Block Is Generated (unit)

Inspect the generated privacy-mode bootstrap for a Lima session.

Expected: after the default route is set to the TUN device, the bootstrap blocks
the connected-subnet resolver route so it has no non-TUN path; the cleanup
script rolls it back; the block coexists with a required verified mediated
resolver, and a connected-subnet-only environment fails closed rather than
silently breaking DNS or leaking.

## 3. DNS Closure End-to-End Proof (real Lima gate)

```bash
HIDEOUT_GATE3_MEDIATED_RESOLVER=1.1.1.1 \
  scripts/test-gate3-hidden-proxy.sh
```

Expected: the forward proof holds — the guest resolver is the DoH stub
(`dns_mediated=yes`) and a target-style resolution plus HTTPS fetch succeed
through the mediated DoH path (`https_request=ok`) — and the mandatory reverse
proof holds (`connected_subnet_blocked=yes`: every captured connected-subnet
resolver is unreachable after the block). If the reverse check cannot run (no
captured resolvers, no query tool), the gate fails closed. Privacy-mode failure
does not fall back to direct.

## 4. Isolation Evidence Artifact (real Lima gates)

```bash
HIDEOUT_SECRET_DEFAULT_PROXY=socks5://host.lima.internal:<port> \
HIDEOUT_ENV_IMAGE_URL='https://<distributor>/<image>.img#sha256:<digest>' \
  scripts/test-release-dogfood.sh
```

Expected: a single manifest under `.hideout-release-evidence` records
`isolationGates` for gate2-lima, gate3-hidden-proxy, gate4-host-escape, and
env-image with per-gate `passed`/`failed`/`not-run`, backend, environment name,
audit path, and Boundary Summary references, plus an `environmentSnapshot`. A
gate without prerequisites (e.g. Gate 4 without a browser, or env-image without
an image URL) is recorded `not-run` with a reason, never omitted or marked
passed. The manifest validates against the updated schema and leaks no
control-plane secrets.

## 5. Repeatability Under Held-Fixed Conditions

Re-run step 4 on the same commit, backend, proxy mode, and host prerequisites.

Expected: an equivalent artifact (same gate set and results). External network
state and the real DNS upstream are recorded in the snapshot but are not
required to match.

## 6. Evidence Is Clean (unit + inspection)

```bash
go test ./internal/audit/... ./internal/inittask/...
```

Expected: no displayed identity reference yields the raw generated machine-id;
InitTask audit entries pass through the same deterministic redaction as the rest
of audit. The named-environment identity and drift tests remain green (the
machine-id change does not perturb the 003 environment model).

## 7. No Silent Fallback (unit + gate)

Expected: a privacy-mode failure (unusable proxy, unestablishable DNS block,
failed DNS proof) exits non-zero without running the target in direct mode; a
Lima isolation-claim failure does not fall back to the native harness; nothing
degrades to an ambient host path.

## 8. Docs And Lint

```bash
markdownlint-cli2 README.md README.zh-CN.md docs specs/004-isolation-evidence-dns
```

Expected: lint passes; the threat-model DNS non-claim becomes a closed claim,
the network privacy doc describes structural DNS mediation, and STATUS reflects
the closed leak and the two redaction fixes.
