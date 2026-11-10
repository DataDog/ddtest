"""Restore a prepared case image into a fresh, owned trial."""
import json
import re

PAYLOAD = "/opt/ddtest-case"
# Model authentication and configuration are never seeded from an image.
SEEDS = ("caches", "tools", "venv", "bundle-bin", "home/.m2", "home/.gradle", "home/bundle", "home/.bundle")


def restore(command, container, image, repository, commit, framework, trial, checkout):
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("--case-image requires --ref to be the full source commit")
    identity = container.control("image", "inspect", "--format", "{{.Id}}", image).stdout.strip()
    running = container.control("inspect", "--format", "{{.Image}}", container.name).stdout.strip()
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", identity) or running != identity:
        raise RuntimeError("Prepared case image digest does not match the running container")
    metadata = json.loads(command(["cat", PAYLOAD + "/manifest.json"]))
    expected = {"schema_version": 1, "repository": repository, "commit": commit, "framework": framework}
    if any(metadata.get(key) != value for key, value in expected.items()):
        raise ValueError("Prepared case image metadata does not match the requested repository, commit and framework")
    baseline = metadata.get("baseline", "")
    if not re.fullmatch(r"[0-9a-f]{40}", baseline):
        raise ValueError("Prepared case image must name its full baseline commit")
    command(["cp", "-a", "--no-preserve=ownership", PAYLOAD + "/repo", str(checkout)])
    for seed in SEEDS:
        command(["sh", "-c", 'if [ -d "$1" ]; then mkdir -p "$2"; cp -a --no-preserve=ownership "$1/." "$2/"; fi',
                 "sh", PAYLOAD + "/" + seed, str(trial / seed)])
    if command(["git", "rev-parse", "HEAD"], cwd=checkout) != baseline:
        raise RuntimeError("Prepared case checkout does not match its baseline commit")
    command(["git", "merge-base", "--is-ancestor", commit, "HEAD"], cwd=checkout)
    if command(["git", "remote", "get-url", "origin"], cwd=checkout) != repository:
        raise RuntimeError("Prepared case checkout has a different repository origin")
    if command(["git", "status", "--porcelain", "--untracked-files=all"], cwd=checkout):
        raise RuntimeError("Prepared case checkout is dirty")
    return {**metadata, "image": image, "image_id": identity}
