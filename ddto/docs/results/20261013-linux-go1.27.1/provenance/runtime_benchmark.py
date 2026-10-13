#!/usr/bin/env python3
"""Refresh Mini runtime observations using qualified prebuilt test binaries.

Native and SDK test/event inventories come from the retained baseline. Neither
reference is executed. Timings include process startup, concurrent package tests,
settings and delivery to loopback HTTP; decoding and receiver setup are excluded.
"""
import argparse
from collections import Counter
import csv
from datetime import datetime, timezone
import gzip
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time

import build_benchmark as build
from benchmark_receiver import Receiver

RESULT = re.compile(r'^\s*--- (PASS|SKIP|FAIL): (.+) \([-]?[0-9.]+s\)$', re.MULTILINE)
PACKAGES = {
    'gin': {'binding.test': 'binding', 'bytesconv.test': 'internal/bytesconv',
            'fs.test': 'internal/fs', 'gin.test': '.', 'ginS.test': 'ginS', 'render.test': 'render'},
    'chi': {'chi.test': '.', 'middleware.test': 'middleware'},
    'testify-direct': {'testify-direct.test': '.'},
    'testify-external': {'testify-external.test': '.'},
}
CONTRACT = ('event_counts', 'tests', 'coverage_items')


def load_baseline(directory, agent=False):
    filename = 'agent/observations.json.gz' if agent else 'runtime/validated-observations.json.gz'
    with gzip.open(directory / filename, 'rt') as source:
        records = json.load(source)
    references = directory / ('agent/reference-inventories.json.gz' if agent else 'runtime/reference-inventories.json.gz')
    if references.exists():
        with gzip.open(references, 'rt') as source:
            records.extend(json.load(source))
    return records


def references(records, case, cpus, subject, flags):
    group = [r for r in records if r.get('case', 'gin') == case and r['cpus'] == cpus]
    native = next(r for r in group if r['variant'] == 'native' and r['returncode'] == 0)
    oracle = next((r for r in group if r['variant'] == 'sdk' and r['returncode'] == 0
                   and (r.get('validated_contract') or r.get('execution_and_event_counts_verified'))), None)
    scope = 'same archived case and CPU count'
    # Preserve the archived validator's restricted non-race fallback.
    if oracle is None and not any(f.startswith('-cover') for f in flags):
        oracle = next((r for r in records if r['variant'] == 'sdk' and r['cpus'] == cpus
                       and r.get('subject') == subject and r['returncode'] == 0
                       and r.get('validated_contract') and '-race' not in r['flags']
                       and not any(f.startswith('-cover') for f in r['flags'])
                       and r['native_results'] == native['native_results']
                       and r['delivery']['coverage_items'] == 0), None)
        scope = 'archived non-race SDK with identical Native inventory and no coverage'
    return native, oracle, scope


def expected_delivery(oracle, native_results, binaries, go):
    """Add native Examples absent from the older SDK's CI inventory.

    Identity comes from the qualified executable's function/source table. Test
    status and multiplicity come from the retained Native run. Existing SDK
    test identities and counters remain unchanged.
    """
    import copy
    delivery = copy.deepcopy({k: oracle['delivery'][k] for k in CONTRACT})
    examples = {(binary, name): (status.lower(), count)
                for binary, name, status, count in native_results if name.startswith('Example')}
    already = {row[2] for row in delivery['tests']}
    missing = {key: value for key, value in examples.items() if key[1] not in already}
    additions = []
    for binary in binaries:
        names = {name for filename, name in missing if filename == Path(binary['path']).name}
        if not names:
            continue
        pattern = '|'.join(r'\.' + re.escape(name) + r'$' for name in sorted(names))
        text = subprocess.check_output([str(go), 'tool', 'objdump', '-s', pattern, binary['path']], text=True)
        for line in text.splitlines():
            match = re.match(r'^TEXT (.+)\.(Example\w*)\(SB\) (.+)$', line)
            if not match or match[2] not in names:
                continue
            status, count = missing[(Path(binary['path']).name, match[2])]
            additions.append([match[1], Path(match[3]).name, match[2], status, count])
    if len(additions) != len(missing):
        raise ValueError('qualified executable does not resolve every Native example')
    suites = {tuple(row[:2]) for row in delivery['tests']}
    modules = {row[0] for row in delivery['tests']}
    delivery['event_counts']['test'] += sum(row[-1] for row in additions)
    delivery['event_counts']['test_suite_end'] += len({tuple(row[:2]) for row in additions} - suites)
    delivery['event_counts']['test_module_end'] += len({row[0] for row in additions} - modules)
    delivery['tests'] = sorted(delivery['tests'] + additions)
    return delivery, additions


