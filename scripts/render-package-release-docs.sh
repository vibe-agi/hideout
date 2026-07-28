#!/usr/bin/env bash
set -euo pipefail

package_root=""
version=""
tag=""
channel=""
archive=""

usage() {
  echo "usage: $0 --package-root <root> --version <version> --tag <tag> --channel <channel> --archive <name>" >&2
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --package-root) package_root="${2:-}"; shift 2 ;;
    --version) version="${2:-}"; shift 2 ;;
    --tag) tag="${2:-}"; shift 2 ;;
    --channel) channel="${2:-}"; shift 2 ;;
    --archive) archive="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage; exit 2 ;;
  esac
done

if [ ! -d "$package_root" ] || [ -z "$version" ] || [ "$tag" != "v$version" ] ||
  [ -z "$channel" ] || [ -z "$archive" ] || [ "$(basename "$archive")" != "$archive" ]; then
  usage
  exit 2
fi

package_root="$(cd "$package_root" && pwd -P)"
identity="hideout-package-candidate: version=$version tag=$tag archive=$archive"

cat >"$package_root/README.md" <<EOF
# Hideout Package Candidate

<!-- markdownlint-disable MD013 -->

Hideout runs AI agents and untrusted CLIs in a local Linux VM while keeping
host access explicit, mediated, and auditable.

