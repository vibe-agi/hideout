# Quickstart: Validate The Ordinary User Release

<!-- markdownlint-disable MD013 -->

This guide is a validation outline. Publication still requires the exact
release workflow, protected signing/notarization credentials, and public
receipt.

## 1. Local contracts

```bash
go test ./internal/app ./internal/doctor ./internal/supportreport ./internal/packagekit ./internal/helperbin
go test -race ./internal/app ./internal/doctor ./internal/supportreport ./internal/packagekit ./internal/helperbin
go run ./cmd/hideout help
go run ./cmd/hideout help --all
go run ./cmd/hideout setup --help
go run ./cmd/hideout doctor --help
go run ./cmd/hideout support report --help
```

Expected:

- primary help shows the first-run journey within 20 non-blank lines;
- expanded help retains every registered command;
- contextual help exits 0 and writes no store state.

## 2. Concise doctor and support report

```bash
export HIDEOUT_STORE_ROOT=/tmp/hideout-044-store
go run ./cmd/hideout doctor
go run ./cmd/hideout doctor --verbose
go run ./cmd/hideout doctor --format json >/tmp/hideout-044-doctor.json
go run ./cmd/hideout support report --out /tmp/hideout-044-support.json
go run ./cmd/hideout-schema-validate \
  schemas/support-report.schema.json \
  /tmp/hideout-044-support.json
```

Expected:

- concise doctor shows only readiness, boundary, actionable findings, and next
  command;
- verbose and JSON retain all findings;
- support output is mode `0600`, schema-valid, bounded, and contains no raw
  audit/workspace/secret data.

## 3. Package content

```bash
scripts/package-local.sh \
  --out /tmp/hideout-v0.1.0-alpha.3-darwin-arm64.tar.gz \
  --version 0.1.0-alpha.3 \
  --channel developer-preview \
  --tag v0.1.0-alpha.3 \
  --signing-mode developer-preview-unsigned
scripts/test-package-smoke.sh
scripts/test-vulnerability-gate.sh --self-test --source
```

Expected:

- package verification lists the Linux guest `tun2socks` helper and its
  manifest;
- third-party notices and upstream license are present;
- removing or modifying the helper makes verification fail;
- source/development override behavior remains explicitly labeled;
- generated archive docs name one candidate/archive without predicting a public
  URL or digest;
- the pinned Go 1.25.12 source scan has no imported-package or fixable module
  findings, the vulnerable fixture fires, and the sole no-fix `openpgp`
  database record is accepted only while its affected packages remain
  unimported.

The unsigned package is local evidence only and cannot satisfy public release
readiness.

## 4. Local aggregate

```bash
scripts/test-ordinary-user-release.sh --local-fast
scripts/test-gate0.sh
```

Expected:

- help, doctor, support, package, upgrade, uninstall, docs, schema, negative
  fixture, and mutation-proof checks pass;
- local-fast evidence remains non-promotable.

## 5. Exact-package real candidate

Freeze one clean public candidate, build it once, retain its identity, then use
the candidate orchestrator. It runs exact-package Gate 2, Gate 3, fully
executed UI E2E, the ordinary-user release lane, and aggregate readiness:

```bash
HIDEOUT_SECRET_DEFAULT_PROXY=socks5://127.0.0.1:1080 \
  scripts/test-public-alpha-candidate.sh \
    --tag v0.1.0-alpha.3 \
    --package /path/to/hideout-v0.1.0-alpha.3-darwin-arm64.tar.gz \
    --signing-observation /path/to/signing.json \
    --notarization-observation /path/to/notarization.json \
    --candidate-observation /path/to/candidate.json \
    --out /path/to/retained-candidate
```

Expected:

- direct first run, lifecycle, projection, required UI path, and privacy
  forwarding use the same package digest;
- all registered release-candidate proof producers run from this command;
- Gate 3 does not build or discover an ambient helper;
- all required evidence is passed, never `not-run`.

## 6. Publication chain

Run existing release readiness, signing, notarization, anonymous download, and
publication-receipt workflows against the retained candidate. Update release
inventory and synchronized documentation only from the validated receipt.

Publication is blocked until:

- the commit is clean, public, and pushed;
- CI and all required exact-package gates pass;
- signature and notarization observations match the retained bytes;
- public download identity matches;
- the publication receipt validates.
