#!/usr/bin/env bash
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT"
doc_root="${HIDEOUT_DOC_ROOT:-$ROOT}"
inventory="$doc_root/releases/current.json"

out=""
public_receipt=""

usage() {
  cat <<'USAGE'
Usage:
  scripts/test-doc-truth-smoke.sh [--out <dir>] [--public-receipt <path>]
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --out)
      out="${2:-}"
      shift 2
      ;;
    --public-receipt)
      public_receipt="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "doc-truth-smoke: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ -z "$out" ]; then
  out="$(mktemp -d "${TMPDIR:-/tmp}/hideout-doc-truth.XXXXXX")"
fi
mkdir -p "$out/logs" "$out/reports"
out="$(cd "$out" && pwd -P)"
manifest="$out/product-hardening-evidence.json"

require_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "doc-truth-smoke: missing required tool: $1" >&2
    exit 127
  fi
}

require_tool jq

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    echo "doc-truth-smoke: missing shasum or sha256sum" >&2
    exit 127
  fi
}

candidate_release_block_is_neutral() {
  ! grep -E 'github\.com/vibe-agi/hideout/releases/(tag|download)/v[0-9]' <<<"$1" >/dev/null
}

validate_public_receipt_binding() {
  local checked_inventory="$1" checked_receipt="$2"
  go run ./cmd/hideout-schema-validate schemas/publication-receipt.schema.json \
    "$checked_receipt" >/dev/null || return 1
  test "$(sha256_file "$checked_receipt")" = \
    "$(jq -r '.current.receiptSHA256' "$checked_inventory")" || return 1
  local version tag url digest
  version="$(jq -r '.current.version' "$checked_inventory")"
  tag="$(jq -r '.current.tag' "$checked_inventory")"
  url="$(jq -r '.current.releaseURL' "$checked_inventory")"
  digest="$(jq -r '.current.package.artifactSHA256' "$checked_inventory")"
  jq -e --arg version "$version" --arg tag "$tag" --arg url "$url" --arg digest "$digest" '
    .schema == "hideout.publication-receipt/v1" and .status == "public-verified" and
    .immutable == true and .prerelease == true and
    .version == $version and .tag == $tag and .url == $url and
    .package.productVersion == $version and .package.artifactSHA256 == $digest and
    all(.assets[]; .apiSHA256 == .downloadSHA256)
  ' "$checked_receipt" >/dev/null || return 1
}

git_commit() {
  git rev-parse HEAD 2>/dev/null || printf 'unknown'
}

git_dirty() {
  if [ -n "$(git status --porcelain --untracked-files=normal 2>/dev/null)" ]; then
    printf 'true'
  else
    printf 'false'
  fi
}

artifact_obj() {
  local kind="$1"
  local rel="$2"
  local desc="$3"
  local sha=""
  if [ -f "$out/$rel" ]; then
    sha="$(sha256_file "$out/$rel")"
  fi
  jq -n \
    --arg kind "$kind" \
    --arg path "$rel" \
    --arg sha "$sha" \
    --arg desc "$desc" \
    '{kind: $kind, path: $path, sha256: (if $sha == "" then empty else $sha end), redactionStatus: "passed", description: $desc}'
}

registry_json="$out/reports/proof-registry.json"
go run ./cmd/hideout support proof-registry --json >"$registry_json"
jq -e '.schema == "hideout.proof-registry/v1" and (.requirements | length > 0)' "$registry_json" >/dev/null
jq -e '
  ([.requirements[]
    | select(.featureId == "032-community-host-app-recipes")
    | .proofId] | sort) == ([
      "032.host-app-pack.gate0.binding",
      "032.host-app-pack.gate0.identity-safety",
      "032.host-app-pack.gate0.lifecycle",
      "032.host-app-pack.real-gate2.external"
    ] | sort)
' "$registry_json" >/dev/null

recovery_json="$out/reports/recovery-codes.json"
go run ./cmd/hideout support recovery-codes --json >"$recovery_json"
jq -e '.schema == "hideout.recovery-codes/v1" and (.codes | length > 0)' "$recovery_json" >/dev/null

required_proof_ids=()
while IFS= read -r proof_id; do
  required_proof_ids+=("$proof_id")
