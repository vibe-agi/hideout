#!/usr/bin/env bash

# Print the canonical feature ID array only when the manifest contains the
# sorted, unique public-alpha feature set required by this release line.
public_alpha_required_feature_ids() {
  local manifest="${1:-}"
  if [ -z "$manifest" ] || [ ! -f "$manifest" ]; then
    echo "public-alpha-features: an existing manifest is required" >&2
    return 2
  fi
  jq -ce '
    .featureIds as $ids |
    if (($ids | type) == "array" and
        ($ids | length) > 0 and
        $ids == ($ids | sort | unique) and
        ($ids | index("045-operator-observability-console")) != null and
        ($ids | index("046-portable-hideout-migration")) != null)
    then $ids
    else error("manifest lacks the required public-alpha feature set")
    end
  ' "$manifest"
}

public_alpha_features_self_test() (
  set -euo pipefail
  local scratch valid
  scratch="$(mktemp -d "${TMPDIR:-/tmp}/hideout-public-alpha-features.XXXXXX")"
  trap 'rm -rf "$scratch"' EXIT
  valid="$scratch/valid.json"
  jq -n '{featureIds:[
    "045-operator-observability-console",
    "046-portable-hideout-migration"
  ]}' >"$valid"
  public_alpha_required_feature_ids "$valid" >/dev/null
  for mutation in missing reversed duplicate; do
    case "$mutation" in
      missing) jq 'del(.featureIds[1])' "$valid" >"$scratch/$mutation.json" ;;
      reversed) jq '.featureIds |= reverse' "$valid" >"$scratch/$mutation.json" ;;
      duplicate) jq '.featureIds[1] = .featureIds[0]' "$valid" >"$scratch/$mutation.json" ;;
    esac
    if public_alpha_required_feature_ids "$scratch/$mutation.json" >/dev/null 2>&1; then
      echo "public-alpha-features: $mutation fixture passed" >&2
      return 1
    fi
  done
)
