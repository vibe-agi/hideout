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
grep -q 'For the alpha package path' README.md
grep -q 'hideout package verify "$HOME/.local"' README.md
grep -q 'For local source-tree development' README.md
grep -q 'Alpha package lifecycle' docs/STATUS.md

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
    binary|linux-helper|helper-manifest|installer|entrypoint|schema|doc|script|packaging)
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

HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" init --no-input --profile default --template dev --backend native --network direct >"$tmp/init.out"
grep -q 'Hideout init' "$tmp/init.out"
test -f "$store/install-state.json"
test -f "$store/logs/init-audit.jsonl"
test -f "$store/profiles/default/profile.json"

HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" doctor --backend native --workspace "$workspace" >"$tmp/doctor.out"
grep -q 'store: ok writable' "$tmp/doctor.out"
grep -q 'profile: ok default' "$tmp/doctor.out"
grep -q 'manager: ok' "$tmp/doctor.out"

HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" tui --once >"$tmp/tui.out"
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

broken_missing_helper="$tmp/package-missing-helper"
cp -R "$prefix" "$broken_missing_helper"
rm -f "$broken_missing_helper/bin/hideout-hostfsd-linux-$arch"
if "$broken_missing_helper/install.sh" --prefix "$tmp/broken-helper-install" --store "$tmp/broken-helper-store" --skip-init >"$tmp/broken-helper.out" 2>"$tmp/broken-helper.err"; then
  echo "package-smoke: installer accepted package missing Linux HostFS daemon" >&2
  exit 1
fi
grep -q 'bin/hideout-hostfsd-linux' "$tmp/broken-helper.err"
if [ -e "$tmp/broken-helper-install/bin/hideout" ]; then
  echo "package-smoke: broken helper package copied binaries before failing" >&2
  exit 1
fi

broken_missing_manifest="$tmp/package-missing-manifest"
cp -R "$prefix" "$broken_missing_manifest"
rm -f "$broken_missing_manifest/package-manifest.json"
if "$broken_missing_manifest/install.sh" --prefix "$tmp/broken-manifest-install" --store "$tmp/broken-manifest-store" --skip-init >"$tmp/broken-manifest.out" 2>"$tmp/broken-manifest.err"; then
  echo "package-smoke: installer accepted package missing package manifest" >&2
  exit 1
fi
grep -q 'open package-manifest.json' "$tmp/broken-manifest.err"
if [ -e "$tmp/broken-manifest-install/bin/hideout" ]; then
  echo "package-smoke: broken manifest package copied binaries before failing" >&2
  exit 1
fi

broken_checksum="$tmp/package-bad-checksum"
cp -R "$prefix" "$broken_checksum"
printf '\ncorrupt-for-smoke\n' >>"$broken_checksum/README.md"
if "$broken_checksum/install.sh" --prefix "$tmp/broken-checksum-install" --store "$tmp/broken-checksum-store" --skip-init >"$tmp/broken-checksum.out" 2>"$tmp/broken-checksum.err"; then
  echo "package-smoke: installer accepted package with checksum mismatch" >&2
  exit 1
fi
grep -q 'package checksum mismatch for README.md' "$tmp/broken-checksum.err"
if [ -e "$tmp/broken-checksum-install/bin/hideout" ]; then
  echo "package-smoke: checksum-mismatched package copied binaries before failing" >&2
  exit 1
fi

