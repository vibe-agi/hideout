#!/usr/bin/env bash

# Shared adapter for the historical 030/032 wrappers. A raw aggregate Gate 2
# log is no longer sufficient: real proofs must come from the unified 043
# exact-package artifact set and pass the production semantic evaluator.

strict_projection_evidence_root() {
  local input="$1"
  if [ -d "$input" ]; then
    input="$input/product-hardening-evidence.json"
  fi
  [ -f "$input" ] || return 1
  local root
  root="$(CDPATH= cd -- "$(dirname -- "$input")" && pwd -P)"
  [ "$root/product-hardening-evidence.json" = "$(cd "$(dirname "$input")" && pwd -P)/$(basename "$input")" ] || return 1
  jq -e '
    .version == "hideout.product-hardening-evidence/v1" and
    .dirty == false and
    any(.proofs[]; .proofId == "043.projection-readiness.real-gate2.readiness" and
      .status == "passed" and .evidenceClass == "projection-readiness-real-gate2")
  ' "$input" >/dev/null || return 1
  printf '%s\n' "$root"
}

validate_strict_projection_evidence() {
  local root="$1"
  HIDEOUT_043_EVIDENCE_DIR="$root" go test -count=1 ./internal/productevidence \
    -run '^TestRetainedProjectionReadinessEvidencePassesProductionEvaluator$' >/dev/null
}

copy_strict_projection_feature() {
  local source_root="$1" destination_root="$2" feature_id="$3"
  local source_manifest="$source_root/product-hardening-evidence.json"
  local relative current component
  validate_strict_projection_evidence "$source_root"
  while IFS= read -r relative; do
    case "$relative" in
      /*|../*|*/../*|*/..) echo "strict projection evidence has an unsafe artifact path: $relative" >&2; return 1 ;;
    esac
    current="$source_root"
    IFS='/' read -r -a components <<<"$relative"
    for component in "${components[@]}"; do
      current="$current/$component"
      [ ! -L "$current" ] || {
        echo "strict projection evidence contains a symlinked artifact: $relative" >&2
        return 1
      }
    done
    [ -f "$source_root/$relative" ] || {
      echo "strict projection evidence is missing artifact: $relative" >&2
      return 1
    }
    mkdir -p "$destination_root/$(dirname "$relative")"
    cp -P "$source_root/$relative" "$destination_root/$relative"
  done < <(jq -r --arg feature "$feature_id" '
    [.proofs[] | select(.featureId == $feature) | .artifacts[].path] |
    unique[]
  ' "$source_manifest")
  jq --arg feature "$feature_id" '
    .proofs |= map(select(.featureId == $feature)) |
    select((.proofs | length) > 0)
  ' "$source_manifest" >"$destination_root/product-hardening-evidence.json"
  go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json \
    "$destination_root/product-hardening-evidence.json" >/dev/null
}
