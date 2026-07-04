#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-package-smoke.XXXXXX")"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

pkg="$tmp/hideout.tar.gz"
store="$tmp/store"
workspace="$tmp/workspace"
mkdir -p "$workspace" "$tmp/install"

scripts/package-local.sh --out "$pkg" >"$tmp/package.out"
test -f "$pkg"

tar -xzf "$pkg" -C "$tmp/install"
prefix="$tmp/install/hideout"
arch="$(go env GOARCH)"

manifest_relative_path() {
  case "$1" in
    ""|/*|../*|*/../*|*/..)
      return 1
      ;;
  esac
}

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    echo "package-smoke: missing shasum or sha256sum" >&2
    exit 127
  fi
}

for path in \
  "$prefix/bin/hideout" \
  "$prefix/bin/hideout-shim" \
  "$prefix/bin/hideout-shim-linux-$arch" \
  "$prefix/bin/hideout-hostfsd-linux-$arch" \
  "$prefix/install.sh" \
  "$prefix/package-manifest.json" \
  "$prefix/README.md" \
  "$prefix/README.zh-CN.md" \
  "$prefix/schemas/package-manifest.schema.json" \
  "$prefix/schemas/release-dogfood.schema.json" \
  "$prefix/schemas/profile.schema.json" \
  "$prefix/schemas/run-plan.schema.json" \
  "$prefix/docs/privacy-run-design.md" \
  "$prefix/packaging/homebrew/hideout.rb"
do
  if [ ! -e "$path" ]; then
    echo "package-smoke: expected package artifact is missing: $path" >&2
    exit 1
  fi
done

test -f "$prefix/bin/hideout-shim-linux-$arch.manifest.json"
test -f "$prefix/bin/hideout-hostfsd-linux-$arch.manifest.json"
go run ./cmd/hideout-schema-validate "$prefix/schemas/package-manifest.schema.json" "$prefix/package-manifest.json"
jq -e \
  --arg host_os "$(go env GOOS)" \
  --arg host_arch "$arch" \
  '
    .schema == "hideout.package-manifest.v1" and
    (.builtAt | type == "string" and length > 0) and
    (.git.commit | type == "string" and length > 0) and
    (.git.dirty | type == "boolean") and
    .target.hostOS == $host_os and
    .target.hostArch == $host_arch and
    .target.linuxGuestArch == $host_arch and
    .layout.root == "hideout" and
    (.layout.binaries | index("bin/hideout")) and
    (.layout.binaries | index("bin/hideout-shim-linux-" + $host_arch)) and
    (.layout.entrypoints | index("install.sh")) and
    (.layout.entrypoints | index("README.md")) and
    (.layout.entrypoints | index("README.zh-CN.md")) and
    (.layout.directories | index("schemas")) and
    (.layout.directories | index("docs")) and
    (.layout.directories | index("packaging")) and
    (.files | type == "array" and length >= 8) and
    any(.files[]; .path == "bin/hideout" and .kind == "binary" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "bin/hideout-shim-linux-" + $host_arch and .kind == "linux-helper" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "install.sh" and .kind == "installer" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "README.md" and .kind == "entrypoint" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "schemas/package-manifest.schema.json" and .kind == "schema" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "schemas/release-dogfood.schema.json" and .kind == "schema" and (.sha256 | test("^[a-f0-9]{64}$")))
  ' "$prefix/package-manifest.json" >/dev/null

jq -r '.layout.binaries[]' "$prefix/package-manifest.json" | while IFS= read -r rel; do
  if ! manifest_relative_path "$rel"; then
    echo "package-smoke: manifest binary path is not package-relative: $rel" >&2
    exit 1
  fi
  if [ ! -x "$prefix/$rel" ]; then
    echo "package-smoke: manifest binary is missing or not executable: $rel" >&2
    exit 1
  fi
done

jq -r '.layout.entrypoints[]' "$prefix/package-manifest.json" | while IFS= read -r rel; do
  if ! manifest_relative_path "$rel"; then
    echo "package-smoke: manifest entrypoint path is not package-relative: $rel" >&2
    exit 1
  fi
  if [ ! -f "$prefix/$rel" ]; then
    echo "package-smoke: manifest entrypoint is missing or not a file: $rel" >&2
    exit 1
  fi
done

jq -r '.layout.directories[]' "$prefix/package-manifest.json" | while IFS= read -r rel; do
  if ! manifest_relative_path "$rel"; then
    echo "package-smoke: manifest directory path is not package-relative: $rel" >&2
    exit 1
  fi
  if [ ! -d "$prefix/$rel" ]; then
    echo "package-smoke: manifest directory is missing: $rel" >&2
    exit 1
  fi
done

