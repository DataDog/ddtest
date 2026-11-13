"""Immutable, case-bound dependency caches. Source and model state are never cached."""

import hashlib
import json
import re
import shlex


IMAGE_DIRECTORY = "/opt/ddtest-eval-cache"
# These locations belong to a new trial. No CLI profile, evaluator binary,
# repository tree, compiled project output or test report is included.
PATHS = (
    "caches/npm", "caches/yarn", "caches/pnpm-store", "caches/pnpm",
    "caches/cypress", "caches/playwright", "caches/puppeteer", "caches/pip", "caches/uv", "caches/xdg-cache",
    "home/.m2", "home/.gradle", "bundle",
)


def fingerprint(case, base_image, speed_trims=False):
    recipe = {"version": 1, "cache_paths": PATHS, "repo": case["repo"], "commit": case["commit"],
              "base_image": base_image, "harness": case["harness"],
              "setup": case["setup_commands"], "readiness": case["readiness_commands"],
              "warmup": case["prepared_image"].get("warmup_commands", []),
              "trims": {kind: case["trims"].get(kind) for kind in
                        (("required", "optional_speed") if speed_trims else ("required",))}}
    return hashlib.sha256(json.dumps(recipe, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def validate_fingerprint(value):
    if not re.fullmatch(r"[0-9a-f]{64}", value or ""):
        raise ValueError("dependency cache fingerprint must be 64 lowercase hex characters")


# The script runs only inside the trial container, before any model credentials
# are installed for preparation, or while they are suspended during restoration.
# The image is trusted and immutable, just like the base runtime image.
SCRIPT = r'''
import json, shutil, subprocess, sys, tarfile
from pathlib import Path

mode, key = sys.argv[1:]
paths = json.loads(PATHS_JSON)
root = Path('/trial')
image = Path('/opt/ddtest-eval-cache')

def destination(name):
    return Path('/tmp/bundle') if name == 'bundle' else root / name

def dependency_only(member):
    relative = member.name.removeprefix('data/home/.gradle/')
    if member.name.startswith('data/home/.gradle/') and (
            relative.startswith('daemon/') or relative == 'daemon' or
            any(part.startswith('build-cache-') for part in Path(relative).parts)):
        return None
    return member

if mode == 'capture':
    entries = [name for name in paths if destination(name).exists()]
    # Bundler's generated lockfile is an input, not a test result. Preserve it
    # only when Git already ignores it; tracked locks come from the fresh clone.
    lock = root / 'repo/Gemfile.lock'
    ignored = lock.is_file() and subprocess.run(
        ['git', 'check-ignore', '-q', '--', 'Gemfile.lock'], cwd=root/'repo').returncode == 0
    if ignored:
        entries.append('repo/Gemfile.lock')
    manifest = root / 'dependency-cache.json'
    manifest.write_text(json.dumps({'version': 1, 'fingerprint': key, 'paths': entries}))
    with tarfile.open(root/'dependency-cache.tar', 'w') as archive:
        archive.add(manifest, arcname='manifest.json')
        for name in entries:
            archive.add(destination(name), arcname='data/' + name, filter=dependency_only)
else:
    manifest = json.loads((image/'manifest.json').read_text())
    if manifest.get('version') != 1 or manifest.get('fingerprint') != key:
        raise ValueError('dependency cache does not match this case, recipe and base image')
    entries = manifest.get('paths')
    if not isinstance(entries, list) or not all(isinstance(name, str) and
            name in [*paths, 'repo/Gemfile.lock'] for name in entries):
        raise ValueError('dependency cache contains an unsupported destination')
    if mode == 'check':
        print(json.dumps({'fingerprint': key, 'paths': entries}))
        sys.exit(0)
    for name in entries:
        source, target = image/'data'/name, destination(name)
        if source.is_symlink():
            raise ValueError('dependency cache root must not be a symlink: ' + name)
        if target.is_symlink():
            raise ValueError('dependency cache destination must not be a symlink: ' + name)
        if name == 'repo/Gemfile.lock':
            if target.exists():
                raise ValueError('dependency cache must not overwrite a repository lockfile')
            if subprocess.run(['git', 'check-ignore', '-q', '--', 'Gemfile.lock'],
                              cwd=root/'repo').returncode:
                raise ValueError('cached Gemfile.lock must remain Git-ignored')
            shutil.copyfile(source, target)
        else:
            shutil.copytree(source, target, symlinks=True, dirs_exist_ok=True)
    print(json.dumps({'fingerprint': key, 'paths': entries}))
'''.replace("PATHS_JSON", repr(json.dumps(PATHS)))


def command(mode, key):
    validate_fingerprint(key)
    if mode not in ("capture", "restore", "check"):
        raise ValueError("unknown dependency cache operation")
    return ["python3", "-c", SCRIPT, mode, key]


def warmup(command):
    """Build in a container-native copy; never add build output to the checkout."""
    return ["sh", "-c", 'd=$(mktemp -d /tmp/ddtest-cache-warm-XXXXXX) || exit; '
            'trap \'rm -rf "$d"\' EXIT; cp -a /trial/repo/. "$d/" && cd "$d" && "$@"',
            "sh", *shlex.split(command)]
