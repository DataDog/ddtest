"""Shard the root Go suite without changing tests; merge its atomic coverage."""

import argparse
import hashlib
from pathlib import Path
import re
import subprocess
import sys


def parse_shard(value):
    try:
        index, total = map(int, value.split("/"))
        if 1 <= index <= total:
            return index, total
    except ValueError:
        pass
    raise ValueError("shard must be index/total with 1 <= index <= total")


def discover_tests():
    # Match CI's build selection, including tests guarded by the race build tag.
    result = subprocess.run(["go", "test", "-race", "-list", ".", "./..."],
                            check=True, capture_output=True, text=True)
    names = sorted({line for line in result.stdout.splitlines()
                    if re.fullmatch(r"(?:Test|Example|Fuzz)\w*", line)})
    if not names:
        raise ValueError("Go reported no tests, examples or fuzz seeds")
    return names


def selected_tests(names, index, total):
    return sorted({name for name in names
                   if int.from_bytes(hashlib.sha256(name.encode()).digest()[:8], "big") % total == index - 1})


def test_pattern(names, shard):
    index, total = parse_shard(shard)
    selected = selected_tests(names, index, total)
    print(f"Go shard {index}/{total}: {len(selected)}/{len(names)} test names", file=sys.stderr, flush=True)
    return "^(" + "|".join(selected) + ")$" if selected else "^$"


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
            print(test_pattern(discover_tests(), args.shard))
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
