"""Select PR smoke checks and affected Go packages from the actual Git diff."""

import argparse
from fnmatch import fnmatchcase
import json
import os
from pathlib import Path
import subprocess


CONFIG = Path(__file__).with_suffix(".json")


def affected_suites(path, config):
    if any(fnmatchcase(path, pattern) for pattern in config["ignored"]):
        return set()
    suites = config["suites"]
    if not path.endswith("_test.go") and any(fnmatchcase(path, pattern) for pattern in config["shared"]):
        return set(suites)
    affected = {name for name, suite in suites.items()
                if any(fnmatchcase(path, pattern) for pattern in suite["inputs"])}
    if not affected:
        return set(suites)
    affected.update(name for name, suite in suites.items()
                    if any(fnmatchcase(path, pattern) for pattern in suite.get("additional_inputs", [])))
    while True:
        expanded = affected | {name for name, suite in suites.items()
                               if affected.intersection(suite.get("depends_on", []))}
        if expanded == affected:
            return affected
        affected = expanded


def relevant_files(files, config):
    if files is None:
        return None
    ignored = config["ignored"] + config.get("non_product", [])
    return [path for path in files if not any(fnmatchcase(path, pattern) for pattern in ignored)]


def changed_files(root, base):
    if not base or base == "0" * 40:
        return None
    try:
        ancestor = subprocess.run(["git", "merge-base", base, "HEAD"], cwd=root,
                                  check=True, capture_output=True, text=True).stdout.strip()
        tracked = subprocess.run(["git", "diff", "--no-renames", "--name-only", "-z", ancestor],
                                 cwd=root, check=True, capture_output=True).stdout
        added = subprocess.run(["git", "ls-files", "--others", "--exclude-standard", "-z"],
                               cwd=root, check=True, capture_output=True).stdout
        return sorted(set(os.fsdecode(tracked + added).split("\0")) - {""})
    except subprocess.CalledProcessError:
        return None


def make_plan(files, config):
    selected = set(config["suites"]) if files is None else set()
    relevant = relevant_files(files, config)
    for path in relevant or []:
        # The instrumentation is a separate module. Its units and the release
        # binary smoke cover changes there; they do not affect framework runners.
        if path.startswith("ddto/"):
            continue
        if path.endswith("_test.go") and not path.endswith("integration_test.go") and not path.startswith("internal/compatibility/"):
            continue
        selected.update(affected_suites(path, config))
    checks = [{"id": entry["id"], "runtime": entry["runtime"]}
              for name, suite in config["suites"].items() if name in selected
              for entry in suite["smoke"]]
    return {
        "mode": "fast", "changed_files": files,
        "go": files is None or bool(relevant),
        "ddto": files is None or any(path.startswith("ddto/") or path in (
            "main.go", "go.mod", "go.sum", "internal/cmd/instrument.go",
            ".github/workflows/go-instrumentation.yml") for path in relevant),
        "evaluator": files is None or any(path.startswith("skills/ddtest-onboarding-eval/")
                                         for path in files),
        "suites": sorted(selected),
        "integrations": [entry for entry in checks if entry["runtime"] != "playwright"],
        "playwright": any(entry["runtime"] == "playwright" for entry in checks),
    }


def json_objects(text):
    decoder = json.JSONDecoder()
    while text.strip():
        value, end = decoder.raw_decode(text.lstrip())
        yield value
        text = text.lstrip()[end:]


def select_packages(packages, files, root):
    by_dir = {Path(package["Dir"]).resolve(): package["ImportPath"] for package in packages}
    all_packages = set(by_dir.values())
    if files is None:
        return sorted(all_packages)
    direct, production = set(), set()
    for path in files:
        if not path.endswith(".go") or path.startswith("ddto/"):
            return sorted(all_packages)
        package = by_dir.get((root / path).resolve().parent)
        if package is None:
            return sorted(all_packages)
        direct.add(package)
        if not path.endswith("_test.go"):
            production.add(package)
    while True:
        expanded = production | {package["ImportPath"] for package in packages
                                 if production.intersection(package.get("Imports", [])
                                                            + package.get("TestImports", [])
                                                            + package.get("XTestImports", []))}
        if expanded == production:
            return sorted(direct | production)
        production = expanded


def run_unit_tests(root, files, config, race=False):
    relevant = relevant_files(files, config)
    if relevant == []:
        print("No code inputs changed; no Go packages selected.")
        return
    command = ["go", "list", "-json"] + (["-race"] if race else []) + ["./..."]
    result = subprocess.run(command, cwd=root, check=True, capture_output=True, text=True)
    packages = select_packages(list(json_objects(result.stdout)), relevant, root)
    if not packages:
        raise ValueError("Go reported no packages for changed code")
    print(f"Testing {len(packages)} affected Go packages", flush=True)
    subprocess.run(["go", "test", "-timeout=20m"] + (["-race"] if race else []) + packages,
                   cwd=root, check=True)


def summary(plan):
    suites = ", ".join(plan["suites"]) or "none (documentation only)"
    return ("## PR test selection\n\n"
            f"Selected integration suites: {suites}.\n\n"
            f"Native Go checks: {plan['go']}; instrumentation units: {plan['ddto']}.\n\n"
            "Historical versions, full scenario matrices and platform parity gate releases "
            "through the Full compatibility workflow.\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["plan", "run"])
    parser.add_argument("--root", type=Path, default=Path("."))
    parser.add_argument("--base", default="origin/main")
    parser.add_argument("--race", action="store_true")
    parser.add_argument("--output", type=Path)
    parser.add_argument("--github-output", type=Path)
    parser.add_argument("--summary", type=Path)
    args = parser.parse_args()
    config = json.loads(CONFIG.read_text())
    root = args.root.resolve()
    files = changed_files(root, args.base)
    if args.action == "run":
        run_unit_tests(root, files, config, args.race)
        return
    plan = make_plan(files, config)
    if args.output:
        args.output.write_text(json.dumps(plan, indent=2) + "\n")
    if args.github_output:
        with args.github_output.open("a") as output:
            for key in ("go", "ddto", "evaluator", "integrations", "playwright"):
                output.write(key + "=" + json.dumps(plan[key], separators=(",", ":")) + "\n")
    if args.summary:
        args.summary.write_text(summary(plan))
    print(summary(plan), end="")


if __name__ == "__main__":
    main()
