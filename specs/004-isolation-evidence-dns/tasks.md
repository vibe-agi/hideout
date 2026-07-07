<!-- markdownlint-disable MD013 -->

# Tasks: Isolation Boundary Evidence And DNS Leak Closure

**Input**: Design documents from `/specs/004-isolation-evidence-dns/`

**Prerequisites**: `plan.md`, `spec.md`, `research.md`, `data-model.md`,
`contracts/dns-mediation.md`, `contracts/isolation-evidence.md`,
`quickstart.md`, `.specify/memory/constitution.md`

**Tests**: Required. This feature changes privacy-mode network enforcement, adds
a fail-closed security boundary, extends release evidence, and touches audit
redaction — every boundary-relevant change needs positive and fail-closed
coverage per constitution Principle IV.

**Organization**: Tasks are grouped by user story. US1 (DNS leak closure) is the
MVP because it closes the actual privacy hole; US2 (isolation evidence) makes the
proof durable; US3 (evidence-surface redaction) keeps the evidence clean.

**Phase 0 gate (empirical, before US1 implementation)**: On real Lima, confirm
whether the current Gate 3 resolves DNS via the bypass leak (expected). This
determines that closing the leak requires the Gate 3 known-good path to use a
controlled mediated resolver rather than relying on the leak; record the result
in `research.md` notes.

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: The controlled DNS listener and shared gate-result emission that
US1 and US2 depend on.

- [X] T001 [P] Add the controlled DNS listener service (UDP+TCP, ordinary non-privileged port, records received queries, exposes a received-count assertion surface) in `internal/testproxy/dns/dns.go`, mirroring `internal/testproxy/socks5/socks5.go`
- [X] T002 [P] Add the `cmd/hideout-gate-dns/main.go` entrypoint that starts the DNS listener, prints its address on stdout, and serves until SIGTERM, mirroring `cmd/hideout-gate-socks5/main.go`
- [X] T003 [P] Add a shared gate-result emission helper (writes `$HIDEOUT_RELEASE_EVIDENCE_DIR/gates/<gate>.json` with `{id,result,reason,auditPath,boundarySummary,environmentName}` when the evidence dir is set) in `scripts/lib/gate-result.sh`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The socks5 observation capability and the manifest schema
extension that the gates and evidence writer build on.

**Critical**: No user story implementation before these are testable.

### Tests First

- [X] T004 [P] Add tests for the DNS listener: records queries, reports zero when none received, distinguishes UDP vs TCP receipt, in `internal/testproxy/dns/dns_test.go`
- [X] T005 [P] Add tests for the socks5 gate recording observed CONNECT targets (forward-proof observation point) in `internal/testproxy/socks5/socks5_test.go`
- [X] T006 [P] Add schema-validation tests/fixtures for the new `isolationGates` and `environmentSnapshot` objects (valid shapes accepted, unknown fields rejected under `additionalProperties:false`) in `schemas/release-dogfood.schema.json`

### Implementation

- [X] T007 Extend the socks5 test proxy to record observed CONNECT targets so a DNS-over-TCP query to the mediated resolver is observable, in `internal/testproxy/socks5/socks5.go`
- [X] T008 Extend `schemas/release-dogfood.schema.json` with the `isolationGates` array (id, backend, environmentName, result passed|failed|not-run, reason, auditPath, boundarySummaryRef) and `environmentSnapshot` object, keeping `additionalProperties:false`, and relax the `command` const to admit the isolation-evidence orchestration

**Checkpoint**: The DNS observation points and the evidence schema exist; user
stories can start.

---

## Phase 3: User Story 1 - Privacy Mode Closes The DNS Resolver Bypass (Priority: P1) MVP

**Goal**: Privacy mode blocks the connected-subnet bypass route so no resolver
on that subnet has a non-TUN path; a separate verified mediated resolver is
required; a connected-subnet-only environment fails closed; the closure is
proven bidirectionally.

**Independent Test**: Quickstart steps 2 and 3 — the generated bootstrap blocks
the bypass route (unit), and on real Lima the bidirectional proof passes for a
mediated known-good config and fails closed for a known-bad config, without
falling back to direct.

### Phase 0 Empirical Gate (US1 prerequisite)

- [X] T000 [US1] On real Lima, run the current Gate 3 and record whether its DNS resolution goes through the connected-subnet bypass (`192.168.5.3`) or through tun2socks, capturing the observation (e.g. route of the resolver, whether resolution survives a manual block) in `specs/004-isolation-evidence-dns/research.md` notes. This determines that the known-good path in T017 must supply a controlled mediated resolver rather than rely on the leak. T014, T017, and T037 depend on this result.

