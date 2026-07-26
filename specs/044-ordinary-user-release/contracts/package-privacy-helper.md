# Contract: Package-Owned Privacy Helper

## Identity

```text
command: tun2socks
upstream: github.com/xjasonlyu/tun2socks/v2
version: v2.6.0
license: MIT
target: linux/<package guest architecture>
```

The source build is pinned by a dedicated `go.mod` and `go.sum`. The Hideout
package includes the upstream license text and records the component in
`THIRD_PARTY_NOTICES.md`.

## Package layout

```text
bin/tun2socks-linux-<arch>
bin/tun2socks-linux-<arch>.manifest.json
third_party/tun2socks/LICENSE
```

The package manifest records all three paths. The helper manifest records
command, upstream module/version, target OS/architecture, artifact name, and
SHA-256.

## Resolution

1. A declared explicit development override is validated and used only when
   valid.
2. An installed release resolves its package-owned helper.
3. A store helper may satisfy development/repair flows only with a matching
   helper manifest.
4. Ambient unrelated host `PATH` entries do not broaden the public package
   claim.

An invalid explicit override fails closed. Missing package helper, digest
mismatch, wrong target, directory, symlink, or non-executable artifact fails
before the target command starts.

## Runtime

The helper is copied into the existing session/environment network service
directory through the current Manager network helper path. Proxy credentials
remain in the existing host-only secret flow. The helper receives only the
existing redacted runtime configuration.

## Gates

- Gate 0: build pin, helper manifest, package manifest, license/notice, package
  verify, damage/removal/non-executable negative fixtures.
- Gate 3: exact-package helper provenance, TUN/default route, mediated DNS,
  gateway observation, upstream proxy forwarding, proxy-secret absence, cleanup,
  and no direct fallback.
- Mutation proof: change/remove packaged helper or manifest digest and observe
  package/privacy readiness fail.
