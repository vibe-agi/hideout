#!/usr/bin/env bash
set -euo pipefail

SOURCE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
# shellcheck source=runtime/developer-standard/build-lib.sh
. "$SOURCE_DIR/build-lib.sh"

revision="2026.07.0"
out_dir=""
allow_dirty=false
validate_only=false

usage() {
  echo "usage: runtime/developer-standard/build.sh --out-dir <dir> [--revision <id>] [--allow-dirty] [--validate-inputs-only]" >&2
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --out-dir) out_dir="${2:-}"; shift 2 ;;
    --revision) revision="${2:-}"; shift 2 ;;
    --allow-dirty) allow_dirty=true; shift ;;
    --validate-inputs-only) validate_only=true; shift ;;
    -h | --help) usage; exit 0 ;;
    *) usage; exit 2 ;;
  esac
done

runtime_validate_source_tree "$SOURCE_DIR"
if $validate_only; then
  echo "runtime-build: inputs valid"
  exit 0
fi

[ -n "$out_dir" ] || { usage; exit 2; }
[[ "$revision" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || runtime_die "invalid revision $revision"
runtime_validate_native_builder "$(uname -s)" "$(uname -m)"
for command in apt-get curl dpkg-deb git jq libguestfs-test-tool qemu-img tar virt-cat virt-copy-out virt-filesystems virt-resize virt-customize xz; do
  runtime_require_command "$command"
done
export LIBGUESTFS_BACKEND=direct
if ! libguestfs-test-tool >/dev/null 2>&1; then
  runtime_die "libguestfs appliance preflight failed; use the locked privileged Linux arm64 builder or a host whose appliance kernel is readable"
fi

repo_root="$(CDPATH= cd -- "$SOURCE_DIR/../.." && pwd)"
dirty=false
if [ -n "$(git -C "$repo_root" status --porcelain --untracked-files=all)" ]; then
  dirty=true
fi
if $dirty && ! $allow_dirty; then
  runtime_die "source tree is dirty; candidate builds require --allow-dirty and cannot be promoted"
fi
commit="$(git -C "$repo_root" rev-parse --verify HEAD)"
source_lock_sha="$(runtime_sha256 "$SOURCE_DIR/sources.lock.json")"
expected_builder_image="$(jq -r '.builder.baseImage' "$SOURCE_DIR/sources.lock.json")"
observed_builder_image="${HIDEOUT_RUNTIME_BUILDER_IMAGE:-native-unpinned}"
if ! $allow_dirty && [ "$observed_builder_image" != "$expected_builder_image" ]; then
  runtime_die "clean candidates require the locked builder identity from the attested CI build"
fi
virtual_bytes="$(jq -r '.output.virtualBytes' "$SOURCE_DIR/sources.lock.json")"
available_kb="$(df -Pk "$(dirname "$out_dir")" | awk 'NR==2 {print $4}')"
# The expanded QCOW2 is sparse. Bound real workspace demand instead of
# requiring its full virtual size as physical free space.
base_bytes="$(jq -r '.base.bytes' "$SOURCE_DIR/sources.lock.json")"
required_kb="$(( (base_bytes + 8589934592) / 1024 ))"
[ "$available_kb" -ge "$required_kb" ] || runtime_die "insufficient builder disk: need ${required_kb}KiB, have ${available_kb}KiB"

mkdir -p "$out_dir"
out_dir="$(CDPATH= cd -- "$out_dir" && pwd)"
started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
cache="$out_dir/.cache"
work="$out_dir/.work-${revision}"
rm -rf "$work"
mkdir -p "$cache" "$work"
cleanup() {
  status=$?
  trap - EXIT
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT

lock="$SOURCE_DIR/sources.lock.json"
base="$cache/base.qcow2"
release="$cache/debian-Release"
packages="$cache/Packages.xz"
node_archive="$cache/node.tar.xz"
go_archive="$cache/go.tar.gz"
runtime_fetch_locked "$(jq -r '.base.location' "$lock")" "$base" sha256 "$(jq -r '.base.sha256' "$lock")"
[ "$(runtime_sha512 "$base")" = "$(jq -r '.base.sha512' "$lock")" ] || runtime_die "base SHA-512 mismatch"
[ "$(wc -c < "$base" | tr -d ' ')" = "$(jq -r '.base.bytes' "$lock")" ] || runtime_die "base size mismatch"
runtime_fetch_locked "$(jq -r '.debianSnapshot.releaseLocation' "$lock")" "$release" sha256 "$(jq -r '.debianSnapshot.releaseSHA256' "$lock")"
runtime_fetch_locked "$(jq -r '.debianSnapshot.packagesLocation' "$lock")" "$packages" sha256 "$(jq -r '.debianSnapshot.packagesSHA256' "$lock")"
release_packages_sha="$(awk '/^SHA256:/{inside=1;next} inside && /^[A-Za-z0-9-]+:/{exit} inside && $3=="main/binary-arm64/Packages.xz" {print $1; exit}' "$release")"
[ "$release_packages_sha" = "$(runtime_sha256 "$packages")" ] || runtime_die "Release does not bind Packages.xz"
runtime_fetch_locked "$(jq -r '.node.location' "$lock")" "$node_archive" sha256 "$(jq -r '.node.sha256' "$lock")"
runtime_fetch_locked "$(jq -r '.go.location' "$lock")" "$go_archive" sha256 "$(jq -r '.go.sha256' "$lock")"
deb_bundle="$work/debian-packages.tar"
runtime_prepare_deb_bundle "$lock" "$packages" "$SOURCE_DIR/packages.lock" "$cache" "$work" "$deb_bundle"

expanded="$work/expanded.qcow2"
qemu-img create -q -f qcow2 "$expanded" "$virtual_bytes"
virt-resize --expand /dev/sda1 "$base" "$expanded"

snapshot="$(jq -r '.debianSnapshot.timestamp' "$lock")"
cat >"$work/sources.list" <<EOF
deb [check-valid-until=no] https://snapshot.debian.org/archive/debian/${snapshot} trixie main
EOF
cat >"$work/99hideout-snapshot" <<'EOF'
Acquire::Check-Valid-Until "false";
Acquire::Retries "3";
APT::Install-Recommends "false";
EOF
package_args="$(paste -sd ' ' "$SOURCE_DIR/packages.lock")"
apt_packages_list="snapshot.debian.org_archive_debian_${snapshot}_dists_trixie_main_binary-arm64_Packages"
virt-customize --no-network -a "$expanded" \
  --upload "$work/sources.list:/etc/apt/sources.list" \
  --upload "$work/99hideout-snapshot:/etc/apt/apt.conf.d/99hideout-snapshot" \
  --upload "$packages:/tmp/hideout-Packages.xz" \
  --upload "$deb_bundle:/tmp/hideout-debian-packages.tar" \
  --upload "$node_archive:/tmp/hideout-node.tar.xz" \
  --upload "$go_archive:/tmp/hideout-go.tar.gz" \
  --run-command "rm -f /etc/apt/sources.list.d/*.sources /etc/apt/sources.list.d/*.list; rm -rf /var/lib/apt/lists/*; mkdir -p /var/lib/apt/lists/partial /var/cache/apt/archives/partial; xz -dc /tmp/hideout-Packages.xz > /var/lib/apt/lists/$apt_packages_list; tar -xf /tmp/hideout-debian-packages.tar -C /var/cache/apt/archives; export DEBIAN_FRONTEND=noninteractive; apt-get install -y --no-download --no-install-recommends $package_args" \
  --run-command 'tar -xJf /tmp/hideout-node.tar.xz --strip-components=1 -C /usr/local' \
  --run-command 'rm -rf /usr/local/go && tar -xzf /tmp/hideout-go.tar.gz -C /usr/local && ln -sfn /usr/local/go/bin/go /usr/local/bin/go && ln -sfn /usr/local/go/bin/gofmt /usr/local/bin/gofmt' \
  --run-command 'rm -f /tmp/hideout-Packages.xz /tmp/hideout-debian-packages.tar /tmp/hideout-node.tar.xz /tmp/hideout-go.tar.gz; apt-get clean; rm -rf /var/lib/apt/lists/* /var/cache/apt/archives/* /tmp/* /var/tmp/*' \
  --run-command 'rm -f /etc/ssh/ssh_host_* /etc/ssl/private/* /var/lib/dbus/machine-id; : > /etc/machine-id; ln -s /etc/machine-id /var/lib/dbus/machine-id; rm -rf /var/lib/cloud/* /root/.ssh /root/.aws /root/.cache /root/.claude /root/.codex /root/.continue /root/.cursor /root/.docker /root/.gemini /root/.kube /root/.npm /root/.aider /etc/claude /etc/codex /etc/continue /etc/cursor /etc/gemini /etc/opencode; rm -f /root/.git-credentials /root/.netrc /root/.npmrc /root/.pypirc; find /home -mindepth 1 -maxdepth 1 -exec rm -rf {} +; find /var/log -type f -exec truncate -s 0 {} +' \
  --run-command 'find /usr/local -type f \( -name "*.key" -o -name "*.p12" -o -name "*.pfx" -o -name id_dsa -o -name id_ecdsa -o -name id_ed25519 -o -name id_rsa -o -name auth.json -o -name credentials -o -name credentials.json -o -name tokens.json -o -name .netrc -o -name .pypirc -o -name .git-credentials \) -delete; find /usr/local -depth -type d \( -name .aws -o -name .cache -o -name .claude -o -name .codex -o -name .continue -o -name .cursor -o -name .docker -o -name .gemini -o -name .kube -o -name .npm -o -name .aider -o -name claude -o -name codex -o -name continue -o -name cursor -o -name gemini -o -name gcloud -o -name gh -o -name opencode \) -exec rm -rf {} +; grep -r -a -E -l -Z -- "^[[:space:]]*(-----BEGIN (OPENSSH |RSA |EC |DSA |ENCRYPTED )?PRIVATE KEY-----|PuTTY-User-Key-File-[0-9]+:|BEGIN SSH2 ENCRYPTED PRIVATE KEY)" /etc /home /root /usr /opt /srv /tmp /var/lib /var/cache /var/spool /var/log /var/tmp | xargs -0 -r rm -f'

virt-cat -a "$expanded" /var/lib/dpkg/status |
  runtime_emit_package_inventory > "$out_dir/package-inventory.txt"
inventory_sha="$(runtime_sha256 "$out_dir/package-inventory.txt")"
virt-customize --no-network -a "$expanded" \
  --mkdir /etc/hideout \
  --upload "$out_dir/package-inventory.txt:/etc/hideout/package-inventory.txt" \
  --chmod '0644:/etc/hideout/package-inventory.txt' \
  --run-command ': > /etc/machine-id'

image_name="developer-standard-${revision}-linux-aarch64.qcow2"
image="$out_dir/$image_name"
rm -f "$image"
qemu-img convert -q -O qcow2 -c "$expanded" "$image"
image_sha="$(runtime_sha256 "$image")"
jq -n \
  --arg schema "hideout.runtime-components/v1" \
  --arg revision "$revision" \
  --arg base "$(jq -r '.base.location' "$lock")" \
  --arg baseSHA256 "$(jq -r '.base.sha256' "$lock")" \
  --arg nodeVersion "$(jq -r '.node.version' "$lock")" \
  --arg nodeSHA256 "$(jq -r '.node.sha256' "$lock")" \
  --arg goVersion "$(jq -r '.go.version' "$lock")" \
  --arg goSHA256 "$(jq -r '.go.sha256' "$lock")" \
  --arg inventorySHA256 "$inventory_sha" \
  '{schema:$schema,revision:$revision,components:[{kind:"base-image",location:$base,sha256:$baseSHA256,license:"Debian main/free software"},{kind:"node",version:$nodeVersion,sha256:$nodeSHA256,license:"Node.js upstream distribution"},{kind:"go",version:$goVersion,sha256:$goSHA256,license:"Go upstream distribution"},{kind:"debian-package-inventory",sha256:$inventorySHA256,license:"per-package Debian metadata"}]}' \
  > "$out_dir/component-manifest.json"

jq -n '{schema:"hideout.runtime-sbom-status/v1",available:false,status:"unavailable-preview",reason:"candidate builder does not yet produce a reviewed SBOM; supported maturity is forbidden"}' > "$out_dir/sbom-status.json"

"$SOURCE_DIR/verify-image.sh" --image "$image" --out "$out_dir/verification-report.json"

jq -n \
  --arg schema "hideout.runtime-build-provenance/v1" \
  --arg revision "$revision" \
  --arg commit "$commit" \
  --argjson dirty "$dirty" \
  --arg sourceLockSHA256 "$source_lock_sha" \
  --arg builderImage "$observed_builder_image" \
  --arg expectedBuilderImage "$expected_builder_image" \
  --arg image "$image_name" \
  --arg imageSHA256 "$image_sha" \
  --arg startedAt "$started_at" \
  --arg completedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg qemu "$(qemu-img --version | head -n1)" \
  --arg libguestfs "$(virt-customize --version | head -n1)" \
  --argjson imageBytes "$(wc -c < "$image" | tr -d ' ')" \
  '{schema:$schema,revision:$revision,source:{commit:$commit,dirty:$dirty,sourceLockSHA256:$sourceLockSHA256},builder:{observedIdentity:$builderImage,expectedIdentity:$expectedBuilderImage,attestation:(if $builderImage == $expectedBuilderImage then "workflow-declared" else "native-unpinned" end),qemu:$qemu,libguestfs:$libguestfs},output:{file:$image,sha256:$imageSHA256,bytes:$imageBytes},startedAt:$startedAt,completedAt:$completedAt,promoted:false}' \
  > "$out_dir/build-provenance.json"

(
  cd "$out_dir"
  for file in "$image_name" package-inventory.txt component-manifest.json sbom-status.json build-provenance.json verification-report.json; do
    printf '%s  %s\n' "$(runtime_sha256 "$file")" "$file"
  done > SHA256SUMS
)
runtime_verify_output_contract "$out_dir" "$revision"
echo "runtime-build: candidate ready at $out_dir/$image_name"
