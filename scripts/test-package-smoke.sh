#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
. "$ROOT/scripts/lib/daemon-temp.sh"

tmp="$(hideout_mktemp_daemon_store)"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT

copy_artifacts() {
  if [ -z "${HIDEOUT_PACKAGE_SMOKE_ARTIFACT_DIR:-}" ]; then
    return
  fi
  mkdir -p "$HIDEOUT_PACKAGE_SMOKE_ARTIFACT_DIR"
  for rel in \
    package-stale-upgrade.out \
    package-stale-verify.out \
    package-stale-verify.err \
    package-repair-dry.out \
    package-repair.out \
    package-repair-summary.json \
    package-repaired-verify.out \
    package-uninstall-dry.out \
    package-uninstall.out \
    package-uninstall-purge-dry.out \
    package-uninstall-purge-unconfirmed.err \
    package-uninstall-purge.out \
    package-scope-uninstall.err \
    package-lifecycle-summary.json \
    package-help-update.out \
    package-help-uninstall.out \
    package-installed-doctor-packaging.json
  do
    if [ -f "$tmp/$rel" ]; then
      cp "$tmp/$rel" "$HIDEOUT_PACKAGE_SMOKE_ARTIFACT_DIR/$rel"
    fi
  done
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

pkg="$tmp/hideout.tar.gz"
stage="$tmp/stage"
store="$tmp/store"
workspace="$tmp/workspace"
mkdir -p "$workspace" "$tmp/install"

scripts/package-local.sh --stage "$stage" >"$tmp/package-stage.out"
test ! -e "$stage/hideout/package-manifest.json"
staged_hideout_sha="$(sha256_file "$stage/hideout/bin/hideout")"
cp "$stage/.package-build.json" "$tmp/package-build-original.json"
jq '.channel = "alpha"' "$stage/.package-build.json" >"$tmp/package-build-alpha.json"
mv "$tmp/package-build-alpha.json" "$stage/.package-build.json"
if scripts/package-local.sh --finalize "$stage" --out "$tmp/wrong-alpha-name.tar.gz" \
  >"$tmp/wrong-alpha-name.out" 2>"$tmp/wrong-alpha-name.err"; then
  echo "package-smoke: mismatched alpha archive name was accepted" >&2
  exit 1
fi
grep -Fq 'alpha archive name does not match staged candidate identity' \
  "$tmp/wrong-alpha-name.err"
mv "$tmp/package-build-original.json" "$stage/.package-build.json"
scripts/package-local.sh --finalize "$stage" --out "$pkg" >"$tmp/package.out"
test -f "$pkg"
test "$staged_hideout_sha" = "$(sha256_file "$stage/hideout/bin/hideout")"
grep -Fq 'brew install vibe-agi/tap/hideout' README.md
grep -Fq '[Distribution And Bootstrap](docs/distribution-bootstrap.md)' README.md
grep -Fq '## Build From Source' README.md
grep -q 'Alpha package lifecycle' docs/STATUS.md

tar -xzf "$pkg" -C "$tmp/install"
prefix="$tmp/install/hideout"
arch="$(go env GOARCH)"
scripts/test-package-docs.sh --package-root "$prefix" --self-test

manifest_relative_path() {
  case "$1" in
    ""|/*|../*|*/../*|*/..)
      return 1
      ;;
  esac
}

