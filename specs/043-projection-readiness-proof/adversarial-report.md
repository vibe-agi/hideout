# Adversarial Report: Projection Readiness Proof

<!-- markdownlint-disable MD013 -->

**Feature**: 043 Projection Readiness Proof

**Baseline commit**: `de82650`

**Baseline date**: 2026-07-23

## Baseline

Command:

```sh
go test -count=1 ./internal/cmdproxy ./internal/broker ./internal/hostcap \
  ./internal/profiletemplate ./internal/backend/lima ./internal/manager \
  ./internal/daemon ./internal/productevidence
```

Result: passed.

The baseline does not claim a deterministic local reproduction of the real
first-run race. Code inspection establishes the actionable gap: Manager adds
built-in and external host-app bindings to the final runtime registry, while
Lima `Prepare` reconstructs bootstrap shims from the profile-only registry and
the streamed supervisor session view does not carry the complete projected
catalog as a readiness prerequisite. The retry succeeds only after the guest
view catches up; no existing test proves first-attempt visibility.

The baseline also retains one independently reproducible schema gap:
`CapabilityDescriptor` does not yet marshal with the public lower-camel field
shape and the public schema does not cover the current `residualPolicy` field.

## 030 Starting Dispositions

| Historical observation | Starting state | Required direct disposition |
| --- | --- | --- |
| Broker verifies exact command registration | Implementation appears present; existing negative path may be denied by another layer | Add a registry/binding mismatch fixture and mutate away `LookupExact` |
| New template workspace-path posture | Current four templates use alias; no direct four-template assertion | Add one direct table test and mutate one template |
| Existing pathMode change requires recreation | Current environment mode/drift tests appear to cover it | Re-run the named direct proof and mutate away the identity input |
| Descriptor and open-resource schema parity | Intent has strict tests; descriptor is drifted | Retain intent proof, repair descriptor tags/schema, and run strict unknown-field fixtures |

No item is closed or removed from `docs/DEBT.md` until its named test and
mutation result are recorded below.

## 030 Current Direct Proofs

| Historical observation | Named current proof | Direct result |
| --- | --- | --- |
| Broker verifies exact command registration | `TestProjectionBindingCannotSubstituteForExactCommandRegistration` | An enabled binding with no exact registry entry is denied before launch and audited as `broker.request` with `commandValidation=unvalidated`; no validated command/capability field remains |
| New template workspace-path posture | `TestEveryNewTemplateUsesNeutralAliasWorkspacePresentation` | Fresh privacy, hardened, dev, and debug renders all expose exact `pathMode=alias` |
| Existing pathMode change requires recreation | `TestDedicatedPathModeFlipRequiresRecreateAndNeverSilentlyRemaps` | Alias-to-preserve changes exact machine and session identities, retains both literal modes, and requires recreate |
| Descriptor and open-resource schema parity | `TestPublicCapabilityDescriptorsMatchStrictSchemaAndDecoder`, `TestUnboundOpenResourceIntentMatchesStrictSchema`, `TestUnboundIntentRejectsForgedRelativeMetadataAndResourceBounds` | Real emitted values validate; unknown, missing, incompatible, and trailing inputs fail strict product decoders/public schemas |
| Reviewed built-in/external catalog ownership | `TestRunServiceApplyRejectsProjectionCatalogDrift`, `TestRunServiceApplyRejectsExternalProjectionCatalogDrift` | Built-in posture and enabled external ownership changes both invalidate the reviewed run before backend authority |
| Complete final catalog readiness | `TestFinalBuiltinAndExternalRegistrySnapshotsOnlyCompleteCatalog` | Built-in `code` plus an enabled external command are in one manifest; omitting the external shim publishes no marker |

Focused command:

```sh
go test -count=1 ./internal/broker ./internal/profiletemplate \
  ./internal/hostcap ./internal/cmdgrammar ./internal/manager
```

Result: passed on 2026-07-23. These direct results do not close the historical
rows until the corresponding mutations below are observed red and restored
green.

All six 030 implementation mutations were observed red and restored green on
2026-07-23. The four historical debt observations are therefore closed by
direct current proofs rather than inference, and the aggregate 030 acceptance
row was removed from `docs/DEBT.md`. This does not promote first-attempt real
backend reliability or exact-package release evidence.

## Implementation Mutation Inventory

