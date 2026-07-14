#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
. "$ROOT/scripts/lib/verified-runtime-cache.sh"
invocation_dir="$PWD"
operator_home="${HOME:-}"
package=""
report=""
real_lima=0
runtime_cache_status="not-applicable"

usage() {
  cat <<'USAGE'
Usage: scripts/test-public-alpha-clean-install.sh --package <tar.gz> [--out <json>] [--real-lima]

The default lane proves packaged installation without source, Go, profile
creation, or daemon startup. --real-lima additionally performs explicit init,
environment creation, and one non-root direct-network run.
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --package) package="${2:-}"; shift 2 ;;
    --out) report="${2:-}"; shift 2 ;;
    --real-lima) real_lima=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "public-alpha-clean-install: unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

[ -f "$package" ] || { echo "public-alpha-clean-install: --package must name an archive" >&2; exit 2; }
package="$(cd "$(dirname "$package")" && pwd -P)/$(basename "$package")"
case "$report" in
  ""|/*) ;;
  *) report="$invocation_dir/$report" ;;
esac
for command in jq tar shasum; do
  command -v "$command" >/dev/null 2>&1 || { echo "public-alpha-clean-install: missing $command" >&2; exit 127; }
done

tmp="$(mktemp -d "${TMPDIR:-/tmp}/hideout-public-alpha-install.XXXXXX")"
store="$tmp/store"
prefix="$tmp/prefix"
home="$tmp/home"
lima_home="$tmp/lima"
short_lima_home=""
if [ "$real_lima" -eq 1 ]; then
  # Lima appends the instance name and ssh.sock suffix to LIMA_HOME. Keep the
  # real-gate root short enough for macOS UNIX_PATH_MAX regardless of TMPDIR.
  short_lima_home="$(mktemp -d "${HIDEOUT_LIMA_SHORT_TMPDIR:-/tmp}/hla.XXXXXX")"
  lima_home="$short_lima_home"
fi
tool_bin="$tmp/tools"
workspace="$tmp/workspace"
mkdir -p "$store" "$prefix" "$home" "$lima_home" "$tool_bin" "$workspace"
cleanup() {
  if [ -x "$prefix/bin/hideout" ]; then
    HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
      "$prefix/bin/hideout" clean >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp"
  if [ -n "$short_lima_home" ]; then
    rm -rf "$short_lima_home"
  fi
}
trap cleanup EXIT

tar -xzf "$package" -C "$tmp"
package_root="$tmp/hideout"
[ -x "$package_root/install.sh" ] || { echo "public-alpha-clean-install: package installer missing" >&2; exit 1; }
cd "$tmp"

seed_verified_runtime_cache() {
  local shared_cache target_cache require_cache
  shared_cache="${HIDEOUT_LIMA_SHARED_CACHE:-$operator_home/Library/Caches/lima}"
  target_cache="$home/Library/Caches/lima"
  require_cache="${HIDEOUT_REQUIRE_RUNTIME_CACHE:-0}"
  runtime_cache_status="$(hideout_seed_verified_runtime_cache \
    "$package_root/runtime/catalog.json" "$shared_cache" "$target_cache" "$require_cache")"
}

lima_status="missing"
lima_version=""
if [ "$real_lima" -eq 1 ]; then
  command -v limactl >/dev/null 2>&1 || {
    echo "public-alpha-clean-install: --real-lima requires limactl" >&2
    exit 127
  }
  ln -s "$(command -v limactl)" "$tool_bin/limactl"
  lima_status="available"
  lima_version="$("$tool_bin/limactl" --version | head -n 1)"
  seed_verified_runtime_cache
fi
clean_path="$tool_bin:/usr/bin:/bin:/usr/sbin:/sbin"
if PATH="$clean_path" command -v go >/dev/null 2>&1; then
  echo "public-alpha-clean-install: clean PATH unexpectedly exposes Go" >&2
  exit 1
fi

HOME="$home" PATH="$clean_path" HIDEOUT_STORE_ROOT="$store" \
  "$package_root/install.sh" --prefix "$prefix" --store "$store" --skip-init \
  >"$tmp/install.out" 2>"$tmp/install.err"
hideout="$prefix/bin/hideout"
HOME="$home" PATH="$clean_path" "$hideout" version --json >"$tmp/version.json"
HOME="$home" PATH="$clean_path" "$hideout" package verify "$prefix" >"$tmp/verify.out"
doctor_rc=0
doctor_status="passed"
HOME="$home" PATH="$clean_path" HIDEOUT_STORE_ROOT="$store" \
  "$hideout" doctor --level light --format json --workspace "$workspace" \
  >"$tmp/doctor.json" \
  2>"$tmp/doctor.err" || doctor_rc=$?
if [ "$real_lima" -eq 0 ]; then
  if [ "$doctor_rc" -eq 0 ]; then
    echo "public-alpha-clean-install: doctor hid the missing Lima prerequisite" >&2
    exit 1
  fi
  if ! jq -e '
    [.findings[] | select(.status == "error")] as $errors |
    ($errors | length) == 1 and
    $errors[0].checkId == "backend" and
    ($errors[0].summary | contains("limactl is required for lima backend"))
  ' "$tmp/doctor.json" >/dev/null; then
    echo "public-alpha-clean-install: doctor reported errors beyond the expected missing Lima prerequisite" >&2
    jq -r '.findings[] | select(.status == "error") | "  \(.checkId): \(.summary)"' \
      "$tmp/doctor.json" >&2 || true
    cat "$tmp/doctor.err" >&2
    exit "$doctor_rc"
  fi
  doctor_status="prerequisite-missing"
elif [ "$doctor_rc" -ne 0 ]; then
  echo "public-alpha-clean-install: packaged doctor failed (exit $doctor_rc)" >&2
  jq -r '.findings[] | select(.status == "error") | "  \(.checkId): \(.summary)"' \
    "$tmp/doctor.json" >&2 || true
  cat "$tmp/doctor.err" >&2
  exit "$doctor_rc"
fi

jq -e '
  .schema == "hideout.binary-identity/v1" and
  (.productVersion | test("^[0-9]+\\.[0-9]+\\.[0-9]+-[0-9A-Za-z.-]+$")) and
  (.sourceCommit | test("^[a-f0-9]{40}$")) and
  .hostOS == "darwin" and .hostArch == "arm64"
' "$tmp/version.json" >/dev/null
if [ -d "$store/profiles" ] && find "$store/profiles" -mindepth 1 -print -quit | grep -q .; then
  echo "public-alpha-clean-install: package installation created a profile" >&2
  exit 1
fi
if [ -e "$store/daemon" ]; then
  echo "public-alpha-clean-install: package installation created daemon state" >&2
  exit 1
fi
if [ -d "$home/.lima" ] && find "$home/.lima" -mindepth 1 -print -quit | grep -q .; then
  echo "public-alpha-clean-install: package installation created a Lima instance" >&2
  exit 1
fi

run_status="not-run"
run_reason="local contract lane does not claim a real Lima run"
environment_id=""
if [ "$real_lima" -eq 1 ]; then
  profile="alpha-direct"
  HOME="$home" PATH="$clean_path" HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" init --profile "$profile" --template dev --backend lima \
    --network direct --runtime developer-standard --no-input >"$tmp/init.out"
  HOME="$home" PATH="$clean_path" HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" env create "$profile" --profile "$profile" --backend lima \
    --workspace "$workspace" --runtime developer-standard >"$tmp/create.out"
  HOME="$home" PATH="$clean_path" HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$hideout" run --profile "$profile" --env "$profile" --workspace "$workspace" -- \
    sh -c 'test "$(id -u)" != 0 && pwd' >"$tmp/run.out"
  environment_id="$profile"
  run_status="passed"
  run_reason=""
fi

if [ -z "$report" ]; then
  report="$invocation_dir/public-alpha-clean-install.json"
fi
mkdir -p "$(dirname "$report")"
package_sha="$(shasum -a 256 "$package" | awk '{print $1}')"
jq -n \
  --arg observedAt "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
  --arg packageSHA256 "$package_sha" \
  --arg version "$(jq -r '.productVersion' "$tmp/version.json")" \
  --arg commit "$(jq -r '.sourceCommit' "$tmp/version.json")" \
  --arg doctorStatus "$doctor_status" \
  --arg limaStatus "$lima_status" --arg limaVersion "$lima_version" \
  --arg runtimeCacheStatus "$runtime_cache_status" \
  --arg runStatus "$run_status" --arg runReason "$run_reason" \
  --arg environmentId "$environment_id" \
  '{schema:"hideout.public-alpha-clean-install/v1",observedAt:$observedAt,
    package:{sha256:$packageSHA256,productVersion:$version,sourceCommit:$commit},
    install:{status:"passed",sourceCheckoutUsed:false,goOnPATH:false,profileCreated:false,
      version:"passed",packageVerify:"passed",doctorLight:$doctorStatus},
    prerequisites:{lima:({status:$limaStatus} +
      (if $limaVersion == "" then {} else {version:$limaVersion} end))},
    realLima:({status:$runStatus,runtimeCacheStatus:$runtimeCacheStatus} +
      (if $runReason == "" then {environmentId:$environmentId,network:"direct",targetUID:"non-root"}
       else {reason:$runReason} end))}' >"$report"

echo "public-alpha-clean-install: passed ($run_status Lima, runtime cache $runtime_cache_status)"
