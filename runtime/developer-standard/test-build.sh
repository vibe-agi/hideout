#!/usr/bin/env bash
set -euo pipefail

SOURCE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
# shellcheck source=runtime/developer-standard/build-lib.sh
. "$SOURCE_DIR/build-lib.sh"

tmp="$(mktemp -d /tmp/h31-build-test.XXXXXX)"
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT

copy_inputs() {
  local destination="$1"
  mkdir -p "$destination"
  cp "$SOURCE_DIR/sources.lock.json" "$SOURCE_DIR/packages.txt" "$SOURCE_DIR/packages.lock" "$destination/"
}

expect_failure() {
  local name="$1"
  shift
  set +e
  (set -e; "$@") >"$tmp/$name.out" 2>&1
  status=$?
  set -e
  if [ "$status" -eq 0 ]; then
    echo "runtime-build-test: $name unexpectedly succeeded" >&2
    exit 1
  fi
}

runtime_validate_source_tree "$SOURCE_DIR"
runtime_validate_native_builder Linux aarch64
expect_failure wrong-os runtime_validate_native_builder Darwin arm64
expect_failure wrong-arch runtime_validate_native_builder Linux x86_64

no_acceleration_device="$tmp/not-a-device"
: > "$no_acceleration_device"
runtime_configure_libguestfs_backend "$no_acceleration_device"
[ "$LIBGUESTFS_BACKEND" = direct ]
[ "$LIBGUESTFS_BACKEND_SETTINGS" = force_tcg ]
[ "$RUNTIME_LIBGUESTFS_ACCELERATION" = tcg ]
[ "$RUNTIME_LIBGUESTFS_BACKEND_SETTINGS" = force_tcg ]
runtime_configure_libguestfs_backend /dev/null
[ "$LIBGUESTFS_BACKEND" = direct ]
[ -z "${LIBGUESTFS_BACKEND_SETTINGS:-}" ]
[ "$RUNTIME_LIBGUESTFS_ACCELERATION" = kvm ]
[ -z "$RUNTIME_LIBGUESTFS_BACKEND_SETTINGS" ]

case_dir="$tmp/digest"
copy_inputs "$case_dir"
printf '\n' >> "$case_dir/packages.lock"
expect_failure lock-digest runtime_validate_source_tree "$case_dir"

case_dir="$tmp/moving"
copy_inputs "$case_dir"
jq '.base.location="https://cloud.debian.org/images/cloud/trixie/latest/base.qcow2"' "$case_dir/sources.lock.json" > "$case_dir/lock.tmp"
mv "$case_dir/lock.tmp" "$case_dir/sources.lock.json"
expect_failure moving-source runtime_validate_source_tree "$case_dir"

case_dir="$tmp/package-drift"
copy_inputs "$case_dir"
sed '/^zip$/d' "$case_dir/packages.txt" > "$case_dir/packages.tmp"
mv "$case_dir/packages.tmp" "$case_dir/packages.txt"
expect_failure package-drift runtime_validate_source_tree "$case_dir"

case_dir="$tmp/secret"
copy_inputs "$case_dir"
jq '.fixture="HIDEOUT_SECRET_PROXY=cap_0123456789abcdef"' "$case_dir/sources.lock.json" > "$case_dir/lock.tmp"
mv "$case_dir/lock.tmp" "$case_dir/sources.lock.json"
expect_failure secret-fixture runtime_validate_source_tree "$case_dir"
grep -F 'control-plane material' "$tmp/secret-fixture.out" >/dev/null

case_dir="$tmp/package-mirror"
copy_inputs "$case_dir"
jq '.debianSnapshot.packageMirror="https://packages.invalid/debian"' "$case_dir/sources.lock.json" > "$case_dir/lock.tmp"
mv "$case_dir/lock.tmp" "$case_dir/sources.lock.json"
expect_failure package-mirror runtime_validate_source_tree "$case_dir"

case_dir="$tmp/builder-kernel"
copy_inputs "$case_dir"
jq '.builder.packages |= map(select(startswith("linux-image-arm64=") | not))' "$case_dir/sources.lock.json" > "$case_dir/lock.tmp"
mv "$case_dir/lock.tmp" "$case_dir/sources.lock.json"
expect_failure builder-kernel runtime_validate_source_tree "$case_dir"
grep -F 'builder package set is missing linux-image-arm64' "$tmp/builder-kernel.out" >/dev/null

