import contextlib
import copy
import io
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tarfile
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


    def test_prepared_pnpm_store_survives_trial_yaml_configuration(self):
        node, pnpm = shutil.which('node'), shutil.which('pnpm')
        if not node or not pnpm:
            self.skipTest('Node and the catalog pnpm 12 fixture are required')
        pnpm = str(Path(pnpm).resolve())
        version = subprocess.check_output([pnpm, '--version'], text=True).strip()
        if not version.startswith('12.'):
            self.skipTest('The catalog pnpm 12 fixture is required')
        commands = [json.loads(line[4:]) for line in prepare_case_images.dockerfile(self.case(), BASE).splitlines()
                    if line.startswith('RUN [')]
        configure = next(command for command in commands
                         if command[0] == 'node' and 'xdg-config/pnpm/rc' in command[-1])
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name in ('home', 'repo', 'tmp', 'pnpm-home'):
                (root / name).mkdir()
            (root / 'npmrc').write_text('')
            (root / 'global-npmrc').write_text('')
            env = {'PATH': os.environ['PATH'], 'HOME': str(root / 'home'),
                   'XDG_CONFIG_HOME': str(root / 'caches/xdg-config'), 'XDG_DATA_HOME': str(root / 'data'),
                   'XDG_CACHE_HOME': str(root / 'cache'), 'PNPM_HOME': str(root / 'pnpm-home'),
                   'NPM_CONFIG_USERCONFIG': str(root / 'npmrc'), 'NPM_CONFIG_GLOBALCONFIG': str(root / 'global-npmrc'),
                   'NPM_CONFIG_CACHE': str(root / 'npm-cache'), 'TMPDIR': str(root / 'tmp'), 'CI': 'true'}
            subprocess.run([node, '-e', configure[-1].replace('/trial', str(root))], env=env, check=True)
            for name in ('fixture-a', 'fixture-b'):
                package = root / name
                package.mkdir()
                (package / 'package.json').write_text(json.dumps({'name': name, 'version': '1.0.0'}))
                with tarfile.open(root / (name + '.tgz'), 'w:gz') as archive:
                    archive.add(package / 'package.json', arcname='package/package.json')
            repo = root / 'repo'
            (repo / 'package.json').write_text(json.dumps({'name': 'fixture', 'version': '1.0.0',
                                                        'dependencies': {'fixture-a': 'file:../fixture-a.tgz'}}))
            prepared_store = subprocess.check_output([pnpm, 'store', 'path'], cwd=repo, env=env, text=True).strip()
            subprocess.run([pnpm, 'install', '--offline', '--ignore-scripts', '--no-frozen-lockfile'],
                           cwd=repo, env=env, check=True, capture_output=True)
            # Restored trials write this YAML configuration, in addition to rc.
            (root / 'caches/xdg-config/pnpm/config.yaml').write_text(
                'storeDir: ' + str(root / 'caches/pnpm-store') + '\ncacheDir: ' + str(root / 'caches/pnpm') + '\n')
            restored_store = subprocess.check_output([pnpm, 'store', 'path'], cwd=repo, env=env, text=True).strip()
            self.assertEqual(prepared_store, restored_store)
            added = subprocess.run([pnpm, 'add', '--offline', '--ignore-scripts', 'file:../fixture-b.tgz'],
                                   cwd=repo, env=env, text=True, capture_output=True)
            self.assertEqual(added.returncode, 0, added.stdout + added.stderr)


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
