#!/usr/bin/env python3
"""Repeat the current parity contract without changing repository files."""
from datetime import datetime, timezone
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path('/var/tmp/dd-ci-benchmark-20261004.s81k5i3w')
WORKTREE = Path('/var/tmp/dd-ci-testing-poc-20261001/minitracer/worktree')
GO = Path('/var/tmp/dd-ci-testing-poc-20261001/minitracer/go1.27.1/bin/go')
ORCHESTRION = Path('/var/tmp/dd-ci-testing-poc-20260930/bin/orchestrion')
OUTPUT = ROOT / 'parity'
RUN_FILTER = '^Test(CIVisibility|DeferredDelivery|MiniGoleakIntegration)'
spec = importlib.util.spec_from_file_location('build_benchmark', WORKTREE / 'scripts/build_benchmark.py')
benchmark = importlib.util.module_from_spec(spec)
spec.loader.exec_module(benchmark)
sys.path.insert(0, str(WORKTREE / 'scripts'))
import parity_report


def budget():
    task = benchmark.read_json(ROOT / 'task.json')
    remaining = (datetime.fromisoformat(task['deadline']) - datetime.now(timezone.utc)).total_seconds()
    if remaining <= 40:
        raise RuntimeError('overall task deadline reached')
    if int(subprocess.check_output(['du', '-sb', str(ROOT)], text=True).split()[0]) > task['limits']['artifact_bytes']:
        raise RuntimeError('overall artifact budget reached')
    return remaining


def environment(cpus, scratch):
    names = ('PATH', 'HOME', 'USER', 'LOGNAME', 'DBUS_SESSION_BUS_ADDRESS',
             'XDG_RUNTIME_DIR', 'LANG', 'LC_ALL')
    env = {key: os.environ[key] for key in names if key in os.environ}
    env.update(PATH=str(GO.parent) + ':' + env.get('PATH', os.defpath),
        GOTOOLCHAIN='local', GOFLAGS='', GOWORK='off', GOMAXPROCS=str(cpus),
        GOPROXY='off', GOSUMDB='off', GOCACHE=str(OUTPUT / 'setup-cache'),
        TMPDIR=str(scratch), TMP=str(scratch), TEMP=str(scratch),
        ORCHESTRION_BIN=str(ORCHESTRION), PYTHONDONTWRITEBYTECODE='1')
    return env


def check_inputs():
    build = benchmark.read_json(ROOT / 'build-stable/manifest.json')
    if build['status'] != 'verified':
        raise RuntimeError('finish compile-only measurement before running parity')
    for path, sha in build['source_and_tool_sha256'].items():
        if benchmark.digest(Path(path)) != sha:
            raise RuntimeError('frozen input changed: ' + path)
    return build


def validate(path):
    rendered = parity_report.render(path)
    path.with_suffix('.md').write_text(rendered)
    groups = [(path, 65), (path.with_name(path.stem + '-deferred.json'), 17),
              (path.with_name(path.stem + '-testify.json'), 26),
              (path.with_name(path.stem + '-testify-deferred.json'), 7)]
    contract = []
    for filename, expected in groups:
        report = benchmark.read_json(filename)
        rows = report['scenarios']
        if len(rows) != expected or len({row['scenario'] for row in rows}) != expected:
            raise RuntimeError('incomplete or duplicate parity cases: ' + str(filename))
        for row in rows:
            parity_report.timing_cells(row, True)
            if (row['status'] != 'passed' or row['sdk'] != row['mini']
                    or row['sdk_exit'] != row['mini_exit']):
                raise RuntimeError('failed parity case: ' + row['scenario'])
            contract.append([filename.stem.replace(path.stem, ''), row['scenario'],
                             row['features'], row['sdk'], row['mini'],
                             row['sdk_exit'], row['mini_exit']])
    return contract