jq -r '.files[] | [.path, .kind, .sha256] | @tsv' "$prefix/package-manifest.json" | while IFS=$'\t' read -r rel kind want; do
  if ! manifest_relative_path "$rel"; then
    echo "package-smoke: manifest file path is not package-relative: $rel" >&2
    exit 1
  fi
  case "$kind" in
    binary|linux-helper|helper-manifest|installer|entrypoint|schema)
      ;;
    *)
      echo "package-smoke: manifest file has unknown kind: $rel ($kind)" >&2
      exit 1
      ;;
  esac
  if [ ! -f "$prefix/$rel" ]; then
    echo "package-smoke: manifest file is missing or not a file: $rel" >&2
    exit 1
  fi
  got="$(sha256_file "$prefix/$rel")"
  if [ "$got" != "$want" ]; then
    echo "package-smoke: manifest checksum mismatch for $rel" >&2
    echo "package-smoke: want $want" >&2
    echo "package-smoke: got  $got" >&2
    exit 1
  fi
done

HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" init --no-input --backend native --network direct >"$tmp/init.out"
grep -q 'Hideout init' "$tmp/init.out"
test -f "$store/install-state.json"
test -f "$store/logs/init-audit.jsonl"
test -f "$store/profiles/default/profile.json"

HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" doctor --backend native --workspace "$workspace" >"$tmp/doctor.out"
grep -q 'store: ok writable' "$tmp/doctor.out"
grep -q 'profile: ok default' "$tmp/doctor.out"
grep -q 'manager: ok' "$tmp/doctor.out"

HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" tui >"$tmp/tui.out"
grep -q '^Hideout TUI$' "$tmp/tui.out"
grep -q '^Status: ok$' "$tmp/tui.out"
grep -q '^Profiles:' "$tmp/tui.out"
if grep -q 'Hideout UI:' "$tmp/tui.out"; then
  echo "package-smoke: tui should not start WebUI" >&2
  cat "$tmp/tui.out" >&2
  exit 1
fi

HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" ui --no-open --print-url --listen 127.0.0.1:0 --ttl 1m >"$tmp/ui.out"
grep -q '^Hideout UI: http://127\.0\.0\.1:' "$tmp/ui.out"
grep -q '^Manager API: http://127\.0\.0\.1:' "$tmp/ui.out"
grep -q '^Token expires:' "$tmp/ui.out"
if grep -q 'Press Ctrl-C to stop' "$tmp/ui.out"; then
  echo "package-smoke: ui --print-url should exit without waiting" >&2
  cat "$tmp/ui.out" >&2
  exit 1
fi

HIDEOUT_STORE_ROOT="$tmp/lima-store" "$prefix/bin/hideout" doctor --fix --dry-run --backend lima --workspace "$workspace" >"$tmp/lima-doctor-fix-dry.out"
grep -q 'task helper.install.linux-shim: ok' "$tmp/lima-doctor-fix-dry.out"
grep -q 'task helper.install.linux-hostfsd: ok' "$tmp/lima-doctor-fix-dry.out"
if [ -e "$tmp/lima-store/install-state.json" ]; then
  echo "package-smoke: lima dry-run repair created install state" >&2
  cat "$tmp/lima-doctor-fix-dry.out" >&2
  exit 1
fi

installed_prefix="$tmp/package-installed"
installed_store="$tmp/package-store"
"$prefix/install.sh" --prefix "$installed_prefix" --store "$installed_store" --backend native --network direct >"$tmp/package-install.out"
test -x "$installed_prefix/bin/hideout"
test -x "$installed_prefix/bin/hideout-shim"
test -x "$installed_prefix/bin/hideout-shim-linux-$arch"
test -x "$installed_prefix/bin/hideout-hostfsd-linux-$arch"
test -f "$installed_store/install-state.json"
test -f "$installed_store/profiles/default/profile.json"
HIDEOUT_STORE_ROOT="$installed_store" "$installed_prefix/bin/hideout" doctor --backend native --workspace "$workspace" >"$tmp/package-installed-doctor.out"
grep -q 'store: ok writable' "$tmp/package-installed-doctor.out"
grep -q 'profile: ok default' "$tmp/package-installed-doctor.out"

proxy_installed_prefix="$tmp/package-proxy-installed"
proxy_installed_store="$tmp/package-proxy-store"
HIDEOUT_SECRET_DEFAULT_PROXY="socks5://user:pass@127.0.0.1:7890" \
  "$prefix/install.sh" --prefix "$proxy_installed_prefix" --store "$proxy_installed_store" --backend native --network tun2socks --proxy-secret default-proxy >"$tmp/package-proxy-install.out"
jq -e '.network.mode == "tun2socks" and .network.proxySecretRef == "default-proxy" and (.network.proxyEnvVisible == false)' "$proxy_installed_store/profiles/default/profile.json" >/dev/null
if grep -R 'socks5://user:pass@127.0.0.1:7890' "$proxy_installed_store" >/dev/null 2>&1; then
  echo "package-smoke: package installer persisted raw proxy URL" >&2
  exit 1
fi

echo "package-smoke: passed"
