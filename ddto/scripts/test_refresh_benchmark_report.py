"""Keep validation failures and all measured repetitions in refreshed reports."""
import copy
import tempfile
from pathlib import Path
import unittest
import refresh_benchmark_report as refresh


class RuntimeStatisticsTests(unittest.TestCase):
    def setUp(self):
        self.manifest={'cases':[{'id':'fixture'}],'cpus':[4]}
        self.rows=[]
        for variant in refresh.RUNTIME:
            for iteration in range(-1,5):
                self.rows.append(dict(case='fixture',cpus=4,variant=variant,iteration=iteration,
                    warmup=iteration==-1,wall_s=float(iteration+2),cpu_s=1.0,peak_bytes=1024,
                    validated_contract=True,validated_failure=None,
                    native_results=[['fixture.test','TestA','PASS',1]],
                    delivery={'event_counts':{'test':1},'coverage_items':0,'traffic':{}}))

    def test_warmup_is_excluded_and_slow_samples_are_kept(self):
        self.rows[0]['wall_s']=999.0
        summary=refresh.runtime_statistics(self.rows,self.manifest)
        native=summary['fixture/4']['native']
        self.assertEqual(native['wall_s']['median'],4.0)
        self.assertEqual(native['wall_s']['max'],6.0)
        self.assertEqual(native['all_wall_s'][0],999.0)

    def test_orchestrion_percentages_use_the_same_native_metric(self):
        for row in self.rows:
            if row['variant'] == 'orchestrion':
                row['wall_s'] *= 1.5
                row['peak_bytes'] *= 2
        cell = refresh.runtime_statistics(self.rows, self.manifest)['fixture/4']
        self.assertEqual(refresh.report.format_cell(cell, 'orchestrion', 'wall_s'),
                         '6.000000 s (+50.0%)')
        self.assertEqual(refresh.report.format_cell(cell, 'orchestrion', 'peak_bytes'),
                         '0.0 MiB (+100.0%)')
        cell['orchestrion'].update(valid=False, failed=1)
        self.assertEqual(refresh.report.format_cell(cell, 'orchestrion', 'wall_s'), 'FAIL 1/6')

    def test_one_failed_run_invalidates_the_whole_comparative_cell(self):
        row=next(r for r in self.rows if r['variant']=='mini' and r['iteration']==0)
        row.update(validated_contract=False,validated_failure='test failed')
        entry=refresh.runtime_statistics(self.rows,self.manifest)['fixture/4']['mini']
        self.assertFalse(entry['valid'])
        self.assertEqual(entry['failed'],1)
        self.assertNotIn('wall_s',entry)
        self.assertEqual(entry['observed_wall_s']['n'],5)

    def test_missing_reference_stays_unverified(self):
        row=next(r for r in self.rows if r['variant']=='mini' and r['iteration']==0)
        row.update(validated_contract=False,validated_failure='historical SDK reference unavailable for this combination')
        entry=refresh.runtime_statistics(self.rows,self.manifest)['fixture/4']['mini']
        self.assertEqual((entry['failed'],entry['unverified']),(0,1))
        self.assertNotIn('wall_s',entry)

    def test_incomplete_or_duplicate_groups_cannot_be_reported(self):
        for rows in (self.rows[:-1],self.rows+[copy.deepcopy(self.rows[0])]):
            with self.assertRaisesRegex(ValueError,'incomplete runtime group'):
                refresh.runtime_statistics(rows,self.manifest)


class CollectionIntegrityTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name)
        self.manifest = {'source_head': 'new', 'started_at': 'new-date',
                         'toolchain': 'go1.27.1', 'cases': [{'id': 'fixture'}], 'cpus': [4]}
        self.rows = [dict(case='fixture', cpus=4, variant=variant, iteration=i,
                         warmup=i == -1, source_head='new', validated_contract=True)
                     for variant in ('mini', 'mini-deferred') for i in range(-1, 5)]
        self.metadata = dict(source_head='new', toolchain='go1.27.1', commands=12,
                             variants=['mini', 'mini-deferred'], repetitions=5,
                             warmups=1, status='verified')
        refresh.build.write_json(self.path / 'manifest.json', self.metadata)

    def test_complete_collection_is_accepted(self):
        refresh.verify_collection(self.path, self.rows, self.manifest)

    def test_missing_extra_and_duplicate_runs_are_rejected(self):
        for rows in (self.rows[:-1], self.rows + [self.rows[0]],
                     self.rows[:-1] + [dict(self.rows[-1], case='other')]):
            with self.assertRaisesRegex(ValueError, 'runtime collection'):
                refresh.verify_collection(self.path, rows, self.manifest)

    def test_wrong_revision_and_warmup_are_rejected(self):
        for changes in ({'source_head': 'other'}, {'warmup': False}):
            rows = copy.deepcopy(self.rows)
            rows[0].update(changes)
            with self.assertRaisesRegex(ValueError, 'source revision or warmup'):
                refresh.verify_collection(self.path, rows, self.manifest)

    def test_interrupted_metadata_cannot_publish_completed_rows(self):
        self.metadata['failure'] = 'collection interrupted'
        refresh.build.write_json(self.path / 'manifest.json', self.metadata)
        with self.assertRaisesRegex(ValueError, 'metadata mismatch'):
            refresh.verify_collection(self.path, self.rows, self.manifest)

    def test_trace_refresh_keeps_controls_and_replaces_mini(self):
        previous = [{'variant': 'native', 'tools': ['original']},
                    {'variant': 'orchestrion', 'tools': ['original']},
                    {'variant': 'sdk', 'tools': ['old']}, {'variant': 'mini', 'tools': ['old']}]
        latest = [{'variant': 'mini', 'tools': ['new']}]
        self.assertEqual(refresh.merge_traces(previous, latest), previous[:2] + latest)

    def test_repeated_refresh_keeps_original_reference_dates(self):
        self.manifest['measurement_origins'] = {
            variant: {'source_head': 'original', 'started_at': 'original-date'}
            for variant in refresh.RETAINED}
        origins = refresh.retained_origins(self.manifest)
        self.assertEqual(origins['native']['source_head'], 'original')
        self.assertEqual(origins['orchestrion']['started_at'], 'original-date')

    def test_repeated_refresh_keeps_sdk_inventory_without_sdk_timings(self):
        oracle = dict(variant='sdk', case='fixture', cpus=4, returncode=0,
                      delivery={'tests': ['original']}, wall_s=99, peak_bytes=99)
        retained = refresh.reference_inventories(self.path, 'runtime', [oracle])
        self.assertNotIn('wall_s', retained[0])
        self.assertNotIn('peak_bytes', retained[0])
        refresh.compressed_json(self.path / 'runtime/reference-inventories.json.gz', retained)
        self.assertEqual(refresh.reference_inventories(self.path, 'runtime', []), retained)


if __name__=='__main__':unittest.main()
