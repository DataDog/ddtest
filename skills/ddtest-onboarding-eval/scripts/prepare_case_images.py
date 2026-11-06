#!/usr/bin/env python3
"""Build one credential-free dependency image per catalog case."""
import argparse
import json
from pathlib import Path
import re
import shlex
import subprocess

import run_catalog
from isolation import validate_image


CACHE_NAMES = {'npm_config_cache': 'npm', 'YARN_CACHE_FOLDER': 'yarn', 'XDG_CACHE_HOME': 'xdg-cache',
               'XDG_DATA_HOME': 'xdg-data', 'XDG_CONFIG_HOME': 'xdg-config', 'PNPM_HOME': 'pnpm-home',
               'CYPRESS_CACHE_FOLDER': 'cypress', 'PLAYWRIGHT_BROWSERS_PATH': 'playwright',
               'PUPPETEER_CACHE_DIR': 'puppeteer', 'PIP_CACHE_DIR': 'pip', 'UV_CACHE_DIR': 'uv'}


def dockerfile(case, base):
    validate_image(base)
    if not re.fullmatch(r'[0-9a-f]{40}', case['commit']):
        raise ValueError('A prepared case requires the full source commit')
    lines = ['ARG BASE', 'FROM ${BASE} AS prepare', 'USER root']
    env = {'HOME': '/trial/home', 'MAVEN_OPTS': '-Duser.home=/trial/home',
           'GRADLE_USER_HOME': '/trial/home/.gradle', 'XDG_STATE_HOME': '/trial/state',
           'PATH': '/trial/tools/node_modules/.bin:/trial/venv/bin:/trial/bundle-bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin'}
    env.update({key: '/trial/caches/' + value for key, value in CACHE_NAMES.items()})
    if case['framework'] == 'pytest':
        env.update(VIRTUAL_ENV='/trial/venv', UV_PROJECT_ENVIRONMENT='/trial/venv')
    for key, value in env.items():
        lines.append('ENV ' + key + '=' + json.dumps(value))
    directories = ['/trial/home/.m2', '/trial/home/.gradle', '/trial/home/bundle', '/trial/home/.bundle',
                   '/trial/harness', '/trial/tools', '/trial/bundle-bin', '/trial/venv',
                   '/trial/caches/pnpm-store', '/trial/caches/pnpm', *[value for key, value in env.items() if key in CACHE_NAMES]]
    def run(argv):
        lines.append('RUN ' + json.dumps(argv))
    run(['mkdir', '-p', *directories])
    run(['node', '-e', "const fs=require('fs'); fs.mkdirSync('/trial/caches/xdg-config/pnpm',{recursive:true}); fs.writeFileSync('/trial/caches/xdg-config/pnpm/rc', 'store-dir=/trial/caches/pnpm-store\\ncache-dir=/trial/caches/pnpm\\n');"])
    run(['git', 'init', '-q', '/trial/repo'])
    lines.append('WORKDIR /trial/repo')
    run(['git', 'remote', 'add', 'origin', case['repo']])
    run(['git', 'fetch', '--depth=1', '--no-tags', 'origin', case['commit']])
    run(['git', 'checkout', '--detach', 'FETCH_HEAD'])
    harness = case['harness']
    if harness.get('pnpm'):
        run(['npm', 'install', '--prefix', '/trial/tools', '--no-save', '--no-audit', '--no-fund', 'pnpm@' + str(harness['pnpm'])])
    if harness.get('python'):
        run([harness['python'], '-m', 'venv', '/trial/venv'])
    for command in [*run_catalog.trim_commands(case), *case['setup_commands']]:
        argv = shlex.split(command)
        # A prepared Ruby bundle must survive restoration at /trial/home.
        if argv[:5] == ['bundle', 'config', 'set', '--global', 'path']:
            argv[-1] = '/trial/home/bundle'
        run(argv)
    if harness.get('bundle_binstubs'):
        run(['env', 'BUNDLE_BIN=/trial/bundle-bin', 'bundle', 'binstubs', *harness['bundle_binstubs']])
    for command in case['readiness_commands']:
        run(shlex.split(command))
    # No ddtest command runs while preparing an image. Reject tracer packages
    # even when a project dependency would have brought one in transitively.
    if case['framework'] == 'pytest':
        run(['python', '-c', "import importlib.util; assert importlib.util.find_spec('ddtrace') is None, 'Tracer must not be baked into a case image'"])
    elif case['framework'] in ('rspec', 'minitest'):
        run(['sh', '-c', 'test -z "$(find /trial/home/bundle -name \'datadog-ci-*\' -print -quit)"'])
    elif case['framework'] in run_catalog.run_eval.JAVA_FRAMEWORKS:
        run(['sh', '-c', 'test -z "$(find /trial -name \'dd-java-agent*.jar\' -print -quit)"'])
    else:
        run(['node', '-e', "try { require.resolve('dd-trace'); process.exit(1); } catch (error) { if (error.code !== 'MODULE_NOT_FOUND') throw error; }"])
    run(['sh', '-c', 'test -z "$(git status --porcelain --untracked-files=all)"'])
    manifest = {'schema_version': 1, 'repository': case['repo'], 'commit': case['commit'],
                'framework': case['framework'], 'case': case['id'], 'base_image': base}
    script = "const fs=require('fs'), cp=require('child_process'); const m=" + json.dumps(manifest) + "; m.baseline=cp.execFileSync('git',['rev-parse','HEAD'],{encoding:'utf8'}).trim(); fs.writeFileSync('/trial/manifest.json', JSON.stringify(m));"
    run(['node', '-e', script])
    # Builds run as root; trial users must be able to restore private cache files.
    run(['chmod', '-R', 'a+rX', '/trial'])
    lines += ['FROM ${BASE}', 'COPY --from=prepare /trial/repo /opt/ddtest-case/repo',
              'COPY --from=prepare /trial/manifest.json /opt/ddtest-case/manifest.json']
    for directory in ('caches', 'tools', 'venv', 'bundle-bin', 'home/.m2', 'home/.gradle', 'home/bundle', 'home/.bundle'):
        lines.append(f'COPY --from=prepare /trial/{directory} /opt/ddtest-case/{directory}')
    return '\n'.join(lines) + '\n'


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('ids', nargs='*')
    parser.add_argument('--all', action='store_true')
    parser.add_argument('--image-file', required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--build', action='store_true', help='Build the generated Dockerfiles and save case-images.json')
    args = parser.parse_args(argv)
    catalog = run_catalog.load_catalog()
    problems = run_catalog.validate(catalog)
    if problems:
        parser.error('; '.join(problems))
    if args.all == bool(args.ids):
        parser.error('Name cases or pass --all')
    cases = catalog['cases']
    if not args.all:
        selected = []
        for name in args.ids:
            matches = [case for case in cases if case['id'] == name or case['id'].split('-')[0] == name]
            if len(matches) != 1:
                parser.error('Unknown case ' + name)
            selected.extend(matches)
        cases = selected
    bases = run_catalog.resolve_images([], args.image_file)
    args.output.mkdir(parents=True, exist_ok=True)
    mapping = args.output / 'case-images.json'
    images = json.loads(mapping.read_text()) if mapping.exists() else {}
    docker = ['docker']
    if args.build:
        endpoint = subprocess.check_output(['docker', 'context', 'inspect', '--format', '{{(index .Endpoints "docker").Host}}'], text=True).strip()
        if not endpoint.startswith('unix://'):
            parser.error('Prepared images require a local Docker VM with no host credentials')
        config = args.output / 'docker-config'
        config.mkdir(exist_ok=True)
        (config / 'config.json').write_text('{"auths":{}}\n')
        docker += ['--config', str(config), '--host', endpoint]
    for case in cases:
        base = bases.get(case['image'])
        if not base:
            parser.error('No base image for ' + case['image'])
        context = args.output / case['id']
        context.mkdir(exist_ok=True)
        (context / 'Dockerfile').write_text(dockerfile(case, base))
        command = [*docker, 'build', '--build-arg', 'BASE=' + base, '--iidfile', str(context / 'image-id'),
                   '-t', 'ddtest-eval-case-' + case['id'].lower(), str(context)]
        print(shlex.join(command), flush=True)
        if args.build:
            subprocess.run(command, check=True)
            images[case['id']] = (context / 'image-id').read_text().strip()
            mapping.write_text(json.dumps(images, indent=2) + '\n')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
