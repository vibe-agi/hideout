# Hideout Alpha Support Matrix

<!-- markdownlint-disable MD013 -->

This document mirrors the Go-owned matrix exposed by:

```bash
hideout support matrix
hideout support matrix --json
```

Machine-readable schema: `hideout.support-matrix/v1`

Matrix version: `2026-07-alpha`

The Go source in `internal/releasecompat` is authoritative. This Markdown file
exists so operators can inspect the early-alpha contract without reading code.

<!-- hideout-public-release:start -->
Current published package: [v0.1.0-alpha.3](https://github.com/vibe-agi/hideout/releases/tag/v0.1.0-alpha.3), public supervised alpha for
`darwin/arm64` with `backend/lima`. The release package SHA-256 is
`61807ce60d7a037139713cffe475f492ee8e60cced56674ba3f0be0580e65050`; `releases/current.json` is the machine-readable source.
<!-- hideout-public-release:end -->

The entries below describe the current source contract. A gate-required or
implemented source entry does not retrofit a capability into alpha.3:
`helper/linux-observer` and the Feature 045 workload-observation entries apply
only to an exact later candidate that carries and passes their required gates.

Supported macOS installation uses the official Homebrew tap:

```bash
brew install vibe-agi/tap/hideout
```

The formula consumes the same published package and does not broaden the
platform, backend, maturity, or automatic-update claims below.

## Platform Support

| Subject | Level | Guidance |
| --- | --- | --- |
| `platform/darwin/arm64` | first-class | Use Lima for isolation claims and release candidates. |
| `platform/linux/amd64` | supported | Supported with narrower smoke coverage; run local checks on the target host. |
| `platform/linux/arm64` | supported | Supported with narrower smoke coverage; run local checks on the target host. |
| `platform/windows` | unsupported | No Windows backend or package path is productized. |

## Backend Support

| Subject | Level | Guidance |
| --- | --- | --- |
| `backend/lima` | first-class | Required for release isolation evidence. |
| `backend/native` | degraded | Development harness only; not isolation evidence. |

## Runtime Helper Support

| Subject | Level | Required Gate |
| --- | --- | --- |
| `helper/linux-shim` | supported | Gate 0 package smoke |
| `helper/linux-hostfsd` | supported | Gate 0 package smoke |
| `helper/linux-dns-stub` | supported | Gate 3 hidden proxy |
| `helper/linux-session-supervisor` | supported | Gate 0 package smoke and 034 real Lima Gate 2 |
| `helper/linux-observer` | supported | Gate 0 package smoke and exact 045 real Lima observation |
| `helper/linux-workspace-portal` | supported | Gate 0 package smoke and 035 real Lima Gate 2 |
| `helper/linux-tun2socks-v2.6.0` | supported | Package checksum/provenance and exact-package Gate 3 |

## Feature And Gate Support

| Subject | Level | Required Gate |
| --- | --- | --- |
| `feature/package-install` | supported | Gate 0 |
| `feature/workload-observation-process` | gate-required | Exact 045 real Lima observation; require an `Available` interval for the selected run |
| `feature/workload-observation-file` | degraded | Exact 045 real Lima observation; current reference completeness is `Partial` |
| `feature/workload-observation-network` | degraded | Exact 045 real Lima observation; current reference route attribution is `Partial` |
| `feature/workload-observation-dns` | degraded | Exact 045 real Lima observation; encrypted/cached/ambiguous names remain `Partial` or unknown |
| `feature/dns-mediation` | gate-required | Gate 3 hidden proxy |
| `feature/hostfs-write-overlay` | gate-required | Gate 2 Lima |
| `feature/guest-privilege-separation` | gate-required | Gate 3 hidden proxy privilege evidence |
| `feature/adapter-pack-lifecycle` | supported | Gate 0 |
| `feature/decision-center` | supported | Gate 0 |
| `feature/doctor` | supported | Gate 0 |
| `feature/support-report` | supported | Gate 0 bounded redaction and safe-write fixtures |
| `feature/hostfs-discoverable-namespace` | gate-required | Gate 2 Lima |
| `feature/host-capability-projection` | gate-required | Gate 2 Lima |
| `feature/supported-cli-runtime` | gate-required | Gate 2 and Gate 3 |
| `feature/community-host-app-recipes` | gate-required | External-pack Gate 2 |
| `feature/concurrent-run-sessions` | gate-required | 034 real Lima Gate 2 |
| `feature/shared-default-vm-cross-workspace` | gate-required | 035 clean real macOS arm64 Lima behavior and performance evidence |
| `feature/zero-friction-setup` | gate-required | 038 packaged PTY plus real macOS arm64 Lima first-run and agent evidence |
| `release/public-alpha-package` | gate-required | Gate 0, Gate 2, Gate 3, anonymous receipt |
| `release/developer-id-notarization` | gate-required | Developer ID and accepted notarization |
| `gate/release-candidate` | gate-required | Clean pushed package, Gate 2, Gate 3, fully executed UI, signing, notarization, and aggregate readiness |

## Workload Observation Coverage

Support level and runtime coverage are different. The table above says whether
a product path is supported or gate-required; every individual run still
reports independent `Available`, `Partial`, or `Unavailable` intervals. The
runtime interval, reason, generation, loss count, and time range are the
authority for that run.

| Subsystem | Current macOS arm64 + Lima/Debian reference | What is recorded | Important limit |
| --- | --- | --- | --- |
| Process | `Available` after the cgroup-v2 boundary and observer are proved | Top-level command and attributable descendants, execution ancestry, bounded argv/cwd, time, and exit | PID alone is never identity; a gap, drop, restart, boundary loss, or target exit closes availability. |
| File | `Partial` | Attributable open/read/write/mmap/create/truncate/rename/unlink/metadata facts, normalized path/identity, counts, bytes when known, and time | Current hook/path outcome coverage is not complete. No file content is captured, and an empty result is not proof of no access. |
| Network | `Partial` | Attributable TCP/UDP destination IP, port, transport, and route/correlation evidence when known | Current reference route attribution is incomplete. No payload is captured; proxy mediation and shared endpoints can reduce attribution. |
| DNS | `Partial` | Plaintext query/response metadata and execution/domain correlation when supported | Hideout does not decrypt encrypted DNS; caches, shared IPs, literals, and external resolvers can make the domain unknown. |

The native backend has no supported cgroup/eBPF workload boundary and reports
these subsystems `Unavailable`. A terminal run may show a historical
`Available` or `Partial` interval followed by current `Unavailable
(target-exited)`; that does not erase the historical evidence. See
[activity-observation.md](activity-observation.md) for retention and risk
defaults.

## Schema And ABI Support

| Subject | Level |
| --- | --- |
| `abi/command-adapter/v1` | supported |
| `abi/adapter-pack/v1` | supported |
| `schema/profile/v1` | supported |
| `schema/doctor-report/v1` | supported |
| `schema/export-artifact/v1` | supported |

## Required Non-Claims

- `guest-root-containment`: Hideout does not claim containment after the target obtains guest root; guest root can tamper with the workload boundary or observer, so affected coverage must degrade.
- `workspace-write-blocking`: Hideout does not block or DLP-scan writes inside the mounted workspace.
- `native-isolation`: The native backend is a weak development harness, not isolation evidence.
- `marketplace-trust`: Local adapter packs are supported; public marketplace trust is not productized.
- `browser-security`: Hideout does not claim general browser exploit containment beyond typed host-open boundaries.
- `unsupported-platforms`: Unsupported platforms have no alpha release support promise.
- `public-alpha-maturity`: The public alpha does not claim GA stability, unattended operation, or automatic updates.
- `runtime-freshness`: The retained runtime has no automatic refresh or patch-response SLA.
- `privacy-prerequisites`: Privacy networking depends on an operator-provided proxy, mediated resolver, and real Gate 3 evidence.
- `ui-maturity`: The local TUI and WebUI are supervised alpha surfaces, not a polished remote operations service.
- `cross-workspace-shared-vm`: Compatible automatic workspaces share one guest kernel. Ordinary session/view isolation is not a VM wall and does not contain guest root; use a dedicated named environment, or a cloned profile plus a dedicated environment, for a separate trust domain.
- `automatic-stop-cleanup`: Automatic final-session stop is non-destructive and does not clean or delete retained state.
- `terminal-emulator-hardening`: Initial dimensions and dynamic SIGWINCH resize are supported; exhaustive terminal-emulator, theme, OSC/CSI, and detach behavior is not claimed.

## Release Readiness

Local-fast readiness is development evidence only:

```bash
scripts/test-release-readiness.sh --local-fast --out /tmp/hideout-readiness.json
```

Release-candidate readiness requires the exact archive, independent signing and
notarization observations, every registered product-evidence manifest, and real
Gate 2 and Gate 3 evidence:

```bash
HIDEOUT_GATE2_EVIDENCE=/path/to/gate2.json \
HIDEOUT_GATE3_EVIDENCE=/path/to/gate3.json \
  scripts/test-release-readiness.sh --release-candidate \
    --package-artifact /path/to/hideout-vX.Y.Z-darwin-arm64.tar.gz \
    --signing-observation /path/to/signing.json \
    --notarization-observation /path/to/notarization.json \
    --product-evidence /path/to/product-hardening.json \
    --out /tmp/hideout-rc.json
```

Repeat `--product-evidence` for every retained manifest. The canonical
`scripts/test-public-alpha-candidate.sh` orchestration supplies that complete
set and additionally requires `scripts/test-ui-e2e.sh --all
--require-executed`. A dirty checkout, package commit not equal to `HEAD`,
commit not reachable from `origin/master`, changed archive bytes, missing
observation, UI `not-run`, or failed/missing gate fails closed.
