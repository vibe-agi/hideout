# Dependency and Generated-Artifact Provenance

Hideout pins every shipped dependency and helper input. `go.mod` and `go.sum`
are the authority for the host binary's Go module graph; isolated helper
modules carry their own module graph. `THIRD_PARTY_NOTICES.md` is the
human-readable direct-dependency license inventory.

## Operator console dependencies

| Component | Version | Purpose | License |
| --- | --- | --- | --- |
| `charm.land/bubbletea/v2` | `v2.0.8` | Terminal application runtime | MIT |
| `charm.land/bubbles/v2` | `v2.1.1` | Terminal controls and viewport | MIT |
| `charm.land/lipgloss/v2` | `v2.0.5` | Capability-aware terminal layout | MIT |

The domain projection and Manager authority remain Hideout-owned Go code.
These modules only render and operate the local terminal client.

## Workload observer dependency

| Component | Version | Purpose | License |
| --- | --- | --- | --- |
| `github.com/cilium/ebpf` | `v0.22.0` | Packaged eBPF loader | MIT |

The observer does not compile or download programs at runtime. The release
build compiles checked-in Hideout eBPF source into CO-RE object files and
embeds them in the fixed guest helper. The generated-artifact gate records and
verifies:

- source and generated-object SHA-256 values;
- compiler name, exact version, flags, architecture, and BPF target;
- helper module graph and license inventory;
- dual source SPDX declaration, exact GPL kernel-program declaration, and any
  included header provenance;
- absence of undeclared generated files;
- package manifest ownership and installed helper digest.

The Hideout-owned BPF source is offered under
`Apache-2.0 OR GPL-2.0-only`. The separately loaded kernel object selects the
GPL option because Linux marks required tracing helpers GPL-only; the Go
observer and the rest of Hideout remain Apache-2.0.
The corresponding license text is shipped at
`LICENSES/GPL-2.0-only.txt`.

`runtime/package-components.json` is the static package contract for the
observer and browser console. The observer's specialized helper manifest binds
the exact packaged executable digest, target, trusted builder identity, build
mode, package ownership, and userspace license. The generated browser asset
manifest binds every compiled HTML/CSS/JavaScript asset to the finalized
`bin/hideout` digest. Both manifests, the component contract, and the required
license text are checksummed again by the outer package manifest and verified
after installation.

The three generated manifests and the source/object/generated-Go digest and
license judge are active in `scripts/gates/dependencies.sh`. A local pass is
retained below `.artifacts/045/dependencies/`. The package/component manifest
lane is active; binary scanning, real-Lima, and exact-candidate aggregation
remain later release-candidate gates.

## Verification

The release candidate must run module verification, direct-license inventory
comparison, vulnerability/advisory scanning, generated-output comparison, and
package-manifest verification against the exact candidate commit. An advisory
may be described as unreachable only when a reproducible call-graph or
platform/build-constraint proof is attached; the dependency remains visible in
the inventory.

The current source scan retains one such module-only advisory:
`GO-2026-5932` for `golang.org/x/crypto`. The policy is deliberately narrower
than a general allowlist. On every run it verifies all of the following:

- the selected module is exactly `golang.org/x/crypto v0.54.0`;
- the authoritative OSV scope contains only
  `golang.org/x/crypto/openpgp` packages;
- the advisory still has no fixed version;
- the symbol scan contains no imported-package trace for the finding;
- every reachable/imported-package finding and every other module-only
  advisory fails the gate.

The gate self-test proves that a known reachable vulnerable symbol, an unknown
module-only record, and a forged `GO-2026-5932` exception are all rejected.
Final package construction must additionally scan every manifest-listed Go
binary with `govulncheck -mode=binary`; source reachability does not substitute
for that package proof.