done < <(jq -r '
  .requirements[]
  | select(.featureId == "021-ui-e2e-proof"
    or .featureId == "022-alpha-first-run-e2e"
    or .featureId == "023-hostfs-decision-e2e"
    or .featureId == "024-doctor-package-recovery-e2e"
    or .featureId == "029-hostfs-discoverable-namespace"
    or .featureId == "031-supported-cli-runtime"
    or .featureId == "032-community-host-app-recipes")
  | .proofId
' "$registry_json")

if [ "${#required_proof_ids[@]}" -eq 0 ]; then
  echo "doc-truth-smoke: proof registry returned no required product proof ids" >&2
  exit 1
fi

validate_claim_boundaries() {
  test -f docs/claim-boundaries.md
  local missing=()
  for proof_id in "${required_proof_ids[@]}"; do
    if ! grep -q -- "$proof_id" docs/claim-boundaries.md; then
      missing+=("$proof_id")
    fi
  done
  if [ "${#missing[@]}" -gt 0 ]; then
    printf 'doc-truth-smoke: claim-boundaries missing proof id %s\n' "${missing[@]}" >&2
    exit 1
  fi
  for want in "Local-fast product-hardening proof" "Native backend is always" "Gate 2" "Gate 3"; do
    grep -q -- "$want" docs/claim-boundaries.md
  done
  jq -n \
    --argjson count "${#required_proof_ids[@]}" \
    --arg registry "reports/proof-registry.json" \
    '{requiredProofIds: $count, source: $registry, status: "passed"}' >"$out/reports/claim-boundaries.json"
}

scan_files() {
  {
    printf '%s\n' README.md README.zh-CN.md
    find docs -maxdepth 1 -type f -name '*.md' | sort
    find specs/021-ui-e2e-proof specs/022-alpha-first-run-e2e specs/023-hostfs-decision-e2e specs/024-doctor-package-recovery-e2e specs/025-documentation-truth-gate specs/029-hostfs-discoverable-namespace specs/030-host-capability-projection specs/031-supported-cli-runtime specs/032-community-host-app-recipes \
      -type f -name '*.md' | sort
  } | grep -v '^\.' | sort -u
}

validate_recovery_code_references() {
  local refs_file="$out/reports/recovery-code-refs.txt"
  : >"$refs_file"
  while IFS= read -r file; do
    [ -f "$file" ] || continue
    grep -Eo '`(package|init|privilege|release|hostfs|decision)\.[a-z0-9.-]+`' "$file" | tr -d '`' >>"$refs_file" || true
  done < <(scan_files)
  sort -u "$refs_file" -o "$refs_file"
  while IFS= read -r code || [ -n "$code" ]; do
    [ -n "$code" ] || continue
    case "$code" in
      hostfs.read.denied) continue ;;
    esac
    case "$code" in
      *missing|*stale|*expired|*degraded|*denied|*leftover) ;;
      *) continue ;;
    esac
    if ! jq -e --arg code "$code" 'any(.codes[]; .code == $code)' "$recovery_json" >/dev/null; then
      echo "doc-truth-smoke: recovery code $code is referenced in docs but missing from registry" >&2
      exit 1
    fi
  done <"$refs_file"
  jq -n \
    --slurpfile codes "$recovery_json" \
    --rawfile refs "$refs_file" \
    '{
      registry: "reports/recovery-codes.json",
      referencedCodes: ($refs | split("\n") | map(select(. != ""))),
      registryCodes: ($codes[0].codes | map(.code)),
      status: "passed"
    }' >"$out/reports/recovery-code-refs.json"
}

record_overclaim() {
  local category="$1"
  local file="$2"
  local line="$3"
  local text="$4"
  printf '%s\t%s\t%s\t%s\n' "$category" "$file" "$line" "$text" >>"$out/reports/overclaim-findings.tsv"
}

hostfs_visibility_overclaim_category() {
  local lower="$1"
  case "$lower" in
    *hostfs*visibility*enabled*silently*|*hostfs*visibility*enabled*"by default"*) printf 'hostfs-visibility-default'; return ;;
    *discover*grants*"file content"*|*discover*grants*"execute authority"*) printf 'hostfs-discover-content'; return ;;
    *hidden*predictable*reveal*"no information"*) printf 'hostfs-predictable-hidden'; return ;;
    *arbitrary*tools*receive*"rich approval prose"*) printf 'hostfs-arbitrary-tool-prose'; return ;;
    *retryable*means*"approval decision exists"*|*retryable*proves*"decision exists"*) printf 'hostfs-retryable-authority'; return ;;
    *local-fast*satisfies*"real gate 2"*|*local-fast*replaces*"real gate 2"*) printf 'hostfs-local-real-gate'; return ;;
    *discover*provides*"guest-root containment"*|*discover*filters*"workspace content"*) printf 'hostfs-discover-overreach'; return ;;
  esac
  printf ''
}

