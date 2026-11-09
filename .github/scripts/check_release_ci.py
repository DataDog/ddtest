"""Require green main push workflows for the exact commit being released."""

import json
import os
import subprocess
import sys
from urllib.parse import urlencode


WORKFLOWS = ("ci.yml", "go-instrumentation.yml")


def check(repository, sha, api):
    evidence = []
    query = urlencode({"branch": "main", "event": "push", "head_sha": sha, "per_page": 100})
    for workflow in WORKFLOWS:
        pages = api(f"repos/{repository}/actions/workflows/{workflow}/runs?{query}")
        runs = [run for page in pages for run in page["workflow_runs"]]
        if not runs:
            raise ValueError(f"{workflow}: no main push run for {sha}; wait for main CI to pass.")
        # Do not filter by success: an older green run must not hide a newer
        # failure or an in-progress rerun. The API reports the current attempt.
        run = max(runs, key=lambda run: run["id"])
        if (run["head_sha"] != sha or run["head_branch"] != "main"
                or run["event"] != "push"
                or run["path"] != f".github/workflows/{workflow}"
                or run["head_repository"]["full_name"] != repository):
            raise ValueError(f"{workflow}: run does not identify a main push of {sha} in {repository}.")
        if run["status"] != "completed" or run["conclusion"] != "success":
            raise ValueError(
                f"{workflow}: {run['status']}/{run['conclusion']} for {sha}: {run['html_url']}; "
                "wait for main CI to pass, then retry the release."
            )
        evidence.append(f"{workflow}: {run['html_url']} (attempt {run['run_attempt']})")
    return evidence


def github_api(endpoint):
    return json.loads(subprocess.check_output(
        ["gh", "api", "--paginate", "--slurp", endpoint], text=True,
    ))


def main():
    repository = os.environ["GITHUB_REPOSITORY"]
    if (repository != "DataDog/ddtest" or os.environ["GITHUB_REF"] != "refs/heads/main"
            or os.environ["GITHUB_EVENT_NAME"] != "workflow_dispatch"):
        raise ValueError("Releases must be dispatched from main in DataDog/ddtest.")
    sha = os.environ["GITHUB_SHA"]
    checkout = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    if checkout != sha:
        raise ValueError(f"Checkout {checkout} does not match dispatched commit {sha}.")
    evidence = check(repository, sha, github_api)
    summary = f"## Main CI verified for {sha}\n" + "\n".join(f"- {line}" for line in evidence) + "\n"
    print(summary)
    with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as output:
        output.write(summary)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        sys.exit(f"Release CI check failed: {error}")
