import os
import pathlib
import subprocess
import sys
import time

candidate_root = pathlib.Path(sys.argv[1])
control_root = pathlib.Path(sys.argv[2])
samples = int(sys.argv[3])

if (
    os.environ.get("GIT_CONFIG_COUNT") != "1"
    or os.environ.get("GIT_CONFIG_KEY_0") != "core.preloadIndex"
    or os.environ.get("GIT_CONFIG_VALUE_0") != "false"
):
    raise RuntimeError(
        "shared Portal session is missing the Core-owned Git preload policy"
    )


def elapsed_ms(operation):
    started = time.monotonic_ns()
    operation()
    return (time.monotonic_ns() - started) / 1_000_000


def git_status(root):
    subprocess.run(
        ["git", "-C", str(root / "git"), "status", "--short"],
        check=True,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
    )


def package_scan(root):
    count = 0
    for current, _dirs, files in os.walk(root / "package" / "node_modules"):
        for name in files:
            os.stat(os.path.join(current, name), follow_symlinks=False)
            count += 1
    if count != 20_000:
        raise RuntimeError(f"package fixture count changed for {root}: {count}")


def measure_pairs(metric, operation):
    operation(candidate_root)
    operation(control_root)
    for index in range(1, samples + 1):
        sides = (("candidate", candidate_root), ("control", control_root))
        if index % 2 == 0:
            sides = tuple(reversed(sides))
        for side, root in sides:
            value = elapsed_ms(lambda: operation(root))
            print(f"{metric}\t{index}\t{side}\t{value:.6f}", flush=True)


measure_pairs("git-status", git_status)
measure_pairs("package-scan", package_scan)