for path in \
  "$prefix/bin/hideout" \
  "$prefix/bin/hideout-shim" \
  "$prefix/bin/hideout-shim-linux-$arch" \
  "$prefix/bin/hideout-hostfsd-linux-$arch" \
  "$prefix/bin/hideout-session-supervisor-linux-$arch" \
  "$prefix/bin/hideout-workspace-portal-linux-$arch" \
  "$prefix/bin/tun2socks-linux-$arch" \
  "$prefix/install.sh" \
  "$prefix/package-manifest.json" \
  "$prefix/README.md" \
  "$prefix/README.zh-CN.md" \
  "$prefix/CHANGELOG.md" \
  "$prefix/RELEASE_NOTES.md" \
  "$prefix/LICENSE" \
  "$prefix/THIRD_PARTY_NOTICES.md" \
  "$prefix/SECURITY.md" \
  "$prefix/third_party/tun2socks/LICENSE" \
  "$prefix/schemas/package-manifest.schema.json" \
  "$prefix/schemas/release-dogfood.schema.json" \
  "$prefix/schemas/runtime-catalog.schema.json" \
  "$prefix/schemas/runtime-verification.schema.json" \
  "$prefix/schemas/capability-descriptor.schema.json" \
  "$prefix/schemas/host-app-pack.schema.json" \
  "$prefix/schemas/host-app-pack-registry.schema.json" \
  "$prefix/schemas/host-app-enablement.schema.json" \
  "$prefix/schemas/host-app-inspection.schema.json" \
  "$prefix/schemas/open-resource-intent.schema.json" \
  "$prefix/schemas/profile.schema.json" \
  "$prefix/schemas/run-plan.schema.json" \
  "$prefix/runtime/catalog.json" \
  "$prefix/runtime/contract.json" \
  "$prefix/runtime/developer-standard/sources.lock.json" \
  "$prefix/runtime/developer-standard/build.sh" \
  "$prefix/host-app/recipes/builtin-vscode.json" \
  "$prefix/host-app/recipes/safety-profiles.json" \
  "$prefix/examples/host-app-packs/cursor/hideout.host-app-pack.json" \
  "$prefix/examples/host-app-packs/zed/hideout.host-app-pack.json" \
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
test -f "$prefix/bin/hideout-session-supervisor-linux-$arch.manifest.json"
test -f "$prefix/bin/hideout-workspace-portal-linux-$arch.manifest.json"
test -f "$prefix/bin/tun2socks-linux-$arch.manifest.json"
jq -e --arg arch "$arch" '
  .version == "hideout.helper-manifest/v1" and
  .command == "tun2socks" and
  .targetOS == "linux" and
  .targetArch == $arch and
  .upstreamModule == "github.com/xjasonlyu/tun2socks/v2" and
  .upstreamVersion == "v2.6.0" and
  .license == "MIT" and
  .buildMode == "source-built-pinned-module" and
  .packageOwned == true
' "$prefix/bin/tun2socks-linux-$arch.manifest.json" >/dev/null

ambient_bin="$tmp/ambient-helper"
mkdir -p "$ambient_bin"
printf '#!/bin/sh\nexit 99\n' >"$ambient_bin/tun2socks-linux-$arch"
chmod 0700 "$ambient_bin/tun2socks-linux-$arch"
PATH="$ambient_bin:$PATH" HIDEOUT_RELEASE_BINARY="$prefix/bin/hideout" \
  scripts/test-gate3-hidden-proxy.sh --verify-package-helper-only \
  >"$tmp/package-tun2socks-gate3.out"
grep -q '^tun2socks_source=package-owned$' "$tmp/package-tun2socks-gate3.out"
grep -q '^tun2socks_upstream_version=v2.6.0$' "$tmp/package-tun2socks-gate3.out"
grep -Eq '^tun2socks_artifact_sha256=[a-f0-9]{64}$' "$tmp/package-tun2socks-gate3.out"
if HIDEOUT_RELEASE_BINARY="$prefix/bin/hideout" \
  HIDEOUT_LINUX_TUN2SOCKS_PATH="$ambient_bin/tun2socks-linux-$arch" \
  scripts/test-gate3-hidden-proxy.sh --verify-package-helper-only \
  >"$tmp/package-tun2socks-override.out" 2>"$tmp/package-tun2socks-override.err"; then
  echo "package-smoke: release Gate 3 accepted an explicit tun2socks override" >&2
  exit 1
fi
grep -q 'release evidence forbids HIDEOUT_LINUX_TUN2SOCKS_PATH' \
  "$tmp/package-tun2socks-override.err"

go run ./cmd/hideout-schema-validate "$prefix/schemas/package-manifest.schema.json" "$prefix/package-manifest.json"
for manifest in \
  "$prefix/host-app/recipes/builtin-vscode.json" \
  "$prefix/examples/host-app-packs/cursor/hideout.host-app-pack.json" \
  "$prefix/examples/host-app-packs/zed/hideout.host-app-pack.json"
do
  go run ./cmd/hideout-schema-validate "$prefix/schemas/host-app-pack.schema.json" "$manifest"
