#!/usr/bin/env bash

runtime_die() {
  echo "runtime-build: $*" >&2
  return 1
}

runtime_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

runtime_sha512() {
  if command -v sha512sum >/dev/null 2>&1; then
    sha512sum "$1" | awk '{print $1}'
  else
    shasum -a 512 "$1" | awk '{print $1}'
  fi
}

runtime_require_command() {
  command -v "$1" >/dev/null 2>&1 || runtime_die "missing required command: $1"
}

runtime_validate_native_builder() {
  local os_name="$1"
  local arch="$2"
  [ "$os_name" = "Linux" ] || runtime_die "candidate images require native Linux, got $os_name"
  case "$arch" in
    aarch64 | arm64) ;;
    *) runtime_die "candidate images require native arm64, got $arch" ;;
  esac
}

runtime_reject_control_plane_material() {
  local file="$1"
  if grep -aEq 'HIDEOUT_SECRET_|cap_[0-9a-fA-F]{8}|claim_[0-9a-fA-F]{8}|ui_[0-9a-fA-F]{8}' "$file"; then
    runtime_die "control-plane material is forbidden in build input $(basename "$file")"
  fi
}

runtime_validate_source_tree() {
  local source_dir="$1"
  local lock="$source_dir/sources.lock.json"
  local names="$source_dir/packages.txt"
  local versions="$source_dir/packages.lock"
  local expected actual

  runtime_require_command jq
  [ -s "$lock" ] || runtime_die "missing sources.lock.json"
  [ -s "$names" ] || runtime_die "missing packages.txt"
  [ -s "$versions" ] || runtime_die "missing packages.lock"
  runtime_reject_control_plane_material "$lock"
  runtime_reject_control_plane_material "$names"
  runtime_reject_control_plane_material "$versions"

  jq -e '
    .schema == "hideout.runtime-source-lock/v1" and
    .architecture == "aarch64" and
    (.base.location | test("^https://[^?#]+/[0-9-]+/[^?#]+\\.qcow2$")) and
    (.base.sha512 | test("^[a-f0-9]{128}$")) and
    (.base.sha256 | test("^[a-f0-9]{64}$")) and
    (.base.bytes > 0) and (.base.virtualBytes > 0) and
    (.debianSnapshot.releaseLocation | test("^https://snapshot\\.debian\\.org/archive/debian/[0-9TZ]+/dists/trixie/Release$")) and
    (.debianSnapshot.releaseSHA256 | test("^[a-f0-9]{64}$")) and
    (.debianSnapshot.packagesLocation | test("^https://snapshot\\.debian\\.org/archive/debian/[0-9TZ]+/dists/trixie/main/binary-arm64/Packages\\.xz$")) and
    (.debianSnapshot.packagesSHA256 | test("^[a-f0-9]{64}$")) and
    (.debianSnapshot.packageMirror == ("https://snapshot.debian.org/archive/debian/" + .debianSnapshot.timestamp)) and
    (.debianSnapshot.packageVersionsSHA256 | test("^[a-f0-9]{64}$")) and
    (.builder.hostOS == "linux") and (.builder.hostArch == "arm64") and
    (.builder.baseImage | test("^docker\\.io/library/debian@sha256:[a-f0-9]{64}$")) and
    (.builder.packages | type == "array" and length > 0) and
    (.node.version | test("^[0-9]+\\.[0-9]+\\.[0-9]+$")) and
    (.node.npmVersion | test("^[0-9]+\\.[0-9]+\\.[0-9]+$")) and
    (.node.location == ("https://nodejs.org/download/release/v" + .node.version + "/node-v" + .node.version + "-linux-arm64.tar.xz")) and
    (.node.sha256 | test("^[a-f0-9]{64}$")) and
    (.go.version | test("^[0-9]+\\.[0-9]+(\\.[0-9]+)?$")) and
    (.go.location == ("https://go.dev/dl/go" + .go.version + ".linux-arm64.tar.gz")) and
    (.go.sha256 | test("^[a-f0-9]{64}$")) and
    (.output.format == "qcow2") and
    (.output.virtualBytes >= 3221225472 and .output.virtualBytes <= 17179869184)
  ' "$lock" >/dev/null || runtime_die "sources.lock.json is incomplete or unsafe"

  if grep -Eqi '(^|[/_.-])(latest|current|daily)([/_.-]|$)|[?#]' < <(jq -r '.base.location, .node.location, .go.location, .debianSnapshot.releaseLocation, .debianSnapshot.packagesLocation, .debianSnapshot.packageMirror' "$lock"); then
    runtime_die "moving, query, or fragment source URL is forbidden"
  fi
  jq -r '.builder.packages[]' "$lock" | LC_ALL=C sort -c -u || runtime_die "builder packages must be sorted and unique"
  if grep -Eqv '^[a-z0-9][a-z0-9+.-]*=[A-Za-z0-9:+.~_-]+$' < <(jq -r '.builder.packages[]' "$lock"); then
    runtime_die "builder packages must use exact package=version declarations"
  fi
  for required_builder_package in libguestfs-tools linux-image-arm64 qemu-utils; do
    jq -er --arg package "$required_builder_package" '.builder.packages[] | select(startswith($package + "="))' "$lock" >/dev/null ||
      runtime_die "builder package set is missing $required_builder_package"
  done
  LC_ALL=C sort -c -u "$names" || runtime_die "packages.txt must be sorted and unique"
  sed 's/=.*//' "$versions" | LC_ALL=C sort -c -u || runtime_die "packages.lock names must be sorted and unique"
  grep -Eqv '^[a-z0-9][a-z0-9+.-]*$' "$names" && runtime_die "packages.txt contains an invalid package name"
  grep -Eqv '^[a-z0-9][a-z0-9+.-]*=[A-Za-z0-9:+.~_-]+$' "$versions" && runtime_die "packages.lock contains an invalid exact version"
  if ! diff -u "$names" <(sed 's/=.*//' "$versions") >/dev/null; then
    runtime_die "packages.txt and packages.lock names differ"
  fi
  expected="$(jq -r '.debianSnapshot.packageVersionsSHA256' "$lock")"
  actual="$(runtime_sha256 "$versions")"
  [ "$actual" = "$expected" ] || runtime_die "packages.lock digest mismatch: want $expected got $actual"
}