deb_fakebin="$tmp/deb-fakebin"
deb_cache="$tmp/deb-cache"
deb_work="$tmp/deb-work"
mkdir -p "$deb_fakebin" "$deb_cache" "$deb_work"
cat >"$deb_fakebin/dpkg-deb" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
[ "${1:-}" = -f ] || exit 2
case "${3:-}" in
  Package) printf 'fixture\n' ;;
  Version) printf '1.0\n' ;;
  Architecture) printf 'arm64\n' ;;
  *) exit 2 ;;
esac
SH
chmod +x "$deb_fakebin/dpkg-deb"
printf 'fixture-deb\n' >"$deb_cache/fixture_1.0_arm64.deb"
deb_sha="$(runtime_sha256 "$deb_cache/fixture_1.0_arm64.deb")"
deb_bytes="$(wc -c <"$deb_cache/fixture_1.0_arm64.deb" | tr -d '[:space:]')"
cat >"$deb_work/Packages" <<EOF
Package: fixture
Version: 1.0
Architecture: arm64
Filename: pool/f/fixture_1.0_arm64.deb
Size: $deb_bytes
SHA256: $deb_sha
EOF
printf 'fixture=1.0\n' >"$deb_work/packages.lock"
PATH="$deb_fakebin:$PATH" runtime_verify_deb_cache "$deb_work/Packages" "$deb_work/packages.lock" "$deb_cache" "$deb_work"
printf 'tampered-deb\n' >"$deb_cache/fixture_1.0_arm64.deb"
expect_failure deb-digest env PATH="$deb_fakebin:$PATH" bash -euo pipefail -c \
  '. "$1"; runtime_verify_deb_cache "$2" "$3" "$4" "$5"' \
  bash "$SOURCE_DIR/build-lib.sh" "$deb_work/Packages" "$deb_work/packages.lock" "$deb_cache" "$deb_work"

out="$tmp/outputs"
mkdir -p "$out"
while IFS= read -r name; do
  printf 'fixture\n' > "$out/$name"
done < <(runtime_required_outputs 2026.07.0)
runtime_verify_output_contract "$out" 2026.07.0
rm "$out/verification-report.json"
expect_failure missing-output runtime_verify_output_contract "$out" 2026.07.0

bash "$SOURCE_DIR/build.sh" --validate-inputs-only >/dev/null

workflow="$SOURCE_DIR/../../.github/workflows/runtime-developer-standard.yml"
retention_workflow="$SOURCE_DIR/../../.github/workflows/runtime-developer-standard-retain.yml"
grep -q 'rm -f /etc/apt/sources.list.d/\*.sources /etc/apt/sources.list.d/\*.list' "$workflow"
grep -q 'package_mirror=$(jq' "$workflow"
grep -Fq 'REVISION="${REVISION:-2026.07.0}"' "$workflow"
grep -Fq 'RUNTIME_REVISION: ${{ inputs.revision || '\''2026.07.0'\'' }}' "$workflow"
grep -Fq 'candidate-developer-standard-${{ env.RUNTIME_REVISION }}-${{ github.sha }}' "$workflow"
grep -q 'printf "deb \[check-valid-until=no\] %s trixie main' "$workflow"
grep -q -- '--env HIDEOUT_RUNTIME_BUILDER_IMAGE="$builder_image"' "$workflow"
grep -q 'find /var/lib/apt/lists -type f -name' "$workflow"
grep -q 'ca-certificates.crt:/tmp/bootstrap-ca.crt:ro' "$workflow"
grep -q 'cp /tmp/bootstrap-ca.crt /etc/ssl/certs/ca-certificates.crt' "$workflow"
grep -Fq 'github.event.workflow_run.conclusion == '\''success'\''' "$retention_workflow"
grep -Fq 'startsWith(github.event.workflow_run.head_branch, '\''runtime-candidate/031-'\'')' "$retention_workflow"
grep -Fq '.source.commit == $commit' "$retention_workflow"
grep -Fq '.source.dirty == false' "$retention_workflow"
grep -Fq 'sha256sum --check SHA256SUMS' "$retention_workflow"
grep -Fq -- '--draft --title "$title"' "$retention_workflow"
if grep -q 'ca-certificates.crt:/etc/ssl/certs/ca-certificates.crt' "$workflow"; then
  echo "runtime-build-test: workflow must not mount a read-only CA bundle over package configuration" >&2
  exit 1