def run():
    build = check_inputs()
    if OUTPUT.exists() and any(OUTPUT.iterdir()):
        raise RuntimeError('parity output already contains observations')
    OUTPUT.mkdir(exist_ok=True)
    setup = OUTPUT / 'setup-tmp'
    setup.mkdir()
    binary = OUTPUT / 'integration.test'
    command = [str(GO), 'test', '-mod=readonly', '-c', '-o', str(binary), './integration']
    with (OUTPUT / 'setup.log').open('wb') as log:
        subprocess.run(command, cwd=WORKTREE, env=environment(4, setup),
                       stdout=log, stderr=subprocess.STDOUT, check=True,
                       timeout=min(600, budget() - 30))
    inputs = {str(p): benchmark.digest(p) for p in (ROOT / 'tools').glob('*.py')}
    inputs[str(binary)] = benchmark.digest(binary)
    manifest = dict(source_head=build['source_head'], sdk_commit=build['sdk_commit'],
        toolchain=build['toolchain'], status='in progress', rounds_per_cpu=6,
        cases_per_round=115, cpus=[4, 32], inputs_sha256=inputs,
        run_filter=RUN_FILTER,
        timing_scope='repository child clocks and true continuous 65/17-case blocks; full harness includes fixture compilation and comparisons',
        order_control='65 testing and 17 deferred cases: 3 SDK-first and 3 Mini-first; Testify cases retain repository SDK-first paired order',
        verified_rounds=[])
    benchmark.write_json(OUTPUT / 'manifest.json', manifest)
    rows, reference = [], None
    try:
        for cpus in (4, 32):
            for iteration in range(6):
                check_inputs()
                for filename, sha in inputs.items():
                    if benchmark.digest(Path(filename)) != sha:
                        raise RuntimeError('frozen parity input changed: ' + filename)
                remaining = budget()
                directory = OUTPUT / f'cpu{cpus}' / f'round{iteration}'
                directory.mkdir(parents=True)
                scratch = ROOT / 't' / f'p{os.getpid():x}-{cpus}-{iteration}'
                scratch.mkdir(parents=True)
                report = directory / 'parity.json'
                order = 'sdk-first' if iteration % 2 == 0 else 'mini-first'
                env = environment(cpus, scratch)
                env.update(PARITY_REPORT_PATH=str(report), PARITY_EXECUTION_ORDER=order)
                affinity = build['affinity'][str(cpus)]
                timeout = min(600, remaining - 30)
                unit = f'ddci-bench-parity-{os.getpid()}-{cpus}-{iteration}'
                command = ['systemd-run', '--user', '--scope', '--quiet', '--unit=' + unit,
                    f'--property=RuntimeMaxSec={timeout + 25}', 'taskset', '-c',
                    ','.join(map(str, affinity)), sys.executable,
                    str(WORKTREE / 'scripts/build_benchmark.py'), '_measure',
                    str(directory / 'harness.json'), str(timeout), str(binary),
                    '-test.v', '-test.count=1', '-test.timeout=9m',
                    '-test.run=' + RUN_FILTER]
                result = subprocess.run(command, cwd=WORKTREE / 'integration', env=env,
                    text=True, capture_output=True, timeout=timeout + 35)
                (directory / 'launcher.log').write_text(result.stdout + result.stderr)
                row = benchmark.read_json(directory / 'harness.json')
                row.update(cpus=cpus, iteration=iteration, order=order, report=str(report),
                           scope=unit, launcher_exit=result.returncode)
                if (result.returncode or row['returncode'] or row['timeout']
                        or not row['process_tree_drained'] or row['affinity'] != affinity):
                    raise RuntimeError(f'parity harness failed: CPU {cpus} round {iteration}')
                contract = validate(report)
                if reference is None:
                    reference = contract
                elif contract != reference:
                    raise RuntimeError('parity contract changed across rounds')
                row['cases_verified'] = len(contract)
                rows.append(row)
                benchmark.write_json(OUTPUT / 'observations.json', rows)
                manifest['verified_rounds'].append({'cpus': cpus, 'iteration': iteration, 'order': order})
                benchmark.write_json(OUTPUT / 'manifest.json', manifest)
                print(f'parity cpu{cpus} round{iteration} {order}: {row["wall_s"]:.3f} s; 115 cases verified', flush=True)
                budget()
        manifest.update(status='verified', complete_rounds=len(rows))
        benchmark.write_json(OUTPUT / 'manifest.json', manifest)
    except BaseException as error:
        manifest.update(status='partial', failure=str(error), complete_rounds=len(rows))
        benchmark.write_json(OUTPUT / 'manifest.json', manifest)
        raise


if __name__ == '__main__':
    run()
