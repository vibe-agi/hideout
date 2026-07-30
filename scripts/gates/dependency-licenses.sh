#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
cd "$repo_root"

for command_name in go jq awk; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    echo "dependency-licenses: required command not found: $command_name" >&2
    exit 1
  fi
done

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  echo "dependency-licenses: shasum or sha256sum is required" >&2
  return 127
}

tmp_root="$(mktemp -d "${TMPDIR:-/tmp}/hideout-dependency-licenses.XXXXXX")"
cleanup() {
  rm -rf "$tmp_root"
}
trap cleanup EXIT

direct_modules="$tmp_root/direct-modules.tsv"
notice_inventory="$tmp_root/notice-inventory.tsv"

go mod edit -json | jq -r '
  .Require[]
  | select(.Indirect != true)
  | [.Path, .Version]
  | @tsv
' >"$direct_modules"

awk -F '|' '
  function trim(value) {
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
    gsub(/^`|`$/, "", value)
    return value
  }
  /^\| `/ {
    component = trim($2)
    version = trim($3)
    license = trim($4)
    if (component != "" && version != "" && license != "") {
      print component "\t" version "\t" license
    }
  }
' THIRD_PARTY_NOTICES.md >"$notice_inventory"

failed=0
while IFS=$'\t' read -r module_name module_version; do
  exact_count="$(
    awk -F '\t' -v module="$module_name" -v version="$module_version" '
      $1 == module && $2 == version { count++ }
      END { print count + 0 }
    ' "$notice_inventory"
  )"
  module_count="$(
    awk -F '\t' -v module="$module_name" '
      $1 == module { count++ }
      END { print count + 0 }
    ' "$notice_inventory"
  )"
  if [[ "$exact_count" -ne 1 || "$module_count" -ne 1 ]]; then
    echo "dependency-licenses: $module_name $module_version needs exactly one matching notice row" >&2
    failed=1
  fi
done <"$direct_modules"

for feature_module in \
  charm.land/bubbles/v2 \
  charm.land/bubbletea/v2 \
  charm.land/lipgloss/v2 \
  github.com/cilium/ebpf; do
  if ! awk -F '\t' -v module="$feature_module" '
    $1 == module && $3 != "" { found = 1 }
    END { exit(found ? 0 : 1) }
  ' "$notice_inventory"; then
    echo "dependency-licenses: feature dependency is missing a license: $feature_module" >&2
    failed=1
  fi
done

tun2socks_version="$(
  GOWORK=off go -C tools/tun2socks-build list -m \
    -f '{{if eq .Path "github.com/xjasonlyu/tun2socks/v2"}}{{.Version}}{{end}}' \
    github.com/xjasonlyu/tun2socks/v2
)"
if [[ "$tun2socks_version" != "v2.6.0" ]] ||
  ! awk -F '\t' -v version="$tun2socks_version" '
    $1 == "github.com/xjasonlyu/tun2socks/v2" &&
      $2 == version && $3 == "MIT" { found++ }
    END { exit(found == 1 ? 0 : 1) }
  ' "$notice_inventory"; then
  echo "dependency-licenses: isolated tun2socks version/license notice is inconsistent" >&2
  failed=1
fi
if [[ ! -f third_party/tun2socks/LICENSE ]] ||
  [[ -L third_party/tun2socks/LICENSE ]]; then
  echo "dependency-licenses: tun2socks redistributed license is missing" >&2
  failed=1
fi
for manifest_path in \
  internal/workloadobs/collector/bpf/observer.generated.json \
  internal/workloadobs/collector/bpf/file_observer.generated.json \
  internal/workloadobs/collector/bpf/network_observer.generated.json; do
  case "$manifest_path" in
    */observer.generated.json)
      expected_source="internal/workloadobs/collector/bpf/programs.c"
      expected_object="internal/workloadobs/collector/bpf/observer_bpfel.o"
      expected_generated="internal/workloadobs/collector/bpf/observer_bpfel.go"
      ;;
    */file_observer.generated.json)
      expected_source="internal/workloadobs/collector/bpf/file_programs.c"
      expected_object="internal/workloadobs/collector/bpf/file_observer_bpfel.o"
      expected_generated="internal/workloadobs/collector/bpf/file_observer_bpfel.go"
      ;;
    */network_observer.generated.json)
      expected_source="internal/workloadobs/collector/bpf/network_programs.c"
      expected_object="internal/workloadobs/collector/bpf/network_observer_bpfel.o"
      expected_generated="internal/workloadobs/collector/bpf/network_observer_bpfel.go"
      ;;
    *)
      echo "dependency-licenses: unknown BPF manifest: $manifest_path" >&2
      failed=1
      continue
      ;;
  esac
  if ! jq -e \
    --arg source "$expected_source" \
    --arg object "$expected_object" \
    --arg generated "$expected_generated" '
    .schema == "hideout.generated-bpf/v2" and
    .source == $source and
    .object == $object and
    .generatedGo == $generated and
    .target == "bpfel" and
    .compiler == "clang" and
    .compilerVersion == "19.1.7" and
    .goVersion == "go1.25.12" and
    .license == "Apache-2.0 OR GPL-2.0-only" and
    .kernelProgramLicense == "GPL" and
    .bpf2goVersion == "v0.22.0" and
    (.sourceSHA256 | test("^[a-f0-9]{64}$")) and
    (.objectSHA256 | test("^[a-f0-9]{64}$")) and
    (.generatedGoSHA256 | test("^[a-f0-9]{64}$"))
  ' "$manifest_path" >/dev/null; then
    echo "dependency-licenses: generated BPF provenance is incomplete: $manifest_path" >&2
    failed=1
    continue
  fi
  for artifact_field in \
    "source:$expected_source:sourceSHA256" \
    "object:$expected_object:objectSHA256" \
    "generated Go:$expected_generated:generatedGoSHA256"; do
    artifact_label="${artifact_field%%:*}"
    artifact_remainder="${artifact_field#*:}"
    artifact_path="${artifact_remainder%%:*}"
    digest_field="${artifact_remainder#*:}"
    expected_digest="$(jq -r --arg field "$digest_field" '.[$field]' "$manifest_path")"
    if [[ ! -f "$artifact_path" ]] ||
      [[ -L "$artifact_path" ]] ||
      [[ "$(sha256_file "$artifact_path")" != "$expected_digest" ]]; then
      echo "dependency-licenses: BPF $artifact_label digest mismatch: $artifact_path" >&2
      failed=1
    fi
  done
done
for source_path in \
  internal/workloadobs/collector/bpf/programs.c \
  internal/workloadobs/collector/bpf/file_programs.c \
  internal/workloadobs/collector/bpf/network_programs.c; do
  if ! awk '
    $0 == "// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-only" { found = 1 }
    END { exit(found ? 0 : 1) }
  ' "$source_path"; then
    echo "dependency-licenses: BPF source SPDX declaration is missing: $source_path" >&2
    failed=1
  fi
done
if [[ ! -f LICENSES/GPL-2.0-only.txt ]] ||
  ! grep -F "GNU GENERAL PUBLIC LICENSE" LICENSES/GPL-2.0-only.txt >/dev/null ||
  ! grep -F "Version 2, June 1991" LICENSES/GPL-2.0-only.txt >/dev/null; then
  echo "dependency-licenses: GPL-2.0-only license text is missing" >&2
  failed=1
fi
if ! grep -F \
  'The Hideout-owned BPF source is offered under' \
  THIRD_PARTY_NOTICES.md >/dev/null ||
  ! grep -F \
    '`github.com/cilium/ebpf`, listed above, is the MIT-licensed Go loader' \
    THIRD_PARTY_NOTICES.md >/dev/null; then
  echo "dependency-licenses: BPF source/loader notice is incomplete" >&2
  failed=1
fi

if [[ "$failed" -ne 0 ]]; then
  exit 1
fi

echo "dependency-licenses: root modules, isolated helper, and generated BPF licenses/digests are accounted for"
