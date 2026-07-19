#!/usr/bin/env python3
"""Measure product attachment readiness and first target byte through hideoutd."""

import argparse
import json
import os
import pathlib
import subprocess
import time


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--samples", type=int, required=True)
    parser.add_argument("--hideout", required=True)
    parser.add_argument("--store", required=True)
    parser.add_argument("--lima-home", required=True)
    parser.add_argument("--profile", required=True)
    parser.add_argument("--workspace", required=True)
    parser.add_argument("--mount-ready-out", required=True)
    parser.add_argument("--first-byte-out", required=True)
    return parser.parse_args()


class Driver:
    def __init__(self, args):
        self.args = args
        self.env = os.environ.copy()
        self.env.update(
            {"HIDEOUT_STORE_ROOT": args.store, "LIMA_HOME": args.lima_home}
        )

    def command(self, target):
        return [
            self.args.hideout,
            "run",
            "--profile",
            self.args.profile,
            "--backend",
            "lima",
            "--network",
            "direct",
            "--workspace",
            self.args.workspace,
            "--guest-workspace",
            "/workspace",
            "--terminal",
            "never",
            "--",
            *target,
        ]

    def attachment_count(self):
        result = subprocess.run(
            [self.args.hideout, "daemon", "status"],
            env=self.env,
            check=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )
        return len(json.loads(result.stdout).get("workspaceAttachments", []))

    def mount_ready(self, record):
        root = pathlib.Path(self.args.workspace)
        ready = root / ".hideout-035-measure-ready"
        release = root / ".hideout-035-measure-release"
        ready.unlink(missing_ok=True)
        release.unlink(missing_ok=True)
        baseline = self.attachment_count()
        process = subprocess.Popen(
            self.command(
                [
                    "sh",
                    "-eu",
                    "-c",
                    "touch /workspace/.hideout-035-measure-ready; "
                    "while [ ! -f /workspace/.hideout-035-measure-release ]; "
                    "do sleep 0.01; done",
                ]
            ),
            env=self.env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            text=True,
        )
        started = time.monotonic_ns()
        deadline = time.monotonic() + 30
        elapsed = None
        try:
            while self.attachment_count() <= baseline:
                if process.poll() is not None:
                    raise RuntimeError(
                        "measured run exited before attachment: " + process.stderr.read()
                    )
                if time.monotonic() >= deadline:
                    raise RuntimeError("timed out observing active workspace attachment")
                time.sleep(0.005)
            elapsed = (time.monotonic_ns() - started) / 1_000_000
            while not ready.exists():
                if process.poll() is not None or time.monotonic() >= deadline:
                    raise RuntimeError("measured target did not become ready")
                time.sleep(0.005)
            release.touch()
            _stdout, stderr = process.communicate(timeout=30)
            if process.returncode != 0:
                raise RuntimeError("measured run failed: " + stderr)
        finally:
            release.touch(exist_ok=True)
            if process.poll() is None:
                process.kill()
                process.wait()
            ready.unlink(missing_ok=True)
            release.unlink(missing_ok=True)
        return elapsed if record else None

    def first_byte(self, record):
        started = time.monotonic_ns()
        process = subprocess.Popen(
            self.command(["sh", "-c", "printf 'workspace-ready\\n'"]),
            env=self.env,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        line = process.stdout.readline()
        first = time.monotonic_ns()
        _tail, stderr = process.communicate(timeout=30)
        if process.returncode != 0 or line != b"workspace-ready\n":
            raise RuntimeError(
                "first-byte run failed: " + stderr.decode("utf-8", "replace")
            )
        return (first - started) / 1_000_000 if record else None


def write_samples(path, values):
    target = pathlib.Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text("".join(f"{value:.6f}\n" for value in values), encoding="utf-8")


def main():
    args = parse_args()
    if args.samples < 1:
        raise SystemExit("--samples must be positive")
    driver = Driver(args)
    driver.mount_ready(False)
    mount_ready = [driver.mount_ready(True) for _ in range(args.samples)]
    driver.first_byte(False)
    first_byte = [driver.first_byte(True) for _ in range(args.samples)]
    write_samples(args.mount_ready_out, mount_ready)
    write_samples(args.first_byte_out, first_byte)


if __name__ == "__main__":
    main()
