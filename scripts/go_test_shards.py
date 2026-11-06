"""Shard the root Go suite by package; merge its atomic coverage."""

import argparse
import hashlib
from pathlib import Path
import subprocess
import sys


# The packages that take the longest, one group per shard. A shard of packages
# needs no -run, which would keep ddtest instrument from backfilling the coverage
# of the tests that Test Impact Analysis skips. The other packages spread by hash.
MODULE = "github.com/DataDog/ddtest/"
PINNED_SHARDS = (
    (MODULE + "internal/testdrive/pytestpaths/configuration",),
    (MODULE + "internal/testdrive/pytestpaths/commands",),
    (MODULE + "internal/testdrive", MODULE + "internal/onboard"),
)


def parse_shard(value):
    try:
        index, total = map(int, value.split("/"))
        if 1 <= index <= total:
            return index, total
    except ValueError:
        pass
    raise ValueError("shard must be index/total with 1 <= index <= total")


def discover_packages():
    result = subprocess.run(["go", "list", "./..."], check=True, capture_output=True, text=True)
    packages = sorted(set(result.stdout.split()))
    if not packages:
        raise ValueError("Go reported no packages")
    return packages


def selected_packages(packages, index, total):
    pinned = {package: position % total for position, group in enumerate(PINNED_SHARDS) for package in group}

    def shard(package):
        if package in pinned:
            return pinned[package]
        return int.from_bytes(hashlib.sha256(package.encode()).digest()[:8], "big") % total

    return sorted({package for package in packages if shard(package) == index - 1})


def shard_packages(packages, shard):
    index, total = parse_shard(shard)
    selected = selected_packages(packages, index, total)
    if not selected:
        raise ValueError(f"Go shard {shard} has no packages")
    print(f"Go shard {index}/{total}: {len(selected)}/{len(packages)} packages", file=sys.stderr, flush=True)
    return "\n".join(selected)


def merge_coverage(paths, output, expected):
    if len(paths) != expected or len({path.resolve() for path in paths}) != expected:
        raise ValueError(f"expected {expected} distinct coverage profiles, got {len(paths)}")
    blocks = {}
    for path in paths:
        lines = path.read_text().splitlines()
        if not lines or lines[0] != "mode: atomic":
            raise ValueError(f"{path}: expected atomic coverage")
        for line in lines[1:]:
            block, statements, count = line.rsplit(" ", 2)
            statements, count = int(statements), int(count)
            if statements < 0 or count < 0:
                raise ValueError(f"{path}: negative coverage count")
            previous, accumulated = blocks.get(block, (statements, 0))
            if previous != statements:
                raise ValueError(f"{path}: inconsistent statement count for {block}")
            blocks[block] = statements, accumulated + count
    output.write_text("mode: atomic\n" + "".join(
        f"{block} {statements} {count}\n" for block, (statements, count) in sorted(blocks.items())))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="action", required=True)
    select = commands.add_parser("select")
    select.add_argument("--shard", required=True)
    merge = commands.add_parser("merge")
    merge.add_argument("--expected", type=int, required=True)
    merge.add_argument("--output", type=Path, required=True)
    merge.add_argument("profiles", nargs="+", type=Path)
    args = parser.parse_args()
    try:
        if args.action == "select":
            print(shard_packages(discover_packages(), args.shard))
            return 0
        merge_coverage(args.profiles, args.output, args.expected)
        return 0
    except subprocess.CalledProcessError as error:
        print(error.stdout or "", file=sys.stderr, end="")
        print(error.stderr or "", file=sys.stderr, end="")
        parser.error(str(error))
    except (OSError, ValueError) as error:
        parser.error(str(error))


if __name__ == "__main__":
    sys.exit(main())