done
jq -e \
  --arg host_os "$(go env GOOS)" \
  --arg host_arch "$arch" \
  '
    .schema == "hideout.package-manifest/v1" and
    (.builtAt | type == "string" and length > 0) and
    .release.productVersion == "0.1.0-dev.0" and
    .release.channel == "developer-preview" and
    .release.tag == "v0.1.0-dev.0" and
    .source.repository == "https://github.com/vibe-agi/hideout" and
    (.source.commit | test("^[a-f0-9]{40}$")) and
    (.source.dirty | type == "boolean") and
    (.build.workflow | type == "string" and length > 0) and
    (.build.ref | type == "string" and length > 0) and
    .target.hostOS == $host_os and
    .target.hostArch == $host_arch and
    .target.linuxGuestArch == $host_arch and
    .layout.root == "hideout" and
    (.layout.binaries | index("bin/hideout")) and
    (.layout.binaries | index("bin/hideout-shim-linux-" + $host_arch)) and
    (.layout.binaries | index("bin/hideout-session-supervisor-linux-" + $host_arch)) and
    (.layout.binaries | index("bin/hideout-workspace-portal-linux-" + $host_arch)) and
    (.layout.binaries | index("bin/tun2socks-linux-" + $host_arch)) and
    (.layout.entrypoints | index("install.sh")) and
    (.layout.entrypoints | index("README.md")) and
    (.layout.entrypoints | index("README.zh-CN.md")) and
    (.layout.directories | index("schemas")) and
    (.layout.directories | index("docs")) and
    (.layout.directories | index("host-app")) and
    (.layout.directories | index("examples")) and
    (.layout.directories | index("packaging")) and
    (.layout.directories | index("runtime")) and
    (.layout.directories | index("third_party")) and
    .runtime.family == "developer-standard" and
    (.runtime.catalogFileSHA256 | test("^[a-f0-9]{64}$")) and
    (.runtime.artifactSHA256 | test("^[a-f0-9]{64}$")) and
    .signingSummary.mode == "developer-preview-unsigned" and
    .migration.installStateSchema == "hideout.package-install-state/v1" and
    (.migration.fromInstalledSchemas | index("hideout.package-install-state/v1")) and
    .migration.minimumPackageSchema == "hideout.package-manifest/v1" and
    .migration.maximumPackageSchema == "hideout.package-manifest/v1" and
    (.files | type == "array" and length >= 8) and
    any(.files[]; .path == "bin/hideout" and .kind == "binary" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "bin/hideout-shim-linux-" + $host_arch and .kind == "linux-helper" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "bin/hideout-session-supervisor-linux-" + $host_arch and .kind == "linux-helper" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "bin/hideout-workspace-portal-linux-" + $host_arch and .kind == "linux-helper" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "bin/tun2socks-linux-" + $host_arch and .kind == "linux-helper" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "bin/tun2socks-linux-" + $host_arch + ".manifest.json" and .kind == "helper-manifest" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "install.sh" and .kind == "installer" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "README.md" and .kind == "entrypoint" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "LICENSE" and .kind == "doc" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "THIRD_PARTY_NOTICES.md" and .kind == "doc" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "SECURITY.md" and .kind == "doc" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "third_party/tun2socks/LICENSE" and .kind == "doc" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "schemas/package-manifest.schema.json" and .kind == "schema" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "schemas/release-dogfood.schema.json" and .kind == "schema" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "schemas/runtime-catalog.schema.json" and .kind == "schema" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "schemas/runtime-verification.schema.json" and .kind == "schema" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "schemas/host-app-pack.schema.json" and .kind == "schema" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "schemas/host-app-enablement.schema.json" and .kind == "schema" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "host-app/recipes/builtin-vscode.json" and .kind == "host-app-core-data" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "host-app/recipes/safety-profiles.json" and .kind == "host-app-core-data" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "examples/host-app-packs/cursor/hideout.host-app-pack.json" and .kind == "host-app-example" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "runtime/catalog.json" and .kind == "runtime-catalog" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "runtime/contract.json" and .kind == "runtime-contract" and (.sha256 | test("^[a-f0-9]{64}$"))) and
    any(.files[]; .path == "runtime/developer-standard/sources.lock.json" and .kind == "runtime-build" and (.sha256 | test("^[a-f0-9]{64}$")))
  ' "$prefix/package-manifest.json" >/dev/null

cmp internal/runtimecatalog/catalog.json "$prefix/runtime/catalog.json"
cmp internal/runtimecatalog/contract.json "$prefix/runtime/contract.json"

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
    binary|linux-helper|helper-manifest|installer|entrypoint|schema|doc|script|packaging|host-app-core-data|host-app-example|runtime-catalog|runtime-contract|runtime-build)
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

HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" doctor --backend native --workspace "$workspace" --verbose >"$tmp/doctor.out"
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
HIDEOUT_STORE_ROOT="$store" "$prefix/bin/hideout" daemon stop >"$tmp/daemon-stop.out"