<!-- hideout-public-release:start -->
<!-- $identity -->
Package candidate: \`${tag}\` for macOS arm64 with the Lima backend.
Archive: \`${archive}\`.

This archive describes its own candidate bytes. It does not claim that a
GitHub release or Homebrew formula has been published. Verify
\`package-manifest.json\` before installation; publication metadata and the
archive SHA-256 are rendered only from a validated public receipt.
<!-- hideout-public-release:end -->

## Install This Archive

From the extracted \`hideout/\` directory:

\`\`\`bash
./install.sh \\
  --prefix "\$HOME/.local" \\
  --store "\$HOME/.hideout" \\
  --skip-init
export PATH="\$HOME/.local/bin:\$PATH"
hideout setup
hideout doctor
\`\`\`

Setup writes configuration only. It does not start a VM or download the
retained runtime. The first run downloads that runtime separately; expect
approximately 1 GB.

## First Result

\`\`\`bash
cd /path/to/project
hideout run -- git status --short
\`\`\`

Direct networking is the default and exposes the normal network origin.
Privacy networking is a separate fail-closed posture requiring the packaged
helper, an operator-owned proxy secret, mediated DNS, and real privacy proof.

## Support And Removal

\`\`\`bash
hideout support report --out ./hideout-support.json
hideout help update
hideout help uninstall
\`\`\`

The support report is local-only and must be inspected before sharing. Upgrade
and normal uninstall preserve durable state under \`~/.hideout\`. Purge is
separate, previewed, and requires exact-store confirmation.

See [first-run-alpha.md](docs/first-run-alpha.md),
[distribution-bootstrap.md](docs/distribution-bootstrap.md), and
[support-matrix.md](docs/support-matrix.md).
EOF

cat >"$package_root/README.zh-CN.md" <<EOF
# Hideout 安装包候选版本

<!-- markdownlint-disable MD013 -->

Hideout 在本地 Linux VM 中运行 AI agent 和不受信任的 CLI，并让主机访问保持
显式、受控且可审计。

<!-- hideout-public-release:start -->
<!-- $identity -->
安装包候选版本：\`${tag}\`，适用于采用 Lima backend 的 macOS arm64。
归档文件：\`${archive}\`。

本归档只描述自身的候选字节，不声称 GitHub Release 或 Homebrew formula 已经发布。
安装前请验证 \`package-manifest.json\`；发布元数据和归档 SHA-256 只会根据验证通过的
公开发布回执生成。
<!-- hideout-public-release:end -->

## 安装本归档

在解压后的 \`hideout/\` 目录执行：

\`\`\`bash
./install.sh \\
  --prefix "\$HOME/.local" \\
  --store "\$HOME/.hideout" \\
  --skip-init
export PATH="\$HOME/.local/bin:\$PATH"
hideout setup
hideout doctor
\`\`\`

Setup 只写入配置，不启动 VM，也不下载保留的 runtime。第一次运行时会另行下载
runtime，大小约 1 GB。

## 第一个结果

\`\`\`bash
cd /path/to/project
hideout run -- git status --short
\`\`\`

默认使用 direct 网络，因此会暴露正常的网络来源。Privacy 网络是独立的
fail-closed posture，必须具备安装包 helper、operator 管理的代理 secret、
mediated DNS 和真实 privacy proof。

## 支持与卸载

\`\`\`bash
hideout support report --out ./hideout-support.json
hideout help update
hideout help uninstall
\`\`\`

Support report 只在本地生成，分享前必须自行检查。升级和普通卸载会保留
\`~/.hideout\` 下的持久数据；purge 是独立、可预览且要求完整 store 路径确认的操作。

能力和边界以 [English README](README.md)、
[first-run-alpha.md](docs/first-run-alpha.md)、
[distribution-bootstrap.md](docs/distribution-bootstrap.md) 和
[support-matrix.md](docs/support-matrix.md) 为准。
EOF

cat >"$package_root/docs/STATUS.md" <<EOF
# Hideout Package Candidate Status

<!-- markdownlint-disable MD013 -->

<!-- hideout-public-release:start -->
<!-- $identity -->
This archive contains Hideout \`${tag}\` for macOS arm64 with the Lima backend.
Archive: \`${archive}\`.

It is a publication-neutral candidate: inclusion in this archive does not prove
that GitHub assets or a Homebrew formula are public. Release identity, signing,
notarization, evidence, anonymous download, and the archive digest remain bound
to the later validated publication receipt.
<!-- hideout-public-release:end -->

## Supported Journey

1. Verify \`package-manifest.json\`.
2. Install with \`./install.sh --prefix <prefix> --store <store> --skip-init\`.
3. Run \`hideout setup\`, then \`hideout doctor\`.
4. From a project, run \`hideout run -- git status --short\`.

Direct networking is the default and exposes the normal network origin.
Privacy networking fails closed unless the packaged helper, operator-owned
proxy secret, mediated resolver, and real privacy evidence are all present.
Upgrade and normal uninstall preserve durable user state; purge is separate,
explicit, previewed, and bounded to the selected Hideout store.

## Non-Claims

This candidate is not GA and does not claim automatic updates, Linux packages,
workspace DLP, guest-root containment, or marketplace trust.
EOF

cat >"$package_root/docs/support-matrix.md" <<EOF
# Hideout Package Candidate Support Matrix

<!-- markdownlint-disable MD013 -->

<!-- hideout-public-release:start -->
<!-- $identity -->
This package candidate is \`${tag}\` for \`darwin/arm64\` with
\`backend/lima\`. Archive: \`${archive}\`.

Candidate status is not publication status.
<!-- hideout-public-release:end -->

| Subject | Level | Guidance |
| --- | --- | --- |
| \`platform/darwin/arm64\` | first-class | Use the archive-local installer and Lima. |
| \`backend/lima\` | first-class | Required for product isolation claims. |
| \`backend/native\` | degraded | Development harness only; not isolation evidence. |
| Linux packages | unsupported | Linux guest helpers are package internals, not host packages. |
| Windows | unsupported | No supported backend or package path. |

Direct networking is the default and exposes the normal network origin.
Privacy networking requires the packaged helper, an operator-owned proxy
secret, mediated DNS, and real Gate 3 evidence. The selected workspace remains
visible and writable to the target. This candidate does not claim GA stability,
automatic updates, workspace DLP, guest-root containment, or marketplace trust.

Upgrade and normal uninstall preserve durable user state. Purge remains a
separate, previewed, exact-store operation.
EOF

cat >"$package_root/CHANGELOG.md" <<EOF
# Changelog

<!-- markdownlint-disable MD013 -->

<!-- hideout-public-release:start -->
<!-- $identity -->
## ${version} candidate

- Candidate archive: \`${archive}\`.
- Converge the ordinary-user setup, help, doctor, support, privacy-helper,
  package lifecycle, and exact-release evidence paths.
- Keep direct networking as the explicit default; privacy mode remains
  prerequisite-bound and fail-closed.
- Preserve durable user state during upgrade and normal uninstall; keep purge
  separate and explicit.
- Publication, archive SHA-256, signing, notarization, and anonymous-download
  claims are intentionally absent until receipt validation.
<!-- hideout-public-release:end -->
EOF

cat >"$package_root/RELEASE_NOTES.md" <<EOF
# Hideout ${tag} Candidate Release Notes

<!-- markdownlint-disable MD013 -->

<!-- $identity -->

Archive: \`${archive}\`

This is a publication-neutral macOS arm64 supervised-alpha candidate using the
Lima backend. It is not a public-release claim.

The primary journey is \`hideout setup\`, \`hideout doctor\`, then
\`hideout run -- <command>\` from a project. Direct networking is the default
and exposes the normal network origin. Privacy networking requires the packaged
helper, an operator-owned proxy secret, mediated DNS, and real Gate 3 evidence;
missing prerequisites fail closed.

Upgrade and normal uninstall preserve durable user state. Purge remains a
separate, previewed, exact-store operation. Verify \`package-manifest.json\`
before installation. Obtain the final archive SHA-256 and public download
location only from the validated publication inventory.
EOF

cat >"$package_root/docs/first-run-alpha.md" <<EOF
# First Run From Hideout ${tag}

<!-- markdownlint-disable MD013 -->

<!-- $identity -->

Archive: \`${archive}\`

This guide applies to the candidate archive that contains it. It does not
install from, or claim publication in, another release channel.

## Prerequisites

- macOS arm64;
- Lima installed from a trusted source and available as \`limactl\`;
- the candidate archive verified and installed with its archive-local
  \`install.sh\`; and
- a dedicated project directory for the first run.

Verify the installed package and configuration before starting a VM:

\`\`\`bash
hideout package verify "\$HOME/.local"
hideout setup
hideout doctor
\`\`\`

\`hideout setup\` writes configuration only. The first run downloads the
retained runtime separately; expect approximately 1 GB.

## First Result

\`\`\`bash
cd /path/to/project
hideout run -- git status --short
\`\`\`

The selected workspace is visible and writable to the target. Direct networking
is the default and exposes the normal network origin. Audit output describes
the applied boundary but is not proof of GA stability or complete containment.

## Privacy Follow-Up

Privacy networking is a distinct fail-closed posture. Do not treat direct
networking as private. Before selecting privacy mode, configure the packaged
helper, an operator-owned proxy secret, mediated DNS, and the required real
privacy evidence. Missing prerequisites must block the run.

## Recovery And Support

\`\`\`bash
hideout doctor --verbose
hideout support report --out ./hideout-support.json
hideout help update
hideout help uninstall
\`\`\`

Inspect a support report before sharing it. Upgrade and normal uninstall
preserve durable state under \`~/.hideout\`; purge is separate, previewed, and
requires exact-store confirmation.
EOF

cat >"$package_root/docs/distribution-bootstrap.md" <<EOF
# Candidate Distribution And Bootstrap

<!-- markdownlint-disable MD013 -->

<!-- $identity -->

Candidate: \`${tag}\`

Archive: \`${archive}\`

This document installs the archive that contains it. It intentionally does not
name a public release URL, public digest, or package-manager formula.

## Verify And Extract

\`\`\`bash
test -f "${archive}"
tar -xzf "${archive}"
cd hideout
./bin/hideout package verify .
\`\`\`

## Install

\`\`\`bash
./install.sh \\
  --prefix "\$HOME/.local" \\
  --store "\$HOME/.hideout" \\
  --skip-init
export PATH="\$HOME/.local/bin:\$PATH"
hideout setup
hideout doctor
\`\`\`

Installation does not initialize a profile, start a VM, or download the
runtime. \`hideout setup\` performs the reviewed configuration step.

## Verify, Repair, Upgrade, And Remove

\`\`\`bash
hideout package verify "\$HOME/.local"
hideout package repair --dry-run "\$HOME/.local"
hideout help update
hideout help uninstall
\`\`\`

For an upgrade, verify and run the newer archive's installer against the same
prefix and store. Normal upgrade and uninstall preserve durable state and
unrelated files. Purge is separate, must be previewed, and requires the exact
selected store path.

## Publication Boundary

Do not infer publication from this archive. Obtain the final download location,
archive SHA-256, signing/notarization observations, and release inventory only
from a validated public receipt.
EOF

cat >"$package_root/packaging/homebrew/hideout.rb" <<EOF
# Candidate-bound reference only; this is not an installable Homebrew formula.
# Promotion renders the official tap URL and SHA-256 from a validated receipt.
# $identity
class Hideout < Formula
  desc "Run AI agents and untrusted CLIs in a local VM"
  homepage "https://github.com/vibe-agi/hideout"
  license "Apache-2.0"

  depends_on arch: :arm64
  depends_on "lima"
  depends_on :macos

  def caveats
    <<~EOS
      Candidate ${tag} archive: ${archive}

      This reference is intentionally not installable. Use the official tap
      only after publication has rendered and validated its exact URL and
      SHA-256. Setup does not start a VM or download the retained runtime.
      Upgrade and normal uninstall preserve ~/.hideout; purge is separate.
    EOS
  end
end
EOF

echo "render-package-release-docs: rendered $tag for $archive"
