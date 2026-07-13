#!/usr/bin/env bash

public_alpha_path_pids() {
  local root="$1"
  ps -axo pid=,stat=,command= | awk -v needle="$root/" -v self="$$" '
    $1 ~ /^[0-9]+$/ && $1 != self && $2 !~ /^Z/ && index($0, needle) > 0 &&
    $0 !~ /awk .*needle/ && index($0, "ps -axo") == 0 {
      print $1
    }
  '
}

public_alpha_process_count() {
  local root="$1" class="$2"
  ps -axo pid=,stat=,command= | awk -v needle="$root/" -v self="$$" -v class="$class" '
    $1 ~ /^[0-9]+$/ && $1 != self && $2 !~ /^Z/ && index($0, needle) > 0 &&
    $0 !~ /awk .*needle/ && index($0, "ps -axo") == 0 {
      command = tolower($0)
      if (class == "all" ||
          (class == "browser" && command ~ /(chrome|chromium|microsoft edge|brave|vivaldi)/) ||
          (class == "lima" && command ~ /(limactl|lima|qemu|vz|hostagent)/)) {
        count++
      }
    }
    END { print count + 0 }
  '
}

public_alpha_cleanup_root() {
  local root="$1" report="$2"
  case "$root" in
    ""|/) echo "public-alpha-cleanup: unsafe root" >&2; return 2 ;;
  esac

  local terminated pid remaining lima browser temp_dirs secret_state cleanup_status
  terminated="$(public_alpha_path_pids "$root" | awk 'NF {count++} END {print count+0}')"
  while IFS= read -r pid; do
    [ -n "$pid" ] || continue
    kill -TERM "$pid" >/dev/null 2>&1 || true
  done < <(public_alpha_path_pids "$root")
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [ "$(public_alpha_process_count "$root" all)" -eq 0 ] && break
    sleep 0.2
  done
  while IFS= read -r pid; do
    [ -n "$pid" ] || continue
    kill -KILL "$pid" >/dev/null 2>&1 || true
  done < <(public_alpha_path_pids "$root")
  sleep 0.2

  rm -rf "$root"
  remaining="$(public_alpha_process_count "$root" all)"
  lima="$(public_alpha_process_count "$root" lima)"
  browser="$(public_alpha_process_count "$root" browser)"
  temp_dirs=0
  secret_state=0
  [ ! -e "$root" ] || temp_dirs=1
  [ ! -e "$root" ] || secret_state=1
  cleanup_status="passed"
  if [ "$remaining" -ne 0 ] || [ "$lima" -ne 0 ] || [ "$browser" -ne 0 ] ||
     [ "$temp_dirs" -ne 0 ] || [ "$secret_state" -ne 0 ]; then
    cleanup_status="failed"
  fi
  jq -n \
    --arg status "$cleanup_status" \
    --argjson terminated "$terminated" \
    --argjson remaining "$remaining" \
    --argjson lima "$lima" \
    --argjson browser "$browser" \
    --argjson tempDirs "$temp_dirs" \
    --argjson secretState "$secret_state" '
    {
      schema:"hideout.public-alpha-cleanup/v1",
      status:$status,
      candidatePathProcessesTerminated:$terminated,
      candidatePathProcessesRetained:$remaining,
      candidateLimaProcessesRetained:$lima,
      candidateBrowserProcessesRetained:$browser,
      candidateTemporaryRootsRetained:$tempDirs,
      candidateSecretBearingStateRetained:$secretState
    }
  ' >"$report"
  [ "$cleanup_status" = "passed" ]
}

public_alpha_cleanup_workflow_state() {
  local root="$1" keychain="$2" report="$3"
  case "$root" in
    ""|/) echo "public-alpha-cleanup: unsafe workflow root" >&2; return 2 ;;
  esac

  local keychain_delete_failed=0 keychains_retained=0 temp_dirs=0 secret_state=0 status=passed
  if [ -n "$keychain" ]; then
    if ! command -v security >/dev/null 2>&1; then
      keychain_delete_failed=1
    elif ! security delete-keychain "$keychain" >/dev/null 2>&1 && [ -e "$keychain" ]; then
      keychain_delete_failed=1
    fi
  fi
  rm -rf "$root"

  if [ -n "$keychain" ]; then
    if [ -e "$keychain" ]; then
      keychains_retained=1
    elif command -v security >/dev/null 2>&1 && security list-keychains -d user 2>/dev/null | grep -F -- "$keychain" >/dev/null; then
      keychains_retained=1
    fi
  fi
  [ ! -e "$root" ] || temp_dirs=1
  if [ "$temp_dirs" -ne 0 ] || [ "$keychains_retained" -ne 0 ] || [ "$keychain_delete_failed" -ne 0 ]; then
    secret_state=1
    status=failed
  fi

  jq -n \
    --arg status "$status" \
    --argjson keychainDeleteFailed "$keychain_delete_failed" \
    --argjson keychainsRetained "$keychains_retained" \
    --argjson tempDirs "$temp_dirs" \
    --argjson secretState "$secret_state" '
    {
      schema:"hideout.public-alpha-workflow-cleanup/v1",
      status:$status,
      keychainDeleteFailures:$keychainDeleteFailed,
      candidateKeychainsRetained:$keychainsRetained,
      candidateTemporaryRootsRetained:$tempDirs,
      candidateSecretBearingStateRetained:$secretState
    }
  ' >"$report"
  [ "$status" = passed ]
}
