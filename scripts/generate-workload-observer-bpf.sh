#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/generate-workload-observer-bpf.sh [--check]

Generate the package-owned little-endian CO-RE workload observer object and
Go embedding with the pinned LLVM and bpf2go toolchain. --check regenerates in
a temporary directory and rejects checked-in drift.

Environment:
  HIDEOUT_BPF_CLANG       clang executable (default: clang-19)
  HIDEOUT_BPF_LLVM_STRIP  llvm-strip executable (default: llvm-strip-19)
USAGE
}

mode='write'
case "${1:-}" in
  "")
    ;;
  --check)
    mode=check
    ;;
  -h|--help)
    usage
    exit 0
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
# shellcheck source=scripts/lib/gate-result.sh
. "$repo_root/scripts/lib/gate-result.sh"
gate_completed=0
package_dir="$repo_root/internal/workloadobs/collector/bpf"
expected_llvm_version="19.1.7"
expected_bpf2go_version="v0.22.0"
clang_command="${HIDEOUT_BPF_CLANG:-clang-19}"
strip_command="${HIDEOUT_BPF_LLVM_STRIP:-llvm-strip-19}"

require_command() {
  local name="$1"
  if ! command -v "$name" >/dev/null 2>&1; then
    echo "generate-workload-observer-bpf: required command not found: $name" >&2
    exit 1
  fi
}

sha256_file() {
  local path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$path" | awk '{print $1}'
}

require_command "$clang_command"
require_command "$strip_command"
require_command go
require_command jq

clang_path="$(command -v "$clang_command")"
strip_path="$(command -v "$strip_command")"
clang_banner="$("$clang_path" --version | sed -n '1p')"
strip_banner="$("$strip_path" --version | sed -n '1,3p' | tr '\n' ' ')"
case "$clang_banner" in
  *"$expected_llvm_version"*)
    ;;
  *)
    echo "generate-workload-observer-bpf: clang must be exactly $expected_llvm_version; got: $clang_banner" >&2
    exit 1
    ;;
esac
case "$strip_banner" in
  *"$expected_llvm_version"*)
    ;;
  *)
    echo "generate-workload-observer-bpf: llvm-strip must be exactly $expected_llvm_version; got: $strip_banner" >&2
    exit 1
    ;;
esac

tmp_root="$(mktemp -d "${TMPDIR:-/tmp}/hideout-bpf-generate.XXXXXX")"
cleanup() {
  local exit_status=$?
  find "$tmp_root" -depth -delete
  if [ "$exit_status" -eq 0 ]; then
    gate_require_completion "generated-bpf"
  fi
}
trap cleanup EXIT

tool_path="$tmp_root/bpf2go"
output_dir="$tmp_root/output"
mkdir -p "$output_dir"
(
  cd "$repo_root"
  go build -trimpath -o "$tool_path" github.com/cilium/ebpf/cmd/bpf2go
)

go_version="$(go env GOVERSION)"

generate_artifact() {
  local identifier="$1"
  local output_stem="$2"
  local source_name="$3"
  local manifest_name="$4"
  local source_file="$package_dir/$source_name"
  local generated_go="${output_stem}_bpfel.go"
  local object="${output_stem}_bpfel.o"

  GOPACKAGE=bpf "$tool_path" \
    -cc "$clang_path" \
    -strip "$strip_path" \
    -target bpfel \
    -output-dir "$output_dir" \
    -output-stem "$output_stem" \
    "$identifier" "$source_file" -- \
    -O2 \
    -g \
    -Wall \
    -Werror \
    -fno-builtin-memset \
    "-fdebug-prefix-map=$repo_root=."

  local source_sha
  local object_sha
  local go_sha
  source_sha="$(sha256_file "$source_file")"
  object_sha="$(sha256_file "$output_dir/$object")"
  go_sha="$(sha256_file "$output_dir/$generated_go")"

  jq -S -n \
    --arg schema "hideout.generated-bpf/v2" \
    --arg source "internal/workloadobs/collector/bpf/$source_name" \
    --arg sourceSHA256 "$source_sha" \
    --arg object "internal/workloadobs/collector/bpf/$object" \
    --arg objectSHA256 "$object_sha" \
    --arg generatedGo "internal/workloadobs/collector/bpf/$generated_go" \
    --arg generatedGoSHA256 "$go_sha" \
    --arg target "bpfel" \
    --arg compiler "clang" \
    --arg compilerVersion "$expected_llvm_version" \
    --arg bpf2goVersion "$expected_bpf2go_version" \
    --arg goVersion "$go_version" \
    --arg license "Apache-2.0 OR GPL-2.0-only" \
    --arg kernelProgramLicense "GPL" \
    '{
      schema: $schema,
      source: $source,
      sourceSHA256: $sourceSHA256,
      object: $object,
      objectSHA256: $objectSHA256,
      generatedGo: $generatedGo,
      generatedGoSHA256: $generatedGoSHA256,
      target: $target,
      compiler: $compiler,
      compilerVersion: $compilerVersion,
      bpf2goVersion: $bpf2goVersion,
      goVersion: $goVersion,
      license: $license,
      kernelProgramLicense: $kernelProgramLicense
    }' >"$output_dir/$manifest_name"
}

generate_artifact observer observer programs.c observer.generated.json
generate_artifact fileObserver file_observer file_programs.c file_observer.generated.json
generate_artifact networkObserver network_observer network_programs.c network_observer.generated.json

generated_files=(
  observer_bpfel.go
  observer_bpfel.o
  observer.generated.json
  file_observer_bpfel.go
  file_observer_bpfel.o
  file_observer.generated.json
  network_observer_bpfel.go
  network_observer_bpfel.o
  network_observer.generated.json
)

if [[ "$mode" == check ]]; then
  failed=0
  for name in "${generated_files[@]}"; do
    if [[ ! -f "$package_dir/$name" ]]; then
      echo "generated-bpf: missing checked-in file $package_dir/$name" >&2
      failed=1
      continue
    fi
    if ! cmp -s "$output_dir/$name" "$package_dir/$name"; then
      echo "generated-bpf: stale checked-in file $package_dir/$name" >&2
      failed=1
    fi
  done
  if [[ "$failed" -ne 0 ]]; then
    exit 1
  fi
  gate_completed=1
  echo "generated-bpf: checked-in artifacts match pinned generation"
  exit 0
fi

for name in "${generated_files[@]}"; do
  install -m 0644 "$output_dir/$name" "$package_dir/$name"
done
gate_completed=1
echo "generated-bpf: wrote ${generated_files[*]}"
