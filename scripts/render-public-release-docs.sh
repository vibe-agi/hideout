#!/usr/bin/env bash
set -euo pipefail

ROOT="${HIDEOUT_DOC_ROOT:-$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)}"
ROOT="$(cd "$ROOT" && pwd -P)"
cd "$ROOT"

inventory=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --inventory) inventory="${2:-}"; shift 2 ;;
    -h|--help)
      echo "usage: scripts/render-public-release-docs.sh --inventory <releases/current.json>"
      exit 0
      ;;
    *) echo "render-public-release-docs: unknown option: $1" >&2; exit 2 ;;
  esac
done

[ -f "$inventory" ] || { echo "render-public-release-docs: inventory is required" >&2; exit 2; }
jq -e '
  .schema == "hideout.published-release-inventory/v1" and
  .current != null and
  .current.maturity == "public-supervised-alpha" and
  .current.platform == "darwin/arm64" and
  .current.backend == "lima" and
  (.current.package.artifactSHA256 | test("^[0-9a-f]{64}$"))
' "$inventory" >/dev/null

version="$(jq -r '.current.version' "$inventory")"
tag="$(jq -r '.current.tag' "$inventory")"
release_url="$(jq -r '.current.releaseURL' "$inventory")"
package_sha="$(jq -r '.current.package.artifactSHA256' "$inventory")"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-release-docs.XXXXXX")"
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT

replace_block() {
  local file="$1" content="$2" start='<!-- hideout-public-release:start -->' end='<!-- hideout-public-release:end -->'
  [ "$(grep -Fxc "$start" "$file")" -eq 1 ] || { echo "render-public-release-docs: missing unique start marker in $file" >&2; exit 1; }
  [ "$(grep -Fxc "$end" "$file")" -eq 1 ] || { echo "render-public-release-docs: missing unique end marker in $file" >&2; exit 1; }
  awk -v start="$start" -v end="$end" -v content="$content" '
    $0 == start {
      print
      while ((getline line < content) > 0) print line
      close(content)
      replacing = 1
      next
    }
    $0 == end { replacing = 0; print; next }
    !replacing { print }
  ' "$file" >"$tmp/rendered"
  mv "$tmp/rendered" "$file"
}

cat >"$tmp/readme-en" <<EOF
Current release: [Hideout ${tag}](${release_url}) for macOS arm64. This is a
public supervised alpha, not a GA or Linux-package claim.

Package SHA-256: \`${package_sha}\`. The release page includes checksums,
the machine-readable release manifest, and bounded verification evidence.
EOF

cat >"$tmp/readme-zh" <<EOF
当前版本：[Hideout ${tag}](${release_url})，支持 macOS arm64。这是需要有人监督的
公开 alpha，不是 GA 或 Linux 安装包承诺。

安装包 SHA-256：\`${package_sha}\`。Release 页面同时提供 checksum、机器可读
release manifest 和有界验证证据。
EOF

cat >"$tmp/status" <<EOF
Current release state: public supervised alpha \`${tag}\` for macOS arm64 with
the Lima backend. Source-of-truth identity and receipt digest are in
\`releases/current.json\`; the public release is ${release_url}.

This status does not add GA, automatic-update, Linux-package, workspace-DLP,
guest-root-containment, or marketplace-trust claims. Real isolation claims
remain bound to the release's retained Gate 2 and Gate 3 evidence.
EOF

cat >"$tmp/support" <<EOF
Current published package: [${tag}](${release_url}), public supervised alpha for
\`darwin/arm64\` with \`backend/lima\`. The release package SHA-256 is
\`${package_sha}\`; \`releases/current.json\` is the machine-readable source.
EOF

cat >"$tmp/changelog" <<EOF
## ${version}

- Publish the first public supervised macOS arm64 alpha package at
  [${tag}](${release_url}).
- Bind package, runtime, signing, notarization, Gate 2, Gate 3, and anonymous
  download evidence to one immutable release identity.
- Keep automatic updates, Linux packages, GA stability, workspace DLP,
  guest-root containment, and marketplace trust outside this release claim.
EOF

replace_block README.md "$tmp/readme-en"
replace_block README.zh-CN.md "$tmp/readme-zh"
replace_block docs/STATUS.md "$tmp/status"
replace_block docs/support-matrix.md "$tmp/support"
replace_block CHANGELOG.md "$tmp/changelog"

echo "render-public-release-docs: rendered $tag"
