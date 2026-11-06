"""Cache recipe binding and archive/restore boundaries; no model or network calls."""

import copy
import contextlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import dependency_cache
import prepare_dependencies


class CacheTests(unittest.TestCase):
    def setUp(self):
        self.storage = tempfile.TemporaryDirectory()
        self.addCleanup(self.storage.cleanup)
        self.root = Path(self.storage.name)
        self.trial = self.root / "trial"
        self.image = self.root / "image"
        self.trial.mkdir()
        self.image.mkdir()
        self.key = "a" * 64

    def script(self, mode, key=None):
        script = dependency_cache.SCRIPT.replace("Path('/trial')", f"Path({str(self.trial)!r})")
        script = script.replace("Path('/opt/ddtest-eval-cache')", f"Path({str(self.image)!r})")
        script = script.replace("Path('/tmp/bundle')", f"Path({str(self.root/'bundle')!r})")
        return subprocess.run([sys.executable, "-c", script, mode, key or self.key],
                              capture_output=True, text=True)

    def test_capture_excludes_source_reports_and_cli_credentials(self):
        for name in ("caches/npm/package", "home/.codex/auth.json", "home/.claude/.credentials.json",
                     "repo/source.py", "repo/target/report.xml", "repo/.testoptimization/testdrive.json"):
            path = self.trial / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("fixture")
        result = self.script("capture")
        self.assertEqual(result.returncode, 0, result.stderr)
        archive = self.root / "cache.tar"
        prepare_dependencies.copy_archive(self.trial, archive, self.key)
        with tarfile.open(archive) as source:
            self.assertEqual(set(source.getnames()), {"manifest.json", "data/caches/npm", "data/caches/npm/package"})

    def test_restore_copies_only_private_cache_paths_and_checks_recipe_first(self):
        (self.image / "data/caches/npm").mkdir(parents=True)
        (self.image / "data/caches/npm/package").write_text("package")
        manifest = {"version": 1, "fingerprint": self.key, "paths": ["caches/npm"]}
        (self.image / "manifest.json").write_text(json.dumps(manifest))
        result = self.script("restore", "b" * 64)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.trial / "caches/npm").exists())
        result = self.script("restore")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.trial / "caches/npm/package").read_text(), "package")
        self.assertFalse((self.trial / "repo").exists())

    def test_gradle_project_build_cache_and_daemon_state_are_not_bundled(self):
        for name in ("home/.gradle/caches/build-cache-1/compiled-project", "home/.gradle/daemon/state",
                     "home/.gradle/caches/modules-2/dependency.jar"):
            path = self.trial / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text("fixture")
        result = self.script("capture")
        self.assertEqual(result.returncode, 0, result.stderr)
        with tarfile.open(self.trial / "dependency-cache.tar") as archive:
            names = archive.getnames()
        self.assertIn("data/home/.gradle/caches/modules-2/dependency.jar", names)
        self.assertFalse(any("build-cache-" in name or "/daemon" in name for name in names))

    def test_restore_rejects_unsupported_destinations_before_copying(self):
        for name in ("../outside", "home/.codex", "/tmp/outside", "repo/node_modules"):
            with self.subTest(name=name):
                (self.image / "manifest.json").write_text(json.dumps(
                    {"version": 1, "fingerprint": self.key, "paths": ["caches/npm", name]}))
                self.assertNotEqual(self.script("restore").returncode, 0)
                self.assertFalse((self.trial / "caches/npm").exists())

    def test_archive_rejects_path_traversal_and_credential_paths(self):
        for member in ("../outside", "/outside", "data/home/.codex/auth.json", "data/caches/npm/../../outside"):
            with self.subTest(member=member):
                path = self.trial / "dependency-cache.tar"
                with tarfile.open(path, "w") as archive:
                    data = json.dumps({"version": 1, "fingerprint": self.key, "paths": ["caches/npm"]}).encode()
                    info = tarfile.TarInfo("manifest.json")
                    info.size = len(data)
                    archive.addfile(info, io.BytesIO(data))
                    archive.addfile(tarfile.TarInfo(member))
                with self.assertRaisesRegex(ValueError, "unsupported"):
                    prepare_dependencies.copy_archive(self.trial, self.root / (str(abs(hash(member))) + ".tar"), self.key)

    def test_archive_rejects_symlinks_leaving_the_cache(self):
        with tarfile.open(self.trial / "dependency-cache.tar", "w") as archive:
            data = json.dumps({"version": 1, "fingerprint": self.key, "paths": ["caches/npm"]}).encode()
            info = tarfile.TarInfo("manifest.json")
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))
            link = tarfile.TarInfo("data/caches/npm/escape")
            link.type = tarfile.SYMTYPE
            link.linkname = "/etc/profile"
            archive.addfile(link)
        with self.assertRaisesRegex(ValueError, "symlink leaves"):
            prepare_dependencies.copy_archive(self.trial, self.root / "unsafe.tar", self.key)

    def test_archive_file_cannot_be_a_symlink(self):
        sentinel = self.root / "outside"
        sentinel.write_text("must not be read")
        (self.trial / "dependency-cache.tar").symlink_to(sentinel)
        with self.assertRaises(OSError):
            prepare_dependencies.copy_archive(self.trial, self.root / "copy.tar", self.key)
        self.assertEqual(sentinel.read_text(), "must not be read")

    def test_archive_rejects_writes_and_dotdot_through_other_links(self):
        for indirect in (False, True):
            with self.subTest(indirect=indirect):
                with tarfile.open(self.trial / "dependency-cache.tar", "w") as archive:
                    data = json.dumps({"version": 1, "fingerprint": self.key, "paths": ["caches/npm"]}).encode()
                    info = tarfile.TarInfo("manifest.json")
                    info.size = len(data)
                    archive.addfile(info, io.BytesIO(data))
                    alias = tarfile.TarInfo("data/caches/npm/alias")
                    alias.type, alias.linkname = tarfile.SYMTYPE, "."
                    archive.addfile(alias)
                    entry = tarfile.TarInfo("data/caches/npm/escape" if indirect else "data/caches/npm/alias/file")
                    if indirect:
                        entry.type, entry.linkname = tarfile.SYMTYPE, "alias/../outside"
                    archive.addfile(entry)
                with self.assertRaisesRegex(ValueError, "through a link|chained link"):
                    prepare_dependencies.copy_archive(self.trial, self.root / (str(indirect) + ".tar"), self.key)

    def test_recipe_changes_invalidate_the_cache(self):
        case = {"repo": "https://example.invalid/public.git", "commit": "0" * 40,
                "harness": {"framework": "jest"}, "setup_commands": ["npm ci"], "readiness_commands": [],
                "prepared_image": {}, "trims": {"required": None, "optional_speed": {"commands": ["git rm slow.test.js"]}}}
        original = dependency_cache.fingerprint(case, "base")
        self.assertNotEqual(original, dependency_cache.fingerprint(case, "other-base"))
        self.assertNotEqual(original, dependency_cache.fingerprint(case, "base", True))
        for field, value in (("commit", "1" * 40), ("setup_commands", ["npm install"]),
                             ("readiness_commands", ["npm test"]),
                             ("prepared_image", {"warmup_commands": ["npm run build"]})):
            changed = copy.deepcopy(case)
            changed[field] = value
            self.assertNotEqual(original, dependency_cache.fingerprint(changed, "base"))

    def test_preparer_cannot_reuse_or_clean_an_existing_evidence_directory(self):
        output = self.root / "existing"
        output.mkdir()
        sentinel = output / "ownership.json"
        sentinel.write_text("existing trial must be preserved")
        with patch.object(prepare_dependencies, "cleanup", side_effect=AssertionError("cleaned existing trial")), \
                contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            prepare_dependencies.main(["E17", "--image", "sha256:" + "a" * 64,
                                       "--ddtest", "/fixture", "--tag", "fixture", "--output", str(output)])
        self.assertEqual(sentinel.read_text(), "existing trial must be preserved")

    def test_local_image_build_does_not_use_registry_credentials_or_proxies(self):
        original = self.root / "docker-profile"
        original.mkdir()
        (original / "contexts").mkdir()
        (original / "cli-plugins").mkdir()
        settings = {"currentContext": "fixture-engine", "credsStore": "must-not-run",
                    "auths": {"registry.invalid": {"auth": "credential-canary"}},
                    "proxies": {"default": {"httpProxy": "proxy-credential-canary"}}}
        (original / "config.json").write_text(json.dumps(settings))
        context = self.root / "build"
        context.mkdir()
        seen = []
        commands = []
        base = "sha256:" + "a" * 64

        def build(argv, **kwargs):
            commands.append(argv)
            config = Path(argv[argv.index("--config") + 1])
            seen.append(config)
            self.assertNotEqual(config.parent, context)
            self.assertEqual(json.loads((config / "config.json").read_text()), {"currentContext": "fixture-engine"})
            self.assertEqual((config / "contexts").resolve(), (original / "contexts").resolve())
            self.assertEqual((config / "cli-plugins").resolve(), (original / "cli-plugins").resolve())
            self.assertNotIn("DOCKER_AUTH_CONFIG", kwargs["env"])
            if "build" in argv:
                reference = (context / "Dockerfile").read_text().splitlines()[0].removeprefix("FROM ")
                self.assertRegex(reference, r"^ddtest-eval-base:[0-9a-f]{32}$")
                self.assertEqual(reference, commands[0][-1])

        with patch.dict(os.environ, {"DOCKER_CONFIG": str(original), "DOCKER_AUTH_CONFIG": "must-not-forward"}), \
                patch.object(prepare_dependencies.subprocess, "run", side_effect=build), \
                patch.object(prepare_dependencies.subprocess, "check_output", return_value=base + "\n") as inspect:
            prepare_dependencies.build_image(context, base, "fixture", 30)
        self.assertEqual(commands[0][-4:-1], ["image", "tag", base])
        self.assertEqual(commands[-1][-3:], ["image", "rm", commands[0][-1]])
        self.assertEqual(inspect.call_args_list[0].args[0][-1], base)
        self.assertEqual(inspect.call_args_list[1].args[0][-1], commands[0][-1])
        self.assertFalse(seen[0].exists())
        self.assertEqual(json.loads((original / "config.json").read_text()), settings)

    def test_temporary_base_tag_is_removed_after_build_failure_or_identity_mismatch(self):
        base = "sha256:" + "a" * 64
        for mismatch in (False, True):
            with self.subTest(mismatch=mismatch):
                context = self.root / str(mismatch)
                context.mkdir()
                commands = []

                def run(argv, **kwargs):
                    commands.append(argv)
                    if "build" in argv:
                        raise subprocess.CalledProcessError(1, argv)

                outputs = [base, "sha256:" + "b" * 64 if mismatch else base]
                with patch.object(prepare_dependencies.subprocess, "run", side_effect=run), \
                        patch.object(prepare_dependencies.subprocess, "check_output", side_effect=outputs), \
                        self.assertRaises(ValueError if mismatch else subprocess.CalledProcessError):
                    prepare_dependencies.build_image(context, base, "fixture", 30)
                self.assertEqual(commands[-1][-3:], ["image", "rm", commands[0][-1]])
                self.assertEqual(any("build" in argv for argv in commands), not mismatch)


if __name__ == "__main__":
    unittest.main()
