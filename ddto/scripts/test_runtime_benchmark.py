"""Check historical reference use without hiding added native workloads."""
import json
import unittest
from pathlib import Path
from unittest.mock import patch
import runtime_benchmark as runtime


class ReferencesTests(unittest.TestCase):
    def test_examples_extend_the_inventory_without_changing_existing_events(self):
        oracle = {'delivery': {'event_counts': {'test': 1, 'test_suite_end': 1, 'test_module_end': 1},
                              'tests': [['example/pkg', 'test_test.go', 'TestExisting', 'pass', 1]],
                              'coverage_items': 7}}
        native = [['pkg.test', 'TestExisting', 'PASS', 1], ['pkg.test', 'Example', 'PASS', 1]]
        binaries = [{'path': '/compiled/pkg.test'}]
        with patch.object(runtime.subprocess, 'check_output', return_value='TEXT example/pkg.Example(SB) /source/example_test.go\n'):
            delivery, added = runtime.expected_delivery(oracle, native, binaries, Path('/go'))
        self.assertEqual(delivery['event_counts'], {'test': 2, 'test_suite_end': 2, 'test_module_end': 1})
        self.assertEqual(delivery['coverage_items'], 7)
        self.assertEqual(added, [['example/pkg', 'example_test.go', 'Example', 'pass', 1]])
        self.assertEqual(oracle['delivery']['event_counts']['test'], 1)

    def test_missing_example_source_identity_is_rejected(self):
        oracle = {'delivery': {'event_counts': {'test': 1, 'test_suite_end': 1, 'test_module_end': 1},
                              'tests': [['example/pkg', 'test_test.go', 'TestExisting', 'pass', 1]],
                              'coverage_items': 0}}
        with patch.object(runtime.subprocess, 'check_output', return_value=''):
            with self.assertRaisesRegex(ValueError, 'every Native example'):
                runtime.expected_delivery(oracle, [['pkg.test', 'Example', 'PASS', 1]],
                                          [{'path': '/compiled/pkg.test'}], Path('/go'))

    def test_failed_atomic_coverage_reference_is_kept_unverified(self):
        native = {'case': 'pkg--race-cover-client', 'cpus': 4, 'variant': 'native', 'returncode': 0,
                  'native_results': [['pkg.test', 'TestA', 'PASS', 1]]}
        sdk = dict(native, variant='sdk', returncode=1, validated_contract=False)
        nonrace = dict(native, case='pkg', variant='sdk', subject='pkg', flags=[],
                       validated_contract=True, delivery={'coverage_items': 0})
        _, reference, _ = runtime.references([native,sdk,nonrace],native['case'],4,'pkg',
                                             ['-race','-coverpkg=./...','-covermode=atomic'])
        self.assertIsNone(reference)



class VariantTests(unittest.TestCase):
    def test_each_variant_builds_with_its_recorded_prefix(self):
        def row(variant, prefix):
            return {'variant': variant, 'command': json.dumps([*prefix, '-p=4', '-mod=readonly', '-c', '-o', 'out/', './...'])}
        rows = [row('native', ['/go/bin/go', 'test']),
                row('orchestrion', ['/go/bin/go', 'test', '-toolexec=/bin/orchestrion toolexec']),
                row('mini', ['/build/ddtest', 'instrument', 'test', '--runtime=mini']),
                row('mini', ['/other/ddtest', 'instrument', 'test', '--runtime=mini'])]
        self.assertEqual(runtime.command_prefixes(rows, ('native', 'orchestrion', 'mini')), {
            'native': ['/go/bin/go', 'test'],
            'orchestrion': ['/go/bin/go', 'test', '-toolexec=/bin/orchestrion toolexec'],
            'mini': ['/build/ddtest', 'instrument', 'test', '--runtime=mini']})
        with self.assertRaisesRegex(ValueError, 'no command'):
            runtime.command_prefixes(rows[2:], ('native', 'mini'))

    def run_row(self, events, requests=None):
        return {'returncode': 0, 'timeout': False, 'process_tree_drained': True, 'affinity': [0],
                'native_results': [['pkg.test', 'TestA', 'PASS', 1]],
                'delivery': {'event_counts': events, 'tests': [], 'coverage_items': 0, 'requests': requests or {}}}

    def test_native_counts_with_its_inventory_and_no_events(self):
        native = {'native_results': [['pkg.test', 'TestA', 'PASS', 1]]}
        row = self.run_row({})
        self.assertIsNone(runtime.validation_failure(row, 'native', native, None, None, [0], False, True))
        self.assertEqual(runtime.validation_failure(row, 'orchestrion', native, None, None, [0], False, False),
                         'no test events delivered')
        other = {'native_results': [['pkg.test', 'TestA', 'FAIL', 1]]}
        self.assertEqual(runtime.validation_failure(row, 'native', other, None, None, [0], False, False),
                         'CPU affinity or Native test inventory differs')
        self.assertEqual(runtime.validation_failure(row, 'native', native, None, None, [0], True, False),
                         'test process failed, raced, timed out or did not drain')

    def test_ci_variants_need_the_sdk_events_and_agent_telemetry(self):
        native = {'native_results': [['pkg.test', 'TestA', 'PASS', 1]]}
        expected = {'event_counts': {'test': 1}, 'tests': [], 'coverage_items': 0}
        row = self.run_row({'test': 1}, {'/evp_proxy/v2/api/v2/citestcycle': 1})
        for variant in ('orchestrion', 'mini', 'mini-deferred'):
            self.assertIsNone(runtime.validation_failure(row, variant, native, {}, expected, [0], False, False))
            self.assertEqual(runtime.validation_failure(row, variant, native, {}, expected, [0], False, True),
                             'no local telemetry request arrived')
            self.assertEqual(runtime.validation_failure(row, variant, native, None, None, [0], False, False),
                             'historical SDK reference unavailable for this combination')
        self.assertIsNone(runtime.validation_failure(row, 'native', native, None, None, [0], False, True))

if __name__ == '__main__': unittest.main()