### Design Prerequisites (US1)

- [X] T037 [US1] Define the mediated-resolver input carrier and its boundary: how a mediated resolver enters `network.Prepare`/`Plan` and the guest verify (an operator-declared profile/network field, not a gate-only backdoor), such that the product path fails closed when no mediated resolver is declared and Gate 3 injects a known-good controlled resolver through the same carrier. Design + a failing test first in `internal/network/network_test.go` (and profile field if used, `internal/profile/profile.go` + `schemas/profile.schema.json`)
- [X] T038 [US1] Define and test the guest DNS mediation mechanism so a target-style resolution through the normal system resolver path (libc/`/etc/resolv.conf`, port 53) reaches the declared mediated resolver as DoH over the privacy path — the root bootstrap points the guest resolver at a guest-local DoH stub (`/etc/resolv.conf` + `resolvectl` override) that forwards each query as DoH/HTTPS to the mediated resolver over the TUN and the SOCKS CONNECT proxy, and blocks the connected-subnet resolvers with `iptables` DROP on `:53`; the reverse proof queries each captured connected-subnet resolver directly and confirms it is unreachable. The bootstrap has no netfilter today, so this is a new root-side mechanism; a synthetic `dig -p <port>` MUST NOT substitute for the normal resolver path. Test the generated mechanism in `internal/network/network_test.go`; rollback in `CleanupScript`

### Tests for User Story 1

- [X] T009 [P] [US1] Add tests asserting the generated tun2socks bootstrap blocks the connected-subnet bypass route immediately after the default route is set to `hideout0`, and that `CleanupScript` rolls it back, in `internal/network/network_test.go`
- [X] T010 [P] [US1] Add tests asserting a connected-subnet-only environment fails closed (no mediated resolver verified) with a diagnostic naming the resolver/route, and that no fallback to direct occurs, in `internal/network/network_test.go`
- [X] T011 [P] [US1] Add tests asserting the DNS policy string reflects enforced behavior (no "connected-subnet resolvers are not yet verified" wording) in `internal/network/network_test.go`
- [X] T012 [P] [US1] Add manager-level tests that a privacy-mode network decision fails closed when the block cannot be established, never degrading to direct/native/ambient, in `internal/manager/run_network_test.go`

### Implementation for User Story 1

- [X] T013 [US1] Implement the connected-subnet bypass-route block in the tun2socks branch of `BootstrapScript` (after the default route is set to `hideout0`) and its rollback in `CleanupScript`, in `internal/network/network.go`
- [X] T014 [US1] Using the T038 DoH stub mechanism and the T037 carrier, add the guest-side structural verification into the existing verify block: confirm the guest resolver is the DoH stub and the connected-subnet resolvers are blocked, fail-closed if not, in `internal/network/network.go`. The observable bidirectional proof (forward DoH resolution + reverse connected-subnet-unreachable) is Gate 3 / T017 (depends on T037, T038)
- [X] T015 [US1] Using the T037 carrier, require a verified mediated resolver for privacy mode and fail closed with a clear diagnostic when none is declared/verified (connected-subnet-only refused), in `internal/network/network.go` and `internal/manager/run_network.go` (depends on T037)
- [X] T016 [US1] Update the DNS policy string to describe the enforced behavior in `internal/network/network.go`
- [X] T017 [US1] Extend `scripts/test-gate3-hidden-proxy.sh` to prove the DNS closure end to end on real Lima (DoH stub design): pass `--mediated-resolver` (a DoH server IP), assert the forward proof (guest resolver is the DoH stub + a target-style resolution and HTTPS fetch succeed through the mediated DoH path) and the mandatory reverse proof (every captured connected-subnet resolver is unreachable; fail closed if the check cannot run), emitting a gate-result via the shared helper (depends on T000, T037, T038)

**Checkpoint**: US1 closes the leak and proves it bidirectionally; privacy mode
fails closed without a mediated resolver.

---

## Phase 4: User Story 2 - Isolation Evidence Is Repeatable And Retained (Priority: P2)

**Goal**: Gate 2/3/4 + env-image record per-gate results and an environment
snapshot into the existing release-evidence manifest; not-run is explicit;
repeatability holds under fixed conditions.