fi
grep -Fq 'rm -rf /var/lib/cloud/*' "$SOURCE_DIR/build.sh"
grep -Fq 'Acquire::https::CaInfo \"/etc/ssl/certs/ca-certificates.crt\";' "$workflow"
grep -Fq 'Acquire::Check-Valid-Until \"false\";' "$workflow"
grep -Fq 'runtime_prepare_deb_bundle' "$SOURCE_DIR/build.sh"
grep -Fq 'native-unpinned' "$SOURCE_DIR/build.sh"
grep -Fq 'virt-customize --no-network' "$SOURCE_DIR/build.sh"
[ "$(grep -Fc 'virt-customize --no-network' "$SOURCE_DIR/build.sh")" -eq 2 ] || {
  echo "runtime-build-test: every image customization pass must disable networking" >&2
  exit 1
}
grep -Fq 'apt-get install -y --no-download' "$SOURCE_DIR/build.sh"
if grep -Fq 'apt-get update' "$SOURCE_DIR/build.sh"; then
  echo "runtime-build-test: guest image customization must not use the network" >&2
  exit 1
fi
grep -Fq -- '--upload "$work/sources.list:/etc/apt/sources.list"' "$SOURCE_DIR/build.sh"
if grep -Fq -- '--write "/etc/apt/sources.list:$work/sources.list"' "$SOURCE_DIR/build.sh"; then
  echo "runtime-build-test: sources.list must be uploaded; virt-customize --write treats the host path as literal content" >&2
  exit 1
fi
backend_line="$(grep -n '^runtime_configure_libguestfs_backend$' "$SOURCE_DIR/build.sh" | cut -d: -f1)"
preflight_line="$(grep -n '^if ! libguestfs-test-tool ' "$SOURCE_DIR/build.sh" | cut -d: -f1)"
resize_line="$(grep -n '^virt-resize ' "$SOURCE_DIR/build.sh" | cut -d: -f1)"
[ -n "$backend_line" ] && [ -n "$preflight_line" ] && [ -n "$resize_line" ] &&
  [ "$backend_line" -lt "$preflight_line" ] && [ "$preflight_line" -lt "$resize_line" ] || {
  echo "runtime-build-test: direct libguestfs preflight must run before virt-resize" >&2
  exit 1
}
grep -Fq 'libguestfsBackendSettings:$libguestfsBackendSettings' "$SOURCE_DIR/build.sh"
grep -Fq 'libguestfsAcceleration:$libguestfsAcceleration' "$SOURCE_DIR/build.sh"
grep -Fq 'find /home -mindepth 1 -maxdepth 1 -exec rm -rf {} +' "$SOURCE_DIR/build.sh"
grep -Fq 'grep -r -a -E -l -Z -- "^[[:space:]]*(-----BEGIN' "$SOURCE_DIR/build.sh"
grep -Fq -- "--run-command ': > /etc/machine-id'" "$SOURCE_DIR/build.sh"

fakebin="$tmp/fakebin"
mkdir -p "$fakebin"
cat >"$fakebin/qemu-img" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  check) exit 0 ;;
  info) printf '%s\n' '{"format":"qcow2","virtual-size":1073741824}' ;;
  *) echo "unexpected qemu-img invocation: $*" >&2; exit 2 ;;
esac
SH
cat >"$fakebin/guestfish" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
root="${HIDEOUT_RUNTIME_TEST_ROOTFS:?}"
while IFS=' ' read -r operation path _; do
  case "$operation" in
    echo)
      printf '%s\n' "$path"
      ;;
    -statns)
      target="$root$path"
      if [ "${HIDEOUT_RUNTIME_FAIL_LIST_PATH:-}" = "$path" ] || { [ ! -e "$target" ] && [ ! -L "$target" ]; }; then
        echo "guestfish: statns: $path cannot be inspected" >&2
        continue
      fi
      if [ -d "$target" ]; then
        mode=16877
        size=4096
      elif [ -f "$target" ]; then
        mode=33188
        if [ -x "$target" ]; then
          mode=33261
        fi
        size="$(wc -c <"$target" | tr -d ' ')"
      else
        echo "guestfish: statns: $path is neither a regular file nor directory" >&2
        continue
      fi
      printf 'st_mode: %s\nst_size: %s\n' "$mode" "$size"
      ;;
    *)
      echo "unexpected guestfish operation: $operation" >&2
      exit 2
      ;;
  esac