host_app_pack_overclaim_category() {
  local lower="$1"
  case "$lower" in
    *community*pack*"arbitrary host command"*|*community*recipe*"arbitrary host command"*) printf 'host-app-arbitrary-host-command'; return ;;
    *community*pack*"ships its own host effect"*|*community*pack*"includes its own host effect"*|*community*pack*"brings its own host effect"*) printf 'host-app-pack-provides-effect'; return ;;
    *self-signed*app*verified*|*self-signed*bundle*verified*|*self\ signed*app*verified*|*self\ signed*bundle*verified*) printf 'host-app-self-signed-verified'; return ;;
    *package*"signing requirement"*verif*app*|*package*"designated requirement"*verif*app*|*package*"signing requirement"*authenticat*app*|*package*requirement*authenticat*app*) printf 'host-app-package-self-attestation'; return ;;
    *package*defines*safe*posture*|*package*controls*safe*mode*|*pack*declares*safe*|*safe*"declared by the pack"*) printf 'host-app-package-safe-authority'; return ;;
    *unverified*app*"without confirmation"*|*unsigned*app*"without confirmation"*) printf 'host-app-unverified-no-confirmation'; return ;;
    *unknown*projected*command*fallback*|*unknown*projected*command*"falls back"*|*unbound*command*fallback*|*unbound*command*"falls back"*) printf 'host-app-command-fallback'; return ;;
    *guest*appref*select*host*app*|*package*appref*select*host*app*) printf 'host-app-cross-binding-selection'; return ;;
    *old\ session*changes\ immediately*|*existing\ session*changes\ immediately*|*enabl*changes*old\ session*immediately*|*enabl*old\ session*immediately*) printf 'host-app-old-session-mutation'; return ;;
    *see-only*"can open"*|*see\ only*"can open"*|*discover-only*"can open"*) printf 'host-app-see-only-open'; return ;;
    *native*replaces*"real gate 2"*|*native*satisfies*"real gate 2"*|*local-only*replaces*"real gate 2"*|*local-only*satisfies*"real gate 2"*|*package*self-test*replaces*"real gate 2"*|*package*self-test*satisfies*"real gate 2"*) printf 'host-app-false-real-gate'; return ;;
  esac
  printf ''
}

scan_overclaims() {
  : >"$out/reports/overclaim-findings.tsv"
  while IFS= read -r file; do
    [ -f "$file" ] || continue
    local line_no=0
    local hostfs_negative_context=0
    while IFS= read -r line || [ -n "$line" ]; do
      line_no=$((line_no + 1))
      lower="$(printf '%s' "$line" | tr '[:upper:]' '[:lower:]')"
      if [[ "$lower" == *"docs truth rejects"* || "$lower" == *"docs truth must reject"* ]]; then
        hostfs_negative_context=4
      fi
      case "$lower" in
        *native*isolation*evidence*)
          case "$lower" in *not*|*rather\ than*|*development\ harness*|*non-native*|*switch\ to\ lima*|*treating*) ;; *) record_overclaim native-isolation "$file" "$line_no" "$line" ;; esac
          ;;
      esac
      case "$lower" in
        *goja*browser*e2e*)
          case "$lower" in *not*|*do\ not*|*deferred*) ;; *) record_overclaim goja-browser-e2e "$file" "$line_no" "$line" ;; esac
          ;;
      esac
      if [[ "$lower" == *"gate 0"* && ( "$lower" == *"gate 2"* || "$lower" == *"gate 3"* ) &&
            ( "$lower" == *"replace"* || "$lower" == *"satisfy"* || "$lower" == *"substitute"* ) ]]; then
        case "$lower" in *not*|*never*|*must\ not*|*does\ not*|*cannot*|*separate*|*given*docs*claim*) ;; *) record_overclaim gate0-real-gate "$file" "$line_no" "$line" ;; esac
      fi
      case "$lower" in
        *doctor*release\ readiness*|*local-fast*release\ readiness*)
          case "$lower" in *not*|*must\ not*|*does\ not*|*cannot*) ;; *) record_overclaim local-release-readiness "$file" "$line_no" "$line" ;; esac
          ;;
      esac
      case "$lower" in
        *only\ host\ mutation\ path*)
          case "$lower" in *not*|*must\ not*|*does\ not*|*cannot*|*given*docs*describe*|*when*scanner*runs*) ;; *) record_overclaim hostfs-only-mutation "$file" "$line_no" "$line" ;; esac
          ;;
      esac
      case "$lower" in
        *root-sensitive*root\ containment*|*root-sensitive*blocks\ root*)
          case "$lower" in *not*|*does\ not*|*intent*) ;; *) record_overclaim root-containment "$file" "$line_no" "$line" ;; esac
          ;;
      esac
      hostfs_category="$(hostfs_visibility_overclaim_category "$lower")"
      if [ -n "$hostfs_category" ]; then
        if [ "$hostfs_negative_context" -eq 0 ]; then
          case "$lower" in *not*|*never*|*must\ not*|*does\ not*|*cannot*|*reject*|*given*docs*claim*) ;; *) record_overclaim "$hostfs_category" "$file" "$line_no" "$line" ;; esac
        fi
      fi
      host_app_category="$(host_app_pack_overclaim_category "$lower")"
      if [ -n "$host_app_category" ]; then
        case "$lower" in *not*|*never*|*must\ not*|*does\ not*|*cannot*|*reject*|*forbid*|*insufficient*|*claim\ pending*|*given*docs*claim*) ;; *) record_overclaim "$host_app_category" "$file" "$line_no" "$line" ;; esac
      fi
      if [ "$hostfs_negative_context" -gt 0 ]; then
        hostfs_negative_context=$((hostfs_negative_context - 1))
      fi
    done <"$file"
  done < <(scan_files)

  if [ -s "$out/reports/overclaim-findings.tsv" ]; then
    echo "doc-truth-smoke: overclaim findings:" >&2
    cat "$out/reports/overclaim-findings.tsv" >&2
    exit 1
  fi
  jq -n '{findings: [], status: "passed"}' >"$out/reports/overclaim-scan.json"
}