**Independent Test**: Quickstart steps 4 and 5 — one manifest records all
isolation gates with passed/failed/not-run and a snapshot, validates against the
extended schema, and re-runs equivalently under held-fixed conditions.

### Tests for User Story 2

- [X] T018 [P] [US2] Add tests that the manifest writer aggregates `gates/<gate>.json` files into `isolationGates` and records the `environmentSnapshot`, in a manifest-writer test alongside `scripts/test-release-dogfood.sh` (shell assertion) or a Go helper test if one exists
- [X] T019 [P] [US2] Add tests that a gate without prerequisites is recorded `not-run` with a reason (Gate 4 without browser, env-image without URL), never omitted or marked passed, exercised through the gate-result helper contract
- [X] T020 [P] [US2] Add tests that native never appears as the backend for a passed isolation claim in the manifest

### Implementation for User Story 2

- [X] T021 [US2] Make each isolation gate emit a gate-result via the shared helper when `HIDEOUT_RELEASE_EVIDENCE_DIR` is set: add emission to `scripts/test-gate2-lima.sh`, `scripts/test-gate3-hidden-proxy.sh`, `scripts/test-gate4-host-escape.sh`, and `scripts/test-env-image.sh`
- [X] T022 [US2] Fix `scripts/test-phase1.sh` so `--env-image` runs in the real gate sequence (not inside `print_plan`) and records `not-run` with a reason when `HIDEOUT_ENV_IMAGE_URL` is unset instead of hard-exiting
- [X] T023 [US2] Add an isolation-evidence orchestration to `scripts/test-phase1.sh` (a mode selecting gate2/gate3/gate4/env-image and capturing per-gate results) and wire it so the evidence is available to the manifest writer
- [X] T024 [US2] Extend the manifest writer in `scripts/test-release-dogfood.sh` to aggregate the per-gate result files into `isolationGates` and to record the `environmentSnapshot` (proxy mode, host prerequisites, uncontrolled external context), validating against the extended schema

**Checkpoint**: US1 and US2 both work; isolation proof is durable and
reviewable, with explicit not-run.

---

## Phase 5: User Story 3 - Evidence Surfaces Do Not Leak Identity Or Control-Plane Material (Priority: P3)

**Goal**: The generated machine-id is not derivable from any displayed identity
reference; InitTask audit passes through deterministic redaction; neither change
perturbs the 003 environment identity model.

**Independent Test**: Quickstart step 6 — no displayed identity reference yields
the raw machine-id; InitTask audit entries are redacted; environment tests stay
green.

### Tests for User Story 3

- [X] T025 [P] [US3] Add tests that no displayed identity reference yields the raw generated machine-id across the actual display surfaces, not only the profile model: `internal/profile/profile_test.go`, plus `hideout explain`/audit views in `internal/app/app_test.go` and the Manager API/overview identity fields in `internal/manager/api_test.go` and `internal/manager/manager_test.go`
- [X] T026 [P] [US3] Add tests that InitTask audit entries pass through the deterministic control-plane redaction (control-plane detail stripped identically to the rest of audit), in `internal/inittask/inittask_test.go`
- [X] T027 [P] [US3] Add/confirm tests that the 003 named-environment identity and drift behavior are unchanged by the machine-id change (existing environment tests remain green), referencing `internal/environment/environment_test.go` and `internal/manager/run_environment_test.go`

### Implementation for User Story 3

- [X] T028 [US3] Make the generated machine-id independent of the identity ID (distinct random value or one-way derivation) so it cannot be recovered from a displayed identityId, in `internal/profile/profile.go` (`machineIDFromIdentityID` at profile.go:513 and materialization path)
- [X] T029 [US3] Route InitTask audit emission through the shared `internal/audit` deterministic redaction (`RedactDetails`) in `internal/inittask/inittask.go` (`auditWriter`/`emitTaskAudit` at inittask.go:546-572)

**Checkpoint**: All three stories independently verifiable; the evidence is
clean.

---

## Phase 6: Polish & Cross-Cutting Concerns

**Purpose**: Documentation truth (including the A3 non-claim), gates, and
end-to-end validation.