done
SH
cat >"$fakebin/virt-ls" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
root="${HIDEOUT_RUNTIME_TEST_ROOTFS:?}"
path="${*: -1}"
if [ "${HIDEOUT_RUNTIME_FAIL_LIST_PATH:-}" = "$path" ]; then
  exit 1
fi
target="$root$path"
[ -e "$target" ] || [ -L "$target" ] || exit 1
if [ -d "$target" ]; then
  find "$target" -mindepth 1 -maxdepth 1 -exec basename {} \; | LC_ALL=C sort
else
  basename "$target"
fi
SH
cat >"$fakebin/virt-copy-out" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
root="${HIDEOUT_RUNTIME_TEST_ROOTFS:?}"
destination="${*: -1}"
last=$(($# - 1))
for ((index=3; index<=last; index++)); do
  path="${!index}"
  if [ "${HIDEOUT_RUNTIME_FAIL_COPY_PATH:-}" = "$path" ]; then
    exit 1
  fi
  [ -e "$root$path" ] || [ -L "$root$path" ] || exit 1
  cp -R "$root$path" "$destination/"
done
SH
cat >"$fakebin/virt-cat" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
root="${HIDEOUT_RUNTIME_TEST_ROOTFS:?}"
cat "$root${3:?}"
SH
chmod +x "$fakebin"/*

clean_root="$tmp/rootfs-clean"
mkdir -p \
  "$clean_root/bin" \
  "$clean_root/etc/hideout" \
  "$clean_root/etc/ssh" \
  "$clean_root/home" \
  "$clean_root/opt" \
  "$clean_root/root" \
  "$clean_root/srv" \
  "$clean_root/tmp" \
  "$clean_root/usr/bin" \
  "$clean_root/usr/sbin" \
  "$clean_root/usr/local/go" \
  "$clean_root/usr/local/bin" \
  "$clean_root/usr/local/include/node" \
  "$clean_root/usr/local/lib/node_modules/npm" \
  "$clean_root/var/cache" \
  "$clean_root/var/lib/cloud" \
  "$clean_root/var/lib/dpkg" \
  "$clean_root/var/log" \
  "$clean_root/var/spool" \
  "$clean_root/var/tmp"
for path in \
  /usr/bin/bash \
  /bin/sh \
  /usr/bin/cc \
  /usr/bin/curl \
  /usr/bin/find \
  /usr/bin/getent \
  /usr/bin/git \
  /usr/bin/gcc \
  /usr/bin/grep \
  /usr/sbin/iptables \
  /usr/bin/jq \
  /usr/bin/make \
  /usr/bin/mount \
  /usr/bin/pip3 \
  /usr/bin/python3 \
  /usr/bin/setpriv \
  /usr/bin/sha256sum \
  /usr/bin/tar \
  /usr/bin/unshare \
  /usr/bin/unzip \
  /usr/local/bin/go \
  /usr/local/bin/node \
  /usr/local/bin/npm; do
  printf '#!/bin/sh\nexit 0\n' >"$clean_root$path"
  chmod 0755 "$clean_root$path"
done
while IFS='=' read -r package version; do
  printf 'Package: %s\nStatus: install ok installed\nVersion: %s\nArchitecture: arm64\n\n' \
    "$package" "$version" >>"$clean_root/var/lib/dpkg/status"
done <"$SOURCE_DIR/packages.lock"
runtime_emit_package_inventory <"$clean_root/var/lib/dpkg/status" \
  >"$clean_root/etc/hideout/package-inventory.txt"
printf '#define NODE_MAJOR_VERSION 22\n#define NODE_MINOR_VERSION 23\n#define NODE_PATCH_VERSION 1\n' \
  >"$clean_root/usr/local/include/node/node_version.h"
printf '{"version":"10.9.8"}\n' >"$clean_root/usr/local/lib/node_modules/npm/package.json"
: >"$clean_root/usr/local/lib/node_modules/npm/.npmrc"
printf 'go1.26.5\n' >"$clean_root/usr/local/go/VERSION"
: >"$clean_root/etc/machine-id"
image_fixture="$tmp/candidate.qcow2"
printf 'qcow2-fixture\n' >"$image_fixture"
clean_report="$tmp/clean-report.json"
env \
  PATH="$fakebin:$PATH" \
  HIDEOUT_RUNTIME_TEST_ROOTFS="$clean_root" \
  bash "$SOURCE_DIR/verify-image.sh" --image "$image_fixture" --out "$clean_report" \
  >"$tmp/verify-clean.out"
jq -e '
  .offline.machineId == "absent" and
  .offline.sshPrivateHostKeys == "absent" and
  .offline.privateKeyMaterial == "absent" and
  .offline.agentCredentialState == "absent" and
  .offline.cloudInitResidue == "absent" and
  .offline.controlPlaneMaterial == "absent" and
    .offline.requiredTools.resolvedTargets == "regular-nonempty-executable" and
    .offline.requiredTools.versions == "offline-metadata-verified" and
    .offline.activeBuildIdentity.path == "/etc/hideout/package-inventory.txt" and
    (.offline.inspectedRoots | sort == ["/etc", "/home", "/opt", "/root", "/srv", "/tmp", "/usr", "/var/cache", "/var/lib", "/var/log", "/var/spool", "/var/tmp"])
' "$clean_report" >/dev/null

parser_root="$tmp/rootfs-parser-string"
mkdir -p "$parser_root"
cp -R "$clean_root/." "$parser_root/"
printf '\001parser literal: -----BEGIN PRIVATE KEY-----\000\n' >"$parser_root/usr/bin/parser-fixture"
env \
  PATH="$fakebin:$PATH" \
  HIDEOUT_RUNTIME_TEST_ROOTFS="$parser_root" \
  bash "$SOURCE_DIR/verify-image.sh" --image "$image_fixture" --out "$tmp/parser-string-report.json" \
  >"$tmp/verify-parser-string.out"
jq -e '.offline.privateKeyMaterial == "absent"' "$tmp/parser-string-report.json" >/dev/null

expect_image_rejection() {
  local name="$1"
  local rootfs="$2"
  local expected="$3"
  local failed_path="${4:-}"
  local failed_list_path="${5:-}"
  expect_failure "image-$name" env \
    PATH="$fakebin:$PATH" \
    HIDEOUT_RUNTIME_TEST_ROOTFS="$rootfs" \
    HIDEOUT_RUNTIME_FAIL_COPY_PATH="$failed_path" \
    HIDEOUT_RUNTIME_FAIL_LIST_PATH="$failed_list_path" \
    bash "$SOURCE_DIR/verify-image.sh" \
      --image "$image_fixture" \
      --out "$tmp/$name-report.json"
  grep -F "$expected" "$tmp/image-$name.out" >/dev/null
}

for key_type in rsa ecdsa ed25519 dsa; do
  case_root="$tmp/rootfs-ssh-$key_type"
  mkdir -p "$case_root"
  cp -R "$clean_root/." "$case_root/"
  printf 'host-private-key\n' >"$case_root/etc/ssh/ssh_host_${key_type}_key"
  expect_image_rejection "ssh-$key_type" "$case_root" "SSH private host key"
done

case_root="$tmp/rootfs-machine-id"
mkdir -p "$case_root"
cp -R "$clean_root/." "$case_root/"
printf '0123456789abcdef0123456789abcdef\n' >"$case_root/etc/machine-id"
expect_image_rejection machine-id "$case_root" "machine identity"

case_root="$tmp/rootfs-dbus-machine-id"
mkdir -p "$case_root"
cp -R "$clean_root/." "$case_root/"
mkdir -p "$case_root/var/lib/dbus"
printf '0123456789abcdef0123456789abcdef\n' >"$case_root/var/lib/dbus/machine-id"
expect_image_rejection dbus-machine-id "$case_root" "D-Bus machine identity"

case_root="$tmp/rootfs-private-key"
mkdir -p "$case_root/usr/local/share"
cp -R "$clean_root/." "$case_root/"
mkdir -p "$case_root/usr/local/share"
printf '%s\n' '-----BEGIN OPENSSH PRIVATE KEY-----' >"$case_root/usr/local/share/fixture.key"
expect_image_rejection private-key "$case_root" "private-key material"

case_root="$tmp/rootfs-binary-private-key"
mkdir -p "$case_root"
cp -R "$clean_root/." "$case_root/"
mkdir -p "$case_root/usr/local/share"
printf '\001\002\003\004' >"$case_root/usr/local/share/fixture.p12"
expect_image_rejection binary-private-key "$case_root" "private-key material"

case_root="$tmp/rootfs-agent-auth"
mkdir -p "$case_root"
cp -R "$clean_root/." "$case_root/"
mkdir -p "$case_root/home/developer/.codex"
printf '{"token":"fixture"}\n' >"$case_root/home/developer/.codex/auth.json"
expect_image_rejection agent-auth "$case_root" "agent authentication, cache, or credential state"

case_root="$tmp/rootfs-agent-cache"
mkdir -p "$case_root"
cp -R "$clean_root/." "$case_root/"
mkdir -p "$case_root/root/.claude/cache"
printf 'fixture\n' >"$case_root/root/.claude/cache/state"
expect_image_rejection agent-cache "$case_root" "agent authentication, cache, or credential state"

case_root="$tmp/rootfs-credential"
mkdir -p "$case_root"
cp -R "$clean_root/." "$case_root/"
mkdir -p "$case_root/home/developer/.aws"
printf 'secret=fixture\n' >"$case_root/home/developer/.aws/credentials"
expect_image_rejection credential "$case_root" "agent authentication, cache, or credential state"

case_root="$tmp/rootfs-system-agent-auth"
mkdir -p "$case_root"
cp -R "$clean_root/." "$case_root/"
mkdir -p "$case_root/etc/codex"
printf '{"token":"fixture"}\n' >"$case_root/etc/codex/auth.json"
expect_image_rejection system-agent-auth "$case_root" "agent authentication, cache, or credential state"

case_root="$tmp/rootfs-installed-agent-state"
mkdir -p "$case_root"
cp -R "$clean_root/." "$case_root/"
mkdir -p "$case_root/usr/local/share/codex/cache"
printf 'fixture\n' >"$case_root/usr/local/share/codex/cache/state"
expect_image_rejection installed-agent-state "$case_root" "agent authentication, cache, or credential state"

case_root="$tmp/rootfs-generic-credential-state"
mkdir -p "$case_root"
cp -R "$clean_root/." "$case_root/"
mkdir -p "$case_root/usr/local/share/tool-state"
printf '{"token":"fixture"}\n' >"$case_root/usr/local/share/tool-state/auth.json"
expect_image_rejection generic-credential-state "$case_root" "agent authentication, cache, or credential state"

case_root="$tmp/rootfs-cloud"
mkdir -p "$case_root"
cp -R "$clean_root/." "$case_root/"
mkdir -p "$case_root/var/lib/cloud/instances/i-fixture"
printf 'instance-id: i-fixture\n' >"$case_root/var/lib/cloud/instances/i-fixture/meta-data"
expect_image_rejection cloud-residue "$case_root" "cloud-init residue"

case_root="$tmp/rootfs-control-plane"
mkdir -p "$case_root"
cp -R "$clean_root/." "$case_root/"
printf 'HIDEOUT_SECRET_PROXY=cap_0123456789abcdef\n' >"$case_root/etc/hideout-secret"
expect_image_rejection control-plane "$case_root" "control-plane material"

case_root="$tmp/rootfs-log-control-plane"
mkdir -p "$case_root"
cp -R "$clean_root/." "$case_root/"
printf 'HIDEOUT_SECRET_PROXY=cap_0123456789abcdef\n' >"$case_root/var/log/agent.log"
expect_image_rejection log-control-plane "$case_root" "control-plane material"

case_root="$tmp/rootfs-log-private-key"
mkdir -p "$case_root"
cp -R "$clean_root/." "$case_root/"
printf '%s\n' '-----BEGIN OPENSSH PRIVATE KEY-----' >"$case_root/var/log/debug.log"
expect_image_rejection log-private-key "$case_root" "private-key material"

expect_image_rejection inspection-failure "$clean_root" "could not copy required system roots" /usr
expect_image_rejection inspection-list-failure "$clean_root" "could not inspect required image root /home" "" /home
echo "runtime-build-test: passed"
