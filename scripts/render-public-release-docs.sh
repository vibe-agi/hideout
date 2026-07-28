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
  .current.tag == ("v" + .current.version) and
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
\`releases/current.json\`; see the [public release](${release_url}).

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

- Publish the public supervised macOS arm64 alpha package at
  [${tag}](${release_url}).
- Bind package, runtime, signing, notarization, Gate 2, Gate 3, and anonymous
  download evidence to one immutable release identity.
- Keep automatic updates, Linux packages, GA stability, workspace DLP,
  guest-root containment, and marketplace trust outside this release claim.
EOF

history_count=0
while IFS= read -r receipt; do
  receipt_tag="$(jq -er '.tag' "$receipt")"
  if [ "$receipt_tag" = "$tag" ]; then
    continue
  fi
  jq -e '
    .schema == "hideout.publication-receipt/v1" and
    .status == "public-verified" and .immutable == true and
    .tag == ("v" + .version) and
    (.package.artifactSHA256 | test("^[0-9a-f]{64}$"))
  ' "$receipt" >/dev/null
  if [ "$history_count" -eq 0 ]; then
    printf '\n## Published history\n' >>"$tmp/changelog"
  fi
  receipt_version="$(jq -er '.version' "$receipt")"
  receipt_url="$(jq -er '.url' "$receipt")"
  receipt_sha="$(jq -er '.package.artifactSHA256' "$receipt")"
  cat >>"$tmp/changelog" <<EOF

### ${receipt_version}

<!-- markdownlint-disable-next-line MD013 -->
- [${receipt_tag}](${receipt_url}) is an immutable earlier
  supervised alpha.
- Package SHA-256: \`${receipt_sha}\`.
EOF
  history_count=$((history_count + 1))
done < <(find releases/receipts -maxdepth 1 -type f -name 'v*.json' | LC_ALL=C sort -r)

replace_block README.md "$tmp/readme-en"
replace_block README.zh-CN.md "$tmp/readme-zh"
replace_block docs/STATUS.md "$tmp/status"
replace_block docs/support-matrix.md "$tmp/support"
replace_block CHANGELOG.md "$tmp/changelog"

formula="packaging/homebrew/hideout.rb"
[ -f "$formula" ] || {
  echo "render-public-release-docs: missing Homebrew source formula" >&2
  exit 1
}
formula_url="https://github.com/vibe-agi/hideout/releases/download/${tag}/hideout-${tag}-darwin-arm64.tar.gz"
awk -v url="$formula_url" -v sha="$package_sha" '
  /^  url "/ {
    urls++
    print "  url \"" url "\""
    next
  }
  /^  sha256 "/ {
    shas++
    print "  sha256 \"" sha "\""
    next
  }
  { print }
  END {
    if (urls != 1 || shas != 1) exit 1
  }
' "$formula" >"$tmp/formula"
mv "$tmp/formula" "$formula"
grep -Fqx "  url \"$formula_url\"" "$formula"
grep -Fqx "  sha256 \"$package_sha\"" "$formula"
if command -v ruby >/dev/null 2>&1; then
  ruby -c "$formula" >/dev/null
fi

formula_snapshot="releases/formulas/${tag}.rb"
mkdir -p "$(dirname "$formula_snapshot")"
tail -n +3 "$formula" >"$tmp/formula-snapshot"
mv "$tmp/formula-snapshot" "$formula_snapshot"
cmp <(tail -n +3 "$formula") "$formula_snapshot"

echo "render-public-release-docs: rendered $tag and $formula_snapshot"
