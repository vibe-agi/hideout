#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

mode="${1:-local}"

run_local() {
  go test -count=1 \
    ./internal/privilege \
    ./internal/backend/lima \
    ./internal/network \
    ./internal/broker \
    ./internal/cmdadapter \
    ./internal/manager \
    ./internal/export \
    ./internal/audit
  jq empty schemas/guest-privilege-status.schema.json >/dev/null

  if grep -REn 'prevents guest[- ]root|guest[- ]root containment is enforced|blocks guest[- ]root' \
    README.md docs specs/009-guest-privilege-separation-risk-audit \
    | grep -Evi 'must not|does not|not claim|no claim|0 docs|non-claim|non-claims|out of scope|do not claim' \
    >/tmp/hideout-privilege-overclaim.$$; then
    echo "privilege-smoke: found guest-root containment overclaim" >&2
    cat /tmp/hideout-privilege-overclaim.$$ >&2
    rm -f /tmp/hideout-privilege-overclaim.$$
    exit 1
  fi
  rm -f /tmp/hideout-privilege-overclaim.$$
  echo "privilege-separation-smoke: passed"
}

build_linux_helpers() {
  local out_dir="$1"
  local goarch
  goarch="$(go env GOARCH)"
  go build -o "$out_dir/hideout" ./cmd/hideout
  GOOS=linux GOARCH="$goarch" go build -o "$out_dir/hideout-shim-linux" ./cmd/hideout-shim
  GOOS=linux GOARCH="$goarch" go build -o "$out_dir/hideout-hostfsd-linux" ./cmd/hideout-hostfsd
}

remove_tmp() {
  local dir="$1"
  [ -n "$dir" ] || return 0
  [ -d "$dir" ] || return 0
  chmod -R u+w "$dir" >/dev/null 2>&1 || true
  rm -rf "$dir"
}