| Assertion | Planned mutation | Red observed | Restored green |
| --- | --- | --- | --- |
| Complete catalog | Omit one projected command from the readiness expectation | `TestFinalBuiltinAndExternalRegistrySnapshotsOnlyCompleteCatalog` reported that `catalog-editor` was omitted | Complete registry snapshot restored; focused test passed |
| Manifest ordering | Write the manifest before the last entry | `TestMaterializeProjectionReadinessPublishesOnlyAfterCompleteCatalog` reported that the final publication collided with the prematurely published marker | Manifest-last publication restored; focused test passed |
| Entry type/integrity | Accept a symlink or wrong digest | `TestObserveProjectionReadinessValidatesExactSessionCatalog` accepted a symlink, and `TestObserveProjectionReadinessRejectsDigestAndIdentityDrift` accepted changed bytes | `Lstat`, regular non-symlink, and digest checks restored; focused tests passed |
| Ready proof identity | Accept another catalog digest | `TestApplySupervisorProjectionReadinessRejectsForeignOrIncompleteProof` accepted a foreign catalog digest | Exact reported-proof comparison restored; focused test passed |
| Lifecycle boundary | Activate or commit before matching ready | `TestApplyRunSharedReadyBarrierActivatesBeforeCommitAndPreventsTargetOnRejection` returned `nil` after the downstream readiness rejection was swallowed | Readiness rejection propagation restored; focused test passed |
| No target retry | Retry a projected target after launch | `TestApplyRunMapsExactRuntimeCommandMissWithoutTargetSideEffect` reported two target attempts | Retry mutation removed; focused test passed with exactly one attempt |
| Broker registration | Skip exact registry lookup | `TestProjectionBindingCannotSubstituteForExactCommandRegistration` failed because the inconsistent request launched the host app | Exact lookup restored; focused test passed |
| Template defaults | Change one new template away from alias | `TestEveryNewTemplateUsesNeutralAliasWorkspacePresentation/debug` reported `preserve`, wanted `alias` | Debug mutation removed; focused test passed |
| pathMode recreation | Remove pathMode from environment identity/drift | `TestDedicatedPathModeFlipRequiresRecreateAndNeverSilentlyRemaps` reported preserve silently remapped to alias | Exact pathMode identity restored; focused test passed |
| Descriptor parity | Omit `residualPolicy` or its JSON tag/schema field | `TestPublicCapabilityDescriptorsMatchStrictSchemaAndDecoder` reported missing `residualPolicy` plus forbidden `ResidualPolicy` | Lower-camel tag restored; focused test passed |
| Strict schema | Permit an unknown descriptor field | `TestPublicCapabilityDescriptorsMatchStrictSchemaAndDecoder/unknown` reported that the strict decoder accepted `unexpected` | `DisallowUnknownFields` restored; focused test passed |
| Reviewed catalog | Skip apply-time catalog digest comparison | `TestRunServiceApplyRejectsExternalProjectionCatalogDrift` returned no error for changed external ownership | Digest binding restored; focused test passed |

All six projection-readiness implementation mutations were observed red and
restored green on 2026-07-23. The restored focused suite covered backend,
session wire, Linux supervisor, Lima, Manager, daemon, and product-evidence
packages.

## Judge Negative-Fixture Inventory

| False-green input | Expected rejection | Result |
| --- | --- | --- |
| Dirty source | Clean provenance required | Rejected by production evaluator fixture |
| Wrong source/package digest | Exact candidate mismatch | Rejected by package/source binding fixtures |
| Wrong runtime artifact/build | Exact runtime mismatch | Rejected by runtime binding fixtures |
| Marker-only/reduced evidence | Semantic artifact required | Rejected by Go and shell marker-only fixtures |
| Nine fresh or 29 warm samples | Minimum sample inventory | Rejected after raw inventory recomputation |
| Edited summary or p95 | Recomputed raw samples disagree | Rejected by edited-p95 fixtures |
| Missing/extra/false check | Closed check map violation | Rejected by strict closed-map fixtures |
| Retry/fallback/timeout/effect nonzero | Fail-closed invariant violation | Rejected independently for every nonzero counter |
| Missing/altered artifact | Digest inventory mismatch | Rejected by missing and changed artifact fixtures |
| Unknown JSON field | Strict decoder rejection | Rejected by strict schema/decoder fixtures |
| `not-run` privacy record | Cannot promote privacy | Rejected for privacy promotion; Gate 2 records `not-promoted` |

## Real-Gate Evidence

| Proof family | Required evidence | Status |
| --- | --- | --- |
| 043 first-attempt readiness | Clean exact-package Gate 2, 10 fresh, 30 warm, concurrent | Passed; strict readiness artifact retained |
| 030 built-in projection | Same candidate, semantic built-in flow artifact | Passed as `030.projection.real-gate2.code-open` and `.trusted-grant` |
| 032 external pack | Same candidate, semantic external-pack artifact | Passed as `032.host-app-pack.real-gate2.external` |
| 039 persistent grant | Same candidate, separate-run reuse/revoke artifact | Passed as `039.trusted-host-app-grant.real-gate2.persistent` |
| Alias privacy | Matching clean Gate 3 artifact | Not promoted; matching Gate 3 was not run |

## Findings And Restorations

The Manager now snapshots the complete final registry, hashes the exact
dispatcher and command bytes, and publishes a private session-local manifest
only after the complete shim catalog exists. Lima preserves that immutable
Manager expectation through prepare and uses its names for bootstrap instead
of reconstructing the older profile-only view.

