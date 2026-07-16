#!/usr/bin/env bash
set -euo pipefail

SOURCE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
# shellcheck source=runtime/developer-standard/build-lib.sh
. "$SOURCE_DIR/build-lib.sh"

image=""
out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --image) image="${2:-}"; shift 2 ;;
    --out) out="${2:-}"; shift 2 ;;
    -h | --help) echo "usage: verify-image.sh --image <qcow2> --out <report.json>"; exit 0 ;;
    *) runtime_die "unknown verify-image argument $1"; exit 2 ;;
  esac
done
[ -f "$image" ] || runtime_die "candidate image is required"
[ -n "$out" ] || runtime_die "verification report path is required"
for command in awk chmod cmp find grep guestfish jq qemu-img sort virt-cat virt-copy-out virt-ls; do
  runtime_require_command "$command"
done

qemu-img check -q "$image"
info="$(qemu-img info --output=json "$image")"
[ "$(jq -r '.format' <<<"$info")" = "qcow2" ] || runtime_die "candidate is not qcow2"
virtual_bytes="$(jq -r '."virtual-size"' <<<"$info")"
image_bytes="$(wc -c < "$image" | tr -d ' ')"
[ "$virtual_bytes" -le 17179869184 ] || runtime_die "candidate virtual size exceeds 16 GiB"
[ "$image_bytes" -le 4294967296 ] || runtime_die "candidate compressed size exceeds 4 GiB"

required_paths=(
  /usr/bin/bash
  /bin/sh
  /usr/bin/cc
  /usr/bin/curl
  /usr/bin/find
  /usr/bin/getent
  /usr/bin/git
  /usr/bin/gcc
  /usr/bin/grep
  /usr/sbin/iptables
  /usr/bin/jq
  /usr/bin/make
  /usr/bin/mount
  /usr/bin/pip3
  /usr/bin/python3
  /usr/bin/setpriv
  /usr/bin/sha256sum
  /usr/bin/tar
  /usr/bin/unshare
  /usr/bin/unzip
  /usr/local/bin/go
  /usr/local/bin/node
  /usr/local/bin/npm
)
required_components=(
  bash dash gcc curl findutils libc-bin git gcc grep iptables jq make util-linux python3-pip
  python3 util-linux coreutils tar util-linux unzip go node npm
)
[ "${#required_paths[@]}" -eq "${#required_components[@]}" ] || runtime_die "internal required-tool contract is inconsistent"

system_roots=(/etc /home /root /usr /opt /srv /tmp)
state_roots=(/var/lib /var/cache /var/spool /var/log /var/tmp)

scan_dir="$(mktemp -d /tmp/h31-scan.XXXXXX)"
cleanup() { rm -rf "$scan_dir"; }
trap cleanup EXIT

tool_stat_commands="$scan_dir/tool-stat.commands"
tool_stats="$scan_dir/tool-stats"
for path in "${required_paths[@]}"; do
  printf 'echo __HIDEOUT_TOOL__:%s\n-statns %s\n' "$path" "$path" >>"$tool_stat_commands"
done
for path in "${system_roots[@]}" "${state_roots[@]}"; do
  printf 'echo __HIDEOUT_TOOL__:%s\n-statns %s\n' "$path" "$path" >>"$tool_stat_commands"
done
if ! guestfish --ro -a "$image" -i <"$tool_stat_commands" >"$tool_stats" 2>"$scan_dir/tool-stats.err"; then
  runtime_die "could not inspect required image tools"
fi

tool_stat_value() {
  local path="$1"
  local field="$2"
  awk -v marker="__HIDEOUT_TOOL__:$path" -v field="$field:" '
    $0 == marker { active=1; next }
    active && index($0, "__HIDEOUT_TOOL__:") == 1 { exit }
    active && $1 == field { print $2; exit }
  ' "$tool_stats"
}