- [X] T030 [P] Update `docs/threat-model.md`: the connected-subnet DNS non-claim becomes a closed claim for non-root paths, AND add the new A3 guest-root routing non-claim (a guest-root target can rewrite guest routing to restore a bypass; out of scope for this slice)
- [X] T031 [P] Update `docs/network-privacy-architecture.md` (structural DNS mediation, the mediated-resolver requirement, and the A3 limitation) and `docs/privacy-run-design.md` (network DNS policy reflects enforced behavior)
- [X] T032 [P] Update `docs/STATUS.md` (network row: DNS leak closed for non-root paths with the A3 limitation; the two redaction known-issues closed) and `docs/privacy-run-test-plan.md` (Gate 3 DNS assertion + isolation evidence bundle)
- [X] T033 Run `go test ./...` from `/Users/null/Code/ym/nodejs/node-v26.4.0` and fix fallout in touched packages
- [X] T034 Run `scripts/test-gate0.sh` from `/Users/null/Code/ym/nodejs/node-v26.4.0` and fix schema/docs/smoke fallout
- [X] T035 Run `markdownlint-cli2 README.md README.zh-CN.md docs specs/004-isolation-evidence-dns` and fix issues
- [X] T036 Walk quickstart steps 1, 2, and 6 locally (unit gates, bootstrap block generation, evidence-clean tests); record that steps 3, 4, 5 (real-Lima DNS proof and evidence bundle) are operator-run in the quickstart notes

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: none.
- **Foundational (Phase 2)**: depends on Setup; blocks all stories.
- **US1 (Phase 3)**: depends on Foundational (DNS listener, socks5 observation).
  MVP. Within US1: T000 (empirical) and the design prerequisites T037 (mediated
  resolver carrier) and T038 (guest DNS redirect mechanism) precede T014/T015/
  T017 — the verification, fail-closed, and Gate 3 tasks consume them.
- **US2 (Phase 4)**: depends on Foundational (schema) and consumes US1's Gate 3
  DNS assertion; lands after US1.
- **US3 (Phase 5)**: depends only on Foundational; independent of US1/US2 and can
  proceed in parallel with them.
- **Polish (Phase 6)**: after the stories it documents.

### User Story Dependencies

- **US1**: none beyond Foundational. Independently shippable — closes the leak.
- **US2**: consumes US1's Gate 3 assertion for the gate3 result; independently
  testable via quickstart steps 4/5.
- **US3**: fully independent (audit/profile), can land any time after Setup.

### Within Each Story

- Tests first; confirm they fail against current behavior before implementing.
- Network changes (block, verify, policy string) before the Gate 3 script.
- Schema extension before the manifest writer consumes it.
- The machine-id change must keep the environment tests green (T027 guards it).

### Parallel Opportunities

- T001–T003 in parallel.
- T004–T006 in parallel; T009–T012 in parallel; T018–T020 in parallel;
  T025–T027 in parallel.
- US3 (Phase 5) can run fully in parallel with US1/US2.
- T030–T032 in parallel after implementation settles.

## Parallel Example: User Story 1

```bash
Task: "T009 [P] [US1] bootstrap-block generation tests in internal/network/network_test.go"
Task: "T010 [P] [US1] connected-subnet-only fail-closed tests in internal/network/network_test.go"
Task: "T012 [P] [US1] manager fail-closed no-fallback tests in internal/manager/run_network_test.go"
```

## Implementation Strategy

### MVP First (US1 Only)

1. T000 Phase 0 empirical gate (does current Gate 3 resolve via the leak?).
2. Phases 1–2 (DNS listener, socks5 observation, schema).
3. US1 design prerequisites: T037 mediated-resolver carrier, T038 guest DNS
   redirect mechanism.
4. US1: bypass-route block + bidirectional proof + fail-closed + Gate 3
   assertion (T013–T017).
5. Validate quickstart steps 2 and 3; stop and review before US2.

### Incremental Delivery

1. US1: close the DNS leak and prove it (the security fix).
2. US2: durable isolation evidence bundle with explicit not-run.
3. US3: clean the evidence surfaces (machine-id, InitTask redaction).
4. Polish: docs (incl. A3 non-claim), gates, quickstart walk.

### Scope Guard

- Do NOT build a turnkey mediated resolver (guest-local stub / DNS-over-proxy)
  to make the default connected-subnet resolver work — connected-subnet-only
  fails closed; the mediated path is a follow-on slice. The T037 carrier only
  accepts an operator-declared mediated resolver; it does not stand one up.
- Do NOT attempt to constrain guest-root network privileges — the A3 guest-root
  routing rewrite is a recorded non-claim, not a guarantee of this slice.
- Do NOT introduce a daemon, shared default environment, Claude credential
  delivery, guest-to-host capabilities, or marketplace/bundle trust.
- Native MUST NOT satisfy an isolation claim.
