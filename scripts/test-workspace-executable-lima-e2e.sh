#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"

out="$ROOT/.hideout-release-evidence/041-workspace-executable-real-gate2"
package_archive=""
samples=30
iterations=100
probe=0
require_real=0

usage() {
  cat <<'USAGE'
Usage: scripts/test-workspace-executable-lima-e2e.sh [options]

  --out <dir>          evidence output directory
  --package <tar.gz>   reuse an exact package archive
  --samples <n>        alternating direct/control samples (product minimum 30)
  --iterations <n>     disjoint-workspace executions (product minimum 100)
  --probe              permit reduced counts and dirty source; emit no product proof
  --require-real       fail instead of emitting supporting not-run evidence
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --out) out="${2:-}"; shift 2 ;;
    --package) package_archive="${2:-}"; shift 2 ;;
    --samples) samples="${2:-}"; shift 2 ;;
    --iterations) iterations="${2:-}"; shift 2 ;;
    --probe) probe=1; shift ;;
    --require-real) require_real=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "workspace executable e2e: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

for value_name in samples iterations; do
  eval "value=\${$value_name}"
  case "$value" in
    ''|*[!0-9]*) echo "workspace executable e2e: --$value_name must be an integer" >&2; exit 2 ;;
  esac
done
if [ "$probe" = "0" ]; then
  [ "$samples" -ge 30 ] || { echo "workspace executable e2e: product evidence requires 30 samples" >&2; exit 2; }
  [ "$iterations" -ge 100 ] || { echo "workspace executable e2e: product evidence requires 100 iterations" >&2; exit 2; }
  [ -z "$(git status --porcelain=v1 --untracked-files=all)" ] || {
    echo "workspace executable e2e: product evidence requires a clean source tree; use --probe for diagnostics" >&2
    exit 2
  }
fi
if [ -n "$package_archive" ] && [ ! -f "$package_archive" ]; then
  echo "workspace executable e2e: package archive does not exist" >&2
  exit 2
fi
if [ -e "$out" ]; then
  echo "workspace executable e2e: output directory already exists: $out" >&2
  exit 2
fi

missing=""
for tool in go git jq limactl python3 shasum tar awk sed; do
  command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done
[ "$(uname -s)" = Darwin ] || missing="$missing macOS"
[ "$(uname -m)" = arm64 ] || missing="$missing arm64"

mkdir -p "$out/logs" "$out/artifacts" "$out/reports"
out="$(cd "$out" && pwd -P)"
manifest="$out/product-hardening-evidence.json"
registry="$out/reports/proof-registry.json"
go run ./cmd/hideout support proof-registry --json >"$registry"

proof_real="041.workspace-executable.real-gate2.execution"
proof_not_run="041.workspace-executable.real-gate2.not-run"
for proof_id in "$proof_real" "$proof_not_run"; do
  jq -e --arg id "$proof_id" '.requirements[] | select(.proofId == $id)' "$registry" >/dev/null || {
    echo "workspace executable e2e: proof is not registered: $proof_id" >&2
    exit 1
  }
done

sha256_file() { shasum -a 256 "$1" | awk '{print $1}'; }
git_dirty() {
  if [ -n "$(git status --porcelain=v1 --untracked-files=all)" ]; then printf true; else printf false; fi
}
claims_json() {
  jq -c --arg id "$1" '
    [.requirements[] | select(.proofId == $id) | .claimIds[] |
      {claimId:.,source:"spec",description:("041 registered contract " + .),scope:"shared-workspace-portal-execution"}]
  ' "$registry"
}
artifact_json() {
  jq -n --arg path "$1" --arg sha "$(sha256_file "$out/$1")" \
    '{kind:"manifest",path:$path,sha256:$sha,redactionStatus:"passed",description:"041 workspace execution result"}'
}
write_manifest() {
  local proofs="$1" package_identity="${2:-null}"
  jq -n --arg generatedAt "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" \
    --arg commit "$(git rev-parse HEAD)" --argjson dirty "$(git_dirty)" \
    --argjson packageIdentity "$package_identity" --slurpfile proofs "$proofs" '
    {version:"hideout.product-hardening-evidence/v1",generatedAt:$generatedAt,
     commit:$commit,dirty:$dirty,proofs:$proofs[0]} +
     (if $packageIdentity == null then {} else {packageIdentity:$packageIdentity} end)
  ' >"$manifest"
  go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json "$manifest" \
    >"$out/logs/evidence-schema.out" 2>"$out/logs/evidence-schema.err"
}

