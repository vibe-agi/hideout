#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

GATE_TIMEOUT="${HIDEOUT_GATE_TIMEOUT:-15m}"

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "gate3: missing required command: $1" >&2
    exit 127
  fi
}

with_timeout() {
  local duration="$1"
  shift
  "$@" &
  local pid=$!
  (
    sleep "$duration"
    if kill -0 "$pid" 2>/dev/null; then
      echo "gate3: command timed out after $duration: $*" >&2
      kill "$pid" 2>/dev/null || true
      sleep 5
      kill -KILL "$pid" 2>/dev/null || true
    fi
  ) &
  local timer=$!
  local status=0
  if wait "$pid"; then
    status=0
  else
    status=$?
  fi
  kill "$timer" 2>/dev/null || true
  wait "$timer" 2>/dev/null || true
  return "$status"
}

latest_audit_log() {
  local store_root="$1"
  local logs=("$store_root"/sessions/*/audit.jsonl)
  [ -e "${logs[0]}" ] || return 0
  ls -t "${logs[@]}" 2>/dev/null | head -n 1
}

prepare_linux_shim() {
  if [ -n "${HIDEOUT_LINUX_SHIM_PATH:-}" ]; then
    if [ ! -x "$HIDEOUT_LINUX_SHIM_PATH" ]; then
      echo "gate3: HIDEOUT_LINUX_SHIM_PATH is not executable: $HIDEOUT_LINUX_SHIM_PATH" >&2
      exit 126
    fi
    return
  fi

  local arch
  arch="$(go env GOARCH)"
  if command -v "hideout-shim-linux-$arch" >/dev/null 2>&1; then
    HIDEOUT_LINUX_SHIM_PATH="$(command -v "hideout-shim-linux-$arch")"
    export HIDEOUT_LINUX_SHIM_PATH
    return
  fi
  if command -v hideout-shim-linux >/dev/null 2>&1; then
    HIDEOUT_LINUX_SHIM_PATH="$(command -v hideout-shim-linux)"
    export HIDEOUT_LINUX_SHIM_PATH
    return
  fi

  HIDEOUT_LINUX_SHIM_PATH="$bin/hideout-shim-linux-$arch"
  export HIDEOUT_LINUX_SHIM_PATH
  "$hideout" shim build-linux --out "$HIDEOUT_LINUX_SHIM_PATH" --goarch "$arch" --source "$ROOT" >/dev/null
}

prepare_linux_tun2socks() {
  if [ -n "${HIDEOUT_LINUX_TUN2SOCKS_PATH:-}" ]; then
    if [ ! -x "$HIDEOUT_LINUX_TUN2SOCKS_PATH" ]; then
      echo "gate3: HIDEOUT_LINUX_TUN2SOCKS_PATH is not executable: $HIDEOUT_LINUX_TUN2SOCKS_PATH" >&2
      exit 126
    fi
    return
  fi

  local arch
  arch="$(go env GOARCH)"
  if command -v "tun2socks-linux-$arch" >/dev/null 2>&1; then
    HIDEOUT_LINUX_TUN2SOCKS_PATH="$(command -v "tun2socks-linux-$arch")"
    export HIDEOUT_LINUX_TUN2SOCKS_PATH
    return
  fi
  if command -v tun2socks-linux >/dev/null 2>&1; then
    HIDEOUT_LINUX_TUN2SOCKS_PATH="$(command -v tun2socks-linux)"
    export HIDEOUT_LINUX_TUN2SOCKS_PATH
    return
  fi

  echo "gate3: building temporary Linux tun2socks for $arch"
  HIDEOUT_LINUX_TUN2SOCKS_PATH="$bin/tun2socks-linux-$arch"
  local build_dir="$tmp/tun2socks-build"
  mkdir -p "$build_dir"
  (
    cd "$build_dir"
    go mod init hideout-gate-tun2socks >/dev/null
    go get github.com/xjasonlyu/tun2socks/v2@v2.6.0 >/dev/null
    GOOS=linux GOARCH="$arch" CGO_ENABLED=0 \
      go build -o "$HIDEOUT_LINUX_TUN2SOCKS_PATH" github.com/xjasonlyu/tun2socks/v2
  )
  chmod 0700 "$HIDEOUT_LINUX_TUN2SOCKS_PATH"
  export HIDEOUT_LINUX_TUN2SOCKS_PATH
}

start_local_proxy() {
  local proxy_bin="$bin/hideout-gate-socks5"
  go build -o "$proxy_bin" ./cmd/hideout-gate-socks5
  "$proxy_bin" --listen 127.0.0.1:0 --url-host host.lima.internal >"$tmp/proxy.url" 2>"$tmp/proxy.log" &
  proxy_pid=$!
  for _ in {1..100}; do
    if [ -s "$tmp/proxy.url" ]; then
      HIDEOUT_SECRET_DEFAULT_PROXY="$(sed -n '1p' "$tmp/proxy.url")"
      export HIDEOUT_SECRET_DEFAULT_PROXY
      echo "gate3: started local SOCKS5 test proxy at $HIDEOUT_SECRET_DEFAULT_PROXY"
      return
    fi
    if ! kill -0 "$proxy_pid" 2>/dev/null; then
      echo "gate3: local SOCKS5 test proxy exited early" >&2
      cat "$tmp/proxy.log" >&2 || true
      exit 1
    fi
    sleep 0.1
  done
  echo "gate3: local SOCKS5 test proxy did not publish a URL" >&2
  cat "$tmp/proxy.log" >&2 || true
  exit 1
}

validate_operator_proxy_url() {
  if [ "${HIDEOUT_GATE3_REQUIRE_OPERATOR_PROXY:-}" != "1" ]; then
    return
  fi
  if [ -z "${HIDEOUT_SECRET_DEFAULT_PROXY:-}" ]; then
    echo "gate3: HIDEOUT_GATE3_REQUIRE_OPERATOR_PROXY=1 requires operator-supplied HIDEOUT_SECRET_DEFAULT_PROXY" >&2
    exit 2
  fi
  local rest
  case "$HIDEOUT_SECRET_DEFAULT_PROXY" in
    http://*) rest="${HIDEOUT_SECRET_DEFAULT_PROXY#http://}" ;;
    https://*) rest="${HIDEOUT_SECRET_DEFAULT_PROXY#https://}" ;;
    socks5://*) rest="${HIDEOUT_SECRET_DEFAULT_PROXY#socks5://}" ;;
    socks5h://*) rest="${HIDEOUT_SECRET_DEFAULT_PROXY#socks5h://}" ;;
    *)
      echo "gate3: HIDEOUT_SECRET_DEFAULT_PROXY must be a http, https, socks5, or socks5h URL with a host" >&2
      exit 2
      ;;
  esac
  local authority="${rest%%/*}"
  if [ -z "$authority" ]; then
    echo "gate3: HIDEOUT_SECRET_DEFAULT_PROXY must include a host" >&2
    exit 2
  fi
}

if [ "${1:-}" = "--preflight-only" ]; then
  HIDEOUT_GATE3_REQUIRE_OPERATOR_PROXY=1
  validate_operator_proxy_url
  echo "gate3: operator proxy preflight passed"
  exit 0
fi

require_command go
require_command limactl
require_command jq

validate_operator_proxy_url

tmp="$(mktemp -d "/tmp/hideout-gate3.XXXXXX")"
proxy_pid=""
cleanup() {
  if [ -n "$proxy_pid" ]; then
    kill "$proxy_pid" 2>/dev/null || true
    wait "$proxy_pid" 2>/dev/null || true
  fi
  if [ -x "${hideout:-}" ]; then
    HIDEOUT_STORE_ROOT="${store:-}" LIMA_HOME="${lima_home:-}" "$hideout" clean >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

bin="$tmp/bin"
store="$tmp/store"
lima_home="$tmp/lima"
workspace="$tmp/workspace"
mkdir -p "$bin" "$store" "$lima_home" "$workspace"

prepare_linux_dns_stub() {
  if [ -n "${HIDEOUT_LINUX_DNS_STUB_PATH:-}" ]; then
    if [ ! -x "$HIDEOUT_LINUX_DNS_STUB_PATH" ]; then
      echo "gate3: HIDEOUT_LINUX_DNS_STUB_PATH is not executable: $HIDEOUT_LINUX_DNS_STUB_PATH" >&2
      exit 126
    fi
    return
  fi
  local arch
  arch="$(go env GOARCH)"
  echo "gate3: building temporary Linux hideout-dns-stub for $arch"
  HIDEOUT_LINUX_DNS_STUB_PATH="$bin/hideout-dns-stub-linux-$arch"
  GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -o "$HIDEOUT_LINUX_DNS_STUB_PATH" ./cmd/hideout-dns-stub
  chmod 0700 "$HIDEOUT_LINUX_DNS_STUB_PATH"
  export HIDEOUT_LINUX_DNS_STUB_PATH
}

hideout="$bin/hideout"
go build -o "$hideout" ./cmd/hideout
prepare_linux_shim
prepare_linux_tun2socks
prepare_linux_dns_stub
if [ -z "${HIDEOUT_SECRET_DEFAULT_PROXY:-}" ]; then
  start_local_proxy
else
  echo "gate3: using HIDEOUT_SECRET_DEFAULT_PROXY from environment"
fi

# Privacy mode enforces DNS mediation: connected-subnet resolvers are blocked and
# the guest resolver is pointed at the DoH stub, which forwards each query as DoH
# (HTTPS) to the mediated resolver over the TUN and the SOCKS CONNECT proxy. The
# mediated resolver is a DoH server reached by IP; it defaults to a public one
# and the operator may override it. The gate proves the closure end to end: the
# guest resolves and fetches through the mediated path while the leak is blocked.
mediated_resolver="${HIDEOUT_GATE3_MEDIATED_RESOLVER:-1.1.1.1}"
echo "gate3: using mediated DoH resolver $mediated_resolver"

echo "gate3: checking doctor hidden proxy plan"
HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" HIDEOUT_SECRET_DEFAULT_PROXY="$HIDEOUT_SECRET_DEFAULT_PROXY" HIDEOUT_LINUX_TUN2SOCKS_PATH="$HIDEOUT_LINUX_TUN2SOCKS_PATH" HIDEOUT_LINUX_DNS_STUB_PATH="$HIDEOUT_LINUX_DNS_STUB_PATH" \
  "$hideout" doctor --backend lima --workspace "$workspace" --network tun2socks --proxy-secret default-proxy --mediated-resolver "$mediated_resolver"

echo "gate3: running hidden proxy env and route smoke"
stdout="$tmp/run.out"
stderr="$tmp/run.err"
if ! with_timeout "$GATE_TIMEOUT" env HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" HIDEOUT_SECRET_DEFAULT_PROXY="$HIDEOUT_SECRET_DEFAULT_PROXY" HIDEOUT_LINUX_TUN2SOCKS_PATH="$HIDEOUT_LINUX_TUN2SOCKS_PATH" HIDEOUT_LINUX_DNS_STUB_PATH="$HIDEOUT_LINUX_DNS_STUB_PATH" \
  "$hideout" run --verbose --backend lima --workspace "$workspace" --network tun2socks --proxy-secret default-proxy --mediated-resolver "$mediated_resolver" -- sh -eu -c '
for name in HTTP_PROXY HTTPS_PROXY ALL_PROXY NO_PROXY http_proxy https_proxy all_proxy no_proxy; do
  eval "value=\${$name:-}"
  if [ -n "$value" ]; then
    echo "proxy env leaked: $name" >&2
    exit 41
  fi
done
printf "proxy_env_absent=yes\n"
# DNS mediation: the guest resolver is the DoH stub; connected-subnet resolvers
# are blocked. Confirm the resolver is the stub, then resolve+fetch (DNS now goes
# over DoH through the privacy path).
grep -q "^nameserver 127.0.0.1" /etc/resolv.conf || { echo "guest resolver not pointed at DNS stub" >&2; exit 44; }
printf "dns_mediated=yes\n"
# Reverse proof (mandatory): every real upstream (connected-subnet) resolver
# captured before the override must now be unreachable — a direct query to it
# must fail. This proves the leak is blocked and the check is not theater. Not
# being able to run the check (no captured resolvers, no query tool) fails
# closed rather than silently passing.
[ -r /hideout/session/network/resolvers.before ] || { echo "reverse proof: resolvers.before is missing" >&2; exit 46; }
query_resolver() {
  if command -v dig >/dev/null 2>&1; then dig @"$1" example.com +time=3 +tries=1 +short >/dev/null 2>&1
  elif command -v nslookup >/dev/null 2>&1; then nslookup -timeout=3 example.com "$1" >/dev/null 2>&1
  else return 2; fi
}
blocked_any=no
for ns in $(cat /hideout/session/network/resolvers.before); do
  case "$ns" in 127.*|::1|"") continue ;; esac
  rc=0
  query_resolver "$ns" || rc=$?
  if [ "$rc" -eq 2 ]; then echo "reverse proof: guest needs dig or nslookup" >&2; exit 46; fi
  if [ "$rc" -eq 0 ]; then echo "leak: connected-subnet resolver $ns still reachable after closure" >&2; exit 45; fi
  blocked_any=yes
done
[ "$blocked_any" = yes ] || { echo "reverse proof: no connected-subnet resolver was captured to verify closure" >&2; exit 46; }
printf "connected_subnet_blocked=yes\n"
if command -v curl >/dev/null 2>&1; then
  curl -fsS --max-time 30 https://example.com/ >/dev/null
  printf "https_request=ok\n"
elif command -v wget >/dev/null 2>&1; then
  wget -q -T 30 -O /dev/null https://example.com/
  printf "https_request=ok\n"
else
  echo "guest requires curl or wget for gate3 route proof" >&2
  exit 127
fi
' >"$stdout" 2>"$stderr"; then
  echo "gate3: hidden proxy env and route smoke failed" >&2
  echo "gate3: stdout" >&2
  cat "$stdout" >&2
  echo "gate3: stderr" >&2
  cat "$stderr" >&2
  exit 1
fi

cat "$stdout"
# Surface the run's environment name and Boundary Summary (from --verbose, on
# either stream) so the evidence orchestrator records real references.
grep -h 'Hideout environment name:' "$stdout" "$stderr" 2>/dev/null | head -n1 || true
grep -qh 'Hideout boundary:' "$stdout" "$stderr" 2>/dev/null && echo "Boundary Summary present" || true
grep -q 'proxy_env_absent=yes' "$stdout"
grep -q 'dns_mediated=yes' "$stdout"
grep -q 'connected_subnet_blocked=yes' "$stdout"
grep -q 'https_request=ok' "$stdout"

audit_path="$(latest_audit_log "$store")"
[ -n "$audit_path" ] || { echo "gate3: audit log missing" >&2; exit 1; }
jq -e '
  select(.action == "guest.privilege.status") |
  .details.status == "enforced" and
  .details["target.uid"] != 0 and
  .details["target.sudoN"] == "fail" and
  .details["target.absoluteSudoN"] == "fail" and
  .details["setup.kind"] == "root-control-ssh"
' "$audit_path" >/dev/null
jq -e '
  select(.action == "hideout.privileged_setup") |
  .decision == "allow" and
  .details.status == "succeeded" and
  .details.category == "network" and
  .details.setupIdentityKind == "root-control-ssh" and
  .details.separateFromTarget == true
' "$audit_path" >/dev/null
echo "privilege_status=enforced"
echo "privileged_setup=network"

if grep -R --fixed-strings "$HIDEOUT_SECRET_DEFAULT_PROXY" "$store" >/dev/null 2>&1; then
  echo "gate3: proxy secret leaked into store artifacts" >&2
  exit 1
fi

if find "$store" -path '*/network/proxy.url' -print -quit | grep -q .; then
  echo "gate3: proxy.url was not removed by cleanup" >&2
  exit 1
fi

echo "gate3: passed"
