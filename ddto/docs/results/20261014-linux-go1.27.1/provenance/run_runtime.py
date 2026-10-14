#!/usr/bin/env python3
"""Run the complete subject/flag matrix using original prebuilt executables."""
from collections import Counter
from datetime import datetime, timezone
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time

from receiver import Receiver

WORKTREE = Path('/var/tmp/dd-ci-testing-poc-20261001/minitracer/worktree')
ROOT = Path('/var/tmp/dd-ci-benchmark-20261004.s81k5i3w')
BUILD = ROOT / 'build-stable'
OUTPUT = ROOT / 'runtime'
spec = importlib.util.spec_from_file_location('build_benchmark', WORKTREE / 'scripts/build_benchmark.py')
benchmark = importlib.util.module_from_spec(spec)
spec.loader.exec_module(benchmark)
VARIANTS = ('native', 'orchestrion', 'sdk', 'mini', 'mini-deferred')
RESULT = re.compile(r'^\s*--- (PASS|SKIP|FAIL): (.+) \([0-9.]+s\)$', re.MULTILINE)
PACKAGE_DIRECTORIES = {
    'gin': {'binding.test': 'binding', 'bytesconv.test': 'internal/bytesconv',
            'fs.test': 'internal/fs', 'gin.test': '.', 'ginS.test': 'ginS',
            'render.test': 'render'},
    'chi': {'chi.test': '.', 'middleware.test': 'middleware'},
    'testify-direct': {'testify-direct.test': '.'},
    'testify-external': {'testify-external.test': '.'},
}


def budget():
    task = benchmark.read_json(ROOT / 'task.json')
    remaining = (datetime.fromisoformat(task['deadline']) - datetime.now(timezone.utc)).total_seconds()
    if remaining <= 40:
        raise RuntimeError('overall task deadline reached')
    size = int(subprocess.check_output(['du', '-sb', str(ROOT)], text=True).split()[0])
    if size > task['limits']['artifact_bytes']:
        raise RuntimeError('overall artifact limit reached')
    return remaining


def environment(cpus, receiver, scratch, coverage, deferred, agent=False):
    names = ('PATH', 'HOME', 'USER', 'LOGNAME', 'DBUS_SESSION_BUS_ADDRESS',
             'XDG_RUNTIME_DIR', 'LANG', 'LC_ALL')
    env = {name: os.environ[name] for name in names if name in os.environ}
    coverdir = scratch / 'coverage'
    coverdir.mkdir()
    env.update(PATH='/var/tmp/dd-ci-testing-poc-20261001/minitracer/go1.27.1/bin:' + env.get('PATH', os.defpath),
        GOMAXPROCS=str(cpus), GOTOOLCHAIN='local', GOFLAGS='', GOWORK='off',
        TMPDIR=str(scratch), TMP=str(scratch), TEMP=str(scratch), XDG_CACHE_HOME=str(scratch),
        GOCOVERDIR=str(coverdir), DD_CIVISIBILITY_ENABLED='true',
        DD_CIVISIBILITY_AGENTLESS_ENABLED='true', DD_CIVISIBILITY_AGENTLESS_URL=receiver.url,
        DD_TRACE_AGENT_URL=receiver.url, DD_API_KEY='local-benchmark-placeholder',
        DD_SERVICE='dd-ci-local-benchmark', DD_ENV='benchmark', DD_TEST_SESSION_NAME='benchmark',
        DD_GIT_REPOSITORY_URL='https://github.com/tonyredondo/dd-ci-testing-poc.git',
        DD_GIT_COMMIT_SHA='1111111111111111111111111111111111111111',
        DD_CIVISIBILITY_FLAKY_RETRY_ENABLED='false',
        DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED='false',
        DD_CIVISIBILITY_GIT_UPLOAD_ENABLED='false',
        DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED='false',
        DD_CIVISIBILITY_CODE_COVERAGE_ENABLED=str(coverage).lower(),
        DD_CIVISIBILITY_DEFERRED_DELIVERY=str(deferred).lower(),
        DD_CIVISIBILITY_LOGS_ENABLED='false', DD_INSTRUMENTATION_TELEMETRY_ENABLED='false',
        DD_REMOTE_CONFIGURATION_ENABLED='false', DD_PROFILING_ENABLED='false',
        DD_APPSEC_ENABLED='false', DD_TRACE_STARTUP_LOGS='false',
        GOPROXY='off', GOSUMDB='off')
    if agent:
        # No API key: telemetry may use only the loopback Agent endpoint.
        env.update(DD_CIVISIBILITY_AGENTLESS_ENABLED='false', DD_API_KEY='',
                   DD_INSTRUMENTATION_TELEMETRY_ENABLED='true')
    return env