installed_prefix="$tmp/package-installed"
installed_store="$tmp/package-store"
"$prefix/install.sh" --prefix "$installed_prefix" --store "$installed_store" --backend native --network direct >"$tmp/package-install.out"
installed_prefix_real="$(cd "$installed_prefix" && pwd -P)"
installed_store_real="$(cd "$installed_store" && pwd -P)"
test -x "$installed_prefix/bin/hideout"
test -x "$installed_prefix/bin/hideout-shim"
test -x "$installed_prefix/bin/hideout-shim-linux-$arch"
test -x "$installed_prefix/bin/hideout-hostfsd-linux-$arch"
test -f "$installed_prefix/share/hideout/package-manifest.json"
test -f "$installed_prefix/share/hideout/schemas/package-manifest.schema.json"
test -f "$installed_prefix/share/hideout/docs/README.md"
test -f "$installed_prefix/share/hideout/docs/STATUS.md"
go run ./cmd/hideout-schema-validate "$prefix/schemas/package-manifest.schema.json" "$installed_prefix/share/hideout/package-manifest.json"
"$installed_prefix/bin/hideout" package verify "$installed_prefix" >"$tmp/package-installed-verify.out"
grep -q 'package: ok mode=installed' "$tmp/package-installed-verify.out"
grep -q "\"installPrefix\": \"$installed_prefix_real\"" "$installed_prefix/share/hideout/package-manifest.json"
test -f "$installed_store/install-state.json"
test -f "$installed_store/profiles/default/profile.json"
HIDEOUT_STORE_ROOT="$installed_store" "$installed_prefix/bin/hideout" doctor --backend native --workspace "$workspace" >"$tmp/package-installed-doctor.out"
grep -q 'store: ok writable' "$tmp/package-installed-doctor.out"
grep -q 'profile: ok default' "$tmp/package-installed-doctor.out"

durable_fixture="$installed_store/evidence/keep.json"
mkdir -p "$(dirname "$durable_fixture")"
printf '{"keep":true}\n' >"$durable_fixture"
before_upgrade_sha="$(sha256_file "$installed_prefix/bin/hideout")"
"$prefix/install.sh" --prefix "$installed_prefix" --store "$installed_store" --skip-init >"$tmp/package-upgrade.out"
grep -q 'package: upgrade' "$tmp/package-upgrade.out"
test -f "$durable_fixture"
after_upgrade_sha="$(sha256_file "$installed_prefix/bin/hideout")"
test "$before_upgrade_sha" = "$after_upgrade_sha"

bad_upgrade="$tmp/package-bad-upgrade"
cp -R "$prefix" "$bad_upgrade"
jq '.migration.fromInstalledSchemas = ["hideout.package-install-state.v0"]' "$bad_upgrade/package-manifest.json" >"$bad_upgrade/package-manifest.json.tmp"
mv "$bad_upgrade/package-manifest.json.tmp" "$bad_upgrade/package-manifest.json"
if "$bad_upgrade/install.sh" --prefix "$installed_prefix" --store "$installed_store" --skip-init >"$tmp/package-bad-upgrade.out" 2>"$tmp/package-bad-upgrade.err"; then
  echo "package-smoke: incompatible migration range was accepted" >&2
  exit 1
fi
grep -q 'outside migration range' "$tmp/package-bad-upgrade.err"
test "$after_upgrade_sha" = "$(sha256_file "$installed_prefix/bin/hideout")"

unrelated_installed="$installed_prefix/bin/not-hideout"
printf 'operator-owned\n' >"$unrelated_installed"
"$installed_prefix/bin/hideout" package uninstall --prefix "$installed_prefix" --dry-run >"$tmp/package-uninstall-dry.out"
grep -q 'package: uninstall dry-run' "$tmp/package-uninstall-dry.out"
grep -q 'remove bin/hideout' "$tmp/package-uninstall-dry.out"
test -x "$installed_prefix/bin/hideout"
test -f "$durable_fixture"
"$installed_prefix/bin/hideout" package uninstall --prefix "$installed_prefix" >"$tmp/package-uninstall.out"
grep -q 'durableState=preserved' "$tmp/package-uninstall.out"
test ! -e "$installed_prefix/bin/hideout"
test -f "$unrelated_installed"
test -f "$durable_fixture"
"$prefix/install.sh" --prefix "$installed_prefix" --store "$installed_store" --skip-init >"$tmp/package-reinstall-for-purge.out"
"$installed_prefix/bin/hideout" package uninstall --prefix "$installed_prefix" --purge >"$tmp/package-uninstall-purge.out"
grep -q 'durableState=purged' "$tmp/package-uninstall-purge.out"
grep -q "purge store=$installed_store_real" "$tmp/package-uninstall-purge.out"
test ! -e "$installed_store"

