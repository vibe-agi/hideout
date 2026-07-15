#!/bin/sh
set -eu

repository="vibe-agi/hideout"
inventory_url="${HIDEOUT_INSTALL_INVENTORY_URL:-https://raw.githubusercontent.com/vibe-agi/hideout/master/releases/current.json}"
prefix="${HIDEOUT_INSTALL_PREFIX:-$HOME/.local}"
store="${HIDEOUT_STORE_ROOT:-$HOME/.hideout}"
skip_init=0

usage() {
  cat <<'USAGE'
Install the current verified Hideout public alpha on macOS arm64.

Usage:
  ./install.sh [--prefix <dir>] [--store <dir>] [--skip-init]

Environment:
  HIDEOUT_INSTALL_PREFIX          default: ~/.local
  HIDEOUT_STORE_ROOT              default: ~/.hideout
  HIDEOUT_INSTALL_INVENTORY_URL   override the release inventory URL

The installer does not use sudo or edit shell startup files. It verifies the
published archive SHA-256 and the macOS code signature before installation.
USAGE
}

die() {
  echo "hideout-install: $*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

json_value() {
  /usr/bin/plutil -extract "$1" raw -o - "$2" 2>/dev/null ||
    die "invalid release inventory field: $1"
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --prefix)
      [ "$#" -ge 2 ] || die "--prefix requires a directory"
      prefix="$2"
      shift 2
      ;;
    --store)
      [ "$#" -ge 2 ] || die "--store requires a directory"
      store="$2"
      shift 2
      ;;
    --skip-init)
      skip_init=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

case "$prefix" in
  /*) ;;
  *) die "--prefix must be an absolute path" ;;
esac
case "$store" in
  /*) ;;
  *) die "--store must be an absolute path" ;;
esac

[ "$(uname -s)" = "Darwin" ] || die "the public alpha supports macOS only"
[ "$(uname -m)" = "arm64" ] || die "the public alpha supports Apple Silicon only"

for command in curl tar shasum; do
  require_command "$command"
done
[ -x /usr/bin/plutil ] || die "required command not found: /usr/bin/plutil"
[ -x /usr/bin/codesign ] || die "required command not found: /usr/bin/codesign"

profile="$store/profiles/default/profile.json"
if [ "$skip_init" -eq 0 ] && [ ! -f "$profile" ]; then
  command -v limactl >/dev/null 2>&1 ||
    die "Lima is required for first setup; install it with: brew install lima"
fi

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-install.XXXXXX")"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup 0 HUP INT TERM

inventory="$tmp/current.json"
if [ -n "${HIDEOUT_INSTALL_INVENTORY_FILE:-}" ]; then
  cp "$HIDEOUT_INSTALL_INVENTORY_FILE" "$inventory"
else
  curl --fail --location --silent --show-error --retry 3 \
    --proto '=https' --tlsv1.2 --output "$inventory" "$inventory_url"
fi

[ "$(json_value schema "$inventory")" = "hideout.published-release-inventory/v1" ] ||
  die "unsupported release inventory schema"
[ "$(json_value current.maturity "$inventory")" = "public-supervised-alpha" ] ||
  die "release inventory does not name a public supervised alpha"
[ "$(json_value current.platform "$inventory")" = "darwin/arm64" ] ||
  die "release inventory does not name darwin/arm64"
[ "$(json_value current.backend "$inventory")" = "lima" ] ||
  die "release inventory does not name the Lima backend"

version="$(json_value current.version "$inventory")"
tag="$(json_value current.tag "$inventory")"
release_url="$(json_value current.releaseURL "$inventory")"
source_commit="$(json_value current.package.sourceCommit "$inventory")"
expected_sha="$(json_value current.package.artifactSHA256 "$inventory")"

case "$version" in
  ''|*[!0-9A-Za-z.-]*) die "release inventory has an unsafe version" ;;
esac
[ "$tag" = "v$version" ] || die "release tag does not match version"
[ "$release_url" = "https://github.com/$repository/releases/tag/$tag" ] ||
  die "release URL does not match the trusted repository and tag"
case "$source_commit" in
  *[!0-9a-f]*|'') die "release inventory has an invalid source commit" ;;
esac
[ "${#source_commit}" -eq 40 ] || die "release inventory source commit must be full length"
case "$expected_sha" in
  *[!0-9a-f]*|'') die "release inventory has an invalid package SHA-256" ;;
esac
[ "${#expected_sha}" -eq 64 ] || die "release inventory package SHA-256 must be full length"

package="hideout-v${version}-darwin-arm64.tar.gz"
archive="$tmp/$package"
if [ -n "${HIDEOUT_INSTALL_PACKAGE_FILE:-}" ]; then
  cp "$HIDEOUT_INSTALL_PACKAGE_FILE" "$archive"
else
  curl --fail --location --silent --show-error --retry 3 \
    --proto '=https' --tlsv1.2 --output "$archive" \
    "https://github.com/$repository/releases/download/$tag/$package"
fi

actual_sha="$(shasum -a 256 "$archive" | awk '{print $1}')"
[ "$actual_sha" = "$expected_sha" ] || die "package SHA-256 does not match the published inventory"

tar -xzf "$archive" -C "$tmp"
package_root="$tmp/hideout"
[ -x "$package_root/bin/hideout" ] || die "package is missing bin/hideout"
[ -x "$package_root/install.sh" ] || die "package is missing install.sh"
[ -f "$package_root/package-manifest.json" ] || die "package is missing package-manifest.json"

manifest="$package_root/package-manifest.json"
[ "$(json_value release.productVersion "$manifest")" = "$version" ] ||
  die "package manifest version does not match release inventory"
[ "$(json_value source.commit "$manifest")" = "$source_commit" ] ||
  die "package manifest commit does not match release inventory"
[ "$(json_value target.hostOS "$manifest")" = "darwin" ] ||
  die "package manifest does not target macOS"
[ "$(json_value target.hostArch "$manifest")" = "arm64" ] ||
  die "package manifest does not target Apple Silicon"

/usr/bin/codesign --verify --strict "$package_root/bin/hideout" >/dev/null 2>&1 ||
  die "package binary failed macOS code-signature verification"

"$package_root/install.sh" --prefix "$prefix" --store "$store" --skip-init

if [ "$skip_init" -eq 0 ]; then
  if [ -f "$profile" ]; then
    echo "hideout-install: preserving existing default profile"
  else
    "$prefix/bin/hideout" init \
      --template dev \
      --profile default \
      --backend lima \
      --network direct \
      --runtime developer-standard \
      --no-input
  fi
fi

echo
echo "Hideout $version installed at $prefix/bin/hideout"
if [ "$skip_init" -eq 1 ]; then
  echo "Initialize it:"
  echo "  $prefix/bin/hideout init --template dev --backend lima --network direct --runtime developer-standard --no-input"
else
  echo "Run it from a dedicated project checkout:"
  echo "  $prefix/bin/hideout run -- git status --short"
fi
case ":$PATH:" in
  *":$prefix/bin:"*) ;;
  *) echo "Add it to PATH: export PATH=\"$prefix/bin:\$PATH\"" ;;
esac