HIDEOUT_STORE_ROOT="$tmp/lima-store" "$prefix/bin/hideout" doctor --fix --dry-run --backend lima --workspace "$workspace" >"$tmp/lima-doctor-fix-dry.out"
grep -q 'task helper.install.linux-shim: ok' "$tmp/lima-doctor-fix-dry.out"
grep -q 'task helper.install.linux-hostfsd: ok' "$tmp/lima-doctor-fix-dry.out"
grep -q 'task helper.install.linux-session-supervisor: ok' "$tmp/lima-doctor-fix-dry.out"
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

broken_missing_tun2socks="$tmp/package-missing-tun2socks"
cp -R "$prefix" "$broken_missing_tun2socks"
rm -f "$broken_missing_tun2socks/bin/tun2socks-linux-$arch"
if "$broken_missing_tun2socks/install.sh" --prefix "$tmp/broken-tun2socks-install" --store "$tmp/broken-tun2socks-store" --skip-init >"$tmp/broken-tun2socks.out" 2>"$tmp/broken-tun2socks.err"; then
  echo "package-smoke: installer accepted package missing tun2socks" >&2
  exit 1
fi
grep -q 'bin/tun2socks-linux' "$tmp/broken-tun2socks.err"
if [ -e "$tmp/broken-tun2socks-install/bin/hideout" ]; then
  echo "package-smoke: missing-tun2socks package copied binaries before failing" >&2
  exit 1
fi

broken_tun2socks_digest="$tmp/package-bad-tun2socks-digest"
cp -R "$prefix" "$broken_tun2socks_digest"
printf '\ncorrupt-for-smoke\n' >>"$broken_tun2socks_digest/bin/tun2socks-linux-$arch"
if HIDEOUT_RELEASE_BINARY="$broken_tun2socks_digest/bin/hideout" \
  scripts/test-gate3-hidden-proxy.sh --verify-package-helper-only \
  >"$tmp/broken-tun2socks-gate3.out" 2>"$tmp/broken-tun2socks-gate3.err"; then
  echo "package-smoke: Gate 3 accepted tun2socks digest drift" >&2
  exit 1
fi
grep -q 'release package tun2socks digest mismatch' "$tmp/broken-tun2socks-gate3.err"
if "$broken_tun2socks_digest/install.sh" --prefix "$tmp/broken-tun2socks-digest-install" --store "$tmp/broken-tun2socks-digest-store" --skip-init >"$tmp/broken-tun2socks-digest.out" 2>"$tmp/broken-tun2socks-digest.err"; then
  echo "package-smoke: installer accepted tun2socks digest drift" >&2
  exit 1
fi
grep -q 'package checksum mismatch for bin/tun2socks-linux' "$tmp/broken-tun2socks-digest.err"

broken_tun2socks_target="$tmp/package-wrong-tun2socks-target"
cp -R "$prefix" "$broken_tun2socks_target"
jq '.targetArch = "wrong-arch"' \
  "$broken_tun2socks_target/bin/tun2socks-linux-$arch.manifest.json" \
  >"$tmp/wrong-target-manifest.json"
cp "$tmp/wrong-target-manifest.json" \
  "$broken_tun2socks_target/bin/tun2socks-linux-$arch.manifest.json"
if HIDEOUT_RELEASE_BINARY="$broken_tun2socks_target/bin/hideout" \
  scripts/test-gate3-hidden-proxy.sh --verify-package-helper-only \
  >"$tmp/wrong-target-gate3.out" 2>"$tmp/wrong-target-gate3.err"; then
  echo "package-smoke: Gate 3 accepted wrong-target tun2socks provenance" >&2
  exit 1
