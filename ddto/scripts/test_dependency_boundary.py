import contextlib
import io
import json
import subprocess
import sys
import unittest
from unittest.mock import patch

from dependency_boundary import MODULE, main, packages_from_json, violations


class DependencyBoundaryTests(unittest.TestCase):
    def test_accepts_standard_library_and_own_packages(self):
        module = {"Module": {"Path": MODULE}, "Require": None}
        packages = [{"Standard": True, "ImportPath": "sync"},
                    {"ImportPath": MODULE + "/testopt", "Module": {"Path": MODULE}}]
        self.assertEqual(violations(module, packages), [])

    def test_rejects_unused_requirements_and_external_packages(self):
        module = {"Module": {"Path": MODULE}, "Require": [{"Path": "example.com/test-only"}]}
        packages = [{"ImportPath": "example.com/runtime", "Module": {"Path": "example.com/runtime"}}]
        errors = violations(module, packages)
        self.assertEqual(len(errors), 2)
        self.assertIn("test-only", errors[0])
        self.assertIn("example.com/runtime", errors[1])

    def test_rejects_wrong_root_and_missing_module_identity(self):
        self.assertEqual(len(violations({"Module": {"Path": "wrong"}}, [{"ImportPath": MODULE + "/testopt"}])), 2)

    def test_decodes_go_json_as_utf8_with_windows_default_encoding(self):
        module = {"Module": {"Path": MODULE}, "Require": None}
        graph = {"ImportPath": MODULE + "/testopt",
                 "Module": {"Path": MODULE, "Dir": "C:/workspace/José”"}}

        def go_output(args, **kwargs):
            document = module if args[1] == "mod" else graph
            encoded = json.dumps(document, ensure_ascii=False).encode("utf-8")
            command = [sys.executable, "-c",
                       "import sys;sys.stdout.buffer.write(" + repr(encoded) + ")"]
            return subprocess.run(command, stdout=subprocess.PIPE, check=True, **kwargs).stdout

        # Exercise actual subprocess decoding: without an explicit encoding,
        # Windows' default cannot decode the closing quote's UTF-8 bytes.
        with patch("dependency_boundary.subprocess.check_output", side_effect=go_output):
            with patch("subprocess._text_encoding", return_value="cp1252"):
                with contextlib.redirect_stdout(io.StringIO()):
                    self.assertEqual(main(), 0)

    def test_reads_concatenated_go_list_json(self):
        rows = [{"Standard": True}, {"ImportPath": MODULE + "/testopt"}]
        self.assertEqual(list(packages_from_json("\n".join(map(json.dumps, rows)))), rows)


if __name__ == "__main__":
    unittest.main()