latest_audit_log() {
  local store_root="$1"
  local logs=("$store_root"/sessions/*/audit.jsonl)
  [ -e "${logs[0]}" ] || return 0
  ls -t "${logs[@]}" 2>/dev/null | head -n 1
}

run_real_enforced() {
  command -v limactl >/dev/null 2>&1 || { echo "privilege-smoke: limactl is required" >&2; exit 2; }
  command -v jq >/dev/null 2>&1 || { echo "privilege-smoke: jq is required" >&2; exit 2; }

  local tmp lima_home store helpers workspace host_file out err audit_path
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hps.XXXXXX")"
  lima_home="$tmp/lima"
  cleanup_real() {
    local cleanup_tmp="$1"
    local cleanup_lima_home="$2"
    while IFS= read -r instance; do
      [ -n "$instance" ] || continue
      LIMA_HOME="$cleanup_lima_home" limactl delete -f "$instance" >/dev/null 2>&1 || true
    done < <(LIMA_HOME="$cleanup_lima_home" limactl list --quiet 2>/dev/null | grep -E '^hideout-privilege-smoke' || true)
    remove_tmp "$cleanup_tmp"
  }
  trap "cleanup_real '$tmp' '$lima_home'" EXIT
  store="$tmp/store"
  helpers="$tmp/helpers"
  workspace="$tmp/workspace"
  mkdir -p "$lima_home" "$store" "$helpers" "$workspace"
  host_file="$tmp/hostfs-proof.txt"
  printf 'hostfs proof\n' >"$host_file"
  build_linux_helpers "$helpers"

  LIMA_HOME="$lima_home" HIDEOUT_STORE_ROOT="$store" \
    HIDEOUT_LINUX_SHIM_PATH="$helpers/hideout-shim-linux" \
    HIDEOUT_LINUX_HOSTFSD_PATH="$helpers/hideout-hostfsd-linux" \
    "$helpers/hideout" profile init privilege-smoke >/dev/null

  out="$tmp/run.out"
  err="$tmp/run.err"
  if ! LIMA_HOME="$lima_home" HIDEOUT_STORE_ROOT="$store" \
    HIDEOUT_LINUX_SHIM_PATH="$helpers/hideout-shim-linux" \
    HIDEOUT_LINUX_HOSTFSD_PATH="$helpers/hideout-hostfsd-linux" \
    "$helpers/hideout" run --profile privilege-smoke --backend lima --workspace "$workspace" \
      --fs "read:$host_file" --verbose -- sh -c 'printf enforced-proof' \
      >"$out" 2>"$err"; then
    echo "privilege-smoke: enforced run failed" >&2
    cat "$err" >&2
    exit 1
  fi

  audit_path="$(latest_audit_log "$store")"
  [ -n "$audit_path" ] || { echo "privilege-smoke: audit log missing" >&2; exit 1; }
  jq -e '
    select(.action == "guest.privilege.status") |
    .details.status == "enforced" and
    .details["target.uid"] != 0 and
    .details["target.sudoN"] == "fail" and
    .details["target.absoluteSudoN"] == "fail" and
    .details["setup.kind"] == "root-control-ssh" and
    .details["setup.separateFromTarget"] == true
  ' "$audit_path" >/dev/null
  jq -e '
    select(.action == "hideout.privileged_setup") |
    .decision == "allow" and
    .details.status == "succeeded" and
    .details.category == "hostfs" and
    .details.setupIdentityKind == "root-control-ssh" and
    .details.separateFromTarget == true
  ' "$audit_path" >/dev/null
  if grep -En 'setupPrivateKey|setupToken|setupCredential=|rootControlSSHConfig|OPENSSH PRIVATE KEY' "$audit_path" "$out" "$err" >/dev/null; then
    echo "privilege-smoke: setup credential material leaked" >&2
    exit 1
  fi
  echo "privilege-separation-smoke real-enforced: passed"
}

run_real_degraded() {
  command -v limactl >/dev/null 2>&1 || { echo "privilege-smoke: limactl is required" >&2; exit 2; }
  command -v jq >/dev/null 2>&1 || { echo "privilege-smoke: jq is required" >&2; exit 2; }
  command -v ssh >/dev/null 2>&1 || { echo "privilege-smoke: ssh is required" >&2; exit 2; }

  local tmp lima_home store helpers workspace host_file out err out2 err2 audit_path env_record instance target_user ssh_config
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/hpd.XXXXXX")"
  lima_home="$tmp/lima"
  cleanup_degraded() {
    local cleanup_tmp="$1"
    local cleanup_lima_home="$2"
    while IFS= read -r instance; do
      [ -n "$instance" ] || continue
      LIMA_HOME="$cleanup_lima_home" limactl delete -f "$instance" >/dev/null 2>&1 || true
    done < <(LIMA_HOME="$cleanup_lima_home" limactl list --quiet 2>/dev/null | grep -E '^hideout-privilege-smoke' || true)
    remove_tmp "$cleanup_tmp"
  }
  trap "cleanup_degraded '$tmp' '$lima_home'" EXIT

  store="$tmp/store"
  helpers="$tmp/helpers"
  workspace="$tmp/workspace"
  mkdir -p "$lima_home" "$store" "$helpers" "$workspace"
  host_file="$tmp/hostfs-proof.txt"
  printf 'hostfs proof\n' >"$host_file"
  build_linux_helpers "$helpers"

  LIMA_HOME="$lima_home" HIDEOUT_STORE_ROOT="$store" \
    HIDEOUT_LINUX_SHIM_PATH="$helpers/hideout-shim-linux" \
    HIDEOUT_LINUX_HOSTFSD_PATH="$helpers/hideout-hostfsd-linux" \
    "$helpers/hideout" profile init privilege-smoke >/dev/null

  out="$tmp/run.out"
  err="$tmp/run.err"
  if ! LIMA_HOME="$lima_home" HIDEOUT_STORE_ROOT="$store" \
    HIDEOUT_LINUX_SHIM_PATH="$helpers/hideout-shim-linux" \
    HIDEOUT_LINUX_HOSTFSD_PATH="$helpers/hideout-hostfsd-linux" \
    "$helpers/hideout" run --profile privilege-smoke --backend lima --workspace "$workspace" \
      --fs "read:$host_file" --verbose -- sh -c 'printf degraded-setup' \
      >"$out" 2>"$err"; then
    echo "privilege-smoke: degraded setup run failed" >&2
    cat "$err" >&2
    exit 1
  fi

  env_record="$(find "$store/environments" -name '*.json' -print | sort | head -n 1)"
  [ -n "$env_record" ] || { echo "privilege-smoke: environment record missing" >&2; exit 1; }
  instance="$(jq -r '.instanceName // empty' "$env_record")"
  target_user="$(jq -r '.user // empty' "$env_record")"
  [ -n "$instance" ] || { echo "privilege-smoke: environment instance missing" >&2; exit 1; }
  if [ -z "$target_user" ]; then
    target_user="$(LIMA_HOME="$lima_home" limactl shell --tty=false "$instance" -- whoami | tr -d '\r' | tail -n 1)"
  fi
  [ -n "$target_user" ] || { echo "privilege-smoke: target user missing" >&2; exit 1; }
  ssh_config="$(LIMA_HOME="$lima_home" limactl ls --format '{{.SSHConfigFile}}' "$instance")"
  [ -n "$ssh_config" ] || { echo "privilege-smoke: ssh config missing" >&2; exit 1; }
  ssh -F "$ssh_config" \
    -o User=root \
    -o ControlMaster=no \
    -o ControlPath=none \
    "lima-$instance" -- sh -s -- "$target_user" <<'ROOTSH'
set -eu
target_user="$1"
if command -v usermod >/dev/null 2>&1; then
  usermod -aG sudo "$target_user" >/dev/null 2>&1 || true
  usermod -aG wheel "$target_user" >/dev/null 2>&1 || true
fi
mkdir -p /etc/sudoers.d
rm -f /etc/sudoers.d/99-hideout-target-no-sudo
printf '%s ALL=(ALL) NOPASSWD:ALL\n' "$target_user" >/etc/sudoers.d/99-hideout-weak-target
chmod 0440 /etc/sudoers.d/99-hideout-weak-target
ROOTSH

  out2="$tmp/run-degraded.out"
  err2="$tmp/run-degraded.err"
  if ! LIMA_HOME="$lima_home" HIDEOUT_STORE_ROOT="$store" \
    HIDEOUT_LINUX_SHIM_PATH="$helpers/hideout-shim-linux" \
    HIDEOUT_LINUX_HOSTFSD_PATH="$helpers/hideout-hostfsd-linux" \
    "$helpers/hideout" run --profile privilege-smoke --backend lima --workspace "$workspace" \
      --fs "read:$host_file" --verbose -- sh -c 'printf degraded-proof' \
      >"$out2" 2>"$err2"; then
    echo "privilege-smoke: degraded proof run failed" >&2
    cat "$err2" >&2
    exit 1
  fi
  audit_path="$(latest_audit_log "$store")"
  [ -n "$audit_path" ] || { echo "privilege-smoke: audit log missing" >&2; exit 1; }
  if ! jq -e '
    select(.action == "guest.privilege.status") |
    .details.status == "degraded" and
    .details["target.sudoN"] == "pass" and
    .details["target.absoluteSudoN"] == "pass" and
    (.details.reason | test("passwordless sudo|target-reachable authority")) and
    (.details.nonClaim | test("does not claim guest-root containment"))
  ' "$audit_path" >/dev/null; then
    echo "privilege-smoke: degraded audit assertion failed" >&2
    tail -n 20 "$audit_path" >&2
    cat "$out2" "$err2" >&2
    exit 1
  fi
  if ! grep -Eq 'does not claim guest-root containment|base image|recreate' "$out2" "$err2"; then
    echo "privilege-smoke: degraded output missing warning/non-claim" >&2
    cat "$out2" "$err2" >&2
    exit 1
  fi
  if grep -En 'prevents guest[- ]root|guest[- ]root containment is enforced|blocks guest[- ]root' "$audit_path" "$out2" "$err2" >/dev/null; then
    echo "privilege-smoke: degraded run overclaimed guest-root containment" >&2
    exit 1
  fi
  if grep -En 'setupPrivateKey|setupToken|setupCredential=|rootControlSSHConfig|OPENSSH PRIVATE KEY' "$audit_path" "$out2" "$err2" >/dev/null; then
    echo "privilege-smoke: setup credential material leaked" >&2
    exit 1
  fi
  echo "privilege-separation-smoke real-degraded: passed"
}

case "$mode" in
  local|"")
    run_local
    ;;
  --real-enforced)
    run_real_enforced
    ;;
  --real-degraded)
    run_real_degraded
    ;;
  *)
    echo "usage: $0 [--real-enforced|--real-degraded]" >&2
    exit 2
    ;;
esac
