#!/usr/bin/env python3
"""Build one case's dependency-cache image without model authentication or an agent."""

import argparse
import hashlib
import json
import os
import posixpath
from pathlib import Path, PurePosixPath
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import uuid

import dependency_cache
import run_catalog
import run_eval
from isolation import cleanup, trial_root, validate_image


def build_image(context, base_image, tag, timeout):
    """Keep the engine context, but never give the build registry logins/proxies.

    Legacy Docker otherwise invokes every configured credential helper even
    for a completely local image. This private config stays outside the build
    context and is removed afterward; the user's configuration is untouched.
    """
    original = Path(os.environ.get("DOCKER_CONFIG", str(Path.home() / ".docker")))
    settings = json.loads((original / "config.json").read_text()) if (original / "config.json").exists() else {}
    with tempfile.TemporaryDirectory(prefix="ddtest-cache-docker-", dir=context.parent) as directory:
        config = Path(directory)
        (config / "config.json").write_text(json.dumps(
            {"currentContext": settings["currentContext"]} if settings.get("currentContext") else {}))
        for name in ("contexts", "cli-plugins"):
            if (original / name).exists():
                (config / name).symlink_to((original / name).resolve(), target_is_directory=True)
        env = {name: value for name, value in os.environ.items() if name != "DOCKER_AUTH_CONFIG"}
        docker = ["docker", "--config", str(config)]
        inspect = ["image", "inspect", "--format", "{{.Id}}"]
        base_id = subprocess.check_output([*docker, *inspect, base_image], env=env, text=True, timeout=20).strip()
        # BuildKit parses FROM sha256:<id> as a registry name. Keep the immutable
        # ID in the recipe, but build through a private, verified local tag.
        alias = "ddtest-eval-base:" + uuid.uuid4().hex
        try:
            subprocess.run([*docker, "image", "tag", base_id, alias], env=env, check=True, timeout=20)
            if subprocess.check_output([*docker, *inspect, alias], env=env, text=True, timeout=20).strip() != base_id:
                raise ValueError("temporary dependency base tag does not match the immutable image")
            (context / "Dockerfile").write_text(
                f"FROM {alias}\nADD cache.tar {dependency_cache.IMAGE_DIRECTORY}/\n"
                f"RUN chmod -R a+rX {dependency_cache.IMAGE_DIRECTORY}\n")
            subprocess.run([*docker, "build", "--network=none", "--pull=false", "-t", tag, str(context)],
                           env=env, check=True, timeout=timeout)
        finally:
            subprocess.run([*docker, "image", "rm", alias], env=env, check=True, capture_output=True, timeout=20)


