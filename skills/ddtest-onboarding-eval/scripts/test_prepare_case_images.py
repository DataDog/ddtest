import contextlib
import copy
import io
import json
import os
from pathlib import Path
import shlex
import tempfile
import unittest
from unittest.mock import patch

import prepare_case_images
import run_catalog

BASE = 'sha256:' + 'a' * 64
BUILT = 'sha256:' + 'b' * 64


class CaseImageBuilderTests(unittest.TestCase):
    def case(self, prefix='E04'):
        return copy.deepcopy(next(case for case in run_catalog.load_catalog()['cases'] if case['id'].startswith(prefix + '-')))

    def test_pins_source_applies_only_required_trims_and_keeps_tracer_out(self):
        case = self.case('E17')
        case['trims']['required'] = {'reason': 'Necessary fixture trim', 'commands': ['git rm required-only']}
        case['trims']['optional_speed'] = {'reason': 'Not required', 'commands': ['rm optional-only']}
        text = prepare_case_images.dockerfile(case, BASE)
        self.assertIn(json.dumps(['git', 'fetch', '--depth=1', '--no-tags', 'origin', case['commit']]), text)
        for command in run_catalog.trim_commands(case):
            self.assertIn('RUN ' + json.dumps(shlex.split(command)), text)
        self.assertNotIn('optional-only', text)
        self.assertIn('verify-commit HEAD', text)
        self.assertIn('commit -S', text)
        self.assertIn('dd-java-agent*.jar', text)
        self.assertNotIn('commit.gpgsign=false', text)
        final = text.split('FROM ${BASE}')[-1]
        self.assertIn('COPY --from=prepare /trial/repo /opt/ddtest-case/repo', final)
        for excluded in ('.codex', '.claude', 'trim-signing-key'):
            self.assertNotIn(excluded, final)

    def test_prepared_python_uses_restored_venv_and_refuses_installed_tracer(self):
        text = prepare_case_images.dockerfile(self.case('E07'), BASE)
        self.assertIn('"/trial/venv"', text)
        self.assertIn("find_spec('ddtrace') is None", text)
        self.assertIn('COPY --from=prepare /trial/venv /opt/ddtest-case/venv', text)
        self.assertIn('git status --porcelain --untracked-files=all', text)
        self.assertIn('RUN ' + json.dumps(['chmod', '-R', 'a+rX', '/trial']), text)

    def test_full_source_and_immutable_base_are_required(self):
        case = self.case()
        case['commit'] = 'main'
        with self.assertRaises(ValueError):
            prepare_case_images.dockerfile(case, BASE)
        with self.assertRaises(ValueError):
            prepare_case_images.dockerfile(self.case(), 'node:latest')

    def test_incremental_build_preserves_other_cases_and_uses_empty_client_credentials(self):
        case = self.case()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            mapping = root / 'case-images.json'
            mapping.write_text(json.dumps({'E07-existing-case': BASE}))
            bases = root / 'bases.json'
            bases.write_text(json.dumps({case['image']: BASE}))
            calls = []
            def build(argv, **kwargs):
                calls.append(argv)
                if '--iidfile' in argv:
                    Path(argv[argv.index('--iidfile') + 1]).write_text(BUILT)
            with patch.object(prepare_case_images.subprocess, 'check_output', return_value='unix:///local/docker.sock\n'), patch.object(prepare_case_images.subprocess, 'run', side_effect=build), contextlib.redirect_stdout(io.StringIO()):
                code = prepare_case_images.main(['E04', '--image-file', str(bases), '--output', str(root), '--build'])
            self.assertEqual(code, 0)
            self.assertEqual(json.loads(mapping.read_text()), {'E07-existing-case': BASE, case['id']: BUILT})
            configuration = json.loads((root / 'docker-config/config.json').read_text())
            self.assertEqual(configuration['auths'], {})
            self.assertFalse(set(configuration) - {'auths', 'cliPluginsExtraDirs'})
            self.assertEqual(calls[0][:5], ['docker', '--config', str(root / 'docker-config'), '--host', 'unix:///local/docker.sock'])


    def test_empty_client_configuration_keeps_only_the_buildx_executable_directory(self):
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / 'client'
            with patch.object(prepare_case_images.shutil, 'which', return_value='/trusted/tools/docker-buildx'), \
                 patch.object(prepare_case_images.subprocess, 'check_output', return_value='unix:///local/docker.sock\n'):
                client = prepare_case_images.docker_client(config)
            self.assertEqual(json.loads((config / 'config.json').read_text()),
                             {'auths': {}, 'cliPluginsExtraDirs': ['/trusted/tools']})
            self.assertEqual(client[-2:], ['--host', 'unix:///local/docker.sock'])

    def test_local_id_build_preserves_base_identity_without_personal_builder_state(self):
        commands = prepare_case_images.image_build_commands(['docker', '--config', '/private/client'], BASE, ['context'])
        alias = 'ddtest-eval-base:' + BASE.removeprefix('sha256:')
        self.assertEqual(commands[0][-4:], ['image', 'tag', BASE, alias])
        self.assertIn('BASE=' + alias, commands[1])
        with patch.dict(os.environ, {'DOCKER_AUTH_CONFIG': 'private-credentials', 'BUILDX_CONFIG': '/personal/state',
                                     'BUILDX_BUILDER': 'remote-builder', 'DOCKER_CONTEXT': 'personal-context'}), \
             patch.object(prepare_case_images.subprocess, 'run') as run:
            prepare_case_images.execute_image_build(commands)
        self.assertEqual(run.call_count, 2)
        for call in run.call_args_list:
            env = call.kwargs['env']
            self.assertNotIn('DOCKER_AUTH_CONFIG', env)
            self.assertNotIn('DOCKER_CONTEXT', env)
            self.assertEqual(env['BUILDX_CONFIG'], '/private/client/buildx')
            self.assertEqual(env['BUILDX_BUILDER'], 'default')
        # No rm/prune command may delete the source ID when it had no other tags.
        self.assertFalse(any('rm' in command or 'prune' in command for command in commands))


if __name__ == '__main__':
    unittest.main()
