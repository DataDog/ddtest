#!/usr/bin/env python3
"""Reject external runtime and test requirements in the distributed module."""

import json
from pathlib import Path
import subprocess
import sys


MODULE = "github.com/tonyredondo/dd-ci-testing-poc"


def packages_from_json(text):
    decoder = json.JSONDecoder()
    offset = 0
    while offset < len(text):
        while offset < len(text) and text[offset].isspace():
            offset += 1
        if offset == len(text):
            break
        value, offset = decoder.raw_decode(text, offset)
        yield value


def violations(module, packages):
    errors = []
    if module.get("Module", {}).get("Path") != MODULE:
        errors.append("check must run in the POC module")
    for requirement in module.get("Require") or []:
        errors.append(f"distributed module requires {requirement['Path']}")
    for package in packages:
        if package.get("Standard"):
            continue
        path = package.get("ImportPath", "")
        owner = (package.get("Module") or {}).get("Path")
        if owner != MODULE or not path.startswith(MODULE + "/"):
            errors.append(f"external runtime package: {path} (module={owner})")
    return errors


def main():
    root = Path(__file__).resolve().parent.parent
    module = json.loads(subprocess.check_output(["go", "mod", "edit", "-json"], cwd=root, text=True, encoding="utf-8"))
    graph = subprocess.check_output(
        ["go", "list", "-deps", "-json=ImportPath,Standard,Module", "./cmd/ddto", "./testopt", "./propagation"],
        cwd=root, text=True, encoding="utf-8",
    )
    errors = violations(module, packages_from_json(graph))
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print("Runtime and CLI use only the standard library and the POC module.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
