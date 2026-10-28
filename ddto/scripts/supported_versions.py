#!/usr/bin/env python3
"""Resolve the dd-trace-go and Orchestrion versions that ddto supports.

supported_versions.json records the window that pull requests test: every
dd-trace-go release from its minimum version, the releases of Orchestrion's
last minors, and each main branch at a fixed pseudo-version. resolve prints
the window that the module proxy has now, as one line of JSON for the
workflow's DDTO_SUPPORTED_VERSIONS: the nightly run and the check before a
release test it. check fails when the recorded releases differ from it; main
moves every day, so check does not compare it.
"""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parents[1]
RECORDED = ROOT / "scripts/supported_versions.json"
RELEASE = re.compile(r"^v(\d+)\.(\d+)\.(\d+)$")


def release_key(version):
    match = RELEASE.match(version)
    return tuple(int(part) for part in match.groups()) if match else None


def window(entry, versions, main):
    """The releases that entry's rule selects from versions, and main."""
    releases = sorted((version for version in versions if release_key(version)), key=release_key)
    if "minimum" in entry:
        selected = [version for version in releases if release_key(version) >= release_key(entry["minimum"])]
    else:
        minors = sorted({release_key(version)[:2] for version in releases})[-entry["minors"]:]
        selected = [version for version in releases if release_key(version)[:2] in minors]
    return {**entry, "releases": selected, "main": main}


def go_list(*args):
    # Outside any module, so that no go.mod selects a toolchain.
    env = {**os.environ, "GOFLAGS": "", "GOWORK": "off"}
    return subprocess.run(["go", "list", "-m", *args], check=True, capture_output=True, text=True, env=env,
                          cwd=tempfile.gettempdir(), timeout=300).stdout


def resolve(recorded, query=go_list):
    resolved = {}
    for name, entry in recorded.items():
        versions = query("-versions", entry["module"]).split()[1:]
        main = json.loads(query("-json", entry["module"] + "@main"))["Version"]
        resolved[name] = window(entry, versions, main)
    return resolved


def differences(recorded, resolved):
    found = []
    for name, entry in recorded.items():
        current = set(resolved[name]["releases"])
        new = sorted(current - set(entry["releases"]), key=release_key)
        old = sorted(set(entry["releases"]) - current, key=release_key)
        if new:
            found.append(f"{name}: new releases {', '.join(new)}")
        if old:
            found.append(f"{name}: releases outside the window {', '.join(old)}")
    return found


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    resolve_parser = commands.add_parser("resolve", help="print the window that the module proxy has now")
    resolve_parser.add_argument("--output", type=Path, help="write it here instead")
    commands.add_parser("check", help="fail when supported_versions.json lists other releases")
    args = parser.parse_args()
    try:
        recorded = json.loads(RECORDED.read_text())
        resolved = resolve(recorded)
        if args.command == "resolve":
            text = json.dumps(resolved, separators=(",", ":")) + "\n"
            if args.output:
                args.output.write_text(text)
            else:
                sys.stdout.write(text)
        elif found := differences(recorded, resolved):
            parser.exit(1, "supported_versions: update supported_versions.json: " + "; ".join(found) + "\n")
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        parser.exit(1, f"supported_versions: {error}\n")


if __name__ == "__main__":
    main()
