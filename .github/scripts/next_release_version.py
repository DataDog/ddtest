"""Print the next stable version for a minor or patch release."""

import re
import sys


def next_version(previous, release_type):
    match = re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", previous)
    if not match:
        raise ValueError(f"Expected a published stable release tag, got: {previous}")
    major, minor, patch = map(int, match.groups())
    if release_type == "minor":
        return f"v{major}.{minor + 1}.0"
    if release_type == "patch":
        return f"v{major}.{minor}.{patch + 1}"
    raise ValueError("Release type must be minor or patch.")


if __name__ == "__main__":
    try:
        print(next_version(*sys.argv[1:]))
    except (ValueError, TypeError) as error:
        sys.exit(str(error))
