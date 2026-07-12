# Developer Standard Runtime Build

This directory owns inputs and tooling for the separately published
`developer-standard` Linux aarch64 QCOW2. It is not invoked by `hideout run`.

The tracked source lock binds the Debian snapshot metadata, exact top-level
package versions, builder identity, base image, Node.js, Go, and output shape.
It does not contain a promoted output digest. Every local or CI output remains
a development candidate and cannot satisfy 031 preview evidence.

Required build host: native Linux aarch64 with `apt-get`, `dpkg-deb`, `xz`,
`tar`, `qemu-img`, `virt-resize`, and `virt-customize`. The builder resolves
the complete Debian package closure from the locked `Packages.xz`, downloads
payloads outside the image, and verifies every payload's digest and size
against that Release-bound index. Image customization runs with networking
disabled and installs only the verified local package closure.

The locked builder package set includes its own arm64 kernel. The build starts
with a real libguestfs appliance preflight and fails before package resolution
when that appliance cannot launch. The retained workflow runs in the locked
privileged builder; local hosts may need an equivalent privileged environment
rather than a late, partial build. The workflow installs the runner trust bundle
at a fixed path and configures APT to use that path explicitly; TLS verification
remains mandatory.

The final scrub removes credential/cache state and any file containing an
anchored private-key block, including public example keys shipped in package
documentation. The offline verifier repeats that content scan while allowing
ordinary parser binaries that merely contain a non-line-anchored marker
string.

Validate inputs without downloading or building:

```sh
runtime/developer-standard/build.sh --validate-inputs-only
runtime/developer-standard/test-build.sh
```

Build a non-promoted candidate on the required host:

```sh
runtime/developer-standard/build.sh \
  --out-dir dist/runtime/developer-standard-2026.07.0
```

Dirty builds require `--allow-dirty`; their provenance records `dirty=true`
and `builder.attestation=native-unpinned`, and they are never promotion inputs.
The candidate workflow passes the locked builder identity, but final promotion
still requires retained CI provenance rather than trusting that declaration
alone. `build.sh` emits the QCOW2, checksums,
package inventory, component/SBOM status, build provenance, and offline
verification report. Clean Lima boot, retained upload, clean re-download, and
real Gate 2/Gate 3 remain separate mandatory promotion steps.
