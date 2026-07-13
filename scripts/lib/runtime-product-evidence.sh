#!/usr/bin/env bash
# Shared product-evidence helpers for the 031 real-runtime gates.

runtime_evidence_sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    echo "runtime-evidence: missing shasum or sha256sum" >&2
    return 127
  fi
}

runtime_evidence_git_commit() {
  # Package manifests and release binaries use the canonical 12-character
  # candidate identity. Evidence must use the same value or readiness will
  # correctly classify an otherwise matching proof as stale.
  git rev-parse --short=12 HEAD
}

runtime_evidence_git_dirty() {
  if [ -n "$(git status --porcelain --untracked-files=normal)" ]; then
    printf 'true\n'
  else
    printf 'false\n'
  fi
}

runtime_evidence_marker() {
  local log="$1" name="$2"
  sed -n "s/^${name}=//p" "$log" | tail -n 1
}

runtime_evidence_binding() {
  local log="$1"
  local family revision artifact environment host_os host_arch guest_arch build_commit build_dirty
  family="$(runtime_evidence_marker "$log" runtime_family)"
  revision="$(runtime_evidence_marker "$log" runtime_revision)"
  artifact="$(runtime_evidence_marker "$log" runtime_artifact_sha256)"
  environment="$(runtime_evidence_marker "$log" runtime_environment_id)"
  host_os="$(runtime_evidence_marker "$log" runtime_host_os)"
  host_arch="$(runtime_evidence_marker "$log" runtime_host_arch)"
  guest_arch="$(runtime_evidence_marker "$log" runtime_guest_arch)"
  build_commit="$(runtime_evidence_marker "$log" runtime_build_commit)"
  build_dirty="$(runtime_evidence_marker "$log" runtime_build_dirty)"

  if [ -z "$family" ] || [ -z "$revision" ] || [ -z "$environment" ] ||
     [ -z "$host_os" ] || [ -z "$host_arch" ] || [ -z "$guest_arch" ] ||
     ! printf '%s' "$artifact" | grep -Eq '^[0-9a-f]{64}$' ||
     ! printf '%s' "$build_commit" | grep -Eq '^[0-9a-f]{12,40}$'; then
    echo "runtime-evidence: incomplete runtime binding markers in $log" >&2
    return 2
  fi
  case "$build_dirty" in
    true | false) ;;
    *) echo "runtime-evidence: invalid image build dirty marker in $log" >&2; return 2 ;;
  esac

  jq -n \
    --arg schema "hideout.runtime-evidence-binding/v1" \
    --arg family "$family" \
    --arg revision "$revision" \
    --arg artifactSHA256 "$artifact" \
    --arg environmentId "$environment" \
    --arg hostOS "$host_os" \
    --arg hostArch "$host_arch" \
    --arg guestArch "$guest_arch" \
    --arg buildCommit "$build_commit" \
    --argjson buildDirty "$build_dirty" \
    '{schema:$schema,family:$family,revision:$revision,artifactSHA256:$artifactSHA256,
      environmentId:$environmentId,hostOS:$hostOS,hostArch:$hostArch,guestArch:$guestArch,
      buildCommit:$buildCommit,buildDirty:$buildDirty}'
}

runtime_evidence_add_proof() {
  local proofs="$1" registry="$2" proof_id="$3" mode="$4" evidence_class="$5"
  local summary="$6" artifact_path="$7" artifact_sha="$8" runtime_json="${9:-null}"
  local claims
  claims="$(jq -c --arg id "$proof_id" '
    [.requirements[] | select(.proofId == $id) | .claimIds[] |
      {claimId:.,source:"spec",description:"031 registered runtime evidence contract",scope:"real-runtime"}]
  ' "$registry")"
  if [ "$(jq 'length' <<<"$claims")" -eq 0 ]; then
    echo "runtime-evidence: proof id is not registered: $proof_id" >&2
    return 2
  fi

  jq -c \
    --arg proofId "$proof_id" \
    --arg mode "$mode" \
    --arg evidenceClass "$evidence_class" \
    --arg summary "$summary" \
    --arg artifactPath "$artifact_path" \
    --arg artifactSHA "$artifact_sha" \
    --argjson claims "$claims" \
    --argjson runtime "$runtime_json" \
    '. + [{
      proofId:$proofId,
      featureId:"031-supported-cli-runtime",
      mode:$mode,
      evidenceClass:$evidenceClass,
      status:"passed",
      commandSummary:$summary,
      coveredClaims:$claims,
      prerequisites:[{name:"real-macos-arm64-lima",status:"available"}],
      artifacts:[{kind:"log",path:$artifactPath,sha256:$artifactSHA,
        redactionStatus:"passed",description:"retained 031 real-runtime gate output"}],
      redactionStatus:"passed"
    } + if $runtime == null then {} else {runtime:$runtime} end]' <<<"$proofs"
}

runtime_evidence_write_manifest() {
  local manifest="$1" proofs="$2" package_commit="${3:-}"
  local commit dirty package_json="null"
  commit="$(runtime_evidence_git_commit)"
  dirty="$(runtime_evidence_git_dirty)"
  if [ -n "$package_commit" ]; then
    if ! printf '%s' "$package_commit" | grep -Eq '^[0-9a-f]{12,40}$'; then
      echo "runtime-evidence: package commit must be canonical" >&2
      return 2
    fi
    package_json="$(jq -n --arg version "$package_commit" '{name:"hideout",version:$version}')"
  fi
  jq -n \
    --arg generated "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    --arg commit "$commit" \
    --argjson dirty "$dirty" \
    --argjson packageIdentity "$package_json" \
    --argjson proofs "$proofs" \
    '{version:"hideout.product-hardening-evidence/v1",generatedAt:$generated,
      commit:$commit,dirty:$dirty,proofs:$proofs} +
      if $packageIdentity == null then {} else {packageIdentity:$packageIdentity} end' >"$manifest"
}
