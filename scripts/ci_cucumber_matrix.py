"""Resolve Cucumber fixtures once and deduplicate identical CI configurations."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess


LANES = [("8.11.1", 1, "^TestPublicCucumberTestdrive/passing$"),
         ("10.9.0", 4, "^TestPublicCucumberTestdrive$"),
         ("13.3.0", 4, "^TestPublicCucumberTestdrive$"),
         ("latest", 4, "^TestPublicCucumberTestdrive$")]
DEPENDENCIES = {"nyc": "17.1.0", "c8": "12.0.0", "dd-trace": "6.20.1",
                "@istanbuljs/esm-loader-hook": "0.3.0", "tsx": "4.23.15"}
VERSION = re.compile(r"\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?")


def execute(command, cwd=None):
    return subprocess.run(command, cwd=cwd, check=True, capture_output=True,
                          text=True, timeout=240).stdout.strip()


def fingerprint(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def resolve_version(requested):
    version = (requested if VERSION.fullmatch(requested) else
               json.loads(execute(["npm", "view", f"@cucumber/cucumber@{requested}", "version", "--json"])))
    if not isinstance(version, str) or not VERSION.fullmatch(version):
        raise ValueError(f"Cucumber {requested}: expected one exact version, got {version!r}")
    return version


def lock_fixture(output, version):
    directory = output / version
    directory.mkdir()
    dependencies = {**DEPENDENCIES, "@cucumber/cucumber": version}
    manifest = {"name": "ddtest-cucumber-ci-fixture", "version": "1.0.0",
                "private": True, "dependencies": dependencies}
    (directory / "package.json").write_text(json.dumps(manifest, indent=2) + "\n")
    execute(["npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund"],
            cwd=directory)
    lock = json.loads((directory / "package-lock.json").read_text())
    for package, expected in dependencies.items():
        if lock.get("packages", {}).get(f"node_modules/{package}", {}).get("version") != expected:
            raise ValueError(f"Cucumber {version}: lockfile did not retain {package}@{expected}")
    return fingerprint(lock)


def deduplicate(rows):
    selected = {}
    for row in rows:
        # Labels are aliases; every execution input, including the complete
        # lockfile, runtime, test filter and shard, participates in the key.
        key = fingerprint({name: value for name, value in row.items() if name != "cucumber"})
        if key not in selected:
            selected[key] = {**row, "aliases": [row["cucumber"]]}
        else:
            selected[key]["aliases"].append(row["cucumber"])
    return [{**row, "aliases": ", ".join(row["aliases"])} for row in selected.values()]


def build_plan(output):
    output.mkdir(parents=True)  # Refuse stale files from an earlier resolution.
    node, npm = execute(["node", "--version"]).removeprefix("v"), execute(["npm", "--version"])
    if not VERSION.fullmatch(node) or not VERSION.fullmatch(npm):
        raise ValueError("expected exact Node and npm versions")
    fixtures, rows = {}, []
    for requested, total, tests in LANES:
        version = resolve_version(requested)
        if version not in fixtures:
            fixtures[version] = lock_fixture(output, version)
        for shard in range(1, total + 1):
            rows.append({"cucumber": requested, "version": version, "fixture": version,
                         "lock": fixtures[version], "node": node, "npm": npm,
                         "tests": tests, "shard": f"{shard}/{total}"})
    return {"requested_jobs": len(rows), "matrix": {"include": deduplicate(rows)}}


def summary(plan):
    rows = plan["matrix"]["include"]
    lines = ["## Resolved Cucumber configurations", "",
             f"{plan['requested_jobs']} requested lanes → {len(rows)} distinct jobs.", "",
             "Matching aliases share a dependency lockfile and execution. Every distinct",
             "runtime, dependency graph, test selection and shard still runs.", "",
             "| Requested labels | Resolved Cucumber | Shard |", "| --- | --- | --- |"]
    lines += [f"| {row['aliases']} | {row['version']} | {row['shard']} |" for row in rows]
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True, help="new directory for fixtures and matrix.json")
    parser.add_argument("--github-output", type=Path)
    parser.add_argument("--summary", type=Path)
    args = parser.parse_args()
    try:
        plan = build_plan(args.output)
        (args.output / "matrix.json").write_text(json.dumps(plan, indent=2) + "\n")
        if args.github_output:
            with args.github_output.open("a") as output:
                output.write("matrix=" + json.dumps(plan["matrix"], separators=(",", ":")) + "\n")
        if args.summary:
            args.summary.write_text(summary(plan))
        print(summary(plan), end="")
    except subprocess.CalledProcessError as error:
        parser.exit(1, error.stderr or str(error))
    except (OSError, ValueError, subprocess.TimeoutExpired) as error:
        parser.exit(1, str(error) + "\n")


if __name__ == "__main__":
    main()
