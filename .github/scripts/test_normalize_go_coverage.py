import tempfile
import unittest
from pathlib import Path

from normalize_go_coverage import normalize


class NormalizeCoverageTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.source = self.root / 'input.out'
        self.output = self.root / 'output.out'

    def test_merges_package_counters_and_is_idempotent(self):
        for mode in ('set', 'count', 'atomic'):
            with self.subTest(mode=mode):
                self.source.write_text(
                    f'mode: {mode}\n'
                    'example/b.go:3.1,4.2 2 0\n'
                    'example/a.go:1.1,2.2 3 0\n'
                    'example/b.go:3.1,4.2 2 1\n'
                    'example/a.go:1.1,2.2 3 1\n'
                    'example/a.go:1.1,2.2 3 1\n'
                )
                normalize(self.source, self.output)
                count = 1 if mode == 'set' else 2
                expected = f'mode: {mode}\nexample/a.go:1.1,2.2 3 {count}\nexample/b.go:3.1,4.2 2 1\n'
                self.assertEqual(expected, self.output.read_text())
                normalize(self.output, self.output)
                self.assertEqual(expected, self.output.read_text())

    def test_keeps_distinct_blocks_on_the_same_line(self):
        self.source.write_text('mode: atomic\nexample/a.go:1.1,1.5 1 1\nexample/a.go:1.8,1.12 1 0\n')
        normalize(self.source, self.output)
        self.assertEqual(self.source.read_text(), self.output.read_text())

    def test_invalid_reports_do_not_replace_output(self):
        for report in ('', 'mode: atomic\n', 'mode: invalid\n', 'mode: atomic\nbad\n',
                       'mode: atomic\na.go:1.1,2.2 3 0\na.go:1.1,2.2 4 1\n'):
            with self.subTest(report=report):
                self.source.write_text(report)
                self.output.write_text('previous')
                with self.assertRaises(ValueError):
                    normalize(self.source, self.output)
                self.assertEqual('previous', self.output.read_text())