runtime_verify_deb_cache() {
  local packages_index="$1"
  local package_lock="$2"
  local deb_cache="$3"
  local work_dir="$4"
  local seen="$work_dir/deb-packages.seen"
  local count=0
  local total_bytes=0
  : >"$seen"

  local deb package version architecture rows row_count expected_sha expected_bytes actual_sha actual_bytes
  while IFS= read -r deb; do
    package="$(dpkg-deb -f "$deb" Package)"
    version="$(dpkg-deb -f "$deb" Version)"
    architecture="$(dpkg-deb -f "$deb" Architecture)"
    rows="$(awk -v wanted_package="$package" -v wanted_version="$version" -v wanted_arch="$architecture" '
      BEGIN { RS=""; FS="\n" }
      {
        package=""; version=""; architecture=""; sha=""; bytes=""
        for (i=1; i<=NF; i++) {
          if ($i ~ /^Package: /) { package=$i; sub(/^Package: /, "", package) }
          if ($i ~ /^Version: /) { version=$i; sub(/^Version: /, "", version) }
          if ($i ~ /^Architecture: /) { architecture=$i; sub(/^Architecture: /, "", architecture) }
          if ($i ~ /^SHA256: /) { sha=$i; sub(/^SHA256: /, "", sha) }
          if ($i ~ /^Size: /) { bytes=$i; sub(/^Size: /, "", bytes) }
        }
        if (package == wanted_package && version == wanted_version && architecture == wanted_arch) {
          print sha "\t" bytes
        }
      }
    ' "$packages_index")"
    row_count="$(printf '%s\n' "$rows" | awk 'NF { count++ } END { print count+0 }')"
    [ "$row_count" -eq 1 ] || runtime_die "downloaded package $package=$version/$architecture is not uniquely bound by locked Packages.xz"
    expected_sha="${rows%%$'\t'*}"
    expected_bytes="${rows#*$'\t'}"
    actual_sha="$(runtime_sha256 "$deb")"
    actual_bytes="$(wc -c <"$deb" | tr -d '[:space:]')"
    [ "$actual_sha" = "$expected_sha" ] || runtime_die "downloaded package digest mismatch for $package=$version"
    [ "$actual_bytes" = "$expected_bytes" ] || runtime_die "downloaded package size mismatch for $package=$version"
    printf '%s=%s\n' "$package" "$version" >>"$seen"
    count=$((count + 1))
    total_bytes=$((total_bytes + actual_bytes))
  done < <(find "$deb_cache" -maxdepth 1 -type f -name '*.deb' -print | LC_ALL=C sort)

  [ "$count" -gt 0 ] && [ "$count" -le 512 ] || runtime_die "downloaded package closure must contain 1-512 debs"
  [ "$total_bytes" -le $((2 << 30)) ] || runtime_die "downloaded package closure exceeds 2 GiB"
  while IFS= read -r locked; do
    grep -Fxq "$locked" "$seen" || runtime_die "downloaded package closure is missing locked top-level package $locked"
  done <"$package_lock"
}

