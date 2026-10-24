"""No live login, keychain, provider or network access."""
import json
from pathlib import Path
import tempfile
import unittest
from model_auth import ModelLogin

class ModelAuthTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.home = self.root/'home'
        (self.home/'.codex').mkdir(parents=True)
        self.source = self.root/'auth.json'
        self.data = {'auth_mode':'chatgpt','tokens':{'access_token':'original-access','refresh_token':'original-refresh'},'last_refresh':'2026-10-23T10:00:00Z','host_metadata':'preserve'}
        self.source.write_text(json.dumps(self.data))

    def test_metadata_refresh_persistence_and_setup_isolation(self):
        secrets = set()
        login = ModelLogin('codex',self.home,secrets,path=self.source)
        login.install()
        copied = json.loads(login.destination.read_text())
        self.assertEqual(copied['last_refresh'],self.data['last_refresh'])
        self.assertNotIn('host_metadata',copied)
        copied['tokens']['refresh_token']='rotated-refresh'
        login.destination.write_text(json.dumps(copied))
        login.suspend()
        self.assertFalse(login.destination.exists())
        login.resume()
        self.assertEqual(json.loads(login.destination.read_text()),copied)
        login.close()
        updated=json.loads(self.source.read_text())
        self.assertEqual(updated['tokens']['refresh_token'],'rotated-refresh')
        self.assertEqual(updated['host_metadata'],'preserve')
        self.assertFalse(login.destination.exists())
        self.assertIn('rotated-refresh',secrets)

    def test_repository_written_login_is_never_persisted(self):
        for replacement in ({'tokens':{'access_token':'attacker-access','refresh_token':'attacker-refresh'}}, 'not json'):
            self.source.write_text(json.dumps(self.data))
            login=ModelLogin('codex',self.home,set(),path=self.source)
            login.install()
            login.suspend()
            login.resume()
            # The agent session runs repository commands as the same user.
            login.destination.write_text(replacement if isinstance(replacement,str) else json.dumps(replacement))
            login.close()
            self.assertEqual(json.loads(self.source.read_text()),self.data)
            self.assertTrue(login.unpersisted_change)
            self.assertFalse(login.destination.exists())

    def test_login_planted_during_setup_is_never_persisted(self):
        login=ModelLogin('codex',self.home,set(),path=self.source)
        login.install()
        login.suspend()
        planted={'tokens':{'access_token':'planted-access','refresh_token':'planted-refresh'}}
        login.destination.write_text(json.dumps(planted))
        login.close()
        self.assertEqual(json.loads(self.source.read_text()),self.data)
        self.assertTrue(login.unpersisted_change)

    def test_concurrent_evaluators_cannot_fork_refresh_state(self):
        first=ModelLogin('codex',self.home,set(),path=self.source)
        first.install()
        self.addCleanup(first.close)
        second=ModelLogin('codex',self.home,set(),path=self.source)
        with self.assertRaisesRegex(ValueError,'in use'):
            second.install()

    def test_external_login_change_is_never_overwritten(self):
        login=ModelLogin('codex',self.home,set(),path=self.source)
        login.install()
        updated=json.loads(login.destination.read_text())
        updated['tokens']['refresh_token']='trial-refresh'
        login.destination.write_text(json.dumps(updated))
        newer={**self.data,'last_refresh':'2026-10-23T12:00:00Z'}
        self.source.write_text(json.dumps(newer))
        with self.assertRaisesRegex(ValueError,'changed outside'):
            login.close()
        self.assertEqual(json.loads(self.source.read_text()),newer)
        self.assertFalse(login.destination.exists())

    def test_unconfirmed_teardown_never_persists_trial_credentials(self):
        login=ModelLogin('codex',self.home,set(),path=self.source)
        login.install()
        updated=json.loads(login.destination.read_text())
        updated['tokens']['refresh_token']='unconfirmed-refresh'
        login.destination.write_text(json.dumps(updated))
        login.close(persist=False)
        self.assertEqual(json.loads(self.source.read_text()),self.data)
        self.assertFalse(login.destination.exists())
        self.assertIsNone(login.lock)

    def test_setup_cannot_redirect_credentials_to_a_host_path(self):
        login=ModelLogin('codex',self.home,set(),path=self.source)
        login.install()
        login.suspend()
        outside=self.root/'outside';outside.mkdir()
        (self.home/'.codex').rmdir()
        (self.home/'.codex').symlink_to(outside,target_is_directory=True)
        with self.assertRaisesRegex(RuntimeError,'not a plain directory'):
            login.resume()
        with self.assertRaisesRegex(RuntimeError,'not a plain directory'):
            login.close()
        self.assertFalse((outside/'auth.json').exists())
        self.assertEqual(json.loads(self.source.read_text()),self.data)