def execute(item, variant, iteration, number, agent=False):
    remaining = budget()
    label = f"{item['case']}-cpu{item['cpus']}-{variant}-round{iteration}"
    if agent:
        label += '-agent'
    directory = OUTPUT / 'rows' / label
    directory.mkdir(parents=True)
    # Gin exercises Unix sockets; sun_path is limited to 108 bytes on Linux.
    scratch = ROOT / 't' / f'{os.getpid():x}-{number:x}'
    scratch.mkdir(parents=True)
    coverage = any(flag.startswith('-cover') for flag in item['flags'])
    receiver = Receiver(directory / 'receiver', coverage)
    timeout = min(600, remaining - 30)
    config = dict(item, output=str(directory / 'measurement.json'), timeout=timeout,
                  environment=environment(item['cpus'], receiver, scratch, coverage,
                                          variant == 'mini-deferred', agent))
    configfile = directory / 'input.json'
    benchmark.write_json(configfile, config)
    unit = f'ddci-bench-runtime-{os.getpid()}-{number}'
    affinity = benchmark.read_json(BUILD / 'manifest.json')['affinity'][str(item['cpus'])]
    command = ['systemd-run', '--user', '--scope', '--quiet', '--unit=' + unit,
               f'--property=RuntimeMaxSec={timeout + 25}', 'taskset', '-c',
               ','.join(map(str, affinity)), sys.executable,
               str(ROOT / 'tools/measure_runtime.py'), str(configfile)]
    try:
        result = subprocess.run(command, cwd=item['cwd'], text=True, capture_output=True,
                                timeout=timeout + 35, env=config['environment'])
    finally:
        receiver.close()
    (directory / 'launcher.log').write_text(result.stdout + result.stderr)
    row = benchmark.read_json(directory / 'measurement.json')
    row.update(case=item['case'], subject=item['subject'], cpus=item['cpus'],
               variant=variant, flags=item['flags'], iteration=iteration,
               warmup=iteration == -1, scope=unit, launcher_exit=result.returncode,
               delivery_profile='agent-telemetry' if agent else 'agentless-gzip')
    counts = Counter()
    for binary in row['binaries']:
        text = Path(binary['stdout']).read_text(errors='replace')
        for status, name in RESULT.findall(text):
            counts[(binary['binary'], name, status)] += 1
        if 'DATA RACE' in Path(binary['stderr']).read_text(errors='replace'):
            raise RuntimeError('race detector reported an error')
    row['native_results'] = [list(key) + [count] for key, count in sorted(counts.items())]
    row['delivery'] = receiver.decoded(ROOT / 'tools/codec/decode')
    benchmark.write_json(directory / 'result.json', row)
    if (result.returncode or row['returncode'] or row['timeout']
            or not row['process_tree_drained'] or row['affinity'] != affinity):
        raise RuntimeError('runtime failed or scope did not drain: ' + label)
    if not counts:
        raise RuntimeError('no actual test results: ' + label)
    if variant == 'native':
        if row['delivery']['event_counts']:
            raise RuntimeError('Native unexpectedly emitted CI events')
    elif not row['delivery']['event_counts'].get('test'):
        raise RuntimeError('instrumentation emitted no test events: ' + label)
    elif coverage and not row['delivery']['coverage_items']:
        raise RuntimeError('coverage requested but no coverage events were delivered: ' + label)
    elif agent and not any('telemetry' in path for path in row['delivery']['requests']):
        raise RuntimeError('telemetry enabled but no local telemetry requests arrived: ' + label)
    print(f"{label}: {row['wall_s']:.6f} s, {row['cpu_s']:.6f} CPU-s, "
          f"{sum(counts.values())} test results, CI {row['delivery']['event_counts']}", flush=True)
    budget()
    return row


