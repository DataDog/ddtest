"""Exercise the PR publisher against a local Git remote and a fake gh CLI."""

import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("open_toolchain_pr.py")


class OpenToolchainPRTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.remote = self.root / "remote.git"
        self.git(self.root, "init", "--bare", str(self.remote))
        self.git(self.remote, "symbolic-ref", "HEAD", "refs/heads/main")

        self.checkout = self.root / "checkout"
        self.checkout.mkdir()
        self.git(self.checkout, "init", "-b", "main")
        self.git(self.checkout, "config", "user.name", "Test User")
        self.git(self.checkout, "config", "user.email", "test@example.com")
        self.git(self.checkout, "config", "commit.gpgsign", "false")
        self.git(self.checkout, "remote", "add", "origin", str(self.remote))
        (self.checkout / "go.mod").write_text("module example.com/test\n\ngo 1.27.1\n")
        (self.checkout / ".golangci-lint-version").write_text("v2.13.2\n")
        (self.checkout / "unrelated.txt").write_text("keep me out of the PR\n")
        self.git(self.checkout, "add", ".")
        self.git(self.checkout, "commit", "-m", "base")
        self.git(self.checkout, "push", "origin", "main")

        fake_bin = self.root / "bin"
        fake_bin.mkdir()
        fake_gh = fake_bin / "gh"
        fake_gh.write_text("""#!/bin/sh
printf '%s\\n' "$*" >> "$GH_CALLS"
if [ "$1" = pr ] && [ "$2" = list ]; then
  printf '%s' "${GH_PR_NUMBER:-}"
fi
if [ "$1" = pr ] && { [ "$2" = create ] || [ "$2" = edit ]; }; then
  for last_arg do :; done
  cat "$last_arg" > "$GH_BODY"
fi
""")
        fake_gh.chmod(0o755)
        self.env = dict(os.environ)
        self.env.update({
            "PATH": f"{fake_bin}:{os.environ['PATH']}",
            "GITHUB_REPOSITORY": "DataDog/ddtest",
            "GH_TOKEN": "test-token",
            "GH_CALLS": str(self.root / "gh-calls"),
            "GH_BODY": str(self.root / "gh-body"),
        })

    def git(self, directory, *args):
        return subprocess.run(["git", *args], cwd=directory, check=True,
                              text=True, capture_output=True).stdout

    def publish(self, directory=None):
        subprocess.run([sys.executable, str(SCRIPT)], cwd=directory or self.checkout,
                       env=self.env, check=True, text=True, capture_output=True)

    def test_creates_pr_with_only_toolchain_changes(self):
        (self.checkout / "go.mod").write_text("module example.com/test\n\ngo 1.27.2\n")
        (self.checkout / "unrelated.txt").write_text("uncommitted change\n")
        self.publish()

        ref = "refs/heads/automation/go-toolchain"
        self.assertIn("go 1.27.2", self.git(self.remote, "show", f"{ref}:go.mod"))
        self.assertEqual("keep me out of the PR\n",
                         self.git(self.remote, "show", f"{ref}:unrelated.txt"))
        calls = (self.root / "gh-calls").read_text()
        self.assertIn("pr create --repo DataDog/ddtest --base main", calls)
        self.assertIn("## E2E testing", (self.root / "gh-body").read_text())

    def test_updates_existing_pr_branch(self):
        (self.checkout / "go.mod").write_text("module example.com/test\n\ngo 1.27.2\n")
        self.publish()

        second = self.root / "second"
        self.git(self.root, "clone", str(self.remote), str(second))
        (second / "go.mod").write_text("module example.com/test\n\ngo 1.27.2\n")
        (second / ".golangci-lint-version").write_text("v2.13.3\n")
        self.env["GH_PR_NUMBER"] = "123"
        self.publish(second)

        ref = "refs/heads/automation/go-toolchain"
        self.assertEqual("v2.13.3\n",
                         self.git(self.remote, "show", f"{ref}:.golangci-lint-version"))
        self.assertIn("pr edit 123 --repo DataDog/ddtest",
                      (self.root / "gh-calls").read_text())

    def test_does_nothing_when_pins_are_unchanged(self):
        self.publish()
        self.assertFalse((self.root / "gh-calls").exists())
        self.assertEqual("", self.git(self.remote, "branch", "--list", "automation/go-toolchain"))


if __name__ == "__main__":
    unittest.main()
