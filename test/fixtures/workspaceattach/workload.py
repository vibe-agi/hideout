import os
import pathlib
import subprocess
import sys
import time

root = pathlib.Path(sys.argv[1])
samples = int(sys.argv[2])


def emit(label, elapsed_ns):
    print(f"{label}\t{elapsed_ns / 1_000_000:.6f}", flush=True)


def measure(label, operation):
    started = time.monotonic_ns()
    operation()
    emit(label, time.monotonic_ns() - started)


def git_status():
    subprocess.run(
        ["git", "-C", str(root / "git"), "status", "--short"],
        check=True,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
    )


def package_scan():
    count = 0
    for current, _dirs, files in os.walk(root / "package" / "node_modules"):
        for name in files:
            os.stat(os.path.join(current, name), follow_symlinks=False)
            count += 1
    if count != 20_000:
        raise RuntimeError(f"package fixture count changed: {count}")


counter = 0


def atomic_save():
    global counter
    counter += 1
    directory = root / "atomic"
    temporary = directory / f"value.tmp.{os.getpid()}"
    target = directory / "value.txt"
    with open(temporary, "wb") as stream:
        stream.write((f"sample={counter}\n".encode("ascii") * 256)[:4096])
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, target)
    descriptor = os.open(directory, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


# Every warm distribution receives exactly one unrecorded warm-up.
git_status()
package_scan()
atomic_save()
for _ in range(samples):
    measure("git-status-warm", git_status)
for _ in range(samples):
    measure("package-scan-warm", package_scan)
for _ in range(samples):
    measure("atomic-save-operation-warm", atomic_save)
