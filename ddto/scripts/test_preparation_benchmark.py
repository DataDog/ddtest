import argparse
import os
from pathlib import Path
import tempfile
import unittest

import preparation_benchmark as benchmark

LOG = """TestOptimization.build v0.1.1-dev DEBUG: +0s command="test"
TestOptimization.build v0.1.1-dev DEBUG: +44.9ms runtime own_source_packages=51
TestOptimization.build v0.1.1-dev DEBUG: +74.951ms prepare finished duration=74.731ms status=ok
something else on stderr
TestOptimization.build v0.1.1-dev DEBUG: +179.7ms go test finished duration=104.8ms status=ok
TestOptimization.build v0.1.1-dev DEBUG: +179.781ms ddto finished duration=0.18s status=ok
"""


class PreparationBenchmarkTests(unittest.TestCase):
    def test_parse_reads_durations_and_own_packages(self):
        self.assertEqual({'own_packages': 51, 'prepare': 74.731, 'go_test': 104.8, 'ddto': 180.0},
                         benchmark.parse(LOG))

    def test_parse_ignores_other_lines(self):
        self.assertEqual({}, benchmark.parse('prepare finished duration=1ms\n'))

    def test_report_gives_medians(self):
        samples = [{'wall': w, 'prepare': p, 'go_test': 100.0, 'ddto': 180.0, 'own_packages': 51}
                   for w, p in ((190.0, 70.0), (200.0, 80.0), (210.0, 75.0))]
        results = {'subject': '/x/gin', 'subject_commit': '73726dc606790000', 'go': 'go version go1.27.1',
                   'cpus': 4, 'runs': 3, 'samples': {'release': samples}}
        self.assertIn('| release | 200.0 ms | 75.00 ms | 100.00 ms | 180.00 ms | 51 |', benchmark.report(results))

    def test_normalize_makes_paths_absolute(self):
        with tempfile.TemporaryDirectory() as directory:
            previous = os.getcwd()
            os.chdir(directory)
            try:
                Path('ddto').write_text('')
                args = argparse.Namespace(subject=Path('gin'), output=Path('out'), report=Path('docs'),
                                          ddto=['local=./ddto', 'here=ddto', 'path=python3'])
                benchmark.normalize(args)
                root = Path(directory).resolve()
                self.assertEqual((root / 'gin', root / 'out', root / 'docs'), (args.subject, args.output, args.report))
                self.assertEqual(str(root / 'ddto'), args.binaries['local'])
                self.assertEqual(str(root / 'ddto'), args.binaries['here'])
                self.assertTrue(Path(args.binaries['path']).is_absolute())
                Path('bin').mkdir()
                Path('bin', 'ddto-relative').write_text('')
                Path('bin', 'ddto-relative').chmod(0o755)
                path = os.environ['PATH']
                os.environ['PATH'] = 'bin' + os.pathsep + path
                try:
                    found = benchmark.executable('ddto-relative')
                finally:
                    os.environ['PATH'] = path
                self.assertEqual(str(root / 'bin' / 'ddto-relative'), found)
                env = benchmark.environment(args.output, 'local')
                self.assertTrue(Path(env['GOCACHE']).is_absolute() and Path(env['XDG_CACHE_HOME']).is_absolute())
            finally:
                os.chdir(previous)


if __name__ == '__main__':
    unittest.main()
