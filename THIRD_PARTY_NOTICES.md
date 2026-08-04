# Third-Party Notices

Hideout is licensed under Apache-2.0. Its binaries incorporate the following
direct Go dependencies. Their source repositories contain the authoritative
license text and copyright notices.

| Component | Version | License |
| --- | --- | --- |
| `charm.land/bubbles/v2` | `v2.1.1` | MIT |
| `charm.land/bubbletea/v2` | `v2.0.8` | MIT |
| `charm.land/lipgloss/v2` | `v2.0.5` | MIT |
| `github.com/Masterminds/semver/v3` | `v3.2.1` | MIT |
| `github.com/charmbracelet/x/ansi` | `v0.11.7` | MIT |
| `github.com/cilium/ebpf` | `v0.22.0` | MIT |
| `github.com/Code-Hex/vz/v3` | `v3.7.1` | MIT |
| `github.com/creack/pty` | `v1.1.24` | MIT |
| `github.com/dop251/goja` | `v0.0.0-20250630131328-58d95d85e994` | MIT |
| `github.com/fsnotify/fsnotify` | `v1.10.1` | BSD-3-Clause |
| `github.com/hanwen/go-fuse/v2` | `v2.10.1` | BSD-3-Clause |
| `github.com/klauspost/compress` | `v1.18.7` | BSD-3-Clause |
| `github.com/santhosh-tekuri/jsonschema/v6` | `v6.0.2` | Apache-2.0 |
| `golang.org/x/crypto` | `v0.54.0` | BSD-3-Clause |
| `golang.org/x/sys` | `v0.47.0` | BSD-3-Clause |
| `golang.org/x/term` | `v0.45.0` | BSD-3-Clause |
| `gopkg.in/yaml.v3` | `v3.0.1` | MIT and Apache-2.0 |

The complete, machine-resolved dependency graph is recorded by `go.mod` and
`go.sum`. Indirect dependencies retain their respective upstream licenses.

The package also contains a separately built Linux guest-network helper with
its own isolated dependency graph:

| Component | Version | License | Build graph |
| --- | --- | --- | --- |
| `github.com/xjasonlyu/tun2socks/v2` | `v2.6.0` | MIT | isolated module |

The upstream license is redistributed at
`third_party/tun2socks/LICENSE`. The helper manifest records its module,
version, target, build mode, package ownership, and artifact SHA-256.

The macOS arm64 migration adoption executor uses
`github.com/Code-Hex/vz/v3` v3.7.1 to construct a dedicated
Virtualization.framework VM with zero network devices. The upstream MIT
license is redistributed at `third_party/vz/LICENSE`; the executor manifest
binds the module version, target, build mode, package ownership, and executable
SHA-256.

The package will also contain a Linux workload-observer helper and embedded
eBPF object files generated from Hideout-owned source. The generated objects
are product artifacts rather than separately sourced dependencies. Their
source digest, compiler identity, target, source license, kernel-program
license declaration, and object SHA-256 are recorded in the checked-in
generated-artifact manifests. The packaged helper manifest separately binds
the exact observer binary SHA-256, build mode, target, package ownership, and
Apache-2.0 userspace license; the package-component contract binds both layers
and the redistributed license text. The Hideout-owned BPF source is offered under
`Apache-2.0 OR GPL-2.0-only`; the object selects the GPL option when loaded
because Linux marks required tracing helpers GPL-only. This does not change
the Apache-2.0 license of the Go helper or the rest of Hideout.
`github.com/cilium/ebpf`, listed above, is the MIT-licensed Go loader used by
that helper. The GPL-2.0-only text is included at
`LICENSES/GPL-2.0-only.txt`.

The separately downloaded `developer-standard` runtime image is not embedded
in the Hideout product archive. Its pinned base image, source inputs, package
inventory, and review status are recorded under `runtime/developer-standard/`
and in `internal/runtimecatalog/catalog.json`.

Hideout may invoke operator-installed tools such as Lima and Git; those tools
are not redistributed in the product archive. The Linux guest `tun2socks`
helper is redistributed as the attributed package-owned component above.