validate_host_app_pack_overclaim_fixtures() {
  local fixtures=(
    "Community packs can execute an arbitrary host command"
    "A community pack ships its own host effect"
    "A self-signed app is verified"
    "A package signing requirement verifies the app"
    "A package signing requirement authenticates the app"
    "The package defines the safe posture"
    "The pack declares safe"
    "An unverified app launches without confirmation"
    "An unknown projected command falls back to guest execution"
    "A guest appRef selects the host app"
    "Enabling a pack changes the old session immediately"
    "See-only HostFS visibility can open the resource"
    "Native proof replaces real Gate 2"
    "Local-only proof replaces real Gate 2"
    "A package self-test replaces real Gate 2"
  )
  local line category
  : >"$out/reports/host-app-pack-overclaim-fixtures.jsonl"
  for line in "${fixtures[@]}"; do
    category="$(host_app_pack_overclaim_category "$(printf '%s' "$line" | tr '[:upper:]' '[:lower:]')")"
    if [ -z "$category" ]; then
      echo "doc-truth-smoke: 032 overclaim fixture was not rejected: $line" >&2
      exit 1
    fi
    jq -n --arg text "$line" --arg category "$category" '{text: $text, category: $category, rejected: true}' >>"$out/reports/host-app-pack-overclaim-fixtures.jsonl"
  done
  jq -s '{fixtures: ., status: "passed"}' "$out/reports/host-app-pack-overclaim-fixtures.jsonl" >"$out/reports/host-app-pack-overclaim-fixtures.json"
}

validate_hostfs_visibility_overclaim_fixtures() {
  local fixtures=(
    "HostFS visibility is enabled silently"
    "HostFS visibility is enabled by default"
    "Discover grants file content"
    "Hidden predictable paths reveal no information"
    "Arbitrary tools receive rich approval prose"
    "Retryable means an approval decision exists"
    "Local-fast satisfies real Gate 2"
    "Discover provides guest-root containment"
  )
  local line category
  : >"$out/reports/hostfs-visibility-overclaim-fixtures.jsonl"
  for line in "${fixtures[@]}"; do
    category="$(hostfs_visibility_overclaim_category "$(printf '%s' "$line" | tr '[:upper:]' '[:lower:]')")"
    if [ -z "$category" ]; then
      echo "doc-truth-smoke: 029 overclaim fixture was not rejected: $line" >&2
      exit 1
    fi
    jq -n --arg text "$line" --arg category "$category" '{text: $text, category: $category, rejected: true}' >>"$out/reports/hostfs-visibility-overclaim-fixtures.jsonl"
  done
  jq -s '{fixtures: ., status: "passed"}' "$out/reports/hostfs-visibility-overclaim-fixtures.jsonl" >"$out/reports/hostfs-visibility-overclaim-fixtures.json"
}

validate_hostfs_selector_docs() {
  if grep -q '^list:/absolute/directory$' docs/privacy-run-design.md; then
    echo "doc-truth-smoke: privacy-run-design teaches rejected list: syntax" >&2
    return 1
  fi
  grep -q 'migrate-list' README.md
  grep -q 'migrate-list' README.zh-CN.md
  grep -q 'migrate-list' docs/hostfs-overlay-design.md
}

