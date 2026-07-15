# Third-Party Notices

Hideout is licensed under Apache-2.0. Its binaries incorporate the following
direct Go dependencies. Their source repositories contain the authoritative
license text and copyright notices.

| Component | Version | License |
| --- | --- | --- |
| `github.com/Masterminds/semver/v3` | `v3.2.1` | MIT |
| `github.com/dop251/goja` | `58d95d85e994` | MIT |
| `github.com/hanwen/go-fuse/v2` | `v2.10.1` | BSD-3-Clause |
| `github.com/santhosh-tekuri/jsonschema/v6` | `v6.0.2` | Apache-2.0 |
| `golang.org/x/crypto` | `v0.53.0` | BSD-3-Clause |
| `golang.org/x/sys` | `v0.46.0` | BSD-3-Clause |
| `golang.org/x/term` | `v0.44.0` | BSD-3-Clause |
| `gopkg.in/yaml.v3` | `v3.0.1` | MIT and Apache-2.0 |

The complete, machine-resolved dependency graph is recorded by `go.mod` and
`go.sum`. Indirect dependencies retain their respective upstream licenses.

The separately downloaded `developer-standard` runtime image is not embedded
in the Hideout product archive. Its pinned base image, source inputs, package
inventory, and review status are recorded under `runtime/developer-standard/`
and in `internal/runtimecatalog/catalog.json`.

Hideout may invoke operator-installed tools such as Lima, Git, and
`tun2socks`; those tools are not redistributed in the product archive.
