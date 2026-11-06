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

    def test_requested_model_is_part_of_the_frozen_inputs(self):
        case = run_catalog.load_catalog()['cases'][0]
        first = eval_score.oracle([case], ['codex'], False, model='model-one')
        second = eval_score.oracle([case], ['codex'], False, model='model-two')
        self.assertNotEqual(first['sha256'], second['sha256'])
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'oracle.json'
            eval_score.freeze(path, first)
            with self.assertRaises(ValueError):
                eval_score.freeze(path, second, evaluate_only=True)


    def test_intake_guard_change_requires_a_new_frozen_run(self):
        case = run_catalog.load_catalog()['cases'][0]
        before = eval_score.oracle([case], ['codex'], False)
        after = eval_score.oracle([case], ['codex'], False, intake_guard='us1-defaults-v1')
        self.assertNotEqual(before['sha256'], after['sha256'])
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'oracle.json'
            eval_score.freeze(path, before)
            with self.assertRaises(ValueError):
                eval_score.freeze(path, after)


    def test_single_trial_reevaluation_keeps_recorded_guard_identity(self):
        from test_catalog_check import write_evidence, real_report
        case = next(c for c in run_catalog.load_catalog()['cases'] if c['id']=='E02-pipeline-schema')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            write_evidence(root/(case['id']+'-codex'), testdrive=real_report('E02'),
                           run={'isolation': {'cleanup': 'removed', 'intake_guard': 'us1-defaults-v1'}})
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(run_catalog.main([case['id'],'--evaluate-only','--output',str(root)]),0)
            result=json.loads((root/'results.json').read_text())
            expected=eval_score.oracle([case],['codex'],False,intake_guard='us1-defaults-v1')
            self.assertEqual(result['oracle_sha256'],expected['sha256'])


    def test_reevaluation_infers_agents_and_keeps_missing_trials_in_the_score(self):
        from test_catalog_check import write_evidence, real_report
        case = next(c for c in run_catalog.load_catalog()['cases'] if c['id'] == 'E02-pipeline-schema')
        image = 'sha256:' + 'a' * 64
        for agents in [('claude',), ('codex', 'claude')]:
            for missing in (False, True):
                with self.subTest(agents=agents, missing=missing), tempfile.TemporaryDirectory() as directory:
                    root = Path(directory)
                    oracle = eval_score.oracle([{**case, 'image': image}], agents, False)
                    eval_score.freeze(root / 'oracle.json', oracle)
                    for trial in range(1, 4):
                        for agent in agents:
                            if missing and trial == 3 and agent == 'claude':
                                continue
                            write_evidence(root / f"{case['id']}-trial-{trial:02d}-{agent}", testdrive=real_report('E02'))
                    with contextlib.redirect_stdout(io.StringIO()):
                        code = run_catalog.main([case['id'], '--evaluate-only', '--trials', '3',
                                                 '--image-map', case['image'] + '=' + image, '--output', str(root)])
                    result = json.loads((root / 'results.json').read_text())
                    self.assertEqual(code, 1 if missing else 0)
                    self.assertEqual(result['oracle_sha256'], oracle['sha256'])
                    self.assertEqual(result['score']['trials'], 3 * len(agents))
                    self.assertEqual({row['agent'] for row in result['score']['cases']}, set(agents))
                    self.assertEqual(len(result['not_evaluated']), int(missing))
                    self.assertEqual(json.loads((root / 'oracle.json').read_text()), oracle)

    def test_single_claude_trial_has_claude_oracle_identity(self):
        from test_catalog_check import write_evidence, real_report
        case = next(c for c in run_catalog.load_catalog()['cases'] if c['id'] == 'E02-pipeline-schema')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            write_evidence(root / (case['id'] + '-claude'), testdrive=real_report('E02'))
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(run_catalog.main([case['id'], '--evaluate-only', '--output', str(root)]), 0)
            result = json.loads((root / 'results.json').read_text())
            self.assertEqual(result['oracle_sha256'], eval_score.oracle([case], ['claude'], False)['sha256'])
            self.assertEqual({row['agent'] for row in result['score']['cases']}, {'claude'})


    def test_reevaluation_discards_stale_passes_when_trial_evidence_disappears(self):
        from test_catalog_check import write_evidence, real_report
        case = next(c for c in run_catalog.load_catalog()['cases'] if c['id'] == 'E02-pipeline-schema')
        image = 'sha256:' + 'a' * 64
        for agents in [('claude',), ('codex', 'claude')]:
            with self.subTest(agents=agents), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                oracle = eval_score.oracle([{**case, 'image': image}], agents, False)
                eval_score.freeze(root / 'oracle.json', oracle)
                for trial in range(1, 4):
                    for agent in agents:
                        write_evidence(root / f"{case['id']}-trial-{trial:02d}-{agent}", testdrive=real_report('E02'))
                argv = [case['id'], '--evaluate-only', '--trials', '3', '--image-map',
                        case['image'] + '=' + image, '--output', str(root)]
                with contextlib.redirect_stdout(io.StringIO()):
                    self.assertEqual(run_catalog.main(argv), 0)
                baseline = root / 'baseline.json'
                baseline.write_text((root / 'results.json').read_text())
                missing_id = case['id'] + '-trial-03'
                (root / (missing_id + '-claude')).rename(root / 'removed-evidence')
                with contextlib.redirect_stdout(io.StringIO()):
                    self.assertEqual(run_catalog.main([*argv, '--compare-results', str(baseline)]), 1)
                result = json.loads((root / 'results.json').read_text())
                total = 3 * len(agents)
                self.assertEqual(result['score']['trials'], total)
                self.assertEqual(len(result['cases']), total - 1)
                for metric in ('expected_pass_rate', 'rigorous_pass_rate'):
                    self.assertEqual(result['score'][metric], (total - 1) / total)
                    self.assertAlmostEqual(result['comparison'][metric + '_delta'], -1 / total)
                self.assertEqual([(row['id'], row['agent']) for row in result['not_evaluated']],
                                 [(missing_id, 'claude')])
                self.assertEqual(result['merged_entries_kept'], 0)
                self.assertEqual(result['oracle_sha256'], oracle['sha256'])

                # A named selection replaces only its slots, preserving earlier cases.
                unrelated = {**result['cases'][0], 'id': 'unselected-case', 'catalog_id': 'unselected-case'}
                stale = next(row for row in json.loads(baseline.read_text())['cases']
                             if (row['id'], row['agent']) == (missing_id, 'claude'))
                result['cases'].extend([unrelated, stale])
                (root / 'results.json').write_text(json.dumps(result))
                with contextlib.redirect_stdout(io.StringIO()):
                    self.assertEqual(run_catalog.main(argv), 1)
                result = json.loads((root / 'results.json').read_text())
                self.assertIn(unrelated, result['cases'])
                self.assertNotIn(stale, result['cases'])
                self.assertEqual(result['merged_entries_kept'], 1)


    def test_frozen_score_excludes_unselected_merged_records(self):
        from test_catalog_check import write_evidence, real_report
        case = next(c for c in run_catalog.load_catalog()['cases'] if c['id'] == 'E02-pipeline-schema')
        image = 'sha256:' + 'a' * 64
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            oracle = eval_score.oracle([{**case, 'image': image}], ['codex'], False)
            eval_score.freeze(root / 'oracle.json', oracle)
            for trial in range(1, 4):
                write_evidence(root / f"{case['id']}-trial-{trial:02d}-codex", testdrive=real_report('E02'))
            argv = [case['id'], '--evaluate-only', '--trials', '3', '--output', str(root),
                    '--image-map', case['image'] + '=' + image]
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(run_catalog.main(argv), 0)
            baseline = root / 'baseline.json'
            baseline.write_text((root / 'results.json').read_text())
            original = json.loads(baseline.read_text())
            sample = original['cases'][0]
            extras = [{**sample, 'id': identifier, 'catalog_id': catalog, 'agent': agent,
                       'outcome': 'agent_failure', 'trajectory': {'status': 'review'}}
                      for identifier, catalog, agent in [(case['id'], case['id'], 'codex'),
                          ('unrelated-case-trial-01', 'unrelated-case', 'codex'),
                          (sample['id'], case['id'], 'claude')]]
            original['cases'].extend(extras)
            (root / 'results.json').write_text(json.dumps(original))
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(run_catalog.main(argv), 0)
            result = json.loads((root / 'results.json').read_text())
            self.assertEqual(result['score']['trials'], 3)
            self.assertEqual(result['score']['expected_pass_rate'], 1)
            self.assertEqual(result['score']['rigorous_pass_rate'], 1)
            self.assertEqual(result['merged_entries_kept'], len(extras))
            self.assertTrue(all(extra in result['cases'] for extra in extras))
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(run_catalog.main([*argv, '--compare-results', str(baseline)]), 0)
            result = json.loads((root / 'results.json').read_text())
            self.assertEqual(result['comparison']['expected_pass_rate_delta'], 0)
            self.assertEqual(result['comparison']['rigorous_pass_rate_delta'], 0)