runtime_prepare_deb_bundle() {
  local lock="$1"
  local packages_xz="$2"
  local package_lock="$3"
  local cache_root="$4"
  local work_dir="$5"
  local output_tar="$6"
  local mirror mirror_list_id packages_sha apt_root packages_index deb_cache package_args snapshot

  runtime_require_command apt-get
  runtime_require_command dpkg-deb
  runtime_require_command tar
  runtime_require_command xz
  mirror="$(jq -r '.debianSnapshot.packageMirror' "$lock")"
  snapshot="$(jq -r '.debianSnapshot.timestamp' "$lock")"
  [ "$mirror" = "https://snapshot.debian.org/archive/debian/$snapshot" ] || runtime_die "unsupported Debian package mirror"
  packages_sha="$(runtime_sha256 "$packages_xz")"
  apt_root="$work_dir/apt"
  deb_cache="$cache_root/debs-$packages_sha"
  mirror_list_id="${mirror#https://}"
  mirror_list_id="${mirror_list_id//\//_}"
  packages_index="$apt_root/state/lists/${mirror_list_id}_dists_trixie_main_binary-arm64_Packages"
  rm -rf "$apt_root"
  mkdir -p "$apt_root/etc/apt" "$apt_root/state/lists/partial" "$deb_cache/partial"
  printf '%s\n' "deb [trusted=yes check-valid-until=no] $mirror trixie main" >"$apt_root/etc/apt/sources.list"
  : >"$apt_root/status"
  xz -dc "$packages_xz" >"$packages_index"
  package_args="$(paste -sd ' ' "$package_lock")"
  apt-get -y --download-only --no-install-recommends \
    -o Debug::NoLocking=true \
    -o Dir::Etc::sourcelist="$apt_root/etc/apt/sources.list" \
    -o Dir::Etc::sourceparts=- \
    -o Dir::State::status="$apt_root/status" \
    -o Dir::State::lists="$apt_root/state/lists" \
    -o Dir::State::extended_states="$apt_root/state/extended_states" \
    -o Dir::Cache::archives="$deb_cache" \
    -o APT::Architecture=arm64 \
    -o Acquire::Retries=3 \
    install $package_args
  runtime_verify_deb_cache "$packages_index" "$package_lock" "$deb_cache" "$work_dir"
  tar --sort=name --mtime='UTC 1970-01-01' --owner=0 --group=0 --numeric-owner \
    -cf "$output_tar" -C "$deb_cache" --exclude=partial --exclude=lock .
}

runtime_emit_package_inventory() {
  awk '
    BEGIN { RS=""; FS="\n" }
    {
      package=""; version=""; architecture=""
      for (i=1; i<=NF; i++) {
        if ($i ~ /^Package: /) { package=$i; sub(/^Package: /, "", package) }
        if ($i ~ /^Version: /) { version=$i; sub(/^Version: /, "", version) }
        if ($i ~ /^Architecture: /) { architecture=$i; sub(/^Architecture: /, "", architecture) }
      }
      if (package != "" && version != "") print package "=" version " architecture=" architecture
    }
  ' | LC_ALL=C sort
}

runtime_dpkg_installed_version() {
  local package="$1"
  local status_file="$2"
  awk -v wanted="$package" '
    BEGIN { RS=""; FS="\n" }
    {
      package=""; status=""; version=""
      for (i=1; i<=NF; i++) {
        if ($i ~ /^Package: /) { package=$i; sub(/^Package: /, "", package) }
        if ($i ~ /^Status: /) { status=$i; sub(/^Status: /, "", status) }
        if ($i ~ /^Version: /) { version=$i; sub(/^Version: /, "", version) }
      }
      if (package == wanted && status == "install ok installed") { print version; exit }
    }
  ' "$status_file"
}

runtime_required_outputs() {
  local revision="$1"
  cat <<EOF
developer-standard-${revision}-linux-aarch64.qcow2
SHA256SUMS
package-inventory.txt
component-manifest.json
sbom-status.json
build-provenance.json
verification-report.json
EOF
}

runtime_verify_output_contract() {
  local out_dir="$1"
  local revision="$2"
  local name
  while IFS= read -r name; do
    [ -s "$out_dir/$name" ] || runtime_die "missing required candidate output: $name"
  done < <(runtime_required_outputs "$revision")
}

runtime_configure_libguestfs_backend() {
  local acceleration_device="${1:-/dev/kvm}"

  export LIBGUESTFS_BACKEND=direct
  if [ -c "$acceleration_device" ] && [ -r "$acceleration_device" ] && [ -w "$acceleration_device" ]; then
    unset LIBGUESTFS_BACKEND_SETTINGS
    RUNTIME_LIBGUESTFS_ACCELERATION="kvm"
    RUNTIME_LIBGUESTFS_BACKEND_SETTINGS=""
  else
    export LIBGUESTFS_BACKEND_SETTINGS=force_tcg
    RUNTIME_LIBGUESTFS_ACCELERATION="tcg"
    RUNTIME_LIBGUESTFS_BACKEND_SETTINGS="force_tcg"
  fi
  export RUNTIME_LIBGUESTFS_ACCELERATION RUNTIME_LIBGUESTFS_BACKEND_SETTINGS
}

runtime_fetch_locked() {
  local url="$1"
  local destination="$2"
  local algorithm="$3"
  local expected="$4"
  local actual
  if [ ! -f "$destination" ]; then
    curl --fail --location --proto '=https' --proto-redir '=https' --tlsv1.2 \
      --retry 3 --connect-timeout 20 --output "$destination.tmp" "$url"
    mv "$destination.tmp" "$destination"
  fi
  case "$algorithm" in
    sha256) actual="$(runtime_sha256 "$destination")" ;;
    sha512) actual="$(runtime_sha512 "$destination")" ;;
    *) runtime_die "unsupported digest algorithm $algorithm" ;;
  esac
  [ "$actual" = "$expected" ] || runtime_die "digest mismatch for $(basename "$destination"): want $expected got $actual"
}