fi
grep -q 'release package tun2socks provenance is invalid' "$tmp/wrong-target-gate3.err"

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
test -x "$installed_prefix/bin/hideout-session-supervisor-linux-$arch"
test -x "$installed_prefix/bin/hideout-workspace-portal-linux-$arch"
test -x "$installed_prefix/bin/tun2socks-linux-$arch"
test -f "$installed_prefix/bin/tun2socks-linux-$arch.manifest.json"
test -f "$installed_prefix/share/hideout/third_party/tun2socks/LICENSE"
test -f "$installed_prefix/share/hideout/package-manifest.json"
test -f "$installed_prefix/share/hideout/schemas/package-manifest.schema.json"
test -f "$installed_prefix/share/hideout/schemas/runtime-catalog.schema.json"
test -f "$installed_prefix/share/hideout/schemas/runtime-verification.schema.json"
test -f "$installed_prefix/share/hideout/schemas/host-app-pack.schema.json"
test -f "$installed_prefix/share/hideout/schemas/host-app-enablement.schema.json"
test -f "$installed_prefix/share/hideout/host-app/recipes/builtin-vscode.json"
test -f "$installed_prefix/share/hideout/host-app/recipes/safety-profiles.json"
test -f "$installed_prefix/share/hideout/examples/host-app-packs/cursor/hideout.host-app-pack.json"
test -f "$installed_prefix/share/hideout/docs/README.md"
test -f "$installed_prefix/share/hideout/docs/STATUS.md"
cmp internal/runtimecatalog/catalog.json "$installed_prefix/share/hideout/runtime/catalog.json"
cmp internal/runtimecatalog/contract.json "$installed_prefix/share/hideout/runtime/contract.json"
cmp runtime/developer-standard/sources.lock.json "$installed_prefix/share/hideout/runtime/developer-standard/sources.lock.json"
go run ./cmd/hideout-schema-validate "$prefix/schemas/package-manifest.schema.json" "$installed_prefix/share/hideout/package-manifest.json"
"$installed_prefix/bin/hideout" package verify "$installed_prefix" >"$tmp/package-installed-verify.out"
grep -q 'package: ok mode=installed' "$tmp/package-installed-verify.out"
grep -q 'package-prerequisite name=tun2socks status=available' "$tmp/package-installed-verify.out"
grep -q 'packageOwned=true' "$tmp/package-installed-verify.out"
grep -q "\"installPrefix\": \"$installed_prefix_real\"" "$installed_prefix/share/hideout/package-manifest.json"
test -f "$installed_store/install-state.json"
test -f "$installed_store/profiles/default/profile.json"
HIDEOUT_STORE_ROOT="$installed_store" "$installed_prefix/bin/hideout" doctor --backend native --workspace "$workspace" --verbose >"$tmp/package-installed-doctor.out"
grep -q 'store: ok writable' "$tmp/package-installed-doctor.out"
grep -q 'profile: ok default' "$tmp/package-installed-doctor.out"
HIDEOUT_STORE_ROOT="$installed_store" "$installed_prefix/bin/hideout" doctor --backend native --workspace "$workspace" --feature packaging --format json >"$tmp/package-installed-doctor-packaging.json"
grep -q 'package-prerequisite tun2socks=available' "$tmp/package-installed-doctor-packaging.json"
grep -q 'packageOwned=true' "$tmp/package-installed-doctor-packaging.json"
HIDEOUT_STORE_ROOT="$installed_store" "$installed_prefix/bin/hideout" daemon stop >"$tmp/package-installed-daemon-stop.out"

durable_fixture="$installed_store/evidence/keep.json"
mkdir -p "$(dirname "$durable_fixture")"
printf '{"keep":true}\n' >"$durable_fixture"
upgrade_unrelated="$installed_prefix/bin/operator-before-upgrade"
printf 'operator-owned-before-upgrade\n' >"$upgrade_unrelated"
before_upgrade_sha="$(sha256_file "$installed_prefix/bin/hideout")"

# Model the last supported package line before tun2socks became package-owned.
# The installed-state schema is still supported, but the old inventory has no
# helper, helper manifest, or redistributed license. The candidate upgrade
# must add all three without touching durable or unrelated files.
installed_state="$installed_prefix/share/hideout/package-manifest.json"
jq --arg arch "$arch" '
  .package.release.productVersion = "0.1.0-alpha.0" |
  .package.release.channel = "alpha" |
  .package.release.tag = "v0.1.0-alpha.0" |
  .files |= map(select(
    .path != ("bin/tun2socks-linux-" + $arch) and
    .path != ("bin/tun2socks-linux-" + $arch + ".manifest.json") and
    .path != "share/hideout/third_party/tun2socks/LICENSE"
  ))
' "$installed_state" >"$tmp/prior-install-state.json"
cp "$tmp/prior-install-state.json" "$installed_state"
rm -f \
  "$installed_prefix/bin/tun2socks-linux-$arch" \
  "$installed_prefix/bin/tun2socks-linux-$arch.manifest.json" \
  "$installed_prefix/share/hideout/third_party/tun2socks/LICENSE"

