import contextlib
import copy
import io
import json
from pathlib import Path
import tempfile
import unittest

import eval_score
import run_catalog


class ScoreTests(unittest.TestCase):
    def test_repetitions_have_separate_evidence_identities(self):
        case = run_catalog.load_catalog()['cases'][0]
        cases = eval_score.expand([case], 3)
        self.assertEqual(len({item['id'] for item in cases}), 3)
        self.assertTrue(all(item['catalog_id'] == case['id'] for item in cases))
        self.assertEqual([item['trial'] for item in cases], [1, 2, 3])
        self.assertEqual(eval_score.expand([case], 1)[0]['id'], case['id'])
        with self.assertRaises(ValueError):
            eval_score.expand([case], 0)

    def test_expected_and_prescription_checked_rates_are_separate(self):
        records = [{'id': f'E01-trial-{n}', 'catalog_id': 'E01', 'agent': 'codex', 'outcome': outcome,
                    'actual': {'verdict': verdict}, 'trajectory': {'status': trajectory}}
                   for n, (outcome, verdict, trajectory) in enumerate([
                       ('pass', 'FAILED', 'pass'), ('pass', 'COMPLETE LOCALLY', 'review'),
                       ('agent_failure', None, 'pass')])]
        score = eval_score.score(records)
        self.assertEqual(score['trials'], 3)
        self.assertEqual(score['expected_pass_rate'], 2 / 3)
        self.assertEqual(score['rigorous_pass_rate'], 1 / 3)
        self.assertEqual(score['cases'][0]['complete_locally'], 1)

    def test_frozen_oracle_cannot_be_changed_after_observing_results(self):
        case = run_catalog.load_catalog()['cases'][0]
        expected = eval_score.oracle([case], ['codex', 'claude'], False)
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / 'oracle.json'
            with self.assertRaises(ValueError):
                eval_score.freeze(path, expected, evaluate_only=True)
            eval_score.freeze(path, expected)
            eval_score.freeze(path, expected, evaluate_only=True)
            altered = copy.deepcopy(case)
            altered['expected']['verdict'] = 'FAILED' if case['expected']['verdict'] != 'FAILED' else 'COMPLETE LOCALLY'
            changed = eval_score.oracle([altered], ['codex', 'claude'], False)
            with self.assertRaises(ValueError):
                eval_score.freeze(path, changed)
            self.assertEqual(json.loads(path.read_text()), expected)

    def test_different_oracles_cannot_be_compared(self):
        score = eval_score.score([{'id': 'E01', 'agent': 'codex', 'outcome': 'pass', 'trajectory': {'status': 'pass'}}])
        old = {'oracle_sha256': 'same', 'ddtest_label': 'old', 'score': score}
        new = {**old, 'ddtest_label': 'new'}
        self.assertEqual(eval_score.compare(new, old)['rigorous_pass_rate_delta'], 0)
        with self.assertRaises(ValueError):
            eval_score.compare({**new, 'oracle_sha256': 'different'}, old)

    def test_absent_repeated_evidence_counts_every_planned_sample_and_fails(self):
        case = run_catalog.load_catalog()['cases'][0]
        image = 'sha256:' + 'a' * 64
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            oracle = eval_score.oracle([{**case, 'image': image}], ['codex', 'claude'], False)
            eval_score.freeze(root / 'oracle.json', oracle)
            with contextlib.redirect_stdout(io.StringIO()):
                code = run_catalog.main([case['id'], '--evaluate-only', '--agent', 'both', '--trials', '3',
                                         '--image-map', case['image'] + '=' + image, '--output', str(root)])
            result = json.loads((root / 'results.json').read_text())
            self.assertEqual(code, 1)
            self.assertEqual(result['score']['trials'], 6)
            self.assertEqual(result['score']['expected_pass_rate'], 0)
            self.assertEqual(len(result['score']['cases']), 2)
            self.assertTrue(all(group['case'] == case['id'] and group['trials'] == 3
                                for group in result['score']['cases']))