def copy_archive(trial, target, key):
    """Do not follow trial symlinks or extract repository-controlled data on the host."""
    root = run_eval.open_directory(trial)
    try:
        descriptor = os.open("dependency-cache.tar", os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=root)
    finally:
        os.close(root)
    with os.fdopen(descriptor, "rb") as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_size > 8 * 1024**3:
            raise ValueError("dependency cache must be an owned regular archive smaller than 8 GiB")
        with target.open("xb") as destination:
            shutil.copyfileobj(source, destination)
    with tarfile.open(target) as archive:
        manifest = archive.getmember("manifest.json")
        if not manifest.isfile() or manifest.size > 1024 * 1024:
            raise ValueError("invalid dependency cache manifest")
        with archive.extractfile(manifest) as stream:
            data = json.load(stream)
        entries = data.get("paths")
        if data.get("version") != 1 or data.get("fingerprint") != key or not isinstance(entries, list):
            raise ValueError("dependency cache manifest does not match the requested recipe")
        allowed = [*dependency_cache.PATHS, "repo/Gemfile.lock"]
        if not all(isinstance(name, str) and name in allowed for name in entries):
            raise ValueError("unsupported dependency cache destination")
        members = archive.getmembers()
        names = [posixpath.normpath(member.name) for member in members]
        if len(set(names)) != len(names):
            raise ValueError("duplicate dependency cache archive path")
        links = {posixpath.normpath(member.name) for member in members if member.issym() or member.islnk()}
        for member in members:
            path = PurePosixPath(member.name)
            if path.is_absolute() or ".." in path.parts or not (
                    member.name == "manifest.json" or any(
                        member.name == "data/" + name or member.name.startswith("data/" + name + "/")
                        for name in entries)):
                raise ValueError("unsupported dependency cache archive path: " + member.name)
            if not (member.isfile() or member.isdir() or member.issym() or member.islnk()):
                raise ValueError("unsupported dependency cache archive entry: " + member.name)
            if any(str(parent) in links for parent in path.parents):
                raise ValueError("dependency cache archive writes through a link: " + member.name)
            if member.issym() or member.islnk():
                raw_target = member.linkname if member.islnk() else posixpath.join(str(path.parent), member.linkname)
                # A lexical '..' check alone misses a/../outside when a is a
                # symlink to '.'. Reject indirect links rather than resolving
                # repository-controlled chains on the host or in Docker ADD.
                prefix = ""
                for part in PurePosixPath(raw_target).parts:
                    prefix = posixpath.normpath(posixpath.join(prefix, part))
                    if prefix in links:
                        raise ValueError("dependency cache contains a chained link: " + member.name)
                target = posixpath.normpath(raw_target)
                owner = next(name for name in entries if member.name == "data/" + name or
                             member.name.startswith("data/" + name + "/"))
                if target != "data/" + owner and not target.startswith("data/" + owner + "/"):
                    raise ValueError("dependency cache symlink leaves its cache: " + member.name)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("case", help="Catalog case id, such as E17")
    parser.add_argument("--catalog", type=Path, default=run_catalog.CATALOG)
    parser.add_argument("--image", required=True, help="Trusted immutable base image ID/digest")
    parser.add_argument("--ddtest", type=Path, required=True, help="Linux ddtest for the base image's architecture")
    parser.add_argument("--tag", required=True, help="Local image tag to build; never published")
    parser.add_argument("--output", type=Path, required=True, help="New preparation evidence directory")
    parser.add_argument("--speed-trims", action="store_true")
    parser.add_argument("--timeout", type=float, default=run_catalog.DEFAULT_TIMEOUT)
    args = parser.parse_args(argv)
    validate_image(args.image)
    catalog = run_catalog.load_catalog(args.catalog)
    problems = run_catalog.validate(catalog)
    if problems:
        parser.error("; ".join(problems))
    matches = [case for case in catalog["cases"] if args.case in (case["id"], case["id"].split("-")[0])]
    if len(matches) != 1:
        parser.error("unknown case " + args.case)
    case = matches[0]
    key = dependency_cache.fingerprint(case, args.image, args.speed_trims)
    output = args.output.resolve()
    try:
        output.mkdir(mode=0o700, parents=True, exist_ok=False)
    except FileExistsError:
        parser.error("--output must be a new directory; existing evidence is never reused or cleaned up")
    evidence = output / "evidence"
    argv = run_catalog.build_argv(case, args.image, args.ddtest.resolve(), evidence,
                                 timeout=args.timeout, speed_trims=args.speed_trims)
    argv += ["--prepare-cache", key]
    for warm in case["prepared_image"].get("warmup_commands", []):
        argv += ["--warmup-command", warm]
    try:
        with run_catalog.Supervisor() as supervisor:
            code = supervisor.run(argv, sys.stdout)
        if code:
            return code
        run = json.loads((evidence / "run.json").read_text())
        if run.get("status") != "prepared" or run.get("prepared_cache") != key:
            raise ValueError("dependency preparation did not complete")
        with tempfile.TemporaryDirectory(prefix="ddtest-cache-build-", dir=trial_root()) as directory:
            context = Path(directory)
            archive = context / "cache.tar"
            copy_archive(Path(run["trial_directory"]), archive, key)
            build_image(context, args.image, args.tag, args.timeout)
            image = subprocess.check_output(["docker", "image", "inspect", "--format", "{{.Id}}", args.tag],
                                            text=True, timeout=20).strip()
            digest = hashlib.sha256()
            with archive.open("rb") as stream:
                for block in iter(lambda: stream.read(1024 * 1024), b""):
                    digest.update(block)
            receipt = {"case": case["id"], "image": image, "base_image": args.image, "fingerprint": key,
                       "archive_sha256": digest.hexdigest()}
            (output / "dependency-image.json").write_text(json.dumps(receipt, indent=2) + "\n")
            print(f"--dependency-image {case['id']}={image}")
        return 0
    finally:
        if (evidence / "ownership.json").exists():
            cleanup(evidence)


if __name__ == "__main__":
    sys.exit(main())
