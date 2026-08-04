#!/usr/bin/env bash
set -euo pipefail

gate2_shared_workspace_evaluate() {
  if [ "$#" -ne 4 ]; then
    echo "usage: gate2_shared_workspace_evaluate <research-baseline-dir> <filesystem-control-dir> <candidate-dir> <output-dir>" >&2
    return 2
  fi
  local research_baseline="$1" filesystem_control="$2" candidate="$3" output="$4"
  python3 - "$research_baseline" "$filesystem_control" "$candidate" "$output" <<'PY'
import json
import pathlib
import sys

research_baseline = pathlib.Path(sys.argv[1])
filesystem_control = pathlib.Path(sys.argv[2])
candidate = pathlib.Path(sys.argv[3])
output = pathlib.Path(sys.argv[4])
output.mkdir(parents=True, exist_ok=True)


def load(path):
    with path.open(encoding="utf-8") as stream:
        return json.load(stream)


def summary(root, name, minimum):
    value = load(root / f"{name}-summary.json")
    if value.get("samples", 0) < minimum:
        raise SystemExit(f"{name} has {value.get('samples', 0)} samples; require {minimum}")
    for field in ("medianMs", "p95Ms"):
        if not isinstance(value.get(field), (int, float)) or value[field] < 0:
            raise SystemExit(f"{name} has invalid {field}")
    return value


baseline_digest = (research_baseline / "fixture.sha256").read_text(encoding="utf-8").strip()
control_digest = (filesystem_control / "fixture.sha256").read_text(encoding="utf-8").strip()
candidate_digest = (candidate / "fixture.sha256").read_text(encoding="utf-8").strip()
if len(baseline_digest) != 64 or candidate_digest != baseline_digest or control_digest != candidate_digest:
    raise SystemExit("candidate/control fixture identity differs from the fixed research fixture")

reference = {
    "git-status": summary(filesystem_control, "git-status", 30),
    "package-scan": summary(filesystem_control, "package-scan", 30),
    "first-byte": summary(research_baseline, "first-byte", 30),
}
current = {
    "git-status": summary(candidate, "git-status", 30),
    "package-scan": summary(candidate, "package-scan", 30),
    "atomic-host-to-guest": summary(candidate, "atomic-host-to-guest", 30),
    "atomic-guest-to-host": summary(candidate, "atomic-guest-to-host", 30),
    "mount-ready": summary(candidate, "mount-ready", 30),
    "first-byte": summary(candidate, "first-byte", 30),
    "saturation-metadata": summary(candidate, "saturation-metadata", 100),
}
path_correctness = load(candidate / "path-correctness.json")
required_path_checks = {
    "productionWorkspaceIdentity", "logicalPhysicalSameObject",
    "logicalWritePhysicalRead", "physicalWriteLogicalRead",
    "atomicRenameAcrossAliases", "modeAcrossAliases", "flushAcrossAliases",
    "deleteAcrossAliases", "repeatedDeleteAcrossAliases",
    "logicalPwdStable", "physicalCwdOpaque",
    "nestedCdStable", "subprocessCwdOpaque", "distinctRootProjectState",
    "sameRootProjectStateStable", "siblingPhysicalRootDenied",
    "goLogicalPwdAliasClassified", "boundedGitSafeDirectories",
    "preserveModeSharedRejected", "externalGitMetadataRejected",
    "resolvedFileAuditLogical", "relativeFileAliasExplicit", "processAuditLogical",
    "processCwdUnavailableExplicit", "physicalArgvCaptureLimitationExplicit",
    "siblingArgvFailClosed",
    "physicalPathAbsentFromActivity",
}
required_path_limitations = [
    "process-cwd-unavailable",
    "physical-workspace-argv-exceeds-kernel-capture-width",
    "relative-workspace-file-path-alias",
]
path_correctness_passed = (
    path_correctness.get("schema") == "hideout.shared-workspace-path-correctness/v1"
    and path_correctness.get("status") == "passed"
    and path_correctness.get("tools") == ["bash", "claude", "codex", "git", "go", "node", "python"]
    and path_correctness.get("representativeAgents") == ["claude", "codex"]
    and path_correctness.get("limitations") == required_path_limitations
    and set(path_correctness.get("checks", {})) == required_path_checks
    and all(path_correctness.get("checks", {}).values())
)

saturation = load(candidate / "saturation.json")
if not isinstance(saturation.get("teardownMs"), (int, float)) or saturation["teardownMs"] < 0:
    raise SystemExit("saturation teardown observation is invalid")

passed = {
    "git-status": current["git-status"]["medianMs"] <= 2000
        and current["git-status"]["medianMs"] <= 2 * reference["git-status"]["medianMs"],
    "package-scan": current["package-scan"]["medianMs"] <= 3 * reference["package-scan"]["medianMs"],
    "atomic-host-to-guest": current["atomic-host-to-guest"]["p95Ms"] <= 250,
    "atomic-guest-to-host": current["atomic-guest-to-host"]["p95Ms"] <= 250,
    "mount-ready": current["mount-ready"]["p95Ms"] <= 1000,
    "first-byte": current["first-byte"]["p95Ms"]
        <= reference["first-byte"]["p95Ms"] + max(500, .15 * reference["first-byte"]["p95Ms"]),
    "path-correctness": path_correctness_passed,
    "saturation-teardown": saturation["teardownMs"] <= 5000,
}
metrics = []
for name in ("git-status", "package-scan", "atomic-host-to-guest", "atomic-guest-to-host", "mount-ready", "first-byte"):
    metric = {"id": name, "candidate": current[name], "passed": passed[name],
              "referenceKind": "absolute-threshold"}
    if name in ("git-status", "package-scan"):
        metric["reference"] = reference[name]
        metric["referenceKind"] = "paired-static-virtiofs"
    elif name == "first-byte":
        metric["reference"] = reference[name]
        metric["referenceKind"] = "retained-research-baseline"
    metrics.append(metric)

result = {
    "schema": "hideout.shared-workspace-gate2-evaluation/v2",
    "result": "passed" if all(passed.values()) else "failed",
    "thresholdsPassed": all(passed.values()),
    "fixtureSHA256": candidate_digest,
    "metrics": metrics,
    "pathCorrectness": {"passed": path_correctness_passed, "observation": path_correctness},
    "saturation": {"passed": passed["saturation-teardown"], "observation": saturation,
                   "metadata": current["saturation-metadata"]},
}
with (output / "shared-workspace-evaluation.json").open("w", encoding="utf-8") as stream:
    json.dump(result, stream, indent=2, sort_keys=True)
    stream.write("\n")
if result["result"] != "passed":
    raise SystemExit("shared workspace Gate 2 thresholds failed")
PY
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  gate2_shared_workspace_evaluate "$@"
fi
