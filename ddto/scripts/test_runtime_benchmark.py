"""Check historical reference use without hiding added native workloads."""
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


if __name__ == '__main__': unittest.main()