"$prefix/install.sh" --prefix "$installed_prefix" --store "$installed_store" --skip-init >"$tmp/package-upgrade.out"
grep -q 'package: upgrade' "$tmp/package-upgrade.out"
test -f "$durable_fixture"
test -f "$upgrade_unrelated"
test -x "$installed_prefix/bin/tun2socks-linux-$arch"
test -f "$installed_prefix/bin/tun2socks-linux-$arch.manifest.json"
test -f "$installed_prefix/share/hideout/third_party/tun2socks/LICENSE"
after_upgrade_sha="$(sha256_file "$installed_prefix/bin/hideout")"
test "$before_upgrade_sha" = "$after_upgrade_sha"
"$installed_prefix/bin/hideout" help update >"$tmp/package-help-update.out"
"$installed_prefix/bin/hideout" help uninstall >"$tmp/package-help-uninstall.out"
grep -q 'brew upgrade vibe-agi/tap/hideout' "$tmp/package-help-update.out"
grep -q 'brew uninstall vibe-agi/tap/hideout' "$tmp/package-help-uninstall.out"
grep -q 'Normal upgrade and uninstall preserve durable state' "$tmp/package-help-uninstall.out"
grep -q -- '--confirm-purge <exact-store>' "$tmp/package-help-uninstall.out"

stale_upgrade="$tmp/package-stale-upgrade"
cp -R "$prefix" "$stale_upgrade"
jq '
  .layout.entrypoints = (.layout.entrypoints | map(select(. != "README.zh-CN.md"))) |
  .files = (.files | map(select(.path != "README.zh-CN.md")))
' "$stale_upgrade/package-manifest.json" >"$stale_upgrade/package-manifest.json.tmp"
mv "$stale_upgrade/package-manifest.json.tmp" "$stale_upgrade/package-manifest.json"
"$stale_upgrade/install.sh" --prefix "$installed_prefix" --store "$installed_store" --skip-init >"$tmp/package-stale-upgrade.out"
grep -q 'stale=1' "$tmp/package-stale-upgrade.out"
grep -q 'obsolete share/hideout/README.zh-CN.md' "$tmp/package-stale-upgrade.out"
test -f "$installed_prefix/share/hideout/README.zh-CN.md"
if "$installed_prefix/bin/hideout" package verify "$installed_prefix" >"$tmp/package-stale-verify.out" 2>"$tmp/package-stale-verify.err"; then
  echo "package-smoke: verify accepted obsolete package-owned file" >&2
  cat "$tmp/package-stale-verify.out" >&2
  exit 1
fi
grep -q 'package repair --prefix' "$tmp/package-stale-verify.err"
HIDEOUT_STORE_ROOT="$installed_store" "$installed_prefix/bin/hideout" doctor --backend native --workspace "$workspace" --feature packaging --format json >"$tmp/package-stale-doctor-packaging.json"
jq -e '
  ([.findings[] | select(
    .checkId == "feature-packaging" and
    .status == "warn" and
    (.summary | contains("installed package verification failed")) and
    (.details.observedFacts | tostring | contains("installedPackageVerification=failed"))
  )] | length) == 1
' "$tmp/package-stale-doctor-packaging.json" >/dev/null
repair_unrelated="$installed_prefix/share/hideout/operator-note.txt"
printf 'operator-owned\n' >"$repair_unrelated"
"$installed_prefix/bin/hideout" package repair --prefix "$installed_prefix" --dry-run >"$tmp/package-repair-dry.out"
grep -q 'package: repair dry-run' "$tmp/package-repair-dry.out"
grep -q 'consider share/hideout/README.zh-CN.md' "$tmp/package-repair-dry.out"
test -f "$installed_prefix/share/hideout/README.zh-CN.md"
test -f "$repair_unrelated"
"$installed_prefix/bin/hideout" package repair --prefix "$installed_prefix" >"$tmp/package-repair.out"
grep -q 'removed share/hideout/README.zh-CN.md' "$tmp/package-repair.out"
grep -q 'durableState=preserved' "$tmp/package-repair.out"
test ! -e "$installed_prefix/share/hideout/README.zh-CN.md"
test -f "$repair_unrelated"
"$installed_prefix/bin/hideout" package verify "$installed_prefix" >"$tmp/package-repaired-verify.out"
grep -q 'package: ok mode=installed' "$tmp/package-repaired-verify.out"
cat >"$tmp/package-repair-summary.json" <<JSON
{
  "obsoleteFile": "share/hideout/README.zh-CN.md",
  "verifyBefore": "failed",
  "dryRunRemoved": false,
  "applyRemovedObsolete": true,
  "verifyAfter": "passed",
  "durableStatePreserved": true,
  "unrelatedFilePreserved": true
}
JSON

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

