import contextlib
import copy
import hashlib
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
            oracle = eval_score.oracle([{**case, 'image': image}], ['codex', 'claude'], False, trials=3)
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


    def test_frozen_reevaluation_rejects_mismatched_trial_provenance(self):
        from test_catalog_check import write_evidence, real_report
        case = next(c for c in run_catalog.load_catalog()['cases'] if c['id'] == 'E02-pipeline-schema')
        image = 'sha256:' + 'a' * 64
        run = {'repository': case['repo'], 'commit': case['commit'], 'agent': 'codex',
               'requested_model': 'model-one', 'case_image': False,
               'isolation': {'cleanup': 'removed', 'image': image, 'intake_guard': 'us1-defaults-v1'}}
        fields = ('repository', 'commit', 'image', 'intake_guard', 'agent', 'requested_model', 'case_image',
                  'missing_commit', 'missing_isolation', 'missing_agent', 'missing_requested_model', 'missing_default_model', 'missing_case_image')
        for field in fields:
            with self.subTest(field=field), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                model = None if field == 'missing_default_model' else 'model-one'
                oracle = eval_score.oracle([{**case, 'image': image}], ['codex'], False,
                                           model=model, intake_guard='us1-defaults-v1')
                eval_score.freeze(root / 'oracle.json', oracle)
                evidence = write_evidence(root / (case['id'] + '-codex'), run={**run, 'requested_model': model}, testdrive=real_report('E02'))
                argv = [case['id'], '--evaluate-only', '--image-map', case['image'] + '=' + image,
                        '--output', str(root)]
                if model is not None:
                    argv += ['--model', model]
                with contextlib.redirect_stdout(io.StringIO()):
                    self.assertEqual(run_catalog.main(argv), 0)
                altered = json.loads((evidence / 'run.json').read_text())
                if field == 'missing_default_model':
                    del altered['requested_model']
                elif field.startswith('missing_'):
                    del altered[field.removeprefix('missing_')]
                elif field in ('image', 'intake_guard'):
                    altered['isolation'][field] = 'sha256:' + 'b' * 64 if field == 'image' else None
                else:
                    altered[field] = {'repository': 'https://example.invalid/other.git', 'commit': 'b' * 40,
                                      'agent': 'claude', 'requested_model': 'model-two', 'case_image': True}[field]
                (evidence / 'run.json').write_text(json.dumps(altered))
                inputs = {path: path.read_bytes() for path in (root / 'oracle.json', root / 'results.json', evidence / 'run.json')}
                errors = io.StringIO()
                with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(errors):
                    with self.assertRaises(SystemExit) as rejected:
                        run_catalog.main(argv)
                self.assertEqual(rejected.exception.code, 2)
                self.assertIn('provenance', errors.getvalue())
                self.assertTrue(all(path.read_bytes() == value for path, value in inputs.items()))

    def test_prepared_image_mode_cannot_change_under_the_same_frozen_image(self):
        from test_catalog_check import write_evidence, real_report
        case = next(c for c in run_catalog.load_catalog()['cases'] if c['id'] == 'E02-pipeline-schema')
        image = 'sha256:' + 'a' * 64
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            oracle = eval_score.oracle([{**case, 'image': image}], ['codex'], False, trials=3)
            eval_score.freeze(root / 'oracle.json', oracle)
            for trial in range(1, 4):
                write_evidence(root / f"{case['id']}-trial-{trial:02d}-codex", testdrive=real_report('E02'),
                               run={'repository': case['repo'], 'commit': case['commit'], 'agent': 'codex',
                                    'requested_model': None, 'case_image': False,
                                    'isolation': {'cleanup': 'removed', 'image': image}})
            argv = [case['id'], '--evaluate-only', '--trials', '3', '--output', str(root),
                    '--image-map', case['image'] + '=' + image, '--image-map', case['id'] + '=' + image]
            with contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(run_catalog.main(argv), 0)
            baseline = root / 'baseline.json'
            baseline.write_bytes((root / 'results.json').read_bytes())
            frozen = {path: path.read_bytes() for path in (root / 'oracle.json', root / 'results.json')}
            for compare in (False, True):
                with self.subTest(compare=compare), contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
                    changed = [*argv, '--case-image']
                    if compare:
                        changed += ['--compare-results', str(baseline)]
                    with self.assertRaises(SystemExit) as rejected:
                        run_catalog.main(changed)
                    self.assertEqual(rejected.exception.code, 2)
                    self.assertTrue(all(path.read_bytes() == value for path, value in frozen.items()))

    def test_frozen_reevaluation_keeps_matching_historical_provenance(self):
        from test_catalog_check import write_evidence, real_report
        case = next(c for c in run_catalog.load_catalog()['cases'] if c['id'] == 'E02-pipeline-schema')
        image = 'sha256:' + 'a' * 64
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            oracle = eval_score.oracle([{**case, 'image': image}], ['codex'], False,
                                       intake_guard='us1-defaults-v1', trials=3)
            eval_score.freeze(root / 'oracle.json', oracle)
            for trial in range(1, 4):
                write_evidence(root / f"{case['id']}-trial-{trial:02d}-codex", testdrive=real_report('E02'),
                               run={'repository': case['repo'], 'commit': case['commit'], 'agent': 'codex',
                                    'requested_model': None, 'case_image': False, 'isolation': {'cleanup': 'removed', 'image': image,
                                                  'intake_guard': 'us1-defaults-v1'}})
            with contextlib.redirect_stdout(io.StringIO()):
                code = run_catalog.main([case['id'], '--evaluate-only', '--trials', '3', '--image-map',
                                         case['image'] + '=' + image, '--output', str(root)])
            self.assertEqual(code, 0)
            self.assertEqual(json.loads((root / 'results.json').read_text())['score']['rigorous_pass_rate'], 1)
            self.assertEqual(json.loads((root / 'oracle.json').read_text()), oracle)

    def test_reevaluation_infers_agents_and_keeps_missing_trials_in_the_score(self):
        from test_catalog_check import write_evidence, real_report
        case = next(c for c in run_catalog.load_catalog()['cases'] if c['id'] == 'E02-pipeline-schema')
        image = 'sha256:' + 'a' * 64
        for agents in [('claude',), ('codex', 'claude')]:
            for missing in (False, True):
                with self.subTest(agents=agents, missing=missing), tempfile.TemporaryDirectory() as directory:
                    root = Path(directory)
                    oracle = eval_score.oracle([{**case, 'image': image}], agents, False, trials=3)
                    eval_score.freeze(root / 'oracle.json', oracle)
                    for trial in range(1, 4):
                        for agent in agents:
                            if missing and trial == 3 and agent == 'claude':
                                continue
                            write_evidence(root / f"{case['id']}-trial-{trial:02d}-{agent}", testdrive=real_report('E02'), run={'repository': case['repo'], 'commit': case['commit'], 'agent': agent, 'requested_model': None, 'case_image': False, 'isolation': {'cleanup': 'removed', 'image': image}})
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

    def test_malformed_trajectory_review_does_not_abort_batch_reevaluation(self):
        from test_catalog_check import write_evidence, real_report
        from test_trajectory_check import PATCH
        import trajectory_check
        case = next(c for c in run_catalog.load_catalog()['cases'] if c['id'] == 'E02-pipeline-schema')
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for agent in ('codex', 'claude'):
                evidence = root / (case['id'] + '-' + agent)
                write_evidence(evidence, testdrive=real_report('E02'))
                if agent == 'codex':
                    (evidence / 'changes.patch').write_text(PATCH)
                    (evidence / 'status.txt').write_text(' M ci.yml\n')
                    review = trajectory_check.evaluate(evidence)
                    (evidence / 'trajectory-review.json').write_text(json.dumps({
                        'patch_sha256': review['patch_sha256'],
                        'prescriptions_sha256': review['prescriptions_sha256'], 'hunks': None}))
            with contextlib.redirect_stdout(io.StringIO()):
                run_catalog.main([case['id'], '--evaluate-only', '--agent', 'both', '--output', str(root)])
            result = json.loads((root / 'results.json').read_text())
            self.assertEqual(len(result['cases']), 2)
            self.assertEqual(next(r for r in result['cases'] if r['agent'] == 'codex')['trajectory']['status'], 'review')

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
                oracle = eval_score.oracle([{**case, 'image': image}], agents, False, trials=3)
                eval_score.freeze(root / 'oracle.json', oracle)
                for trial in range(1, 4):
                    for agent in agents:
                        write_evidence(root / f"{case['id']}-trial-{trial:02d}-{agent}", testdrive=real_report('E02'), run={'repository': case['repo'], 'commit': case['commit'], 'agent': agent, 'requested_model': None, 'case_image': False, 'isolation': {'cleanup': 'removed', 'image': image}})
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
            oracle = eval_score.oracle([{**case, 'image': image}], ['codex'], False, trials=3)
            eval_score.freeze(root / 'oracle.json', oracle)
            for trial in range(1, 4):
                write_evidence(root / f"{case['id']}-trial-{trial:02d}-codex", testdrive=real_report('E02'), run={'repository': case['repo'], 'commit': case['commit'], 'agent': 'codex', 'requested_model': None, 'case_image': False, 'isolation': {'cleanup': 'removed', 'image': image}})
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


    def test_repeated_plan_cannot_be_resized_after_freezing(self):
        from unittest.mock import patch
        from test_catalog_check import write_evidence, real_report
        case = next(c for c in run_catalog.load_catalog()['cases'] if c['id'] == 'E02-pipeline-schema')
        image = 'sha256:' + 'a' * 64
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for trial in range(1, 4):
                write_evidence(root / f"{case['id']}-trial-{trial:02d}-codex", testdrive=real_report('E02'), run={'repository': case['repo'], 'commit': case['commit'], 'agent': 'codex', 'requested_model': None, 'case_image': False, 'isolation': {'cleanup': 'removed', 'image': image}})
            argv = [case['id'], '--evaluate-only', '--agent', 'codex', '--trials', '3',
                    '--output', str(root), '--image-map', case['image'] + '=' + image]
            freeze = eval_score.freeze
            # Seed a pre-model oracle for this synthetic evidence without a model run.
            with patch.object(eval_score, 'freeze', side_effect=lambda path, document, evaluate_only=False: freeze(path, document)), contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(run_catalog.main(argv), 0)
            original = (root / 'oracle.json').read_bytes()
            baseline = root / 'baseline.json'
            baseline.write_bytes((root / 'results.json').read_bytes())
            for trials in (2, 5, 1):
                for compare in (False, True):
                    with self.subTest(trials=trials, compare=compare):
                        changed = list(argv)
                        changed[changed.index('--trials') + 1] = str(trials)
                        if compare:
                            changed += ['--compare-results', str(baseline)]
                        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as raised:
                            run_catalog.main(changed)
                        self.assertEqual(raised.exception.code, 2)
                        self.assertEqual((root / 'oracle.json').read_bytes(), original)
                        self.assertEqual((root / 'results.json').read_bytes(), baseline.read_bytes())


    def test_trial_count_changes_oracle_identity(self):
        case = run_catalog.load_catalog()['cases'][0]
        documents = [eval_score.oracle([case], ['codex'], False, trials=count) for count in (1, 2, 3, 5)]
        self.assertEqual(len({document['sha256'] for document in documents}), 4)
        self.assertEqual([document['trials_per_case_agent'] for document in documents], [1, 2, 3, 5])
        self.assertTrue(all(document['schema_version'] == 3 for document in documents))
        with self.assertRaises(ValueError):
            eval_score.oracle([case], ['codex'], False, trials=0)

    def test_legacy_oracle_is_preserved_without_inventing_preparation_mode(self):
        case = run_catalog.load_catalog()['cases'][0]
        document = eval_score.oracle([case], ['codex'], False, trials=3)
        legacy = {key: value for key, value in document.items() if key not in ('case_image', 'sha256')}
        legacy['schema_version'] = 2
        legacy['sha256'] = hashlib.sha256(json.dumps(legacy, sort_keys=True, separators=(',', ':')).encode()).hexdigest()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'oracle.json'
            path.write_text(json.dumps(legacy))
            original = path.read_bytes()
            with self.assertRaisesRegex(ValueError, 'predates preparation mode'):
                eval_score.freeze(path, document, evaluate_only=True)
            self.assertEqual(path.read_bytes(), original)

    def test_legacy_oracle_is_preserved_without_inventing_a_trial_count(self):
        case = run_catalog.load_catalog()['cases'][0]
        document = eval_score.oracle([case], ['codex'], False, trials=3)
        legacy = {key: value for key, value in document.items() if key != 'trials_per_case_agent'}
        legacy['schema_version'] = 1
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'oracle.json'
            path.write_text(json.dumps(legacy))
            original = path.read_bytes()
            with self.assertRaisesRegex(ValueError, 'predates planned trial counts'):
                eval_score.freeze(path, document, evaluate_only=True)
            self.assertEqual(path.read_bytes(), original)
