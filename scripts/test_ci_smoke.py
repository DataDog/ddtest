"""A selected smoke must execute its named cases, not pass after filtering them."""

import io
import subprocess
import unittest
from unittest.mock import patch

import ci_smoke


class SmokeTests(unittest.TestCase):
    def test_missing_skipped_or_filtered_cases_cannot_pass(self):
        for passed in (set(), {"TestSmoke"}, {"TestSmoke/other"}):
            with self.assertRaises(ValueError):
                ci_smoke.require_cases(passed, ["TestSmoke/passing"])
        ci_smoke.require_cases({"TestSmoke/passing"}, ["TestSmoke/passing"])

    def test_json_exit_and_required_case_both_gate_success(self):
        class Process:
            returncode = 0
            stdout = io.StringIO('{"Action":"pass","Test":"TestSmoke/passing"}\n')
            def __enter__(self): return self
            def __exit__(self, *args): pass
            def wait(self): return self.returncode
        with patch.object(ci_smoke.subprocess, "Popen", return_value=Process()):
            ci_smoke.run_tests(["go"], {}, ["TestSmoke/passing"])
        process = Process()
        process.returncode = 1
        with patch.object(ci_smoke.subprocess, "Popen", return_value=process):
            with self.assertRaises(subprocess.CalledProcessError):
                ci_smoke.run_tests(["go"], {}, ["TestSmoke/passing"])


if __name__ == "__main__":
    unittest.main()