# A poisoned installed-state path must be rejected before the first
# package-owned file is removed. This proves whole-scope validation, not merely
# that an out-of-root victim survives.
scope_prefix="$tmp/package-scope-installed"
scope_store="$tmp/package-scope-store"
"$prefix/install.sh" --prefix "$scope_prefix" --store "$scope_store" --skip-init \
  >"$tmp/package-scope-install.out"
scope_outside="$tmp/scope-outside.txt"
printf 'outside-must-survive\n' >"$scope_outside"
scope_state="$scope_prefix/share/hideout/package-manifest.json"
jq '
  .obsoleteFiles += [{
    "path": "../scope-outside.txt",
    "kind": "doc",
    "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "executable": false,
    "reason": "adversarial scope fixture"
  }]
' "$scope_state" >"$tmp/package-scope-state.json"
cp "$tmp/package-scope-state.json" "$scope_state"
if "$scope_prefix/bin/hideout" package uninstall --prefix "$scope_prefix" \
  >"$tmp/package-scope-uninstall.out" 2>"$tmp/package-scope-uninstall.err"; then
  echo "package-smoke: uninstall accepted an out-of-root installed-state path" >&2
  exit 1
fi
grep -q 'path must stay inside root' "$tmp/package-scope-uninstall.err"
test -x "$scope_prefix/bin/hideout"
grep -q '^outside-must-survive$' "$scope_outside"

unrelated_installed="$installed_prefix/bin/not-hideout"
printf 'operator-owned\n' >"$unrelated_installed"
"$installed_prefix/bin/hideout" package uninstall --prefix "$installed_prefix" --dry-run >"$tmp/package-uninstall-dry.out"
grep -q 'package: uninstall dry-run' "$tmp/package-uninstall-dry.out"
grep -q 'remove bin/hideout' "$tmp/package-uninstall-dry.out"
grep -q "remove bin/tun2socks-linux-$arch" "$tmp/package-uninstall-dry.out"
test -x "$installed_prefix/bin/hideout"
test -f "$durable_fixture"
"$installed_prefix/bin/hideout" package uninstall --prefix "$installed_prefix" >"$tmp/package-uninstall.out"
grep -q 'durableState=preserved' "$tmp/package-uninstall.out"
test ! -e "$installed_prefix/bin/hideout"
test ! -e "$installed_prefix/bin/tun2socks-linux-$arch"
test ! -e "$installed_prefix/bin/tun2socks-linux-$arch.manifest.json"
test -f "$unrelated_installed"
test -f "$durable_fixture"
"$prefix/install.sh" --prefix "$installed_prefix" --store "$installed_store" --skip-init >"$tmp/package-reinstall-for-purge.out"
"$installed_prefix/bin/hideout" package uninstall --prefix "$installed_prefix" --purge --dry-run \
  >"$tmp/package-uninstall-purge-dry.out"
grep -q 'package: uninstall dry-run' "$tmp/package-uninstall-purge-dry.out"
grep -q "purge store=$installed_store_real" "$tmp/package-uninstall-purge-dry.out"
grep -q -- "--confirm-purge $installed_store_real" "$tmp/package-uninstall-purge-dry.out"
test -x "$installed_prefix/bin/hideout"
test -f "$durable_fixture"
if "$installed_prefix/bin/hideout" package uninstall --prefix "$installed_prefix" --purge \
  >"$tmp/package-uninstall-purge-unconfirmed.out" 2>"$tmp/package-uninstall-purge-unconfirmed.err"; then
  echo "package-smoke: purge without exact store confirmation succeeded" >&2
  exit 1
fi
grep -q 'confirm-purge' "$tmp/package-uninstall-purge-unconfirmed.err"
test -x "$installed_prefix/bin/hideout"
test -f "$durable_fixture"
"$installed_prefix/bin/hideout" package uninstall --prefix "$installed_prefix" \
  --purge --confirm-purge "$installed_store_real" >"$tmp/package-uninstall-purge.out"
