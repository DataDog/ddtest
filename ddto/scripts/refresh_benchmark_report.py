#!/usr/bin/env python3
"""Combine retained Native/Orchestrion records with a new Mini measurement.

This command does not execute benchmarks. The selected references are copied
without changing a timing, memory peak, command or validation outcome. Reports
record the source and date of each variant independently.
"""
import argparse
import copy
import csv
import gzip
import json
from pathlib import Path
import shutil
import statistics

import build_benchmark as build
import benchmark_report as report

RETAINED = ('native', 'orchestrion')
RUNTIME = (*RETAINED, 'mini', 'mini-deferred')
ORACLE_FIELDS = ('case', 'cpus', 'subject', 'variant', 'flags', 'returncode',
                 'native_results', 'delivery', 'validated_contract',
                 'execution_and_event_counts_verified')


def reference_inventories(baseline, phase, records):
    path = baseline / phase / 'reference-inventories.json.gz'
    if path.exists():
        with gzip.open(path, 'rt') as source:
            records = json.load(source)
    return [{key: row[key] for key in ORACLE_FIELDS if key in row}
            for row in records if row['variant'] == 'sdk']


def retained_origins(manifest):
    previous = manifest.get('measurement_origins', {})
    return {variant: {**previous.get(variant, {
                'source_head': manifest['source_head'],
                'started_at': manifest['started_at']}), 'retained': True}
            for variant in RETAINED}


def verify_collection(directory, records, manifest, agent=False):
    metadata = build.read_json(directory / 'manifest.json')
    variants = ('mini',) if agent else ('mini', 'mini-deferred')
    repetitions = 3 if agent else 5
    cases = {'gin'} if agent else {case['id'] for case in manifest['cases']}
    expected = {(case, cpus, variant, iteration)
                for case in cases for cpus in manifest['cpus']
                for variant in variants for iteration in range(-1, repetitions)}
    actual = [(row['case'], row['cpus'], row['variant'], row['iteration']) for row in records]
    if len(actual) != len(expected) or set(actual) != expected:
        raise ValueError('incomplete or unexpected runtime collection')
    if any(row.get('source_head') != manifest['source_head'] or
           row['warmup'] != (row['iteration'] == -1) for row in records):
        raise ValueError('runtime source revision or warmup mismatch')
    if (metadata.get('source_head') != manifest['source_head'] or
        metadata.get('toolchain') != manifest['toolchain'] or
        metadata.get('commands') != len(records) or
        tuple(metadata.get('variants', ())) != variants or
        metadata.get('repetitions') != repetitions or
        metadata.get('warmups') != 1 or metadata.get('failure')):
        raise ValueError('runtime collection metadata mismatch')
    status = 'verified' if all(row['validated_contract'] for row in records) else 'partial'
    if metadata.get('status') != status:
        raise ValueError('runtime collection validation status mismatch')
    if agent and status != 'verified':
        raise ValueError('Agent delivery contract is not verified')


def compressed_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open('wb') as target:
        with gzip.GzipFile(fileobj=target, mode='wb', mtime=0) as stream:
            stream.write((json.dumps(value, separators=(',', ':'))+'\n').encode())


def merge_traces(previous, latest):
    if any(row['variant'] != 'mini' for row in latest):
        raise ValueError('unexpected variant in Mini qualification traces')
    return [row for row in previous if row['variant'] in RETAINED] + latest


def rows(path):
    with path.open(newline='') as source:
        return list(csv.DictReader(source))


def write_rows(path, values):
    with path.open('w', newline='') as target:
        writer=csv.DictWriter(target, fieldnames=build.FIELDS)
        writer.writeheader(); writer.writerows(values)


