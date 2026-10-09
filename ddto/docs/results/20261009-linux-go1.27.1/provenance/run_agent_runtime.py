#!/usr/bin/env python3
"""Supplement the gzip matrix with real CI telemetry from the Gin suite."""
from pathlib import Path

import run_runtime as runtime


def run():
    runtime.OUTPUT = runtime.ROOT / 'runtime-agent'
    build = runtime.benchmark.read_json(runtime.BUILD / 'manifest.json')
    primary = runtime.benchmark.read_json(runtime.ROOT / 'runtime/manifest.json')
    if build['status'] != 'verified' or primary['status'] != 'verified':
        raise RuntimeError('primary matrices must finish before the Agent control')
    inputs = runtime.benchmark.read_json(runtime.BUILD / 'runtime-inputs.json')
    variants = ('native', 'orchestrion', 'sdk', 'mini')
    records, number = [], 0
    manifest = dict(status='in progress', source_head=build['source_head'],
                    sdk_commit=build['sdk_commit'], cpus=[4, 32], case='gin', repetitions=3,
                    warmups=1, telemetry_enabled=True, delivery='local EVP Agent proxy',
                    external_agentless_fallback=False, verified_cpus=[])
    runtime.OUTPUT.mkdir()
    runtime.benchmark.write_json(runtime.OUTPUT / 'manifest.json', manifest)
    for cpus in (4, 32):
        for filename, expected in {**build['source_and_tool_sha256'], **primary['helper_sha256']}.items():
            if runtime.benchmark.digest(Path(filename)) != expected:
                raise RuntimeError('frozen Agent input changed: ' + filename)
        selected = {r['variant']: r for r in inputs if r['case'] == 'gin' and r['cpus'] == cpus}
        if set(selected) != set(variants):
            raise RuntimeError('Agent control binaries missing')
        baseline_native, baseline_events = None, None
        for iteration in range(-1, 3):
            offset = (iteration + 1) % len(variants)
            for variant in variants[offset:] + variants[:offset]:
                item = selected[variant]
                for binary in item['binaries']:
                    if runtime.benchmark.digest(Path(binary['path'])) != binary['sha256']:
                        raise RuntimeError('Agent control binary changed')
                    binary['cwd'] = str(Path(item['cwd']) / runtime.PACKAGE_DIRECTORIES[item['subject']][Path(binary['path']).name])
                number += 1
                row = runtime.execute(item, variant, iteration, number, agent=True)
                if baseline_native is None:
                    baseline_native = row['native_results']
                elif row['native_results'] != baseline_native:
                    raise RuntimeError('Agent actual-test inventory differs')
                if variant != 'native':
                    events = {k: row['delivery'][k] for k in ('event_counts','tests','coverage_items')}
                    if baseline_events is None:
                        baseline_events = events
                    elif events != baseline_events:
                        raise RuntimeError('Agent CI event inventory differs')
                row['execution_and_event_counts_verified'] = True
                records.append(row)
                runtime.benchmark.write_json(runtime.OUTPUT / 'observations.json', records)
        manifest['verified_cpus'].append(cpus)
        runtime.benchmark.write_json(runtime.OUTPUT / 'manifest.json', manifest)
    manifest.update(status='verified', commands=number, measured_observations=24)
    runtime.benchmark.write_json(runtime.OUTPUT / 'manifest.json', manifest)


if __name__ == '__main__':
    run()