grep -q 'durableState=purged' "$tmp/package-uninstall-purge.out"
grep -q "purge store=$installed_store_real" "$tmp/package-uninstall-purge.out"
test ! -e "$installed_store"
cat >"$tmp/package-lifecycle-summary.json" <<JSON
{
  "priorVersionUpgrade": true,
  "packageHelperAdded": true,
  "durableStatePreservedOnUpgrade": true,
  "unrelatedFilePreservedOnUpgrade": true,
  "homebrewGuidance": true,
  "normalUninstallPreservedState": true,
  "purgeDryRunPreservedState": true,
  "purgeRequiredExactStoreConfirmation": true,
  "destructiveScopeRejectedBeforeMutation": true,
  "unrelatedFilePreservedOnUninstall": true
}
JSON

default_installed_prefix="$tmp/package-default-installed"
default_installed_store="$tmp/package-default-store"
"$prefix/install.sh" --prefix "$default_installed_prefix" --store "$default_installed_store" >"$tmp/package-default-install.out"
test -x "$default_installed_prefix/bin/hideout"
test -x "$default_installed_prefix/bin/hideout-shim"
test -x "$default_installed_prefix/bin/hideout-shim-linux-$arch"
test -x "$default_installed_prefix/bin/hideout-hostfsd-linux-$arch"
test -x "$default_installed_prefix/bin/hideout-session-supervisor-linux-$arch"
test -x "$default_installed_prefix/bin/hideout-workspace-portal-linux-$arch"
test -x "$default_installed_prefix/bin/tun2socks-linux-$arch"
test -f "$default_installed_store/install-state.json"
test -f "$default_installed_store/profiles/default/profile.json"
grep -q 'backend: lima' "$tmp/package-default-install.out"
if command -v limactl >/dev/null 2>&1; then
  HIDEOUT_STORE_ROOT="$default_installed_store" "$default_installed_prefix/bin/hideout" doctor --workspace "$workspace" --verbose >"$tmp/package-default-doctor.out"
  grep -q 'backend: ok lima available' "$tmp/package-default-doctor.out"
else
  if HIDEOUT_STORE_ROOT="$default_installed_store" "$default_installed_prefix/bin/hideout" doctor --workspace "$workspace" --verbose >"$tmp/package-default-doctor.out" 2>"$tmp/package-default-doctor.err"; then
    echo "package-smoke: default doctor succeeded without limactl" >&2
    cat "$tmp/package-default-doctor.out" >&2
    exit 1
  fi
  grep -q 'backend: error lima unavailable: limactl is required for lima backend' "$tmp/package-default-doctor.out"
fi
HIDEOUT_STORE_ROOT="$default_installed_store" "$default_installed_prefix/bin/hideout" doctor --fix --dry-run --backend lima --workspace "$workspace" >"$tmp/package-default-lima-doctor-fix-dry.out"
grep -q 'task helper.install.linux-shim: ok' "$tmp/package-default-lima-doctor-fix-dry.out"
grep -q 'task helper.install.linux-hostfsd: ok' "$tmp/package-default-lima-doctor-fix-dry.out"
grep -q 'task helper.install.linux-session-supervisor: ok' "$tmp/package-default-lima-doctor-fix-dry.out"
HIDEOUT_STORE_ROOT="$default_installed_store" "$default_installed_prefix/bin/hideout" daemon stop >"$tmp/package-default-daemon-stop.out"

skip_installed_prefix="$tmp/package-skip-installed"
skip_installed_store="$tmp/package-skip-store"
"$prefix/install.sh" --prefix "$skip_installed_prefix" --store "$skip_installed_store" --skip-init >"$tmp/package-skip-install.out"
test -x "$skip_installed_prefix/bin/hideout"
test -x "$skip_installed_prefix/bin/hideout-shim"
test -x "$skip_installed_prefix/bin/hideout-shim-linux-$arch"
test -x "$skip_installed_prefix/bin/hideout-hostfsd-linux-$arch"
test -x "$skip_installed_prefix/bin/hideout-session-supervisor-linux-$arch"
test -x "$skip_installed_prefix/bin/hideout-workspace-portal-linux-$arch"
test -x "$skip_installed_prefix/bin/tun2socks-linux-$arch"
HIDEOUT_STORE_ROOT="$skip_installed_store" "$skip_installed_prefix/bin/hideout" help >"$tmp/package-help.out"
grep -q 'hideout setup' "$tmp/package-help.out"
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
HIDEOUT_STORE_ROOT="$proxy_installed_store" "$proxy_installed_prefix/bin/hideout" daemon stop >"$tmp/package-proxy-daemon-stop.out"

copy_artifacts
echo "package-smoke: passed"