if [ -n "$missing" ]; then
  reason="real Gate 2 prerequisites unavailable:$missing"
  jq -n --arg reason "$reason" \
    '{schema:"hideout.workspace-executable-not-run/v1",status:"not-run",reason:$reason}' \
    >"$out/artifacts/not-run.json"
  if [ "$require_real" = "1" ] || [ "$probe" = "1" ]; then
    echo "workspace executable e2e: $reason" >&2
    exit 1
  fi
  claims="$(claims_json "$proof_not_run")"
  artifact="$(artifact_json artifacts/not-run.json)"
  jq -n --arg proofId "$proof_not_run" --arg reason "$reason" \
    --argjson claims "$claims" --argjson artifact "$artifact" '[{
      proofId:$proofId,featureId:"041-workspace-executable-support",mode:"real-gate",
      evidenceClass:"workspace-executable-real-gate2-not-run",status:"not-run",
      commandSummary:"real workspace executable Gate 2 was not run",coveredClaims:$claims,
      prerequisites:[{name:"real-macos-arm64-lima-packaged",status:"missing",reason:$reason}],
      artifacts:[$artifact],redactionStatus:"not-run",notRunReason:$reason
    }]' >"$out/reports/proofs.json"
  write_manifest "$out/reports/proofs.json"
  echo "workspace executable e2e: passed status=not-run evidence=$manifest"
  exit 0
fi

short_root="${HIDEOUT_041_SHORT_TMPDIR:-/tmp}"
work="$(mktemp -d "$short_root/hideout-041-gate2.XXXXXX")"
store="$work/store"
lima_home="$work/lima"
install_root="$work/installed"
workspace_a="$work/workspace-a"
workspace_b="$work/workspace-b"
outside="$work/outside"
candidate=""

cleanup() {
  if [ -n "$candidate" ] && [ -x "$candidate" ]; then
    HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$candidate" daemon stop >/dev/null 2>&1 || true
  fi
  if [ -d "$lima_home" ]; then
    LIMA_HOME="$lima_home" limactl list --quiet 2>/dev/null | while IFS= read -r instance; do
      [ -n "$instance" ] && LIMA_HOME="$lima_home" limactl delete --force --tty=false "$instance" >/dev/null 2>&1 || true
    done
  fi
  find "$work" -depth -delete 2>/dev/null || true
}
trap cleanup EXIT

mkdir -p "$store" "$lima_home" "$install_root" "$workspace_a/scripts" \
  "$workspace_a/node_modules/.bin" "$workspace_b/scripts" "$workspace_b/node_modules/.bin" "$outside"
chmod 700 "$store" "$lima_home"
printf 'workspace-a\n' >"$workspace_a/marker.txt"
printf 'workspace-b\n' >"$workspace_b/marker.txt"

for workspace in "$workspace_a" "$workspace_b"; do
  cat >"$workspace/scripts/workspace-tool" <<'SCRIPT'
#!/bin/sh
set -eu
marker="$(cat marker.txt)"
printf 'marker=%s arg=%s pwd=%s\n' "$marker" "${1:-none}" "$PWD"
if [ "$#" -ge 2 ]; then
  printf '%s\n' "$1" >"$2"
fi
SCRIPT
  chmod 0755 "$workspace/scripts/workspace-tool"
  ln -s ../../scripts/workspace-tool "$workspace/node_modules/.bin/workspace-tool"
done

