"""Prepared case images and dependency caches retain their separate contracts."""
import contextlib
import io
import json
from pathlib import Path
import shlex
import sys
import tempfile
from types import SimpleNamespace
import unittest

import dependency_cache
import eval_score
import run_catalog
import run_eval


class CatalogImageModesTests(unittest.TestCase):
    def setUp(self):
        self.case = next(case for case in run_catalog.load_catalog()['cases'] if case['id'].startswith('E02-'))
        self.base = 'sha256:' + 'a' * 64
        self.cached = 'sha256:' + 'b' * 64

    def main(self, *argv):
        output = io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(io.StringIO()):
            code = run_catalog.main(list(argv))
        return code, output.getvalue()

    def test_repeated_trials_keep_the_cache_image_and_original_recipe(self):
        code, output = self.main('E02', '--dry-run', '--trials', '3', '--agent', 'both',
                                 '--image-map', self.case['image'] + '=' + self.base,
                                 '--dependency-image', 'E02=' + self.cached)
        self.assertEqual(code, 0)
        trials = [shlex.split(line) for line in output.splitlines() if 'run_eval.py' in line and '--cleanup' not in line]
        self.assertEqual(len(trials), 6)
        for trial in trials:
            self.assertEqual(trial[trial.index('--image') + 1], self.cached)
            self.assertEqual(trial[trial.index('--dependency-cache') + 1], dependency_cache.fingerprint(self.case, self.base))
            self.assertIn('--setup-command', trial)
            self.assertNotIn('--case-image', trial)

    def test_expanded_trials_still_require_the_original_base_for_caches(self):
        with self.assertRaises(SystemExit) as error:
            self.main('E02', '--dry-run', '--trials', '3', '--dependency-image', 'E02=' + self.cached)
        self.assertEqual(error.exception.code, 2)

    def test_case_images_cannot_be_combined_with_dependency_images(self):
        with self.assertRaises(SystemExit) as error:
            self.main('E02', '--dry-run', '--case-image', '--dependency-image', 'E02=' + self.cached)
        self.assertEqual(error.exception.code, 2)

    def test_cache_image_is_bound_to_the_repeated_trial_oracle(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            expected = eval_score.oracle([{**self.case, 'image': self.cached}], ['codex'], False, trials=3)
            eval_score.freeze(root / 'oracle.json', expected)
            code, _ = self.main('E02', '--evaluate-only', '--trials', '3', '--output', str(root),
                                '--image-map', self.case['image'] + '=' + self.base,
                                '--dependency-image', 'E02=' + self.cached)
            self.assertEqual(code, 1)  # No completed evidence, but the frozen oracle must agree.
            self.assertEqual(json.loads((root / 'results.json').read_text())['oracle_sha256'], expected['sha256'])

    def test_harness_refuses_cache_modes_with_prepared_cases_before_creating_a_trial(self):
        for option in ('prepare_cache', 'dependency_cache'):
            with self.subTest(option=option), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / 'evidence'
                args = SimpleNamespace(agent='codex', timeout=30, ref='a' * 40, image=self.base,
                                       ddtest=Path(sys.executable), framework='jest', case_image=True,
                                       setup_command=[], output=output, **{option: 'a' * 64})
                with self.assertRaisesRegex(ValueError, 'cannot use dependency-cache'):
                    run_eval.run(args)
                self.assertFalse(output.exists())