def run():
    build = benchmark.read_json(BUILD / 'manifest.json')
    if build['status'] != 'verified':
        raise ValueError('full build and qualification must pass before runtime')
    for filename, expected in build['source_and_tool_sha256'].items():
        if benchmark.digest(Path(filename)) != expected:
            raise ValueError('frozen build input changed: ' + filename)
    inputs = benchmark.read_json(BUILD / 'runtime-inputs.json')
    if len(inputs) != 24 * 2 * 4:
        raise ValueError('incomplete runtime executable set')
    for item in inputs:
        for binary in item['binaries']:
            if benchmark.digest(Path(binary['path'])) != binary['sha256']:
                raise ValueError('prebuilt executable changed')
            binary['cwd'] = str(Path(item['cwd']) / PACKAGE_DIRECTORIES[item['subject']][Path(binary['path']).name])
            if not Path(binary['cwd']).is_dir():
                raise ValueError('original package directory missing: ' + binary['cwd'])
    records, cells, number = [], {}, 0
    for item in inputs:
        cells.setdefault((item['case'], item['cpus']), {})[item['variant']] = item
    if OUTPUT.exists() and any(OUTPUT.iterdir()):
        raise RuntimeError('runtime output already contains observations')
    OUTPUT.mkdir(exist_ok=True)
    manifest = {'head': build['source_head'], 'toolchain': build['toolchain'],
        'sdk_commit': build['sdk_commit'], 'status': 'in progress', 'repetitions': 5,
        'warmups': 1, 'variants': VARIANTS, 'scope':
        'prebuilt package binaries: process startup, concurrent test execution, settings and final delivery; receiver setup/decoding excluded',
        'telemetry_enabled': False, 'coverage_enabled_when_compiled': True,
        'telemetry_validation': 'separate parity inventory test uses local Agent proxy; agentless telemetry would use an external intake',
        'receiver': 'loopback HTTP/1.1; real gzip/MessagePack and local successful settings/intake',
        'helper_sha256': {str(p): benchmark.digest(p) for p in
            [*(ROOT/'tools').glob('*.py'), *(ROOT/'tools/codec').glob('*.go'),
             *(ROOT/'tools/codec').glob('go.*'), ROOT/'tools/codec/decode']}}
    benchmark.write_json(OUTPUT / 'manifest.json', manifest)
    for (case, cpus), variants in cells.items():
        for filename, expected in build['source_and_tool_sha256'].items():
            if benchmark.digest(Path(filename)) != expected:
                raise ValueError('frozen runtime source changed: ' + filename)
        for filename, expected in manifest['helper_sha256'].items():
            if benchmark.digest(Path(filename)) != expected:
                raise ValueError('frozen runtime helper changed: ' + filename)
        baseline_native, baseline_events = None, None
        for iteration in range(-1, 5):
            offset = (iteration + 1) % len(VARIANTS)
            order = VARIANTS[offset:] + VARIANTS[:offset]
            for variant in order:
                number += 1
                if number > 1500:
                    raise RuntimeError('runtime command budget reached')
                item = variants['mini' if variant == 'mini-deferred' else variant]
                row = execute(item, variant, iteration, number)
                if baseline_native is None:
                    baseline_native = row['native_results']
                elif row['native_results'] != baseline_native:
                    raise RuntimeError(f'actual test names/counts/status differ: {case}/{cpus}/{variant}')
                if variant != 'native':
                    contract = {key: row['delivery'][key] for key in ('event_counts', 'tests', 'coverage_items')}
                    if baseline_events is None:
                        baseline_events = contract
                    elif contract != baseline_events:
                        raise RuntimeError(f'CI hierarchy/test counts/status differ: {case}/{cpus}/{variant}')
                row['execution_and_event_counts_verified'] = True
                records.append(row)
                with (OUTPUT / 'observations.jsonl').open('a') as ledger:
                    ledger.write(json.dumps(row) + '\n')
        benchmark.write_json(OUTPUT / 'observations.json', records)
        manifest.setdefault('verified_cells', []).append({'case': case, 'cpus': cpus})
        benchmark.write_json(OUTPUT / 'manifest.json', manifest)
    manifest.update(status='verified', runtime_suite_commands=number,
                    measured_observations=sum(not r['warmup'] for r in records),
                    raw_observations_sha256=benchmark.digest(OUTPUT / 'observations.json'))
    benchmark.write_json(OUTPUT / 'manifest.json', manifest)


if __name__ == '__main__':
    try:
        run()
    except BaseException as error:
        manifest_path = OUTPUT / 'manifest.json'
        if manifest_path.exists():
            manifest = benchmark.read_json(manifest_path)
            manifest.update(status='partial', failure=str(error))
            benchmark.write_json(manifest_path, manifest)
        raise