cat >"$workspace_a/scripts/not-executable" <<'SCRIPT'
#!/bin/sh
echo should-not-run
SCRIPT
chmod 0644 "$workspace_a/scripts/not-executable"
cat >"$workspace_a/scripts/missing-interpreter" <<'SCRIPT'
#!/definitely/missing/hideout-interpreter
echo should-not-run
SCRIPT
chmod 0755 "$workspace_a/scripts/missing-interpreter"
cat >"$outside/escape-target" <<SCRIPT
#!/bin/sh
printf 'fallback-ran\n' >"$outside/host-fallback-marker"
SCRIPT
chmod 0755 "$outside/escape-target"
ln -s "$outside/escape-target" "$workspace_a/escape-link"

CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -o "$workspace_a/hideout-workspace-exec-probe" ./cmd/hideout-gate-fsread
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath \
  -o "$workspace_a/incompatible-darwin-binary" ./cmd/hideout-gate-fsread

if [ -z "$package_archive" ]; then
  package_archive="$out/artifacts/hideout-041-candidate.tar.gz"
  scripts/package-local.sh --out "$package_archive" \
    >"$out/logs/package.out" 2>"$out/logs/package.err"
fi
package_archive="$(cd "$(dirname "$package_archive")" && pwd -P)/$(basename "$package_archive")"
tar -xzf "$package_archive" -C "$install_root"
prefix="$install_root/hideout"
candidate="$prefix/bin/hideout"
[ -x "$candidate" ]
"$candidate" package verify "$prefix" \
  >"$out/logs/package-verify.out" 2>"$out/logs/package-verify.err"

archive_sha="$(sha256_file "$package_archive")"
package_identity="$(jq -c --arg archiveSHA "$archive_sha" '{
  name:"hideout",productVersion:.release.productVersion,
  sourceCommit:.source.commit,artifactSHA256:$archiveSHA,
  hostOS:.target.hostOS,hostArch:.target.hostArch
}' "$prefix/package-manifest.json")"

HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" "$candidate" init \
  --template dev --profile gate041 --backend lima --network direct \
  --runtime developer-standard --no-input \
  >"$out/logs/init.out" 2>"$out/logs/init.err"

run_target() {
  local workspace="$1"
  shift
  HIDEOUT_STORE_ROOT="$store" LIMA_HOME="$lima_home" \
    "$candidate" run --profile gate041 --backend lima --workspace "$workspace" -- "$@"
}
reject_portal_unsupported() {
  if grep -Eiq 'operation not supported|EOPNOTSUPP' "$1"; then
    echo "workspace executable e2e: compatibility failure collapsed to Portal EOPNOTSUPP" >&2
    return 1
  fi
}
expect_exec_failure() {
  local name="$1"
  shift
  if run_target "$workspace_a" "$@" >"$work/$name.out" 2>"$work/$name.err"; then
    echo "workspace executable e2e: $name unexpectedly executed" >&2
    return 1
  fi
  reject_portal_unsupported "$work/$name.err"
}

run_target "$workspace_a" ./scripts/workspace-tool direct-script effect-script.txt \
  >"$work/direct-script.out" 2>"$work/direct-script.err"
grep -Fxq 'marker=workspace-a arg=direct-script pwd=/workspace' "$work/direct-script.out"
[ "$(cat "$workspace_a/effect-script.txt")" = direct-script ]

run_target "$workspace_a" ./hideout-workspace-exec-probe \
  --read marker.txt --deny missing.txt >"$work/direct-binary.out" 2>"$work/direct-binary.err"
grep -Fxq 'hostfs_go=workspace-a' "$work/direct-binary.out"
grep -Fxq 'hostfs_go_denied=yes' "$work/direct-binary.out"

run_target "$workspace_a" ./node_modules/.bin/workspace-tool launcher effect-launcher.txt \
  >"$work/launcher.out" 2>"$work/launcher.err"
grep -Fxq 'marker=workspace-a arg=launcher pwd=/workspace' "$work/launcher.out"
[ "$(cat "$workspace_a/effect-launcher.txt")" = launcher ]
run_target "$workspace_a" sh -eu -c 'test "$(cat effect-launcher.txt)" = launcher; printf "later-session-visible=yes\n"' \
  >"$work/later-session.out" 2>"$work/later-session.err"
grep -Fxq 'later-session-visible=yes' "$work/later-session.out"