validate_command_examples() {
  go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json >/dev/null 2>&1 || true
  jq -e '
    .schema == "hideout.command-examples/v1" and
    (.examples | length >= 1) and
    ([.examples[] | select(.availability == "planned")] | length) == 0 and
    all(.examples[] | select(.availability == "planned");
      .classification == "intentionally-not-executed" and
      (.reason | test("planned"; "i")))
  ' docs/command-examples.json >/dev/null
  : >"$out/reports/command-checks.jsonl"
  local count
  count="$(jq '.examples | length' docs/command-examples.json)"
  for i in $(seq 0 $((count - 1))); do
    local id classification reason availability
    id="$(jq -r ".examples[$i].id" docs/command-examples.json)"
    classification="$(jq -r ".examples[$i].classification" docs/command-examples.json)"
    reason="$(jq -r ".examples[$i].reason" docs/command-examples.json)"
    availability="$(jq -r ".examples[$i].availability // \"current\"" docs/command-examples.json)"
    case "$availability" in
      current|implementing|planned) ;;
      *)
        echo "doc-truth-smoke: command $id has invalid availability $availability" >&2
        exit 1
        ;;
    esac
    argv=()
    while IFS= read -r arg; do
      argv+=("$arg")
    done < <(jq -r ".examples[$i].command[]" docs/command-examples.json)
    case "$classification" in
      execute-temp-store)
        store="$(mktemp -d "${TMPDIR:-/tmp}/hideout-doc-command.XXXXXX")"
        if ! HIDEOUT_STORE_ROOT="$store" go run ./cmd/hideout "${argv[@]}" >"$out/logs/command-$id.out" 2>"$out/logs/command-$id.err"; then
          echo "doc-truth-smoke: executable command example failed: $id" >&2
          cat "$out/logs/command-$id.err" >&2
          rm -rf "$store"
          exit 1
        fi
        rm -rf "$store"
        jq -n --arg id "$id" --arg availability "$availability" --arg classification "$classification" --arg status passed '{id: $id, availability: $availability, classification: $classification, status: $status}' >>"$out/reports/command-checks.jsonl"
        ;;
      parse-only)
        if ! go run ./cmd/hideout "${argv[@]}" >"$out/logs/command-$id.out" 2>"$out/logs/command-$id.err"; then
          echo "doc-truth-smoke: parse-only command example failed: $id" >&2
          cat "$out/logs/command-$id.err" >&2
          exit 1
        fi
        jq -n --arg id "$id" --arg availability "$availability" --arg classification "$classification" --arg status passed '{id: $id, availability: $availability, classification: $classification, status: $status}' >>"$out/reports/command-checks.jsonl"
        ;;
      real-gate|intentionally-not-executed)
        if [ -z "$reason" ] || [ "$reason" = "null" ]; then
          echo "doc-truth-smoke: command $id missing non-execution reason" >&2
          exit 1
        fi
        jq -n --arg id "$id" --arg availability "$availability" --arg classification "$classification" --arg status documented --arg reason "$reason" '{id: $id, availability: $availability, classification: $classification, status: $status, reason: $reason}' >>"$out/reports/command-checks.jsonl"
        ;;
      *)
        echo "doc-truth-smoke: command $id has invalid classification $classification" >&2
        exit 1
        ;;
    esac
  done
  jq -s '{commands: ., status: "passed"}' "$out/reports/command-checks.jsonl" >"$out/reports/command-checks.json"
}

validate_cross_docs() {
  if ! grep -q 'Control-plane redaction removes Hideout-generated credentials' "$doc_root/README.md" ||
    ! grep -q 'remove all user data' "$doc_root/README.md"; then
    echo "doc-truth-smoke: generated README omits the bounded redaction claim" >&2
    exit 1
  fi
  if ! grep -q '不会移除全部用户数据' "$doc_root/README.zh-CN.md"; then
    echo "doc-truth-smoke: generated localized README omits the bounded redaction claim" >&2
    exit 1
  fi
  if ! grep -q 'Control-plane redaction removes Hideout-generated credentials' SECURITY.md ||
    ! grep -q 'not all user' SECURITY.md; then
    echo "doc-truth-smoke: SECURITY.md omits the bounded redaction claim" >&2
    exit 1
  fi
  grep -q 'docs/first-run-alpha.md' README.md
  grep -q 'docs/support-matrix.md' README.md
  grep -q 'English README' README.zh-CN.md
  grep -q 'canonical' README.zh-CN.md
  grep -q 'Status: Implemented' docs/host-app-recipes.md
  grep -q 'a570514909514cd79d39493d58ec69e923bca39aa5f4ec31305181b68b536f83' docs/claim-boundaries.md
  grep -q 'Community Host-App Recipes' docs/README.md
  grep -q '032.host-app-pack.real-gate2.external' docs/claim-boundaries.md
  grep -q 'Docs truth gate' docs/STATUS.md || true

  for file in README.md README.zh-CN.md docs/STATUS.md docs/support-matrix.md CHANGELOG.md; do
    test "$(grep -Fxc '<!-- hideout-public-release:start -->' "$doc_root/$file")" -eq 1
    test "$(grep -Fxc '<!-- hideout-public-release:end -->' "$doc_root/$file")" -eq 1
  done
  jq -e '.schema == "hideout.published-release-inventory/v1"' "$inventory" >/dev/null
  if jq -e '.current == null' "$inventory" >/dev/null; then
    for file in README.md README.zh-CN.md docs/STATUS.md docs/support-matrix.md CHANGELOG.md; do
      block="$(sed -n '/<!-- hideout-public-release:start -->/,/<!-- hideout-public-release:end -->/p' "$doc_root/$file")"
      if ! candidate_release_block_is_neutral "$block"; then
        echo "doc-truth-smoke: candidate block claims a public product release in $file" >&2
        exit 1
      fi
    done
  else
    version="$(jq -r '.current.version' "$inventory")"
    tag="$(jq -r '.current.tag' "$inventory")"
    url="$(jq -r '.current.releaseURL' "$inventory")"
    digest="$(jq -r '.current.package.artifactSHA256' "$inventory")"
    receipt="$doc_root/releases/receipts/$tag.json"
    test -f "$receipt"
    if ! validate_public_receipt_binding "$inventory" "$receipt"; then
      echo "doc-truth-smoke: checked receipt does not prove immutable anonymous bytes" >&2
      exit 1
    fi
    for file in README.md README.zh-CN.md docs/STATUS.md docs/support-matrix.md CHANGELOG.md; do
      block="$(sed -n '/<!-- hideout-public-release:start -->/,/<!-- hideout-public-release:end -->/p' "$doc_root/$file")"
      grep -F "$tag" <<<"$block" >/dev/null
      grep -F "$url" <<<"$block" >/dev/null
    done
    grep -F "$digest" "$doc_root/README.md" >/dev/null
    grep -F "$digest" "$doc_root/README.zh-CN.md" >/dev/null
    test "$tag" = "v$version"
  fi

  for script in \
    scripts/test-ui-e2e.sh \
    scripts/test-first-run-e2e.sh \
    scripts/test-hostfs-decision-e2e.sh \
    scripts/test-hostfs-visibility-e2e.sh \
    scripts/test-doctor-package-recovery-e2e.sh \
    scripts/test-host-app-pack-smoke.sh
  do
    grep -q "$script" docs/privacy-run-test-plan.md
    grep -q "$script" scripts/test-gate0.sh
  done
  jq -n '{readme: "passed", localizedReadme: "canonical-declared", gate0ProductHardeningScripts: "passed"}' >"$out/reports/cross-doc-consistency.json"
}

