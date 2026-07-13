# Contract: Package And Binary Identity

## Canonical Values

```text
productVersion = 0.1.0-alpha.1
tag            = v0.1.0-alpha.1
sourceCommit   = 40 lowercase hex characters
artifactSHA256 = 64 lowercase hex characters
target         = darwin/arm64
```

No field substitutes for another. Human output may show a short commit only in
addition to the full machine identity.

## `hideout version`

Human form remains stable and adds release channel/package context:

```text
hideout 0.1.0-alpha.1
commit: <40-hex>
builtAt: <RFC3339>
go: <version>
platform: darwin/arm64
channel: alpha
supportMatrix: hideout.support-matrix/v1 <version>
support: <summary>
```

Machine form:

```text
hideout version --json
```

returns one strict object:

```json
{
  "schema": "hideout.binary-identity/v1",
  "productVersion": "0.1.0-alpha.1",
  "sourceCommit": "<40-hex>",
  "builtAt": "<RFC3339>",
  "goVersion": "go1.25.0",
  "hostOS": "darwin",
  "hostArch": "arm64",
  "channel": "alpha",
  "supportMatrixVersion": "<version>"
}
```

Release builds reject `dev`, `unknown`, abbreviated commits, dirty source, a
tag/version mismatch, or an unsupported target. Development builds retain
their current explicit `dev/unknown` identity and cannot satisfy release
readiness.

## Package Manifest V1

Required release fragment:

```json
{
  "schema": "hideout.package-manifest/v1",
  "release": {
    "productVersion": "0.1.0-alpha.1",
    "channel": "alpha",
    "tag": "v0.1.0-alpha.1"
  },
  "source": {
    "repository": "https://github.com/vibe-agi/hideout",
    "commit": "<40-hex>",
    "dirty": false
  },
  "target": {
    "hostOS": "darwin",
    "hostArch": "arm64",
    "linuxGuestArch": "arm64"
  }
}
```

The outer archive digest is intentionally absent. The final package identity
is created by pairing a verified canonical package root with the caller-supplied
archive path and computing that path's SHA-256.

## Readiness CLI

Release-candidate mode requires the archive, not only an extracted root:

```text
hideout support readiness \
  --mode release-candidate \
  --package-artifact <exact.tar.gz> \
  --runtime-family developer-standard \
  --gate2-evidence <gate2.json> \
  --gate3-evidence <gate3.json> \
  --product-evidence <manifest>... \
  --signing-observation <signing.json> \
  --notarization-observation <notarization.json> \
  --out <readiness.json>
```

`--package-root` remains a local diagnostic input but cannot satisfy public
release readiness because it has no outer archive digest.

## Compatibility Start Point

- Pre-033 unpublished package/install-state shapes fail with typed
  rebuild/reinstall recovery and are not public compatibility inputs.
- Unknown, newer, or unsupported downgrade state fails closed before mutation.
- A later public alpha must test upgrade from the previous published alpha;
  the first alpha uses deterministic future/unknown/downgrade fixtures without
  pretending a private format is N-1.