expect_exec_failure permission ./scripts/not-executable
expect_exec_failure missing-interpreter ./scripts/missing-interpreter
expect_exec_failure incompatible-format ./incompatible-darwin-binary --read marker.txt --deny missing.txt
expect_exec_failure escaping-symlink ./escape-link
[ ! -e "$outside/host-fallback-marker" ]

for i in $(seq 1 "$iterations"); do
  if [ $((i % 2)) -eq 0 ]; then
    workspace="$workspace_a"
    want="workspace-a"
  else
    workspace="$workspace_b"
    want="workspace-b"
  fi
  run_target "$workspace" ./node_modules/.bin/workspace-tool "isolation-$i" \
    >"$work/isolation.out" 2>"$work/isolation.err"
  grep -Fxq "marker=$want arg=isolation-$i pwd=/workspace" "$work/isolation.out" || {
    echo "workspace executable e2e: disjoint workspace substitution at iteration $i" >&2
    exit 1
  }
done

direct_values="$out/artifacts/direct.values"
control_values="$out/artifacts/control.values"
: >"$direct_values"
: >"$control_values"
measure_run() {
  local values="$1" workspace="$2"
  shift 2
  local started finished
  started="$(python3 -c 'import time; print(time.time_ns())')"
  run_target "$workspace" "$@" >"$work/measure.out" 2>"$work/measure.err"
  finished="$(python3 -c 'import time; print(time.time_ns())')"
  python3 - "$started" "$finished" >>"$values" <<'PY'
import sys
print(f"{(int(sys.argv[2]) - int(sys.argv[1])) / 1_000_000:.6f}")
PY
}
for i in 1 2 3; do
  run_target "$workspace_a" ./node_modules/.bin/workspace-tool "warm-direct-$i" >/dev/null 2>&1
  run_target "$workspace_a" /bin/sh ./node_modules/.bin/workspace-tool "warm-control-$i" >/dev/null 2>&1
done
for i in $(seq 1 "$samples"); do
  if [ $((i % 2)) -eq 0 ]; then
    measure_run "$control_values" "$workspace_a" /bin/sh ./node_modules/.bin/workspace-tool "sample-$i"
    measure_run "$direct_values" "$workspace_a" ./node_modules/.bin/workspace-tool "sample-$i"
  else
    measure_run "$direct_values" "$workspace_a" ./node_modules/.bin/workspace-tool "sample-$i"
    measure_run "$control_values" "$workspace_a" /bin/sh ./node_modules/.bin/workspace-tool "sample-$i"
  fi
done

performance="$(python3 - "$direct_values" "$control_values" <<'PY'
import json
import math
import statistics
import sys

def values(path):
    with open(path, encoding="utf-8") as handle:
        return [float(line) for line in handle if line.strip()]

direct = values(sys.argv[1])
control = values(sys.argv[2])
ordered = sorted(direct)
p95 = ordered[max(0, math.ceil(len(ordered) * 0.95) - 1)]
result = {
    "samples": len(direct),
    "warmFirstOutputP95Ms": p95,
    "medianRegressionRatio": statistics.median(direct) / statistics.median(control),
}
print(json.dumps(result, separators=(",", ":")))
PY
)"
warm_p95="$(jq -r '.warmFirstOutputP95Ms' <<<"$performance")"
median_ratio="$(jq -r '.medianRegressionRatio' <<<"$performance")"
if [ "$probe" = "0" ]; then
  jq -e '.samples >= 30 and .warmFirstOutputP95Ms > 0 and .warmFirstOutputP95Ms <= 2000 and .medianRegressionRatio > 0 and .medianRegressionRatio <= 1.10' \
    <<<"$performance" >/dev/null
fi

record="$(find "$store/environments" -name environment.json -type f -print -quit)"
[ -f "$record" ]
record_count="$(find "$store/environments" -name environment.json -type f | wc -l | tr -d ' ')"
[ "$record_count" = 1 ]
jq -e '.mode == "shared" and (.sharedSlot | length) > 0 and
  .dedicatedWorkspace == null and .boundWorkspace == null' "$record" >/dev/null