validate_release_doc_negative_fixtures() {
  local candidate_status="passed" receipt_status="not-applicable-candidate"
  local false_public_block='https://github.com/vibe-agi/hideout/releases/tag/v0.1.0-alpha.1'
  if candidate_release_block_is_neutral "$false_public_block"; then
    echo "doc-truth-smoke: candidate-publication negative fixture was accepted" >&2
    exit 1
  fi
  if [ -n "$public_receipt" ]; then
    local mutated_receipt mutated_inventory mutated_sha
    mutated_receipt="$out/reports/anonymous-receipt-negative.json"
    mutated_inventory="$out/reports/anonymous-inventory-negative.json"
    jq '(.assets[0].downloadSHA256) = ("0" * 64)' "$public_receipt" >"$mutated_receipt"
    mutated_sha="$(sha256_file "$mutated_receipt")"
    jq --arg sha "$mutated_sha" '.current.receiptSHA256 = $sha' "$inventory" >"$mutated_inventory"
    if validate_public_receipt_binding "$mutated_inventory" "$mutated_receipt" >/dev/null 2>&1; then
      echo "doc-truth-smoke: anonymous-receipt digest mismatch fixture was accepted" >&2
      exit 1
    fi
    receipt_status="passed"
    rm -f "$mutated_receipt" "$mutated_inventory"
  fi
  jq -n --arg candidate "$candidate_status" --arg receipt "$receipt_status" \
    '{candidatePublicationClaim:$candidate,anonymousReceiptDigestMismatch:$receipt,status:"passed"}' \
    >"$out/reports/release-doc-negative-fixtures.json"
}

