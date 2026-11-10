import json
from pathlib import Path
from types import SimpleNamespace
import unittest

import case_image

SHA = 'a' * 40
IMAGE = 'sha256:' + 'b' * 64


class PreparedImageTests(unittest.TestCase):
    def restore(self, *, metadata=None, actual=SHA, dirty='', running=IMAGE):
        calls = []
        manifest = {'schema_version': 1, 'repository': 'https://example.invalid/repo.git', 'commit': SHA,
                    'baseline': SHA, 'framework': 'jest'}
        manifest.update(metadata or {})
        def command(argv, **kwargs):
            calls.append(argv)
            if argv[0] == 'cat':
                return json.dumps(manifest)
            if argv[:2] == ['git', 'rev-parse']:
                return actual
            if argv[:3] == ['git', 'remote', 'get-url']:
                return manifest['repository']
            if argv[:2] == ['git', 'status']:
                return dirty
            return ''
        container = SimpleNamespace(name='trial', control=lambda *argv: SimpleNamespace(stdout=IMAGE if argv[0] == 'image' else running))
        result = case_image.restore(command, container, IMAGE, 'https://example.invalid/repo.git', SHA, 'jest', Path('/trial'), Path('/trial/repo'))
        return result, calls

    def test_exact_clean_case_records_the_verified_image(self):
        result, calls = self.restore()
        self.assertEqual(result['image_id'], IMAGE)
        self.assertIn(['git', 'merge-base', '--is-ancestor', SHA, 'HEAD'], calls)
        self.assertFalse(any(call[:2] in (['git', 'clone'], ['git', 'fetch']) for call in calls))
        self.assertFalse(any('/.codex' in arg or '/.claude' in arg for call in calls for arg in call))

    def test_mismatched_commit_framework_image_or_dirty_checkout_are_refused(self):
        for kwargs in ({'metadata': {'commit': 'c' * 40}}, {'metadata': {'framework': 'pytest'}},
                       {'actual': 'd' * 40}, {'dirty': ' M package.json'}, {'running': 'sha256:' + 'c' * 64}):
            with self.subTest(kwargs=kwargs), self.assertRaises((ValueError, RuntimeError)):
                self.restore(**kwargs)