default_installed_prefix="$tmp/package-default-installed"
default_installed_store="$tmp/package-default-store"
"$prefix/install.sh" --prefix "$default_installed_prefix" --store "$default_installed_store" >"$tmp/package-default-install.out"
test -x "$default_installed_prefix/bin/hideout"
test -x "$default_installed_prefix/bin/hideout-shim"
test -x "$default_installed_prefix/bin/hideout-shim-linux-$arch"
test -x "$default_installed_prefix/bin/hideout-hostfsd-linux-$arch"
test -f "$default_installed_store/install-state.json"
test -f "$default_installed_store/profiles/default/profile.json"
grep -q 'backend: lima' "$tmp/package-default-install.out"
if command -v limactl >/dev/null 2>&1; then
  HIDEOUT_STORE_ROOT="$default_installed_store" "$default_installed_prefix/bin/hideout" doctor --workspace "$workspace" >"$tmp/package-default-doctor.out"
  grep -q 'backend: ok lima available' "$tmp/package-default-doctor.out"
else
  if HIDEOUT_STORE_ROOT="$default_installed_store" "$default_installed_prefix/bin/hideout" doctor --workspace "$workspace" >"$tmp/package-default-doctor.out" 2>"$tmp/package-default-doctor.err"; then
    echo "package-smoke: default doctor succeeded without limactl" >&2
    cat "$tmp/package-default-doctor.out" >&2
    exit 1
  fi
  grep -q 'backend: error lima unavailable: limactl is required for lima backend' "$tmp/package-default-doctor.out"
fi
HIDEOUT_STORE_ROOT="$default_installed_store" "$default_installed_prefix/bin/hideout" doctor --fix --dry-run --backend lima --workspace "$workspace" >"$tmp/package-default-lima-doctor-fix-dry.out"
grep -q 'task helper.install.linux-shim: ok' "$tmp/package-default-lima-doctor-fix-dry.out"
grep -q 'task helper.install.linux-hostfsd: ok' "$tmp/package-default-lima-doctor-fix-dry.out"

skip_installed_prefix="$tmp/package-skip-installed"
skip_installed_store="$tmp/package-skip-store"
"$prefix/install.sh" --prefix "$skip_installed_prefix" --store "$skip_installed_store" --skip-init >"$tmp/package-skip-install.out"
test -x "$skip_installed_prefix/bin/hideout"
test -x "$skip_installed_prefix/bin/hideout-shim"
test -x "$skip_installed_prefix/bin/hideout-shim-linux-$arch"
test -x "$skip_installed_prefix/bin/hideout-hostfsd-linux-$arch"
if [ -e "$skip_installed_store/install-state.json" ] || [ -e "$skip_installed_store/profiles/default/profile.json" ]; then
  echo "package-smoke: package installer --skip-init wrote init state" >&2
  cat "$tmp/package-skip-install.out" >&2
  exit 1
fi

proxy_installed_prefix="$tmp/package-proxy-installed"
proxy_installed_store="$tmp/package-proxy-store"
HIDEOUT_SECRET_DEFAULT_PROXY="socks5://user:pass@127.0.0.1:7890" \
  "$prefix/install.sh" --prefix "$proxy_installed_prefix" --store "$proxy_installed_store" --backend native --network tun2socks --proxy-secret default-proxy >"$tmp/package-proxy-install.out"
jq -e '.network.mode == "tun2socks" and .network.proxySecretRef == "default-proxy" and (.network.proxyEnvVisible == false)' "$proxy_installed_store/profiles/default/profile.json" >/dev/null
jq -e '.network.mediatedResolver == "1.1.1.1" and .metadata.templateId == "privacy"' "$proxy_installed_store/profiles/default/profile.json" >/dev/null
if grep -R 'socks5://user:pass@127.0.0.1:7890' "$proxy_installed_store" >/dev/null 2>&1; then
  echo "package-smoke: package installer persisted raw proxy URL" >&2
  exit 1
fi

echo "package-smoke: passed"
