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
No public Hideout product package has been published. Matrix support levels do
not by themselves create a public-download or release-readiness claim.
<!-- hideout-public-release:end -->

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

## Feature And Gate Support

| Subject | Level | Required Gate |
| --- | --- | --- |
| `feature/dns-mediation` | gate-required | Gate 3 hidden proxy |
| `feature/hostfs-write-overlay` | gate-required | Gate 2 Lima |
| `feature/guest-privilege-separation` | gate-required | Gate 3 hidden proxy privilege evidence |
| `feature/adapter-pack-lifecycle` | supported | Gate 0 |
| `feature/decision-center` | supported | Gate 0 |
| `feature/doctor` | supported | Gate 0 |
| `feature/hostfs-discoverable-namespace` | gate-required | Gate 2 Lima |
| `feature/host-capability-projection` | gate-required | Gate 2 Lima |
| `feature/supported-cli-runtime` | gate-required | Gate 2 and Gate 3 |
| `feature/community-host-app-recipes` | gate-required | External-pack Gate 2 |
| `release/public-alpha-package` | gate-required | Gate 0, Gate 2, Gate 3, anonymous receipt |
| `release/developer-id-notarization` | gate-required | Developer ID and accepted notarization |
| `gate/release-candidate` | gate-required | Gate 2 and Gate 3 real evidence |

## Schema And ABI Support

| Subject | Level |
| --- | --- |
| `abi/command-adapter/v1` | supported |
| `abi/adapter-pack/v1` | supported |
| `schema/profile/v1` | supported |
| `schema/doctor-report/v1` | supported |
| `schema/export-artifact/v1` | supported |

## Required Non-Claims

- `guest-root-containment`: Hideout does not claim containment after the target obtains guest root.
- `workspace-write-blocking`: Hideout does not block or DLP-scan writes inside the mounted workspace.
- `native-isolation`: The native backend is a weak development harness, not isolation evidence.
- `marketplace-trust`: Local adapter packs are supported; public marketplace trust is not productized.
- `browser-security`: Hideout does not claim general browser exploit containment beyond typed host-open boundaries.
- `unsupported-platforms`: Unsupported platforms have no alpha release support promise.
- `public-alpha-maturity`: The public alpha does not claim GA stability, unattended operation, or automatic updates.
- `runtime-freshness`: The retained runtime has no automatic refresh or patch-response SLA.
- `privacy-prerequisites`: Privacy networking depends on an operator-provided proxy, mediated resolver, and real Gate 3 evidence.
- `ui-maturity`: The local TUI and WebUI are supervised alpha surfaces, not a polished remote operations service.

## Release Readiness

Local-fast readiness is development evidence only:

```bash
scripts/test-release-readiness.sh --local-fast --out /tmp/hideout-readiness.json
```

Release-candidate readiness requires real Gate 2 and Gate 3 evidence:

```bash
HIDEOUT_GATE2_EVIDENCE=/path/to/gate2.json \
HIDEOUT_GATE3_EVIDENCE=/path/to/gate3.json \
  scripts/test-release-readiness.sh --release-candidate --out /tmp/hideout-rc.json
```

If the real gate evidence is missing, release-candidate mode fails closed and
records the missing gates in a redacted `hideout.release-readiness/v1` artifact.
