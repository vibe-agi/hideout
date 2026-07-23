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
| Complete catalog | Omit one projected command from the readiness expectation | Pending | Pending |
| Manifest ordering | Write the manifest before the last entry | Pending | Pending |
| Entry type/integrity | Accept a symlink or wrong digest | Pending | Pending |
| Ready proof identity | Accept another catalog digest | Pending | Pending |
| Lifecycle boundary | Activate or commit before matching ready | Pending | Pending |
| No target retry | Retry a projected target after launch | Pending | Pending |
| Broker registration | Skip exact registry lookup | `TestProjectionBindingCannotSubstituteForExactCommandRegistration` failed because the inconsistent request launched the host app | Exact lookup restored; focused test passed |
| Template defaults | Change one new template away from alias | `TestEveryNewTemplateUsesNeutralAliasWorkspacePresentation/debug` reported `preserve`, wanted `alias` | Debug mutation removed; focused test passed |
| pathMode recreation | Remove pathMode from environment identity/drift | `TestDedicatedPathModeFlipRequiresRecreateAndNeverSilentlyRemaps` reported preserve silently remapped to alias | Exact pathMode identity restored; focused test passed |
| Descriptor parity | Omit `residualPolicy` or its JSON tag/schema field | `TestPublicCapabilityDescriptorsMatchStrictSchemaAndDecoder` reported missing `residualPolicy` plus forbidden `ResidualPolicy` | Lower-camel tag restored; focused test passed |
| Strict schema | Permit an unknown descriptor field | `TestPublicCapabilityDescriptorsMatchStrictSchemaAndDecoder/unknown` reported that the strict decoder accepted `unexpected` | `DisallowUnknownFields` restored; focused test passed |
| Reviewed catalog | Skip apply-time catalog digest comparison | `TestRunServiceApplyRejectsExternalProjectionCatalogDrift` returned no error for changed external ownership | Digest binding restored; focused test passed |

## Judge Negative-Fixture Inventory

| False-green input | Expected rejection | Result |
| --- | --- | --- |
| Dirty source | Clean provenance required | Pending |
| Wrong source/package digest | Exact candidate mismatch | Pending |
| Wrong runtime artifact/build | Exact runtime mismatch | Pending |
| Marker-only/reduced evidence | Semantic artifact required | Pending |
| Nine fresh or 29 warm samples | Minimum sample inventory | Pending |
| Edited summary or p95 | Recomputed raw samples disagree | Pending |
| Missing/extra/false check | Closed check map violation | Pending |
| Retry/fallback/timeout/effect nonzero | Fail-closed invariant violation | Pending |
| Missing/altered artifact | Digest inventory mismatch | Pending |
| Unknown JSON field | Strict decoder rejection | Pending |
| `not-run` privacy record | Cannot promote privacy | Pending |

## Real-Gate Evidence

| Proof family | Required evidence | Status |
| --- | --- | --- |
| 043 first-attempt readiness | Clean exact-package Gate 2, 10 fresh, 30 warm, concurrent | Pending |
| 030 built-in projection | Same candidate, semantic built-in flow artifact | Pending |
| 032 external pack | Same candidate, semantic external-pack artifact | Pending |
| 039 persistent grant | Same candidate, separate-run reuse/revoke artifact | Pending |
| Alias privacy | Matching clean Gate 3 artifact | Pending; remains unpromoted without Gate 3 |

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

Result: passed. Implementation mutations and real-backend evidence remain
pending and are not implied by this mechanical regression.