class Runner:
    def __init__(self, args):
        self.args = args
        self.started = time.monotonic()
        self.output = args.output.resolve()
        if self.output.exists() or Path('/tmp') in self.output.parents or build.ROOT in self.output.parents:
            raise ValueError('use a new artifact directory outside /tmp and the repository')
        self.output.mkdir(parents=True)
        self.manifest = build.read_json(args.build / 'manifest.json')
        if self.manifest['status'] != 'verified' or build.variants(self.manifest) != ('mini',):
            raise ValueError('Mini compilation and binary qualification must pass first')
        self.qualified = build.read_json(args.build / 'final-binaries.json')
        self.go = next(Path(p) for p in self.manifest['source_and_tool_sha256'] if p.endswith('/bin/go'))
        self.decoder = self.output / 'decode'
        env = dict(os.environ, GOTOOLCHAIN='local', GOWORK='off', GOFLAGS='', GOPROXY='off',
                   GOCACHE=str(self.output / 'setup-cache'), TMPDIR=str(self.output))
        subprocess.run([str(self.go), 'build', '-mod=readonly', '-o', str(self.decoder),
                        './scripts/testdata/runtime-benchmark/decode.go'], cwd=build.ROOT, env=env,
                       check=True, timeout=600)
        # Build from the untouched subject sources. Qualification binaries contain
        # the reachable-edit marker and must not be used for runtime measurements.
        self.qualified = self.prepare_binaries(env)
        self.baseline = load_baseline(args.baseline, args.agent)
        self.records = []
        self.number = 0
        self.helpers = {str(p): build.digest(p) for p in [Path(__file__),
                        build.ROOT/'scripts/benchmark_receiver.py',
                        build.ROOT/'scripts/benchmark_runtime_measure.py', self.decoder]}

    def prepare_binaries(self, env):
        records = []
        cache = self.output / 'runtime-build-cache'
        with (self.args.build / 'observations.csv').open(newline='') as source:
            row = next(r for r in csv.DictReader(source) if r['variant'] == 'mini')
        driver = Path(json.loads(row['command'])[0])
        if str(driver) not in self.manifest['source_and_tool_sha256'] or driver.parent != self.args.build:
            raise ValueError('recorded Mini driver is outside its qualified build')
        cases = [case for case in self.manifest['cases']
                 if (not self.args.case or case['id'] in self.args.case)
                 and (not self.args.agent or case['id'] == 'gin')]
        if not cases or (self.args.case and set(self.args.case) - {c['id'] for c in self.manifest['cases']}):
            raise ValueError('unknown or empty runtime case selection')
        cpus = self.args.cpus or self.manifest['cpus']
        if set(cpus) - set(self.manifest['cpus']):
            raise ValueError('runtime CPUs were not qualified by the build matrix')
        for case in cases:
            for count in cpus:
                destination = self.output / 'binaries' / f"{case['id']}-cpu{count}"
                destination.mkdir(parents=True)
                command = [str(driver), 'test', '--runtime=mini', f'-p={count}',
                           '-mod=readonly', '-c', '-o', str(destination)+os.sep,
                           '-ldflags=-w', *case['flags'], './...']
                setup_env = dict(env, GOCACHE=str(cache), GOMAXPROCS=str(count),
                                 PATH=str(self.go.parent)+os.pathsep+env.get('PATH',os.defpath),
                                 DD_CIVISIBILITY_ENABLED='parent', DD_INSTRUMENTATION_TELEMETRY_ENABLED='false')
                result = subprocess.run(command, cwd=self.args.build/'subjects'/case['subject'],
                                        env=setup_env, text=True, capture_output=True,
                                        timeout=min(600,self.budget()))
                (destination/'build.log').write_text(result.stdout+result.stderr)
                if result.returncode:
                    raise RuntimeError('original runtime binary build failed: '+case['id'])
                for binary in destination.glob('*.test'):
                    symbols = subprocess.check_output([str(self.go),'tool','nm',str(binary)], text=True)
                    hook = 'github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting.instrumentTestingMWithControl'
                    if hook not in symbols or b'ddci-real-test-body-edit-999' in binary.read_bytes():
                        raise ValueError('runtime executable is uninstrumented or contains an edit marker')
                    records.append(dict(case=case['id'],cpus=count,variant='mini',path=str(binary),
                                        sha256=build.digest(binary),bytes=binary.stat().st_size))
                expected = {Path(b['path']).name for b in self.qualified if b['case']==case['id'] and b['cpus']==count}
                if expected != {b.name for b in destination.glob('*.test')}:
                    raise ValueError('runtime binary set differs from the qualified build')
        build.write_json(self.output/'binaries.json',records)
        return records

    def budget(self):
        remaining = self.args.max_seconds - (time.monotonic() - self.started)
        if remaining < 40:
            raise RuntimeError('runtime time limit reached; observations retained')
        size = int(subprocess.check_output(['du', '-sb', str(self.output)], text=True).split()[0])
        if size > self.args.max_gib * 1024**3:
            raise RuntimeError('runtime artifact limit reached; observations retained')
        return remaining

    def check_inputs(self):
        for filename, expected in {**self.manifest['source_and_tool_sha256'], **self.helpers}.items():
            if build.digest(Path(filename)) != expected:
                raise ValueError('frozen input changed: ' + filename)
        for record in self.qualified:
            if build.digest(Path(record['path'])) != record['sha256']:
                raise ValueError('qualified binary changed: ' + record['path'])

    def environment(self, cpus, receiver, scratch, flags, deferred):
        names = ('PATH', 'HOME', 'USER', 'LOGNAME', 'DBUS_SESSION_BUS_ADDRESS',
                 'XDG_RUNTIME_DIR', 'LANG', 'LC_ALL')
        env = {name: os.environ[name] for name in names if name in os.environ}
        coverage = scratch/'coverage'; coverage.mkdir()
        env.update(PATH=str(self.go.parent)+os.pathsep+env.get('PATH', os.defpath),
            GOMAXPROCS=str(cpus), GOTOOLCHAIN='local', GOFLAGS='', GOWORK='off',
            TMPDIR=str(scratch), TMP=str(scratch), TEMP=str(scratch), XDG_CACHE_HOME=str(scratch),
            GOCOVERDIR=str(coverage), DD_CIVISIBILITY_ENABLED='true',
            DD_CIVISIBILITY_AGENTLESS_ENABLED='true', DD_CIVISIBILITY_AGENTLESS_URL=receiver.url,
            DD_TRACE_AGENT_URL=receiver.url, DD_API_KEY='local-benchmark-placeholder',
            DD_SERVICE='dd-ci-local-benchmark', DD_ENV='benchmark', DD_TEST_SESSION_NAME='benchmark',
            DD_GIT_REPOSITORY_URL='https://github.com/tonyredondo/dd-ci-testing-poc.git',
            DD_GIT_COMMIT_SHA='1111111111111111111111111111111111111111',
            DD_CIVISIBILITY_FLAKY_RETRY_ENABLED='false',
            DD_CIVISIBILITY_EARLY_FLAKE_DETECTION_ENABLED='false',
            DD_CIVISIBILITY_GIT_UPLOAD_ENABLED='false',
            DD_CIVISIBILITY_CODE_COVERAGE_REPORT_UPLOAD_ENABLED='false',
            DD_CIVISIBILITY_CODE_COVERAGE_ENABLED=str(any(f.startswith('-cover') for f in flags)).lower(),
            DD_CIVISIBILITY_DEFERRED_DELIVERY=str(deferred).lower(),
            DD_CIVISIBILITY_LOGS_ENABLED='false', DD_INSTRUMENTATION_TELEMETRY_ENABLED='false',
            DD_REMOTE_CONFIGURATION_ENABLED='false', DD_PROFILING_ENABLED='false',
            DD_APPSEC_ENABLED='false', DD_TRACE_STARTUP_LOGS='false', GOPROXY='off', GOSUMDB='off')
        if self.args.agent:
            env.update(DD_CIVISIBILITY_AGENTLESS_ENABLED='false', DD_API_KEY='',
                       DD_INSTRUMENTATION_TELEMETRY_ENABLED='true')
        return env

    def execute(self, case, cpus, variant, iteration):
        remaining = self.budget()
        self.number += 1
        label = f"{case['id']}-cpu{cpus}-{variant}-round{iteration}"
        directory = self.output/'rows'/label; directory.mkdir(parents=True)
        # Gin's Unix socket tests need short scratch paths (sun_path <=108 bytes).
        scratch = self.args.build.parent/'t'/f'{os.getpid():x}-{self.number:x}'
        scratch.mkdir(parents=True)
        receiver = Receiver(directory/'receiver', any(f.startswith('-cover') for f in case['flags']))
        affinity = self.manifest['affinity'][str(cpus)]
        timeout = min(600, remaining-30)
        binaries = []
        for b in self.qualified:
            if b['case'] == case['id'] and b['cpus'] == cpus:
                binary = Path(b['path'])
                cwd = self.args.build/'subjects'/case['subject']/PACKAGES[case['subject']][binary.name]
                binaries.append({'path': str(binary), 'cwd': str(cwd)})
        expected = self.manifest['source_head']
        if not binaries:
            raise ValueError('qualified binaries missing: '+label)
        configuration = dict(output=str(directory/'measurement.json'), timeout=timeout,
                             binaries=binaries, cpus=cpus,
                             environment=self.environment(cpus, receiver, scratch, case['flags'], variant=='mini-deferred'))
        build.write_json(directory/'input.json', configuration)
        unit = f'ddci-bench-runtime-{os.getpid()}-{self.number}'
        command = ['systemd-run', '--user', '--scope', '--quiet', '--unit='+unit,
                   f'--property=RuntimeMaxSec={timeout+25}', 'taskset', '-c',
                   ','.join(map(str,affinity)), sys.executable,
                   str(build.ROOT/'scripts/benchmark_runtime_measure.py'), str(directory/'input.json')]
        try:
            result = subprocess.run(command, capture_output=True, text=True,
                                    env=configuration['environment'], timeout=timeout+35)
        finally:
            receiver.close()
        (directory/'launcher.log').write_text(result.stdout+result.stderr)
        row = build.read_json(directory/'measurement.json')
        counts = Counter()
        race = False
        for binary in row['binaries']:
            counts.update((binary['binary'], name, status) for status,name in RESULT.findall(Path(binary['stdout']).read_text(errors='replace')))
            race |= 'DATA RACE' in Path(binary['stderr']).read_text(errors='replace')
        row.update(case=case['id'], subject=case['subject'], cpus=cpus, variant=variant,
                   flags=case['flags'], iteration=iteration, warmup=iteration==-1,
                   native_results=[list(k)+[v] for k,v in sorted(counts.items())],
                   delivery=receiver.decoded(self.decoder), source_head=expected,
                   delivery_profile='agent-telemetry' if self.args.agent else 'agentless-gzip')
        native,oracle,scope = references(self.baseline,case['id'],cpus,case['subject'],case['flags'])
        expected_events, additions = expected_delivery(oracle, native['native_results'], binaries, self.go) if oracle else (None, [])
        failure = None
        if race or result.returncode or row['returncode'] or row['timeout'] or not row['process_tree_drained']:
            failure='test process failed, raced, timed out or did not drain'
        elif row['affinity'] != affinity or row['native_results'] != native['native_results']:
            failure='CPU affinity or Native test inventory differs'
        elif not row['delivery']['event_counts'].get('test'):
            failure='no test events delivered'
        elif oracle is None:
            failure='historical SDK reference unavailable for this combination'
        elif {k:row['delivery'][k] for k in CONTRACT} != expected_events:
            failure='CI event inventory, hierarchy or coverage count differs from the archived SDK'
        elif self.args.agent and not any('telemetry' in p for p in row['delivery']['requests']):
            failure='no local telemetry request arrived'
        row.update(validated_contract=failure is None, validated_failure=failure,
                   execution_and_event_counts_verified=failure is None,
                   sdk_reference_scope=scope, sdk_reference_case=oracle['case'] if oracle else None,
                   native_examples_added_to_reference=additions)
        build.write_json(directory/'result.json',row)
        with (self.output/'observations.jsonl').open('a') as ledger: ledger.write(json.dumps(row)+'\n')
        self.records.append(row)
        print(f"{label}: {row['wall_s']:.6f} s; {row['cpu_s']:.6f} CPU-s; {failure or 'verified'}",flush=True)
        self.budget()

    def run(self):
        selected = [c for c in self.manifest['cases'] if not self.args.case or c['id'] in self.args.case]
        if self.args.agent: selected=[c for c in selected if c['id']=='gin']
        counts=self.args.cpus or self.manifest['cpus']
        variants=('mini',) if self.args.agent else ('mini','mini-deferred')
        repetitions=3 if self.args.agent else 5
        metadata=dict(source_head=self.manifest['source_head'],toolchain=self.manifest['toolchain'],
                      variants=variants,repetitions=repetitions,warmups=1,status='in progress',
                      baseline=str(self.args.baseline),started_at=datetime.now(timezone.utc).isoformat(),
                      helper_sha256=self.helpers,baseline_sha256=build.digest(self.args.baseline/('agent/observations.json.gz' if self.args.agent else 'runtime/validated-observations.json.gz')))
        build.write_json(self.output/'manifest.json',metadata)
        try:
            for case in selected:
                for cpus in counts:
                    self.check_inputs()
                    for iteration in range(-1,repetitions):
                        for variant in build.rotated(iteration+1,variants): self.execute(case,cpus,variant,iteration)
                    build.write_json(self.output/'observations.json',self.records)
            metadata.update(status='verified' if all(r['validated_contract'] for r in self.records) else 'partial',
                            commands=self.number,elapsed_seconds=time.monotonic()-self.started)
        except BaseException as error:
            metadata.update(status='partial',failure=str(error),commands=self.number)
            raise
        finally:
            build.write_json(self.output/'observations.json',self.records)
            build.write_json(self.output/'manifest.json',metadata)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--build',type=Path,required=True)
    parser.add_argument('--baseline',type=Path,required=True)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--case',action='append')
    parser.add_argument('--cpus',type=build.cpu_counts)
    parser.add_argument('--agent',action='store_true')
    parser.add_argument('--max-seconds',type=build.positive,required=True)
    parser.add_argument('--max-gib',type=build.positive,required=True)
    Runner(parser.parse_args()).run()


if __name__=='__main__': main()
