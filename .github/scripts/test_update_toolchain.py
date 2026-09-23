import tempfile
import unittest
from pathlib import Path

from update_toolchain import update


class ToolchainUpdateTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.mod = self.root / 'go.mod'
        self.mod.write_text('module example.com/test\n\ngo 1.27.1\n\nrequire example.com/dep v1.2.3\n')
        self.lint = self.root / '.golangci-lint-version'
        self.lint.write_text('v2.13.2\n')

    def releases(self, go='1.27.2', lint='2.13.3'):
        return ([{'version': f'go{go}', 'stable': True}],
                {'tag_name': f'v{lint}', 'draft': False, 'prerelease': False})

    def test_updates_both_pins_and_preserves_dependencies(self):
        self.assertTrue(update(self.root, *self.releases()))
        self.assertIn('go 1.27.2\n', self.mod.read_text())
        self.assertIn('require example.com/dep v1.2.3\n', self.mod.read_text())
        self.assertEqual('v2.13.3\n', self.lint.read_text())
        self.assertFalse(update(self.root, *self.releases()))

    def test_selects_newest_stable_numerically(self):
        go, lint = self.releases()
        go.extend([{'version': 'go1.27.10', 'stable': True},
                   {'version': 'go1.28rc1', 'stable': False}])
        update(self.root, go, lint)
        self.assertIn('go 1.27.10\n', self.mod.read_text())

    def test_updates_minor_go_release(self):
        update(self.root, *self.releases(go='1.28.0'))
        self.assertIn('go 1.28.0\n', self.mod.read_text())

    def test_removes_obsolete_toolchain_override_when_upgrading_go(self):
        self.mod.write_text(self.mod.read_text() + '\ntoolchain go1.27.1\n')
        update(self.root, *self.releases())
        self.assertNotIn('toolchain ', self.mod.read_text())
        self.assertIn('go 1.27.2\n', self.mod.read_text())

    def test_does_not_downgrade(self):
        before = self.mod.read_text()
        self.assertFalse(update(self.root, *self.releases(go='1.26.6', lint='2.12.2')))
        self.assertEqual(before, self.mod.read_text())
        self.assertEqual('v2.13.2\n', self.lint.read_text())

    def test_linter_only_update(self):
        before = self.mod.read_text()
        self.assertTrue(update(self.root, *self.releases(go='1.27.1')))
        self.assertEqual(before, self.mod.read_text())
        self.assertEqual('v2.13.3\n', self.lint.read_text())

    def test_bad_metadata_does_not_write_partial_updates(self):
        before = self.mod.read_text()
        for changes in [{'prerelease': True}, {'draft': True}, {'tag_name': 'v2.14.0-rc1'},
                        {'tag_name': 'v2.14.0\nchanged=true'}]:
            with self.subTest(changes=changes):
                go, lint = self.releases()
                lint.update(changes)
                with self.assertRaises(ValueError):
                    update(self.root, go, lint)
                self.assertEqual(before, self.mod.read_text())
                self.assertEqual('v2.13.2\n', self.lint.read_text())

    def test_empty_stable_release_list_fails(self):
        _, lint = self.releases()
        with self.assertRaises(ValueError):
            update(self.root, [], lint)


if __name__ == '__main__':
    unittest.main()
