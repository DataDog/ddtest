"""Publish a toolchain update with Git and the GitHub CLI."""

import os
from pathlib import Path
import subprocess
import tempfile


BRANCH = "automation/go-toolchain"
TITLE = "build(deps): update Go and golangci-lint"
BODY = """## What

Update Go and/or golangci-lint to their latest stable releases. CI and release
builds read these pins from go.mod and .golangci-lint-version.

## Why

Pick up toolchain security fixes and keep the linter current.
Release sources: https://go.dev/dl/ and https://github.com/golangci/golangci-lint/releases.

## E2E testing

Prerequisite: check out this PR and have a configured test project with Datadog
credentials available for `ddtest plan`.

1. Run `make build`. Expect the `ddtest` binary to be created successfully.
2. Run `go version -m ./ddtest`. Expect the Go version to match the new `go.mod` pin.
3. Run `make lint`. Expect golangci-lint to work with the new version pin.
4. Run `./ddtest plan` in the configured test project. Expect a successful plan
   and the usual Datadog requests, with no toolchain-related errors.
5. Check this PR's CI results. Expect the build, test, and lint jobs to pass.

No cleanup is needed beyond removing the local `ddtest` binary if desired.
"""


def command(*args, capture=False, check=True):
    return subprocess.run(args, check=check, text=True, capture_output=capture)


def main():
    repository = os.environ["GITHUB_REPOSITORY"]
    command("git", "add", "--", "go.mod", ".golangci-lint-version")
    diff = command("git", "diff", "--cached", "--quiet", check=False)
    if diff.returncode == 0:
        print("No toolchain changes to publish")
        return
    if diff.returncode != 1:
        raise subprocess.CalledProcessError(diff.returncode, diff.args)

    command("git", "switch", "-c", BRANCH)
    command("git", "config", "user.name", "ddtest-toolchain[bot]")
    command("git", "config", "user.email", "ddtest-toolchain[bot]@users.noreply.github.com")
    command("git", "config", "commit.gpgsign", "false")
    command("git", "commit", "-m", TITLE)

    # Checkout has persist-credentials: false. gh supplies credentials only to
    # this ephemeral runner, using the scoped installation token in GH_TOKEN.
    command("gh", "auth", "setup-git")
    ref = f"refs/heads/{BRANCH}"
    remote = command("git", "ls-remote", "--heads", "origin", ref, capture=True).stdout
    previous_sha = remote.split()[0] if remote else ""
    command("git", "push", f"--force-with-lease={ref}:{previous_sha}", "origin", f"HEAD:{ref}")

    with tempfile.TemporaryDirectory() as directory:
        body_file = Path(directory) / "pr-body.md"
        body_file.write_text(BODY)
        existing = command(
            "gh", "pr", "list", "--repo", repository, "--head", BRANCH,
            "--base", "main", "--state", "open", "--json", "number",
            "--jq", ".[0].number // empty", capture=True,
        ).stdout.strip()
        if existing:
            command("gh", "pr", "edit", existing, "--repo", repository,
                    "--title", TITLE, "--body-file", str(body_file))
        else:
            command("gh", "pr", "create", "--repo", repository, "--base", "main",
                    "--head", BRANCH, "--title", TITLE, "--body-file", str(body_file))


if __name__ == "__main__":
    main()