for path in "${required_paths[@]}"; do
  mode="$(tool_stat_value "$path" st_mode)"
  size="$(tool_stat_value "$path" st_size)"
  if ! [[ "$mode" =~ ^[0-9]+$ ]] || ! [[ "$size" =~ ^[0-9]+$ ]]; then
    runtime_die "candidate required tool does not resolve to a regular executable: $path"
  fi
  mode_number=$((10#$mode))
  if (( (mode_number & 0170000) != 0100000 || (mode_number & 0111) == 0 )); then
    runtime_die "candidate required tool does not resolve to a regular executable: $path"
  fi
  if (( 10#$size == 0 )); then
    runtime_die "candidate required tool is empty: $path"
  fi
done

for path in "${system_roots[@]}" "${state_roots[@]}"; do
  mode="$(tool_stat_value "$path" st_mode)"
  if ! [[ "$mode" =~ ^[0-9]+$ ]] || (( (10#$mode & 0170000) != 0040000 )); then
    runtime_die "could not inspect required image root $path"
  fi
done

if ! virt-copy-out -a "$image" "${system_roots[@]}" "$scan_dir" >/dev/null 2>&1; then
  runtime_die "could not copy required system roots"
fi
mkdir -p "$scan_dir/var"
if ! virt-copy-out -a "$image" "${state_roots[@]}" "$scan_dir/var" >/dev/null 2>&1; then
  runtime_die "could not copy required state roots"
fi

if ! chmod -R u+rX "$scan_dir"; then
  runtime_die "could not make copied image state readable for inspection"
fi

scan_roots=(
  "$scan_dir/etc"
  "$scan_dir/home"
  "$scan_dir/root"
  "$scan_dir/usr"
  "$scan_dir/opt"
  "$scan_dir/srv"
  "$scan_dir/tmp"
  "$scan_dir/var/lib"
  "$scan_dir/var/cache"
  "$scan_dir/var/spool"
  "$scan_dir/var/log"
  "$scan_dir/var/tmp"
)

dpkg_status="$scan_dir/var/lib/dpkg/status"
embedded_inventory="$scan_dir/etc/hideout/package-inventory.txt"
[ -f "$dpkg_status" ] && [ ! -L "$dpkg_status" ] || runtime_die "candidate package database is missing or unsafe"
[ -s "$embedded_inventory" ] && [ ! -L "$embedded_inventory" ] || runtime_die "candidate active-build package inventory is missing or unsafe"
runtime_emit_package_inventory <"$dpkg_status" >"$scan_dir/expected-package-inventory.txt"
if ! cmp -s "$scan_dir/expected-package-inventory.txt" "$embedded_inventory"; then
  runtime_die "candidate active-build package inventory does not match installed package state"
fi
package_inventory_sha="$(runtime_sha256 "$embedded_inventory")"

while IFS='=' read -r package expected_version; do
  actual_version="$(runtime_dpkg_installed_version "$package" "$dpkg_status")"
  if [ "$actual_version" != "$expected_version" ]; then
    runtime_die "candidate locked package version mismatch for $package: want $expected_version got ${actual_version:-missing}"
  fi
done <"$SOURCE_DIR/packages.lock"

node_header="$scan_dir/usr/local/include/node/node_version.h"
[ -s "$node_header" ] && [ ! -L "$node_header" ] || runtime_die "candidate Node.js version metadata is missing or unsafe"
node_major="$(awk '$1 == "#define" && $2 == "NODE_MAJOR_VERSION" { print $3; exit }' "$node_header")"
node_minor="$(awk '$1 == "#define" && $2 == "NODE_MINOR_VERSION" { print $3; exit }' "$node_header")"
node_patch="$(awk '$1 == "#define" && $2 == "NODE_PATCH_VERSION" { print $3; exit }' "$node_header")"
node_version="$node_major.$node_minor.$node_patch"
expected_node_version="$(jq -r '.node.version' "$SOURCE_DIR/sources.lock.json")"
[ "$node_version" = "$expected_node_version" ] || runtime_die "candidate Node.js version mismatch: want $expected_node_version got $node_version"

npm_manifest="$scan_dir/usr/local/lib/node_modules/npm/package.json"
[ -s "$npm_manifest" ] && [ ! -L "$npm_manifest" ] || runtime_die "candidate npm version metadata is missing or unsafe"
npm_version="$(jq -er '.version | select(type == "string")' "$npm_manifest")" || runtime_die "candidate npm version metadata is invalid"
expected_npm_version="$(jq -r '.node.npmVersion' "$SOURCE_DIR/sources.lock.json")"
[ "$npm_version" = "$expected_npm_version" ] || runtime_die "candidate npm version mismatch: want $expected_npm_version got $npm_version"

go_version_file="$scan_dir/usr/local/go/VERSION"
[ -s "$go_version_file" ] && [ ! -L "$go_version_file" ] || runtime_die "candidate Go version metadata is missing or unsafe"
go_version="$(sed -n '1{s/^go//;p;}' "$go_version_file")"
expected_go_version="$(jq -r '.go.version' "$SOURCE_DIR/sources.lock.json")"
[ "$go_version" = "$expected_go_version" ] || runtime_die "candidate Go version mismatch: want $expected_go_version got $go_version"

for machine_id_path in "$scan_dir/etc/machine-id"; do
  if [ -e "$machine_id_path" ] || [ -L "$machine_id_path" ]; then
    [ ! -L "$machine_id_path" ] || runtime_die "candidate machine identity path is a symlink"
    machine_id="$(tr -d '[:space:]' <"$machine_id_path")" || runtime_die "could not inspect machine identity"
    [ -z "$machine_id" ] || runtime_die "candidate contains machine identity state"
  fi
done
if virt-ls -a "$image" /var/lib/dbus/machine-id >/dev/null 2>&1; then
  dbus_machine_id="$(virt-cat -a "$image" /var/lib/dbus/machine-id 2>/dev/null | tr -d '[:space:]')" || runtime_die "could not inspect D-Bus machine identity"
  [ -z "$dbus_machine_id" ] || runtime_die "candidate contains D-Bus machine identity state"
fi

if ! ssh_private_key="$(find "$scan_dir/etc/ssh" -name 'ssh_host_*_key' ! -name '*.pub' -print -quit)"; then
  runtime_die "could not inspect SSH host-key state"
fi
[ -z "$ssh_private_key" ] || runtime_die "candidate contains an SSH private host key"

private_key_pattern='^[[:space:]]*(-----BEGIN (OPENSSH |RSA |EC |DSA |ENCRYPTED )?PRIVATE KEY-----|PuTTY-User-Key-File-[0-9]+:|BEGIN SSH2 ENCRYPTED PRIVATE KEY)'
set +e
LC_ALL=C grep -r -a -E -q -- "$private_key_pattern" \
  "${scan_roots[@]}"
private_key_status=$?
set -e
case "$private_key_status" in
  0) runtime_die "candidate contains private-key material" ;;
  1) ;;
  *) runtime_die "could not inspect image roots for private-key material" ;;
esac
if ! private_key_file="$(find \
  "${scan_roots[@]}" \
  -type f \( \
    -name '*.key' -o -name '*.p12' -o -name '*.pfx' -o \
    -name id_dsa -o -name id_ecdsa -o -name id_ed25519 -o -name id_rsa \
  \) -print -quit)"; then
  runtime_die "could not inspect image roots for private-key files"
fi
[ -z "$private_key_file" ] || runtime_die "candidate contains private-key material"

if ! agent_state="$(find "${scan_roots[@]}" \
  \( -type d \( \
    -name .aws -o -name .cache -o -name .claude -o -name .codex -o \
    -name .continue -o -name .cursor -o -name .docker -o -name .gemini -o \
    -name .kube -o -name .npm -o -name .ssh -o -name .aider -o \
    -name claude -o -name codex -o -name continue -o -name cursor -o \
    -name gemini -o -name gcloud -o -name gh -o -name opencode \
  \) -o -type f \( \
    -name .git-credentials -o -name .netrc -o -name .pypirc -o \
    -name auth.json -o -name credentials -o -name credentials.json -o -name tokens.json \
  \) \) -print -quit)"; then
  runtime_die "could not inspect image roots for agent authentication, cache, or credential state"
fi
[ -z "$agent_state" ] || runtime_die "candidate contains agent authentication, cache, or credential state"

npmrc_list="$scan_dir/npmrc-files"
if ! find "${scan_roots[@]}" -type f -name .npmrc -print0 >"$npmrc_list"; then
  runtime_die "could not inspect image roots for npm authentication configuration"
fi
npm_auth_pattern='(^|[/:[:space:]])(_auth(token)?|username|password|token|certfile|keyfile)[[:space:]]*='
while IFS= read -r -d '' npmrc; do
  set +e
  LC_ALL=C grep -a -E -i -q -- "$npm_auth_pattern" "$npmrc"
  npm_auth_status=$?
  set -e
  case "$npm_auth_status" in
    0) runtime_die "candidate contains npm authentication configuration" ;;
    1) ;;
    *) runtime_die "could not inspect npm authentication configuration" ;;
  esac
done <"$npmrc_list"

cloud_residue=""
if [ -d "$scan_dir/var/lib/cloud" ] && ! cloud_residue="$(find "$scan_dir/var/lib/cloud" -mindepth 1 -print -quit)"; then
  runtime_die "could not inspect cloud-init state"
fi
if [ -n "$cloud_residue" ]; then
  runtime_die "candidate contains cloud-init residue"
fi
if ! cloud_log_residue="$(find "$scan_dir/var/log" -type f -name 'cloud-init*.log' -size +0c -print -quit)"; then
  runtime_die "could not inspect cloud-init logs"
fi
if [ -n "$cloud_log_residue" ]; then
  runtime_die "candidate contains cloud-init residue"
fi

set +e
LC_ALL=C grep -r -a -E -i -q \
  'HIDEOUT_SECRET_[A-Z0-9_]*|(^|[^[:alnum:]_])(cap|claim|ui)_[0-9a-f]{16,}([^[:alnum:]_]|$)|(capabilityToken|brokerToken|managerToken|uiToken|claimToken|setupPrivateKey|setupToken|setupCredential|rootControlSSH|rootControlSSHConfig)[[:space:]]*[:=]' \
  "${scan_roots[@]}"
control_plane_status=$?
set -e
case "$control_plane_status" in
  0) runtime_die "candidate contains control-plane material" ;;
  1) ;;
  *) runtime_die "could not inspect image roots for control-plane material" ;;