instance_count="$(LIMA_HOME="$lima_home" limactl list --quiet | awk 'NF {count++} END {print count+0}')"
[ "$instance_count" = 1 ]
catalog="$(find "$prefix" -path '*/runtime/catalog.json' -type f -print -quit)"
[ -f "$catalog" ]
build_commit="$(jq -r --slurpfile record "$record" '
  .families[] | select(.id == $record[0].runtime.family) |
  .revisions[] | select(.id == $record[0].runtime.revision) | .artifacts[] |
  select(.hostOS == $record[0].runtime.hostOS and
         .hostArch == $record[0].runtime.hostArch and
         .guestArch == $record[0].runtime.guestArch and
         .sha256 == $record[0].runtime.artifactSHA256) | .source.buildCommit
' "$catalog")"
runtime="$(jq -c --arg buildCommit "$build_commit" '{
  schema:"hideout.runtime-evidence-binding/v1",family:.runtime.family,
  revision:.runtime.revision,artifactSHA256:.runtime.artifactSHA256,
  environmentId:.id,hostOS:.runtime.hostOS,hostArch:.runtime.hostArch,
  guestArch:.runtime.guestArch,buildCommit:$buildCommit,buildDirty:false
}' "$record")"

jq -n --arg commit "$(git rev-parse HEAD)" --argjson dirty "$(git_dirty)" \
  --argjson samples "$samples" --argjson warmP95 "$warm_p95" \
  --argjson medianRatio "$median_ratio" '{
  schema:"hideout.workspace-executable-gate2/v1",status:"passed",
  commit:$commit,dirty:$dirty,backend:"lima",hostOS:"darwin",hostArch:"arm64",
  guestArch:"aarch64",workspaceMechanism:"workspace-portal",
  checks:{checkoutWriteVisible:true,directBinary:true,directScript:true,
    disjointIsolation:true,escapingSymlinkRejected:true,
    incompatibleFormatFailurePreserved:true,laterSessionVisible:true,
    localLauncher:true,missingInterpreterFailurePreserved:true,
    noHostFallback:true,noWorkspaceCopy:true,permissionFailurePreserved:true,
    sharedModeObserved:true},
  samples:$samples,warmFirstOutputP95Ms:$warmP95,
  medianRegressionRatio:$medianRatio,nonClaims:{staticVirtiofs:"not-claimed"}
}' >"$out/artifacts/workspace-executable.json"

if [ "$probe" = "1" ]; then
  echo "workspace executable e2e: probe passed; no product proof emitted p95_ms=$warm_p95 median_ratio=$median_ratio"
  exit 0
fi

claims="$(claims_json "$proof_real")"
artifact="$(artifact_json artifacts/workspace-executable.json)"
jq -n --arg proofId "$proof_real" --argjson claims "$claims" \
  --argjson artifact "$artifact" --argjson runtime "$runtime" '[{
    proofId:$proofId,featureId:"041-workspace-executable-support",mode:"real-gate",
    evidenceClass:"workspace-executable-real-gate2",status:"passed",
    commandSummary:"validated direct shared-Portal scripts, binaries, launchers, boundaries, checkout effects, isolation, and warm performance",
    coveredClaims:$claims,
    prerequisites:[{name:"real-macos-arm64-lima-packaged",status:"available"}],
    artifacts:[$artifact],redactionStatus:"passed",runtime:$runtime
  }]' >"$out/reports/proofs.json"
write_manifest "$out/reports/proofs.json" "$package_identity"

HIDEOUT_041_EVIDENCE_DIR="$out" go test -count=1 ./internal/productevidence \
  >"$out/logs/production-evaluator.out" 2>"$out/logs/production-evaluator.err"

if grep -E 'claim_[0-9a-f]{16,}|cap_[A-Za-z0-9]{12,}|credential|endpoint|/Users/|/tmp/hideout-041-gate2' \
  "$out/artifacts/workspace-executable.json" "$manifest" >/dev/null 2>&1; then
  echo "workspace executable e2e: public evidence contains control-plane material" >&2
  exit 1
fi

trap - EXIT
cleanup
echo "workspace executable e2e: passed evidence=$manifest"