def runtime_statistics(records, manifest):
    summary={}
    for case in manifest['cases']:
        for cpus in manifest['cpus']:
            current=summary[f"{case['id']}/{cpus}"]={}
            for variant in RUNTIME:
                group=[r for r in records if r['case']==case['id'] and r['cpus']==cpus and r['variant']==variant]
                if sorted(r['iteration'] for r in group) != list(range(-1,5)):
                    raise ValueError(f"incomplete runtime group: {case['id']}/{cpus}/{variant}")
                samples=[r for r in group if not r['warmup']]
                failed=[r for r in group if not r['validated_contract']]
                unverified=[r for r in failed if 'reference unavailable' in (r.get('validated_failure') or '')]
                entry=current[variant]=dict(complete=True, valid=not failed, attempted=len(group),
                    failed=len(failed)-len(unverified),unverified=len(unverified),
                    valid_measured=sum(r['validated_contract'] for r in samples),
                    failures=sorted({r['validated_failure'] for r in failed}),
                    all_wall_s=[r['wall_s'] for r in group],
                    observed_wall_s=build.distribution([r['wall_s'] for r in samples]),
                    observed_cpu_s=build.distribution([r['cpu_s'] for r in samples]),
                    observed_peak_bytes_median=statistics.median(r['peak_bytes'] for r in samples))
                if not failed:
                    entry.update(wall_s=build.distribution([r['wall_s'] for r in samples]),
                        cpu_s=build.distribution([r['cpu_s'] for r in samples]),
                        peak_bytes_median=statistics.median(r['peak_bytes'] for r in samples),
                        event_counts=samples[0]['delivery']['event_counts'],
                        actual_test_results=sum(t[-1] for t in samples[0]['native_results']),
                        coverage_items=[r['delivery']['coverage_items'] for r in samples],
                        event_wire_bytes_median=statistics.median(r['delivery']['traffic'].get('event_wire_bytes',0) for r in samples),
                        event_uncompressed_bytes_median=statistics.median(r['delivery']['traffic'].get('event_uncompressed_bytes',0) for r in samples))
    return summary


def runtime_csv(path, records):
    fields=('case','cpus','variant','iteration','warmup','wall_s','cpu_s','user_s','system_s',
            'peak_bytes','returncode','process_tree_drained','validated_contract','validated_failure')
    with path.open('w',newline='') as target:
        writer=csv.DictWriter(target,fieldnames=fields);writer.writeheader()
        for row in records: writer.writerow({field:row.get(field) for field in fields})


def runtime_readme(directory, manifest, summary):
    lines=['# Runtime comparisons','',
           'Five measured executions follow one warmup at each CPU count. All package binaries',
           'come from the original subject sources, without the build-edit markers.',
           'Timings include startup, concurrent tests, settings and final delivery to loopback HTTP.',
           'Receiver setup, decoding and compilation are outside the timed scope.','',
           'Native and Orchestrion observations retain their original dates. Mini and Mini deferred',
           'use the new source revision. Test inventories match Native; CI inventories use the',
           'recorded SDK reference. Native Examples absent from that reference are checked against',
           'the executable function/source table and added explicitly.','',
           "Orchestrion's percentage compares time against Native. Mini's percentages compare",
           'Orchestrion first, then Native. Positive values mean more time; negative values mean less.','',
           report.comparison_table(manifest,summary,'wall_s'),
           '## CPU, memory and dispersion','',
           '| Case | CPUs | Variant | Median (s) | Min-max (s) | CPU median (CPU-s) | Memory median (MiB) | Validation |',
           '| --- | ---: | --- | ---: | --- | ---: | ---: | --- |']
    for key,cell in summary.items():
        case,cpus=key.split('/')
        for variant,entry in cell.items():
            observed=entry['observed_wall_s']
            status='verified' if entry['valid'] else ('failed' if entry['failed'] else 'unverified')
            cpu=f"{entry['observed_cpu_s']['median']:.6f}"
            memory=f"{entry['observed_peak_bytes_median']/2**20:.2f}"
            lines.append(f"| {case} | {cpus} | {variant} | {observed['median']:.6f} | {observed['min']:.6f}-{observed['max']:.6f} | {cpu} | {memory} | {status} |")
    lines+=['','## Validation gaps','']
    gaps=[]
    for key,cell in summary.items():
        for variant,entry in cell.items():
            if not entry['valid']: gaps.append(f"- {key}/{variant}: "+'; '.join(entry['failures']))
    lines+=gaps or ['All repetitions passed the recorded validation contracts.']
    lines+=['','The observed-time table keeps successful and failed durations. Failed or unverified',
            'groups have no comparative median in the main table. Individual CPU and memory values',
            'remain in observations.csv. No slow observation is removed.','']
    (directory/'runtime/README.md').write_text('\n'.join(lines))