esac

image_sha="$(runtime_sha256 "$image")"
jq -n \
  --arg schema "hideout.runtime-image-verification/v1" \
  --arg image "$(basename "$image")" \
  --arg sha256 "$image_sha" \
  --arg packageInventorySHA256 "$package_inventory_sha" \
  --arg nodeVersion "$node_version" \
  --arg npmVersion "$npm_version" \
  --arg goVersion "$go_version" \
  --arg observedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --argjson bytes "$image_bytes" \
  --argjson virtualBytes "$virtual_bytes" \
  --argjson requiredCount "${#required_paths[@]}" \
  '{schema:$schema,image:$image,sha256:$sha256,observedAt:$observedAt,format:"qcow2",bytes:$bytes,virtualBytes:$virtualBytes,offline:{qemuCheck:"passed",requiredTools:{status:"passed",resolvedTargets:"regular-nonempty-executable",versions:"offline-metadata-verified",count:$requiredCount},requiredPaths:"passed",requiredCount:$requiredCount,inspectedRoots:["/etc","/home","/root","/usr","/opt","/srv","/tmp","/var/lib","/var/cache","/var/spool","/var/log","/var/tmp"],activeBuildIdentity:{path:"/etc/hideout/package-inventory.txt",sha256:$packageInventorySHA256,installedState:"matched"},componentVersions:{node:$nodeVersion,npm:$npmVersion,go:$goVersion,lockedDebianPackages:"matched"},machineId:"absent",sshPrivateHostKeys:"absent",privateKeyMaterial:"absent",agentCredentialState:"absent",cloudInitResidue:"absent",controlPlaneMaterial:"absent"},boot:{status:"not-run",reason:"clean Lima boot and the artifact-bound runtime identity observation are separate candidate-review steps"}}' \
  > "$out"
echo "runtime-verify-image: passed $image_sha"
