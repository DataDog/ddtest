"""Update the pinned Go and golangci-lint versions from stable upstream releases."""

import json
import os
from pathlib import Path
import re
import urllib.request


def version(value):
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", value):
        raise ValueError(f"Expected a stable release version, got {value!r}")
    return tuple(map(int, value.split(".")))


def update(root, go_releases, lint_release):
    # Parse all upstream data before writing either file. Never downgrade.
    latest_go = max(
        (release["version"].removeprefix("go") for release in go_releases if release["stable"]),
        key=version,
    )
    if lint_release["draft"] or lint_release["prerelease"]:
        raise ValueError("Expected a stable golangci-lint release")
    latest_lint = lint_release["tag_name"].removeprefix("v")
    version(latest_lint)

    go_mod = root / "go.mod"
    content = go_mod.read_text()
    match = re.search(r"^go ([0-9]+\.[0-9]+\.[0-9]+)$", content, re.MULTILINE)
    if match is None:
        raise ValueError("go.mod must pin a full Go release version")
    lint_file = root / ".golangci-lint-version"
    current_lint = lint_file.read_text().strip().removeprefix("v")
    version(current_lint)

    changed = False
    if version(latest_go) > version(match[1]):
        content = content[:match.start(1)] + latest_go + content[match.end(1):]
        # An explicit toolchain added by a dependency update must not override
        # the Go version that setup-go reads from this file.
        content = re.sub(r"^toolchain go[0-9.]+\n", "", content, flags=re.MULTILINE)
        go_mod.write_text(content)
        changed = True
    if version(latest_lint) > version(current_lint):
        lint_file.write_text(f"v{latest_lint}\n")
        changed = True
    return changed


def fetch(url, token=None):
    headers = {"User-Agent": "ddtest-toolchain-updater"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    request = urllib.request.Request(url, headers=headers)
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def main():
    go_releases = fetch("https://go.dev/dl/?mode=json")
    lint_release = fetch(
        "https://api.github.com/repos/golangci/golangci-lint/releases/latest",
        os.environ.get("GH_TOKEN"),
    )
    changed = update(Path.cwd(), go_releases, lint_release)
    with open(os.environ["GITHUB_OUTPUT"], "a") as output:
        output.write(f"changed={str(changed).lower()}\n")
    print("Toolchain update prepared" if changed else "Toolchain is up to date")


if __name__ == "__main__":
    main()
