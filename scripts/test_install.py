"""Runs install.sh and install.ps1 against release files served from a local directory."""

import functools
import hashlib
import http.server
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile
import threading
import unittest

SCRIPTS = Path(__file__).resolve().parent
OS = {"Linux": "linux", "Darwin": "darwin"}.get(platform.system())
ARCH = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine().lower())


def fake_ddtest(version="v1.2.3"):
    return f'#!/bin/sh\nif [ "$1" = --version ]; then echo "{version}"; else echo usage: ddtest; fi\n'


FAKE_DDTEST = fake_ddtest()


class Recording(http.server.SimpleHTTPRequestHandler):
    def do_GET(self):
        self.server.paths.append(self.path)
        super().do_GET()

    def log_message(self, *args):
        pass


@unittest.skipUnless(OS and ARCH and shutil.which("bash"), "install.sh targets Unix shells on amd64 and arm64")
class InstallTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.assets = self.root / "assets"
        self.assets.mkdir()
        self.dir = self.root / "bin"
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), functools.partial(Recording, directory=self.assets))
        server.paths = self.paths = []
        threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        self.url = f"http://127.0.0.1:{server.server_port}"
        self.asset = f"ddtest-{OS}-{ARCH}"

    def release(self, data=FAKE_DDTEST, checksum=None):
        (self.assets / self.asset).write_text(data)
        digest = checksum or hashlib.sha256(data.encode()).hexdigest()
        (self.assets / "SHA256SUMS").write_text(f"{digest}  {self.asset}\n{'0' * 64}  ddtest-other-arch\n")

    def install(self, *args, path=None, github_path=None, cwd=None, **extra):
        env = {key: value for key, value in os.environ.items() if not key.startswith(("DDTEST_INSTALL_", "GITHUB_"))}
        env.update(DDTEST_INSTALL_DOWNLOAD_URL=self.url, DDTEST_INSTALL_DIR=str(self.dir), PATH=path or os.environ["PATH"])
        env.update(extra)
        if github_path:
            env["GITHUB_PATH"] = str(github_path)
        return subprocess.run(self.command(*args), env=env, capture_output=True, text=True, timeout=120, cwd=cwd)

    def command(self, *args):
        return ["bash", str(SCRIPTS / "install.sh"), *args]

    def test_installs_the_checked_release_executable(self):
        self.release()
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        installed = self.dir / "ddtest"
        self.assertEqual(installed.read_text(), FAKE_DDTEST)
        self.assertTrue(os.access(installed, os.X_OK))
        self.assertIn("(release latest)", result.stderr)

    def test_installs_with_a_backslash_in_the_temporary_directory(self):
        # sha256sum and shasum prefix the digest of such a file with a backslash.
        temporary = self.root / "tmp\\dir"
        temporary.mkdir()
        self.release()
        result = self.install(TMPDIR=str(temporary))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.dir / "ddtest").read_text(), FAKE_DDTEST)

    def test_rejects_a_checksum_mismatch(self):
        self.release(checksum="f" * 64)
        result = self.install()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("does not match SHA256SUMS", result.stderr)
        self.assertFalse((self.dir / "ddtest").exists())

    def test_fails_without_a_release_executable(self):
        (self.assets / "SHA256SUMS").write_text(f"{'0' * 64}  ddtest-other-arch\n")
        result = self.install("--version", "v1.2.3")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(f"no release executable {self.asset}", result.stderr)
        self.assertIn("download ddtest from https://github.com/DataDog/ddtest/releases", result.stderr)
        self.assertFalse((self.dir / "ddtest").exists())

    def preinstall(self, data=FAKE_DDTEST):
        self.dir.mkdir()
        installed = self.dir / "ddtest"
        installed.write_text(data)
        installed.chmod(0o755)

    def test_keeps_an_installed_requested_version_without_downloading(self):
        self.preinstall()
        result = self.install("--version", "v1.2.3")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("ddtest v1.2.3 in", result.stderr)
        self.assertIn("(already installed)", result.stderr)
        self.assertEqual(self.paths, [])

    def test_keeps_the_latest_release_when_its_checksum_matches(self):
        self.release()
        self.preinstall()
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("(already installed)", result.stderr)
        self.assertEqual(self.paths, ["/SHA256SUMS"])

    def test_replaces_another_version(self):
        self.release()
        self.preinstall(fake_ddtest("v1.0.0"))
        result = self.install()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.dir / "ddtest").read_text(), FAKE_DDTEST)
        self.assertEqual(self.paths, ["/SHA256SUMS", "/" + self.asset])

    def test_keeps_the_installed_version_when_the_copy_fails(self):
        self.release()
        self.preinstall(fake_ddtest("v1.0.0"))
        # A cp that fails partway, as on a full disk.
        tools = self.root / "tools"
        tools.mkdir()
        (tools / "cp").write_text('#!/bin/sh\nprintf partial > "$2"\nexit 1\n')
        (tools / "cp").chmod(0o755)
        result = self.install(path=f"{tools}{os.pathsep}{os.environ['PATH']}")
        self.assertNotEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.dir / "ddtest").read_text(), fake_ddtest("v1.0.0"))
        self.assertEqual(sorted(p.name for p in self.dir.iterdir()), ["ddtest"])

    def test_keeps_the_installed_version_when_chmod_fails(self):
        self.release()
        self.preinstall(fake_ddtest("v1.0.0"))
        tools = self.root / "tools"
        tools.mkdir()
        (tools / "chmod").write_text("#!/bin/sh\nexit 1\n")
        (tools / "chmod").chmod(0o755)
        result = self.install(path=f"{tools}{os.pathsep}{os.environ['PATH']}")
        self.assertNotEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.dir / "ddtest").read_text(), fake_ddtest("v1.0.0"))
        self.assertEqual(sorted(p.name for p in self.dir.iterdir()), ["ddtest"])

    def test_removes_its_copy_when_terminated(self):
        self.release()
        self.preinstall(fake_ddtest("v1.0.0"))
        # The runner cancels the job while the copy beside the target runs.
        tools = self.root / "tools"
        tools.mkdir()
        (tools / "cp").write_text('#!/bin/sh\nprintf partial > "$2"\nkill -TERM "$PPID"\n')
        (tools / "cp").chmod(0o755)
        result = self.install(path=f"{tools}{os.pathsep}{os.environ['PATH']}")
        self.assertNotEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.dir / "ddtest").read_text(), fake_ddtest("v1.0.0"))
        self.assertEqual(sorted(p.name for p in self.dir.iterdir()), ["ddtest"])

    def test_hides_the_download_url_credentials(self):
        url = self.url.replace("http://", "http://reader:secret@") + "/missing?token=sensitive"
        result = self.install(DDTEST_INSTALL_DOWNLOAD_URL=url)
        self.assertNotEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"no SHA256SUMS at {self.url}/missing", result.stderr)
        self.assertNotIn("secret", result.stderr)
        self.assertNotIn("sensitive", result.stderr)

    def other_ddtest(self):
        # An older executable in another directory, as go install can leave one.
        other = self.root / "other"
        other.mkdir()
        executable = other / "ddtest"
        executable.write_text(fake_ddtest("v0.0.1"))
        executable.chmod(0o755)
        return other

    def test_warns_when_another_ddtest_comes_first_on_path(self):
        other = self.other_ddtest()
        self.release()
        result = self.install(path=f"{other}{os.pathsep}{self.dir}{os.pathsep}{os.environ['PATH']}")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"another ddtest comes first on PATH, {other / 'ddtest'}: remove it, or put {self.dir} before its directory", result.stderr)

    def test_does_not_warn_when_it_comes_first_on_path(self):
        other = self.other_ddtest()
        self.release()
        result = self.install(path=f"{self.dir}{os.pathsep}{other}{os.pathsep}{os.environ['PATH']}")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("another ddtest", result.stderr)

    def test_adds_the_directory_to_github_path(self):
        self.release()
        github_path = self.root / "github_path"
        github_path.write_text("")
        result = self.install(github_path=github_path)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(github_path.read_text().split(), [str(self.dir.resolve())])

    def test_adds_the_directory_to_github_path_when_this_step_has_it(self):
        # A step that exported the directory itself: the next steps need GITHUB_PATH.
        self.release()
        github_path = self.root / "github_path"
        github_path.write_text("")
        result = self.install(github_path=github_path, path=f"{self.dir}{os.pathsep}{os.environ['PATH']}")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(github_path.read_text().split(), [str(self.dir.resolve())])



# GitHub's runners have pwsh, so CI runs these; elsewhere they need it on PATH.
@unittest.skipUnless(shutil.which("pwsh") or os.environ.get("GITHUB_ACTIONS") == "true", "pwsh is not installed")
class PowerShellInstallTests(InstallTests):
    """The same cases for install.ps1, whose parameters are -Version and -Dir."""

    def command(self, *args):
        names = {"--version": "-Version", "--dir": "-Dir"}
        return ["pwsh", "-NoProfile", "-NonInteractive", "-File", str(SCRIPTS / "install.ps1"),
                *(names.get(arg, arg) for arg in args)]

    def test_keeps_the_installed_version_when_the_copy_fails(self):
        self.skipTest("Copy-Item does not run cp; install.ps1 moves its copy over the target the same way")

    def test_removes_its_copy_when_terminated(self):
        self.skipTest("Copy-Item does not run cp; install.ps1 removes its copy in a finally block")


if __name__ == "__main__":
    unittest.main()