The fixed guest supervisor strictly validates the manifest, every regular
non-symlink executable, entry digest, catalog digest, and bound session,
environment, and snapshot identities before reporting readiness. The
authenticated ready proof must match exactly before commit. Pre-commit
cancellation closes the owning SSH session immediately; the existing
post-commit graceful path remains unchanged.

Manager lifecycle activation, daemon readiness publication, and delayed preview
effects now sit behind the matching proof. Readiness audits expose only status,
stable reason code, a fixed recovery hint, catalog digest, bounded counts and
duration, and the target classification. A negative fixture confirms that a
private path present in both the underlying error and caller-provided hint is
not serialized.

Focused combined regression on 2026-07-23:

```sh
go test -count=1 ./internal/backend ./internal/sessionwire \
  ./cmd/hideout-session-supervisor ./internal/backend/lima \
  ./internal/manager ./internal/daemon ./internal/productevidence
```

Result: passed.

Race and full regression commands:

```sh
go test -race -count=1 ./internal/backend/lima ./internal/manager \
  ./internal/app ./internal/audit ./internal/productevidence \
  ./internal/releasecompat
go test -count=1 ./...
scripts/test-projection-readiness-smoke.sh
```

Result: passed. The race run exposed one test-harness `io.Pipe` ownership race;
the harness was corrected and the complete race selection then passed.

The adversarial real probe and final local gate exposed and restored nine
integration or evidence-harness defects:

1. runtime-only shared-machine verification incorrectly required workspace
   roots;
2. generic shared transport metadata leaked into machine verification as a
   partial workspace attachment;
3. the daemon stream harness and audit schema omitted the new readiness
   observation;
4. the cancellation sampler terminated before proving the guest was blocked at
   the pre-commit readiness boundary;
5. a missing `limactl` shell binding masked cancellation diagnostics;
6. failed-gate cleanup captured the daemon before `hideout clean`, although
   `clean` could start a replacement daemon;
7. the isolation-evidence smoke still treated a legacy log-only 031 envelope as
   sufficient for the strengthened 032 real proof;
8. doctor gave each broker request only one second of its five-second total
   budget, so loaded runs disconnected and retried already-admitted work; and
9. unbounded Go package parallelism on a high-core host starved bounded helper
   processes and converted product timeouts into scheduler-load flakes.

Each defect received a focused regression or a real-probe assertion before the
restored suite passed.

Clean exact-package command:

```sh
scripts/test-projection-readiness-lima-e2e.sh --require-real \
  --fresh 10 --warm 30 \
  --out .hideout-release-evidence/043-projection-readiness-real-gate2
```

Result: passed on real macOS arm64 Lima. The qualifying clean run bound commit
`74ba7e61d2c7ac3c662e691be6e9ee0f82bf64a0`, the verified package, runtime
`developer-standard@2026.07.0`, Darwin arm64 host, and Linux aarch64 guest.
The canonical retained directory is regenerated from the final clean
convergence commit so its manifest remains the exact source of candidate
identity.

The retained raw inventory contains 10 fresh samples, 30 warm samples, one
concurrent pair, and one pre-commit cancellation sample. Independently
recomputed nearest-rank results were fresh p95 27 ms and warm p95 14 ms;
cancellation was 0 ms. Operator retries, target retries, ambient fallbacks,
timeouts, unauthorized host effects, and cross-session access were all zero.
The production evidence evaluator accepted all five required 030/032/039/043
proof entries and every retained artifact digest.

No matching clean Gate 3 was produced. The Gate 2 artifact therefore records
`privacy.status=not-promoted`; this feature promotes first-attempt readiness and
the direct projection/pack/grant flows, not clean alias privacy.

## Final Local Convergence

Commands:

```sh
scripts/test-gate0.sh
markdownlint-cli2 'specs/043-projection-readiness-proof/**/*.md'
scripts/test-doc-truth-smoke.sh
git diff --check
```

Result: passed on 2026-07-23. Gate 0 ran Go tests with bounded package
parallelism, all seven formal models, install/package contracts, every product
smoke, strict 032/043 false-green fixtures, HostFS, daemon/PTY, lifecycle,
first-run, UI, doctor/recovery, release hardening, and documentation truth.

The first full-gate attempt correctly rejected the stale log-only 032 fixture;
that input is now an expected negative case. Two subsequent unbounded-package
runs exposed the broker request-budget and helper-scheduling flakes described
above. After the request-budget regression and bounded Gate 0 concurrency were
added, the complete gate passed from the beginning without a retry.

All FR-001 through FR-026, SC-001 through SC-009, three user-story acceptance
sets, listed edge cases, and the specification checklist now map to a named
unit/race/mutation/schema/smoke/real-evidence result. The only conditional
result is clean alias privacy: FR-022 and the third US3 scenario explicitly
permit it to remain unpromoted when no matching Gate 3 artifact exists, and
the retained artifact plus product docs record that exact state. No additional
043 implementation task remains.