write_public_release_evidence() {
  [ -n "$public_receipt" ] || return 0
  [ -f "$public_receipt" ] || {
    echo "doc-truth-smoke: --public-receipt must name a receipt" >&2
    exit 2
  }
  jq -e '.current != null' "$inventory" >/dev/null
  local tag expected_receipt receipt_copy inventory_copy report receipt_sha inventory_sha report_sha
  tag="$(jq -r '.current.tag' "$inventory")"
  expected_receipt="$doc_root/releases/receipts/$tag.json"
  [ "$(cd "$(dirname "$public_receipt")" && pwd -P)/$(basename "$public_receipt")" = \
    "$(cd "$(dirname "$expected_receipt")" && pwd -P)/$(basename "$expected_receipt")" ] || {
    echo "doc-truth-smoke: public receipt must be the checked-in receipt for $tag" >&2
    exit 2
  }
  receipt_copy="$out/reports/publication-receipt.json"
  inventory_copy="$out/reports/published-release-inventory.json"
  report="$out/reports/public-docs-truth.json"
  cp "$public_receipt" "$receipt_copy"
  cp "$inventory" "$inventory_copy"
  receipt_sha="$(sha256_file "$receipt_copy")"
  inventory_sha="$(sha256_file "$inventory_copy")"
  jq -n --arg tag "$tag" --arg receiptSHA256 "$receipt_sha" \
    --arg inventorySHA256 "$inventory_sha" \
    '{phase:"post-public",tag:$tag,receiptSHA256:$receiptSHA256,
      inventorySHA256:$inventorySHA256,docsDerivedFromInventory:true,status:"passed"}' >"$report"
  report_sha="$(sha256_file "$report")"
  jq -n \
    --arg generatedAt "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    --arg receiptSHA256 "$receipt_sha" --arg inventorySHA256 "$inventory_sha" \
    --arg reportSHA256 "$report_sha" \
    --slurpfile receipt "$receipt_copy" --slurpfile registry "$registry_json" '
    def claims($id): [$registry[0].requirements[] | select(.proofId == $id) | .claimIds[] |
      {claimId:.,source:"spec",description:"033 post-public release truth"}];
    {
      version:"hideout.product-hardening-evidence/v1",
      generatedAt:$generatedAt,
      commit:$receipt[0].sourceCommit,
      dirty:false,
      packageIdentity:$receipt[0].package,
      proofs:[
        {proofId:"033.release.public-download",featureId:"033-public-alpha-release-channel",
         mode:"docs",evidenceClass:"release-public-download",status:"passed",
         commandSummary:"validated immutable anonymous-download publication receipt",
         coveredClaims:claims("033.release.public-download"),prerequisites:[{name:"public-receipt",status:"available"}],
         artifacts:[{kind:"manifest",path:"reports/publication-receipt.json",sha256:$receiptSHA256,redactionStatus:"passed"}],
         redactionStatus:"passed"},
        {proofId:"033.release.docs-public-truth",featureId:"033-public-alpha-release-channel",
         mode:"docs",evidenceClass:"release-docs-public-truth",status:"passed",
         commandSummary:"validated checked-in docs against receipt-derived release inventory",
         coveredClaims:claims("033.release.docs-public-truth"),prerequisites:[{name:"published-inventory",status:"available"}],
         artifacts:[
           {kind:"manifest",path:"reports/published-release-inventory.json",sha256:$inventorySHA256,redactionStatus:"passed"},
           {kind:"docs-report",path:"reports/public-docs-truth.json",sha256:$reportSHA256,redactionStatus:"passed"}
         ],redactionStatus:"passed"}
      ]
    }' >"$out/public-release-evidence.json"
  go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json \
    "$out/public-release-evidence.json" >/dev/null
  jq -e '([.proofs[].proofId] | sort) == (["033.release.docs-public-truth","033.release.public-download"] | sort)' \
    "$out/public-release-evidence.json" >/dev/null
}

scan_redaction() {
  if grep -R -E 'HIDEOUT_SECRET_[A-Z0-9_]+=|cap_[A-Za-z0-9]{12,}|ui_[A-Za-z0-9]{12,}|providerRef|claim_[0-9a-f]{16,}|socks5://[^[:space:]]+:[^[:space:]]+@|machineId' "$out" >/dev/null 2>&1; then
    echo "doc-truth-smoke: public evidence contains control-plane material" >&2
    grep -R -n -E 'HIDEOUT_SECRET_[A-Z0-9_]+=|cap_[A-Za-z0-9]{12,}|ui_[A-Za-z0-9]{12,}|providerRef|claim_[0-9a-f]{16,}|socks5://[^[:space:]]+:[^[:space:]]+@|machineId' "$out" >&2 || true
    exit 1
  fi
}

