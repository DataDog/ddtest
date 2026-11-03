"""Run a pinned PR integration smoke and require its named cases to pass."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile

from ci_test_plan import CONFIG


def require_cases(passed, required):
    missing = set(required) - passed
    if missing:
        raise ValueError(f"Smoke cases did not pass (missing, skipped or filtered out): {sorted(missing)}")


def run_tests(command, env, required):
    passed = set()
    with subprocess.Popen(command, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True) as process:
        for line in process.stdout:
            try:
                event = json.loads(line)
            except ValueError:
                print(line, end="", flush=True)
                continue
            if event.get("Action") == "pass" and event.get("Test"):
                passed.add(event["Test"])
            if event.get("Output"):
                print(event["Output"], end="", flush=True)
        if process.wait():
            raise subprocess.CalledProcessError(process.returncode, command)
    require_cases(passed, required)


def run_smoke(entry):
    env = {**os.environ, **entry.get("env", {}), "DDTEST_TEST_SHARD": "1/1"}
    with tempfile.TemporaryDirectory(prefix="ddtest-smoke-", dir=os.environ.get("RUNNER_TEMP")) as directory:
        if entry.get("dependencies"):
            subprocess.run(["npm", "install", "--prefix", directory, "--no-audit", "--no-fund"]
                           + entry["dependencies"], check=True)
            for variable in entry["modules"]:
                env[variable] = str(Path(directory) / "node_modules")
        for command in entry.get("setup", []):
            subprocess.run(command, check=True, env=env)
        command = (["xvfb-run", "-a"] if entry.get("xvfb") else []) + [
            "go", "test", "-json", "-count=1", "-timeout=8m", "-run", entry["tests"],
        ] + entry.get("packages", ["./internal/testdrive"])
        run_tests(command, env, entry["required"])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--id", required=True)
    args = parser.parse_args()
    config = json.loads(CONFIG.read_text())
    entries = {entry["id"]: entry for suite in config["suites"].values() for entry in suite["smoke"]}
    if args.id not in entries:
        parser.error(f"unknown smoke: {args.id}")
    run_smoke(entries[args.id])


if __name__ == "__main__":
    main()
