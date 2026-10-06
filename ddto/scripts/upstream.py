#!/usr/bin/env python3
"""Audit incorporated source and prepare upstream comparisons without modifying code.

Manifests live beside their sources. Checks are offline; comparisons require an
explicit upstream checkout/archive. Updating a revision is a reviewable operation,
not an automatic overwrite of CI-specific adaptations.
"""

import argparse
import difflib
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import sys

ROOT = Path(__file__).resolve().parent.parent
ORIGINS = ROOT / "internal" / "thirdparty"


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def contained(root, relative):
    path = PurePosixPath(relative)
    if path.is_absolute() or ".." in path.parts or "\\" in relative:
        raise ValueError(f"unsafe source path: {relative}")
    result = root.joinpath(*path.parts)
    if not result.resolve().is_relative_to(root.resolve()):
        raise ValueError(f"source path escapes its root: {relative}")
    return result


def verify_library(directory, manifest):
    errors = []
    if not re.fullmatch(r"[0-9a-f]{40}", manifest["commit"]):
        errors.append("an exact 40-character upstream commit is required")
    entries = manifest["files"]
    recorded = set()
    for entry in entries:
        relative = entry["path"]
        if relative in recorded:
            errors.append(f"duplicate manifest entry: {relative}")
        recorded.add(relative)
        try:
            file = contained(directory, relative)
            if not file.is_file():
                errors.append(f"missing file: {relative}")
            elif digest(file) != entry["sha256"]:
                errors.append(f"local source changed: {relative}")
        except ValueError as error:
            errors.append(str(error))
    for license_file in manifest["licenses"]:
        if license_file not in recorded or not (directory / license_file).is_file():
            errors.append(f"license missing from source/manifest: {license_file}")
    if not manifest["licenses"]:
        errors.append("at least one license is required")
    # Go ignores underscore directories during ./... discovery. Include them in
    # the audit so helper generators and their provenance cannot disappear.
    for file in directory.rglob("*"):
        if file.is_file() and file.suffix in (".go", ".s"):
            relative = file.relative_to(directory).as_posix()
            if relative not in recorded:
                errors.append(f"source absent from manifest: {relative}")
    return errors


def source_entries(manifest):
    entries = {}
    for item in manifest["files"]:
        if "source_path" in item:
            entry = {"path": item["source_path"], "sha256": item["source_sha256"]}
            entries[entry["path"]] = entry
    for item in manifest.get("additional_sources", []):
        entries[item["path"]] = item
    return sorted(entries.values(), key=lambda item: item["path"])


def compare_source(manifest, source):
    """List selected upstream changes; absent files never silently disappear."""
    changes = []
    for entry in source_entries(manifest):
        file = contained(source, entry["path"])
        if not file.is_file():
            changes.append(("missing", entry["path"]))
        elif digest(file) != entry["sha256"]:
            changes.append(("changed", entry["path"]))
    return changes


def local_patch(directory, manifest, source):
    """Compare each adaptation with the original at the recorded base revision."""
    changes = compare_source(manifest, source)
    if changes:
        raise ValueError("source is not the recorded base: " + repr(changes))
    output = []
    for entry in manifest["files"]:
        if "source_path" not in entry or entry.get("role") == "license":
            continue
        upstream = contained(source, entry["source_path"])
        local = contained(directory, entry["path"])
        try:
            before = upstream.read_text().splitlines(keepends=True)
            after = local.read_text().splitlines(keepends=True)
        except UnicodeDecodeError:
            continue  # Binary fixtures remain covered by SHA-256 verification.
        if before != after:
            output.extend(difflib.unified_diff(
                before, after,
                fromfile="upstream/" + entry["source_path"],
                tofile="local/" + entry["path"],
            ))
    return "".join(output)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("verify", "diff", "patch", "rehash"))
    parser.add_argument("--library", help="origin directory name, e.g. dd-trace-go")
    parser.add_argument("--source", type=Path, help="explicit upstream source root")
    args = parser.parse_args()
    directories = [ORIGINS / args.library] if args.library else sorted(
        manifest.parent for manifest in ORIGINS.glob("*/SOURCE.json")
    )
    if args.command != "verify" and (not args.library or not args.source):
        parser.error("diff/patch require --library and --source")
    if not directories:
        parser.error("no incorporated-source manifests found")
    failed = False
    for directory in directories:
        manifest = json.loads((directory / "SOURCE.json").read_text())
        if args.command == "rehash":
            changes = compare_source(manifest, args.source)
            if changes:
                print(f"{directory.name}: source is not the recorded base: {changes}", file=sys.stderr)
                failed = True
                continue
            errors = verify_library(directory, manifest)
            # A source edit is expected here; missing files, untracked additions,
            # changed license texts and unsafe paths still require explicit review.
            license_paths = set(manifest["licenses"])
            permitted = {"local source changed: " + item["path"] for item in manifest["files"] if item["path"] not in license_paths}
            errors = [error for error in errors if error not in permitted]
            if errors:
                for error in errors:
                    print(f"{directory.name}: {error}", file=sys.stderr)
                failed = True
                continue
            for item in manifest["files"]:
                item["sha256"] = digest(contained(directory, item["path"]))
            (directory / "SOURCE.json").write_text(json.dumps(manifest, indent=2) + "\n")
            print(f"{directory.name}: reviewed local hashes recorded; base revision unchanged")
            continue
        errors = verify_library(directory, manifest)
        for error in errors:
            print(f"{directory.name}: {error}", file=sys.stderr)
        if errors:
            failed = True
            continue
        if args.command == "verify":
            print(f"{directory.name}: {len(manifest['files'])} files verified at {manifest['commit']}")
        elif args.command == "diff":
            changes = compare_source(manifest, args.source)
            for status, path in changes:
                print(f"{status}: {path}")
            if not changes:
                print("Selected upstream files match the recorded base.")
            failed |= any(status == "missing" for status, _ in changes)
        else:
            try:
                sys.stdout.write(local_patch(directory, manifest, args.source))
            except ValueError as error:
                print(error, file=sys.stderr)
                failed = True
    return int(failed)


if __name__ == "__main__":
    sys.exit(main())