def merge(args):
    baseline=args.baseline.resolve()
    fresh=args.build.resolve()
    output=args.output.resolve()
    if output.exists(): raise ValueError('refusing to overwrite a dataset')
    report.verify_archive(baseline)
    old=build.read_json(baseline/'build/manifest.json')
    new=build.read_json(fresh/'manifest.json')
    if new['status']!='verified' or build.variants(new)!=('mini',):
        raise ValueError('Mini build and output qualification are incomplete')
    for key in ('toolchain','cpus','cases','repetitions','sdk_version','sdk_commit'):
        if old[key]!=new[key]: raise ValueError('baseline input changed: '+key)
    previous, _ = build.observations(baseline/'build',old)
    latest, _ = build.observations(fresh,new)
    retained=[r for r in previous if r['variant'] in RETAINED]
    combined=retained+[r for r in latest if r['variant']=='mini']
    shutil.copytree(baseline,output)
    # Keep the prior collection metadata dated, outside the new summaries.
    retained_metadata = output / 'provenance/retained-metadata'
    retained_metadata.mkdir(exist_ok=True)
    for name in ('verification.json', 'runtime/collection.json', 'build/manifest.json', 'agent/manifest.json'):
        source = baseline / name
        if source.exists():
            target = retained_metadata / name.replace('/', '-')
            shutil.copyfile(source, target)
    for trace in fresh.glob('*-traces.json'):
        old_trace = baseline / 'build' / trace.name
        previous_traces = build.read_json(old_trace) if old_trace.exists() else []
        build.write_json(output / 'build' / trace.name, merge_traces(previous_traces, build.read_json(trace)))
    for modules in fresh.glob('*-modules.json'):
        old_modules = baseline / 'build' / modules.name
        if old_modules.exists():
            shutil.copyfile(old_modules, retained_metadata / modules.name)
        shutil.copyfile(modules, output / 'build' / modules.name)
    write_rows(output/'build/observations.csv',combined)
    manifest=copy.deepcopy(new)
    manifest.update(variants=list(build.VARIANTS),input_source_head=old.get('input_source_head',old['source_head']),
        measurement_origins=retained_origins(old),
        raw_csv_sha256=build.digest(output/'build/observations.csv'))
    manifest['measurement_origins']['mini']={'source_head':new['source_head'],'started_at':new['started_at'],'retained':False}
    reference = manifest['measurement_origins']['native']
    manifest['completed_commands']=len(combined)
    build.write_json(output/'build/manifest.json',manifest)
    old_qualification=build.read_json(baseline/'build/final-binaries.json')
    qualification=[r for r in old_qualification if r['variant'] in RETAINED]+build.read_json(fresh/'final-binaries.json')
    build.write_json(output/'build/final-binaries.json',qualification)
    manifest['qualified_binaries'] = len(qualification)
    build.write_json(output/'build/manifest.json',manifest)
    (output/'build/methodology.md').write_text(
        '# Compile-only comparisons\n\n'
        f"Mini: `{new['source_head']}`, collected {new['started_at'][:10]}. "
        f"Native/Orchestrion: retained from {reference['started_at'][:10]} at `{reference['source_head']}`.\n\n"
        f"All variants use `{new['toolchain']}`, the recorded subject versions, flags, CPU affinity and repetition counts. "
        'Each observation includes wall time, aggregate cgroup CPU and memory, command and exit status. '
        'Cold builds start with an empty Go build cache; module downloads and the OS page cache remain warm. '
        'The other scenarios reuse unchanged output, force a fresh link, edit a reachable test body or edit an unused constant.\n\n'
        'Mini traces and symbols are checked again. Native and Orchestrion retain their original qualification. '
        'Those variants were not rerun. Dates differ, so small timing differences need the recorded ranges and uncertainty. '
        "Orchestrion's percentage compares time against Native. Mini's percentages compare Orchestrion first, then Native. All time cells are seconds.\n")
    (output/'build/notes.md').write_text('The original convergence control is retained evidence for its collection date. It is not a new host-stability check.\n')
    build.report_command(argparse.Namespace(input=output/'build',output=None,check=False))
    for v in RETAINED:
        if [r for r in rows(output/'build/observations.csv') if r['variant']==v] != [r for r in previous if r['variant']==v]:
            raise ValueError('retained build observations changed: '+v)
    with gzip.open(baseline/'runtime/validated-observations.json.gz','rt') as source: original_runtime=json.load(source)
    fresh_runtime=build.read_json(args.runtime/'observations.json')
    verify_collection(args.runtime, fresh_runtime, new)
    selected={(case['id'],cpus) for case in manifest['cases'] for cpus in manifest['cpus']}
    merged_runtime=[r for r in original_runtime if r['variant'] in RETAINED and (r['case'],r['cpus']) in selected]+fresh_runtime
    if any(r['variant'] not in ('mini','mini-deferred') for r in fresh_runtime):
        raise ValueError('runtime refresh contains an unexpected variant')
    compressed_json(output/'runtime/validated-observations.json.gz',merged_runtime)
    with gzip.open(baseline/'runtime/original-observations.jsonl.gz','rt') as source:
        original_rows=[json.loads(line) for line in source if line.strip()]
    raw_records=[row for row in original_rows if row['variant'] in RETAINED and (row['case'],row['cpus']) in selected]+fresh_runtime
    with (output/'runtime/original-observations.jsonl.gz').open('wb') as target:
        with gzip.GzipFile(fileobj=target,mode='wb',mtime=0) as stream:
            for row in raw_records:
                stream.write((json.dumps(row,separators=(',',':'))+'\n').encode())
    summary=runtime_statistics(merged_runtime,manifest)
    build.write_json(output/'runtime/statistics.json',summary)
    shutil.copyfile(args.runtime/'binaries.json', output/'runtime/binaries.json')
    old_inputs = build.read_json(baseline/'build/runtime-inputs.json')
    runtime_inputs = [row for row in old_inputs if row['variant'] in RETAINED]
    new_binaries = build.read_json(args.runtime/'binaries.json')
    for case in manifest['cases']:
        for cpus in manifest['cpus']:
            runtime_inputs.append({
                'case': case['id'], 'subject': case['subject'], 'cpus': cpus,
                'variant': 'mini', 'flags': case['flags'],
                'binaries': [row for row in new_binaries
                             if row['case'] == case['id'] and row['cpus'] == cpus]})
    build.write_json(output/'build/runtime-inputs.json', runtime_inputs)
    runtime_csv(output/'runtime/observations.csv',merged_runtime)
    build.write_json(output/'runtime/manifest.json',{'status':'verified' if all(v['valid'] for c in summary.values() for v in c.values()) else 'partial',
        'completed_cells':len(summary),'verified_cells':sum(all(v['valid'] for v in c.values()) for c in summary.values()),
        'raw_observations':len(merged_runtime),'source_head':new['source_head'],
        'measurement_origins':manifest['measurement_origins'],
        'mini_collection':build.read_json(args.runtime/'manifest.json')})
    build.write_json(output/'runtime/collection.json', {
        'scope': 'prebuilt original package binaries; receiver setup and decoding excluded',
        'telemetry_enabled': False, 'repetitions': 5, 'warmups': 1,
        'variants': list(RUNTIME), 'measurement_origins': manifest['measurement_origins'],
        'mini_collection': build.read_json(args.runtime/'manifest.json')})
    # The SDK remains an event oracle, with no SDK timing column in this dataset.
    compressed_json(output/'runtime/reference-inventories.json.gz',
                    reference_inventories(baseline, 'runtime', original_runtime))
    runtime_readme(output,manifest,summary)
    with gzip.open(baseline/'agent/observations.json.gz','rt') as source: agent=json.load(source)
    retained_agent=[r for r in agent if r['variant'] in RETAINED and r['cpus'] in manifest['cpus']]
    fresh_agent=build.read_json(args.agent/'observations.json')
    verify_collection(args.agent, fresh_agent, new, agent=True)
    if any(r['variant']!='mini' for r in fresh_agent): raise ValueError('unexpected Agent variant')
    compressed_json(output/'agent/observations.json.gz',retained_agent+fresh_agent)
    compressed_json(output/'agent/reference-inventories.json.gz',
                    reference_inventories(baseline, 'agent', agent))
    build.write_json(output/'agent/manifest.json', {
        'status': 'verified', 'source_head': new['source_head'],
        'measurement_origins': manifest['measurement_origins'],
        'cpus': manifest['cpus'], 'case': 'gin', 'repetitions': 3, 'warmups': 1,
        'telemetry_enabled': True, 'delivery': 'local EVP Agent proxy',
        'commands': len(retained_agent)+len(fresh_agent),
        'measured_observations': sum(not row['warmup'] for row in retained_agent+fresh_agent),
        'mini_collection': build.read_json(args.agent/'manifest.json')})
    agent_manifest={**manifest,'cases':[{'id':'gin','label':'Gin','flags':[]}]}
    agent_stats={key+'/agent':cell for key,cell in report.agent_summary(output,manifest['cpus']).items()}
    original_agent_stats = build.read_json(baseline/'agent/statistics.json')
    refreshed_agent_stats = {}
    for cpus in manifest['cpus']:
        samples = [row for row in fresh_agent if row['cpus'] == cpus and not row['warmup']]
        cell = {variant: original_agent_stats[str(cpus)][variant] for variant in RETAINED}
        cell['mini'] = {
            'wall_s': build.distribution([row['wall_s'] for row in samples]),
            'cpu_s': build.distribution([row['cpu_s'] for row in samples]),
            'peak_bytes_median': statistics.median(row['peak_bytes'] for row in samples),
            'event_counts': samples[0]['delivery']['event_counts'],
            'actual_test_results': sum(test[-1] for test in samples[0]['native_results']),
            'telemetry_requests': [sum(count for path, count in row['delivery']['requests'].items()
                                      if 'telemetry' in path) for row in samples],
            'event_wire_bytes_median': statistics.median(row['delivery']['traffic']['event_wire_bytes'] for row in samples)}
        refreshed_agent_stats[str(cpus)] = cell
    build.write_json(output/'agent/statistics.json', refreshed_agent_stats)
    shutil.copyfile(args.agent/'binaries.json', output/'agent/binaries.json')
    (output/'agent/README.md').write_text(
        '# Gin with local Agent delivery and telemetry\n\n'
        'Three measured runs follow one warmup at each CPU count. Native and Orchestrion retain '
        'their original observations; Mini uses the new revision. Real CI events and telemetry '
        'are sent through the local EVP/telemetry proxy.\n\n'
        "Orchestrion's percentage compares the same metric against Native. Mini's percentages "
        'compare Orchestrion first, then Native.\n\n'
        +report.comparison_table(agent_manifest,agent_stats,'wall_s','agent')
        +'\n## Memory\n\n'+report.comparison_table(agent_manifest,agent_stats,'peak_bytes','agent'))
    (output/'README.md').write_text(
        '# Benchmark data\n\n'
        f"Mini was measured at `{new['source_head']}` on {new['started_at'][:10]}. "
        f"Native and Orchestrion retain the observations from {reference['started_at'][:10]}. "
        'measurement_origins in build/manifest.json records both sources.\n\n'
        '[Comparison tables](../../benchmarks.md) cover build, runtime and memory. '
        '[Collection and regeneration](../../build-benchmarks.md) describe the commands. '
        'CSV and compressed JSON retain every observation and validation outcome. '
        'The 115-case parity records are unchanged historical evidence, dated in their own files.\n')
    collectors = args.collection_scripts or build.ROOT / 'scripts'
    parity_readme = output / 'parity/README.md'
    parity_text = parity_readme.read_text()
    if 'These are archived parity measurements' not in parity_text:
        parity_head = build.read_json(output/'parity/manifest.json')['source_head']
        heading, body = parity_text.split('\n', 1)
        parity_readme.write_text(heading + '\n\nThese are archived parity measurements at `'
                                + parity_head + '`, from the 2026-10-04/05 collection. '
                                'They were not rerun for the Mini performance refresh.\n' + body)
    for helper in ('build_benchmark.py', 'runtime_benchmark.py', 'benchmark_receiver.py', 'benchmark_runtime_measure.py'):
        shutil.copyfile(collectors / helper, output / 'provenance' / helper)
    shutil.copyfile(collectors / 'testdata/runtime-benchmark/decode.go', output / 'provenance/decode.go.txt')
    build.write_json(output/'verification.json', {
        'head': new['source_head'], 'measurement_origins': manifest['measurement_origins'],
        'retained_build_rows_unchanged': True,
        'runtime_complete_cells': len(summary),
        'runtime_verified_cells': sum(all(v['valid'] for v in cell.values()) for cell in summary.values()),
        'raw_runtime_groups': len(merged_runtime),
        'measured_runtime_groups': sum(not row['warmup'] for row in merged_runtime),
        'mini_build_observations': sum(not build.boolean(row['validation_only']) for row in latest),
        'mini_runtime_groups': len(fresh_runtime), 'mini_agent_groups': len(fresh_agent),
        'parity_refreshed': False})
    # Qualification and runtime manifests bind the retained collector copies
    # to the exact helpers used by the measured commands.
    recorded_helpers = new['source_and_tool_sha256']
    recorded_helpers = {**recorded_helpers, **build.read_json(args.runtime/'manifest.json')['helper_sha256']}
    for helper in ('build_benchmark.py', 'runtime_benchmark.py', 'benchmark_receiver.py', 'benchmark_runtime_measure.py'):
        expected_hash = next(value for name, value in recorded_helpers.items()
                             if name.endswith('/scripts/' + helper))
        if build.digest(output/'provenance'/helper) != expected_hash:
            raise ValueError('collector source differs from measured helper: ' + helper)
    archive={'schema_version':2,'source_head':new['source_head'],'measurement_origins':manifest['measurement_origins'],'files':{}}
    for path in output.rglob('*'):
        if not path.is_file() or path.suffix=='.md' or path.name=='archive.json': continue
        record={'sha256':build.digest(path)}
        if path.suffix=='.gz':
            import hashlib
            with gzip.open(path,'rb') as source: record['source_sha256']=hashlib.sha256(source.read()).hexdigest()
        archive['files'][path.relative_to(output).as_posix()]=record
    build.write_json(output/'archive.json',archive)
    report.verify_archive(output)
    print('Retained Native and Orchestrion rows are unchanged; Mini observations refreshed:',output)


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--baseline',type=Path,required=True)
    parser.add_argument('--build',type=Path,required=True)
    parser.add_argument('--runtime',type=Path,required=True)
    parser.add_argument('--agent',type=Path,required=True)
    parser.add_argument('--output',type=Path,required=True)
    parser.add_argument('--collection-scripts',type=Path,help='exact collector sources if changed after measurement')
    merge(parser.parse_args())


if __name__=='__main__':main()
