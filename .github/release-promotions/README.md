# Public Alpha Release Governance

Hideout uses a build, retain, prove, approve, and promote sequence. A candidate
is not a public release, and source documentation must not claim that a public
package exists until a validated publication receipt updates
`releases/current.json` through review.

## Environments

`public-alpha-signing` contains the Apple credentials used only by the candidate
job:

- `APPLE_DEVELOPER_ID_P12_BASE64`
- `APPLE_DEVELOPER_ID_P12_PASSWORD`
- `APPLE_SIGNING_IDENTITY`
- `APPLE_NOTARY_KEY_ID`
- `APPLE_NOTARY_ISSUER_ID`
- `APPLE_NOTARY_KEY_P8_BASE64`

`public-alpha` protects publication. It has no Apple credentials, requires an
authenticated reviewer, and currently allows the sole maintainer to approve
their own deployment. Once a second release maintainer is active, repository
governance should enable prevent-self-review and require the other maintainer.

Both `public-alpha-signing` and `public-alpha` currently require the GitHub user
`xujinzheng` as a reviewer. The environment definitions contain no Apple
credentials until the six documented signing/notarization secrets are added.

Both workflows pin third-party actions by full commit SHA. Candidate failures
leave a private draft. Promotion recomputes the exact four-asset identity,
waits for approval, publishes once, and verifies every asset through anonymous
HTTPS before producing a receipt.

## Exact Public Assets

For version `<version>`, the release contains exactly:

```text
hideout-v<version>-darwin-arm64.tar.gz
hideout-v<version>-evidence.tar.gz
hideout-v<version>-release.json
SHA256SUMS
```

GitHub-generated source archives are not Hideout package assets. A publication
receipt is workflow evidence and is not appended to an immutable release. A
receipt-derived pull request updates `releases/current.json`, checks the
bounded receipt into `releases/receipts/<tag>.json`, and regenerates only the
marked public-release blocks. After that pull request lands,
`hideout-alpha-public-truth.yml` emits the distinct checked-in documentation
proof; a candidate or unmerged branch cannot emit it.

## Release Notes

Every public-alpha release note states:

- the supervised-alpha maturity and supported `macOS arm64 + Lima` scope;
- exact source commit, package SHA-256, runtime family/revision/digest, signing
  identity, and notarization status;
- that the roughly 1 GB runtime is downloaded separately on first use;
- migrations, removed package-owned files, and support changes;
- the direct first-success path, then the privacy and safe `code .` walkthrough;
- install, verification, recovery, issue, and private security-report links;
- non-claims from the support matrix; and
- Apache-2.0 coverage for Hideout code, with third-party terms described
  separately in `THIRD_PARTY_NOTICES.md`.

Release notes never describe a candidate, draft, unsigned preview, local test,
or unverified URL as publicly available.

## Promotion Requests

Promotion requests are reviewed JSON files named
`public-alpha-<version>.json`. They identify a retained draft and exact source
commit but carry no evidence authority. The promotion workflow re-downloads
and validates every referenced byte before approval.