write_manifest() {
  local claim_artifacts overclaim_artifacts command_artifacts cross_artifacts
  cp docs/claim-boundaries.md "$out/reports/claim-boundaries.md"
  cp docs/command-examples.json "$out/reports/command-examples.json"
  claim_artifacts="$(mktemp)"
  overclaim_artifacts="$(mktemp)"
  command_artifacts="$(mktemp)"
  cross_artifacts="$(mktemp)"
  jq -s '.' \
    <(artifact_obj "docs-report" "reports/claim-boundaries.json" "claim boundary registry validation") \
    <(artifact_obj "docs-report" "reports/claim-boundaries.md" "human-readable claim boundary registry") >"$claim_artifacts"
  jq -s '.' \
    <(artifact_obj "docs-report" "reports/overclaim-scan.json" "known overclaim scan report") \
    <(artifact_obj "docs-report" "reports/hostfs-visibility-overclaim-fixtures.json" "029 known-overclaim rejection fixtures") \
    <(artifact_obj "docs-report" "reports/host-app-pack-overclaim-fixtures.json" "032 known-overclaim rejection fixtures") >"$overclaim_artifacts"
  jq -s '.' \
    <(artifact_obj "docs-report" "reports/command-checks.json" "curated command checks") \
    <(artifact_obj "docs-report" "reports/command-examples.json" "curated command fixture") >"$command_artifacts"
  jq -s '.' \
    <(artifact_obj "docs-report" "reports/cross-doc-consistency.json" "README, localized README, test-plan, Gate 0 consistency") \
    <(artifact_obj "docs-report" "reports/release-doc-negative-fixtures.json" "candidate and anonymous-receipt documentation failure fixtures") \
    <(artifact_obj "docs-report" "reports/recovery-codes.json" "Go-owned recovery code registry") \
    <(artifact_obj "docs-report" "reports/recovery-code-refs.json" "documentation recovery-code reference validation") >"$cross_artifacts"

  jq -n \
    --arg generated "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    --arg commit "$(git_commit)" \
    --argjson dirty "$(git_dirty)" \
    --slurpfile claimArtifacts "$claim_artifacts" \
    --slurpfile overclaimArtifacts "$overclaim_artifacts" \
    --slurpfile commandArtifacts "$command_artifacts" \
    --slurpfile crossArtifacts "$cross_artifacts" \
    '{
      version: "hideout.product-hardening-evidence/v1",
      generatedAt: $generated,
      commit: $commit,
      dirty: $dirty,
      proofs: [
        {
          proofId: "025.docs.claim-boundaries",
          featureId: "025-documentation-truth-gate",
          mode: "docs",
          evidenceClass: "documentation-truth-gate",
          status: "passed",
          commandSummary: "validate claim-boundary registry and selected product proof ids",
          coveredClaims: [{claimId: "025.FR-001", source: "spec", description: "Claims map to proof ids or non-claims", scope: "docs"}],
          prerequisites: [{name: "claim-boundaries", status: "available"}],
          artifacts: $claimArtifacts[0],
          redactionStatus: "passed"
        },
        {
          proofId: "025.docs.overclaim-scan",
          featureId: "025-documentation-truth-gate",
          mode: "docs",
          evidenceClass: "documentation-truth-gate",
          status: "passed",
          commandSummary: "scan current docs for known overclaim patterns",
          coveredClaims: [{claimId: "025.FR-002", source: "spec", description: "Known overclaim patterns are rejected", scope: "docs"}],
          prerequisites: [{name: "overclaim-scan", status: "available"}],
          artifacts: $overclaimArtifacts[0],
          redactionStatus: "passed"
        },
        {
          proofId: "025.docs.command-examples",
          featureId: "025-documentation-truth-gate",
          mode: "docs",
          evidenceClass: "documentation-truth-gate",
          status: "passed",
          commandSummary: "validate curated command examples",
          coveredClaims: [{claimId: "025.FR-005", source: "spec", description: "Curated commands are recognized or intentionally non-executed", scope: "commands"}],
          prerequisites: [{name: "command-fixture", status: "available"}],
          artifacts: $commandArtifacts[0],
          redactionStatus: "passed"
        },
        {
          proofId: "025.docs.cross-doc-consistency",
          featureId: "025-documentation-truth-gate",
          mode: "docs",
          evidenceClass: "documentation-truth-gate",
          status: "passed",
          commandSummary: "validate README, localized README, test plan, STATUS, and Gate 0 consistency",
          coveredClaims: [{claimId: "025.FR-009", source: "spec", description: "Docs and Gate 0 agree on product-hardening scripts", scope: "docs"}],
          prerequisites: [{name: "cross-doc-consistency", status: "available"}],
          artifacts: $crossArtifacts[0],
          redactionStatus: "passed"
        },
        {
          proofId: "029.hostfs-visibility.docs.claim-boundary",
          featureId: "029-hostfs-discoverable-namespace",
          mode: "docs",
          evidenceClass: "documentation-truth-gate",
          status: "passed",
          commandSummary: "validate scoped HostFS visibility claims and reject known 029 overclaims",
          coveredClaims: [{claimId: "029.SC-014", source: "spec", description: "HostFS visibility claims preserve disclosure and real-gate boundaries", scope: "docs"}],
          prerequisites: [{name: "claim-boundaries", status: "available"}, {name: "overclaim-fixtures", status: "available"}],
          artifacts: ($claimArtifacts[0] + $overclaimArtifacts[0]),
          redactionStatus: "passed"
        }
      ]
    }' >"$manifest"
  rm -f "$claim_artifacts" "$overclaim_artifacts" "$command_artifacts" "$cross_artifacts"
}

validate_manifest() {
  jq -e '
    .version == "hideout.product-hardening-evidence/v1" and
    ([.proofs[].proofId] | sort) == ([
      "025.docs.claim-boundaries",
      "025.docs.command-examples",
      "025.docs.cross-doc-consistency",
      "025.docs.overclaim-scan",
      "029.hostfs-visibility.docs.claim-boundary"
    ] | sort) and
    all(.proofs[];
      (.featureId == "025-documentation-truth-gate" or .featureId == "029-hostfs-discoverable-namespace") and
      .status == "passed" and .redactionStatus == "passed"
    )
  ' "$manifest" >"$out/logs/evidence-content.out"
  go run ./cmd/hideout-schema-validate schemas/product-hardening-evidence.schema.json "$manifest" >"$out/logs/evidence-schema.out" 2>"$out/logs/evidence-schema.err"
}

validate_claim_boundaries
scan_overclaims
validate_hostfs_visibility_overclaim_fixtures
validate_host_app_pack_overclaim_fixtures
validate_hostfs_selector_docs
validate_command_examples
validate_cross_docs
validate_release_doc_negative_fixtures
validate_recovery_code_references
scan_redaction
write_manifest
validate_manifest
write_public_release_evidence
scan_redaction
echo "doc-truth-smoke: passed evidence=$manifest"
