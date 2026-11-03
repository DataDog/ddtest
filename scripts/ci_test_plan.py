"""Shadow integration selection: compare source inputs with a passed main run.

This never skips tests. Matching sources still need matching resolved dependencies
and runtime identities before their test results could be reused.
"""

import argparse
from fnmatch import fnmatchcase
import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile


SCHEMA = 1
ARTIFACT = "ci-integration-source-evidence-v1"
CONFIG = Path(__file__).with_suffix(".json")


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def affected_suites(path, config):
    if any(fnmatchcase(path, pattern) for pattern in config["ignored"]):
        return set()
    suites = config["suites"]
    if any(fnmatchcase(path, pattern) for pattern in config["shared"]):
        return set(suites)
    affected = {name for name, suite in suites.items()
                if any(fnmatchcase(path, pattern) for pattern in suite["inputs"])}
    if not affected:
        return set(suites)  # Shared and unclassified inputs invalidate everything.
    # Additional consumers cannot turn an otherwise shared path into a private one.
    affected.update(name for name, suite in suites.items()
                    if any(fnmatchcase(path, pattern) for pattern in suite.get("additional_inputs", [])))
    while True:
        expanded = affected | {name for name, suite in suites.items()
                               if affected.intersection(suite.get("depends_on", []))}
        if expanded == affected:
            return affected
        affected = expanded


def source_inputs(root, config):
    result = subprocess.run(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
                            cwd=root, check=True, capture_output=True)
    inputs = {}
    for name in sorted(set(os.fsdecode(result.stdout).split("\0")) - {""}):
        if not affected_suites(name, config):
            continue
        path = root / name
        try:
            mode = path.lstat().st_mode
        except FileNotFoundError:
            continue  # A tracked deletion must disappear from the fingerprint.
        if stat.S_ISLNK(mode):
            content, kind = os.fsencode(os.readlink(path)), "symlink"
        elif stat.S_ISREG(mode):
            content, kind = path.read_bytes(), "executable" if mode & 0o111 else "file"
        else:
            raise ValueError(f"cannot fingerprint {name}: expected a file or symlink")
        inputs[name] = [kind, hashlib.sha256(content).hexdigest()]
    return inputs


def valid_baseline(baseline, repository):
    return (isinstance(baseline, dict) and baseline.get("schema") == SCHEMA
            and baseline.get("repository") == repository
            and isinstance(baseline.get("head"), str) and isinstance(baseline.get("run_id"), str)
            and isinstance(baseline.get("inputs"), dict)
            and all(isinstance(path, str) and isinstance(value, list) and len(value) == 2
                    and value[0] in ("file", "executable", "symlink")
                    and isinstance(value[1], str) and len(value[1]) == 64
                    for path, value in baseline["inputs"].items()))


def github_baseline(repository):
    """Only completed, successful pushes to main in this workflow are eligible."""
    try:
        result = subprocess.run([
            "gh", "run", "list", "--repo", repository, "--workflow", "ci.yml",
            "--branch", "main", "--event", "push", "--status", "success", "--limit", "10",
            "--json", "databaseId,headSha",
        ], check=True, capture_output=True, text=True, timeout=30)
        runs = json.loads(result.stdout)
        for run in runs:
            with tempfile.TemporaryDirectory(prefix="ci-test-baseline-") as directory:
                result = subprocess.run([
                    "gh", "run", "download", str(run["databaseId"]), "--repo", repository,
                    "--name", ARTIFACT, "--dir", directory,
                ], capture_output=True, timeout=30)
                if result.returncode:
                    continue  # Old runs and expired artifacts need no special handling.
                try:
                    baseline = json.loads((Path(directory) / "plan.json").read_text())
                except (OSError, ValueError):
                    continue
                if (valid_baseline(baseline, repository)
                        and baseline.get("head") == run["headSha"]
                        and baseline.get("run_id") == str(run["databaseId"])):
                    return baseline
    except (OSError, ValueError, KeyError, subprocess.SubprocessError):
        pass  # An unavailable baseline can never produce an unchanged-source decision.
    return None


def make_plan(inputs, config, repository, head, baseline=None):
    if not valid_baseline(baseline, repository):
        baseline = None
    previous = baseline["inputs"] if baseline else {}
    changed = sorted(path for path in inputs.keys() | previous.keys()
                     if inputs.get(path) != previous.get(path)) if baseline else []
    affected = {name: [] for name in config["suites"]}
    for path in changed:
        for name in affected_suites(path, config):
            affected[name].append(path)
    suites = {}
    for name, suite in config["suites"].items():
        selected = {path: value for path, value in inputs.items()
                    if name in affected_suites(path, config)}
        suites[name] = {
            "jobs": suite["jobs"], "fingerprint": digest(selected),
            "decision": "source_unchanged" if baseline and not affected[name] else "run",
            "reason": ("source inputs unchanged; runtime/dependency verification still required"
                       if baseline and not affected[name] else
                       "source inputs changed" if baseline else "no eligible baseline"),
            "changed_inputs": affected[name],
        }
    return {
        "schema": SCHEMA, "mode": "shadow", "repository": repository, "head": head,
        "run_id": os.environ.get("GITHUB_RUN_ID", ""),
        "baseline": {key: baseline[key] for key in ("head", "run_id")} if baseline else None,
        "external_inputs_verified": False, "always_run": config["always_run"],
        "inputs": inputs, "suites": suites,
    }


def summary(plan):
    lines = ["## Integration selection (shadow)", "",
             "All tests still run. Unchanged sources are candidates only: resolved dependencies",
             "and runtime identities have not been verified. No test evidence is reused.", ""]
    if plan["baseline"]:
        origin = (f"successful main run {plan['baseline']['run_id']}"
                  if plan["baseline"]["run_id"] else "local snapshot")
        lines.append(f"Compared with {origin} at `{plan['baseline']['head']}`.")
    else:
        lines.append("No eligible successful main baseline; every suite must run.")
    lines += ["", "| Suite | Source decision | Changed inputs |", "| --- | --- | --- |"]
    for name, suite in plan["suites"].items():
        paths = suite["changed_inputs"]
        details = ", ".join("`" + path.replace("`", "").replace("|", "&#124;") + "`"
                            for path in paths[:5])
        if len(paths) > 5:
            details += f" (+{len(paths) - 5} more)"
        lines.append(f"| {name} | {suite['decision']} | {details or '—'} |")
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path("."))
    parser.add_argument("--repository", default=os.environ.get("GITHUB_REPOSITORY", "local"))
    baseline_args = parser.add_mutually_exclusive_group()
    baseline_args.add_argument("--baseline", type=Path)
    baseline_args.add_argument("--github-baseline", action="store_true")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--summary", type=Path)
    args = parser.parse_args()
    config = json.loads(CONFIG.read_text())
    baseline = github_baseline(args.repository) if args.github_baseline else None
    if args.baseline:
        try:
            baseline = json.loads(args.baseline.read_text())
        except (OSError, ValueError):
            baseline = None
    head = subprocess.run(["git", "rev-parse", "HEAD"], cwd=args.root,
                          check=True, capture_output=True, text=True).stdout.strip()
    plan = make_plan(source_inputs(args.root, config), config, args.repository, head, baseline)
    args.output.write_text(json.dumps(plan, indent=2, sort_keys=True) + "\n")
    if args.summary:
        args.summary.write_text(summary(plan))
    print(summary(plan), end="")


if __name__ == "__main__":
    main()
