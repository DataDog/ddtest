#!/usr/bin/env python3
"""Report original runtime measurements without hiding failed observations.

The initial harness incorrectly required per-test upload for Go set coverage.
The pinned SDK and Mini explicitly exclude that mode in CanCollect. Revalidate
those rows against the unchanged SDK contract and preserve the initial failure.
No measurement, test, flag, source input or race failure is replaced.
"""
from collections import defaultdict
import copy
import json
from pathlib import Path
import statistics

import generate_report as report

ROOT=report.ROOT
BUILD=ROOT/'build-stable'
SOURCE=ROOT/'runtime-continuation'
OUTPUT=SOURCE/'validated'
COVERAGE_LINE=__import__('re').compile(r'^coverage: ([0-9.]+)% of statements$',__import__('re').MULTILINE)
SET_NOTICE='coverage requested but no coverage events were delivered: '


def is_set(flags):
    if '-race' in flags:return False
    explicit=next((v.split('=',1)[1] for v in flags if v.startswith('-covermode=')),None)
    covered=any(v.startswith('-cover') for v in flags)
    return covered and explicit in (None,'set')


def stdout_coverage(row):
    result={}
    for binary in row['binaries']:
        text=Path(binary['stdout']).read_text(errors='replace')
        result[binary['binary']]=sorted(set(COVERAGE_LINE.findall(text)))
    return result


def validate():
    raw=[json.loads(line) for line in (SOURCE/'observations.jsonl').read_text().splitlines() if line.strip()]
    if len({(r['case'],r['cpus'],r['variant'],r['iteration']) for r in raw})!=len(raw):
        raise RuntimeError('duplicated original runtime observations')
    grouped=defaultdict(list)
    for r in raw:grouped[(r['case'],r['cpus'])].append(r)
    validated=[]; corrections=0
    for (case,cpus),rows in grouped.items():
        native=[r for r in rows if r['variant']=='native' and r['returncode']==0 and not r.get('failure')]
        if not native:continue
        baseline_native=native[0]['native_results']; baseline_coverage=stdout_coverage(native[0])
        events=None
        # The full SDK is the oracle; its lack of per-test coverage in set mode
        # is verified from pinned code, not inferred from Mini's output.
        oracle=next((r for r in rows if r['variant']=='sdk' and r['returncode']==0
                     and (not r.get('failure') or (is_set(r['flags']) and r['failure'].startswith(SET_NOTICE)))),None)
        reference_scope='same case and CPU count'
        covered=any(v.startswith('-cover') for v in rows[0]['flags'])
        if oracle is None and not covered:
            # A failing race oracle must not manufacture a Mini failure. The
            # already verified non-race SDK case is usable only when it has
            # exactly the same Native test inventory and no coverage payload.
            oracle=next((r for r in raw if r['variant']=='sdk' and r['subject']==rows[0]['subject']
                and r['cpus']==cpus and not r.get('failure') and r['returncode']==0
                and not any(v.startswith('-cover') for v in r['flags']) and '-race' not in r['flags']
                and r['native_results']==baseline_native and r['delivery']['coverage_items']==0),None)
            reference_scope='verified non-race SDK case with identical Native test inventory and zero coverage'
        if oracle and 'delivery' in oracle:
            events={k:oracle['delivery'][k] for k in ('event_counts','tests','coverage_items')}
        for source in rows:
            r=copy.deepcopy(source)
            r['original_failure']=r.get('failure')
            r['sdk_reference_scope']=reference_scope
            r['sdk_reference_case']=oracle['case'] if oracle else None
            failure=r.get('failure')
            corrected=failure and is_set(r['flags']) and failure.startswith(SET_NOTICE)
            eligible=not failure or corrected
            if corrected:
                if not events or events['coverage_items']!=0 or r.get('delivery',{}).get('coverage_items')!=0:
                    eligible=False
                elif stdout_coverage(r)!=baseline_coverage or not all(baseline_coverage.values()):
                    eligible=False
                    failure='Go aggregate coverage differs from Native or is absent'
            if eligible:
                if r['returncode'] or r['timeout'] or not r['process_tree_drained']:
                    failure='runtime exit, timeout or process drainage failed'
                elif r['native_results']!=baseline_native:
                    failure='actual test identities, statuses or multiplicities differ from Native'
                elif r['variant']!='native' and events is None:
                    failure='SDK reference unavailable for this failed-oracle combination'
                elif r['variant']!='native' and {k:r['delivery'][k] for k in events}!=events:
                    failure='CI event inventory, hierarchy or per-test coverage count differs from SDK'
                elif r['variant']=='native' and r['delivery']['event_counts']:
                    failure='Native unexpectedly emitted CI events'
                else:
                    failure=None
                    if corrected:
                        corrections+=1
                        r['classification_correction']='Go set mode emits aggregate coverage only; pinned SDK CanCollect excludes set. Native aggregate coverage and SDK CI inventories match.'
            r['validated_contract']=failure is None
            r['validated_failure']=failure
            validated.append(r)
    OUTPUT.mkdir(exist_ok=True)
    report.benchmark.write_json(OUTPUT/'observations.json',validated)
    build=report.benchmark.read_json(BUILD/'manifest.json')
    lines=['# Runtime comparison of original test executables','',
        'Medians in seconds, five repetitions after one warmup. All tests and original flags are retained.',
        'Compilation and CLI preparation are excluded. Each clock covers concurrent package startup, execution, settings and final delivery.',
        'Real gzip/MessagePack is sent to a successful loopback HTTP receiver. Telemetry is disabled here and checked separately in parity.',
        'Mini deferred uses the same executable. Percentages compare total Orchestrion walltime first, then Native.',
        'A failed repetition makes that variant ineligible for a valid timing comparison; failed durations remain in the raw ledger.',
        'An empty cell is pending or incomplete. `FAIL n/6` includes warmup failures. Full ranges, CPU and peak memory are retained below.',
        'Go `-cover` defaults to `set`: aggregate Go coverage works, but the pinned SDK and Mini do not send per-test coverage for that mode.',
        'The initial temporary harness incorrectly demanded a per-test upload in set mode. Original notices are preserved and revalidated against SDK and Native.',
        'Atomic coverage still requires nonempty per-test coverage payloads.',
        'If every SDK race attempt fails, a passing Mini run must match Native and the verified non-race SDK CI inventory; this fallback requires identical Native inventories and no coverage.', '',
        '| Project | Flags | CPUs | Native | Orchestrion | POC SDK | POC Mini | Mini deferred |',
        '| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |']
    stats={};completed=0;verified=0
    for case in build['cases']:
        for cpus in build['cpus']:
            current={}
            for variant in ('native','orchestrion','sdk','mini','mini-deferred'):
                observations=[r for r in validated if r['case']==case['id'] and r['cpus']==cpus and r['variant']==variant]
                samples=[r for r in observations if not r['warmup']]
                complete=sorted(r['iteration'] for r in observations)==list(range(-1,5))
                valid=complete and all(r['validated_contract'] for r in observations)
                value={'complete':complete,'valid':valid,'attempted':len(observations),
                    'failed':sum(not r['validated_contract'] and r['validated_failure']!='SDK reference unavailable for this failed-oracle combination' for r in observations),
                    'unverified':sum(r['validated_failure']=='SDK reference unavailable for this failed-oracle combination' for r in observations),
                    'valid_measured':sum(r['validated_contract'] for r in samples),
                    'failures':sorted(set(r['validated_failure'] for r in observations if r['validated_failure'])),
                    'all_wall_s':[r['wall_s'] for r in observations]}
                if valid:
                    value.update(wall_s=report.benchmark.distribution([r['wall_s'] for r in samples]),
                        cpu_s=report.benchmark.distribution([r['cpu_s'] for r in samples]),
                        peak_bytes_median=statistics.median(r['peak_bytes'] for r in samples),
                        event_counts=samples[0]['delivery']['event_counts'],
                        actual_test_results=sum(x[-1] for x in samples[0]['native_results']),
                        coverage_items=[r['delivery']['coverage_items'] for r in samples],
                        event_wire_bytes_median=statistics.median(r['delivery']['traffic'].get('event_wire_bytes',0) for r in samples),
                        event_uncompressed_bytes_median=statistics.median(r['delivery']['traffic'].get('event_uncompressed_bytes',0) for r in samples))
                current[variant]=value
            if all(v['complete'] for v in current.values()):completed+=1
            if all(v['valid'] for v in current.values()):verified+=1
            stats[f"{case['id']}/{cpus}"]=current
            native=current['native'];oracle=current['orchestrion'];values=[]
            for variant in ('native','orchestrion','sdk','mini','mini-deferred'):
                v=current[variant]
                if v['failed']:
                    text=f"FAIL {v['failed']}/{v['attempted']}"
                elif v['unverified']:
                    text=f"unverified ({v['unverified']}/6)"
                elif not v['valid']:
                    text=f"pending ({v['attempted']}/6)"
                else:
                    median=v['wall_s']['median'];text=f'{median:.6f} s'
                    if variant not in ('native','orchestrion') and oracle['valid'] and native['valid']:
                        text+=f" ({report.percentage(median,oracle['wall_s']['median'])}; {report.percentage(median,native['wall_s']['median'])})"
                    elif variant not in ('native','orchestrion') and native['valid']:
                        text+=f" (vs Orchestrion unavailable; {report.percentage(median,native['wall_s']['median'])} vs Native)"
                values.append(text)
            lines.append(f"| {case['label']} | `{report.cell(' '.join(case['flags']) or 'none')}` | {cpus} | "+' | '.join(values)+' |')
    lines+=['','## CPU, memory and dispersion','',
        '| Case | CPUs | Variant | Wall median (s) | Min–max (s) | CV | CPU median (CPU-s) | Peak median (MiB) |',
        '| --- | ---: | --- | ---: | --- | ---: | ---: | ---: |']
    for key,current in stats.items():
        case,cpus=key.split('/')
        for variant,v in current.items():
            if not v['valid']:continue
            w=v['wall_s'];lines.append(f"| {case} | {cpus} | {variant} | {w['median']:.6f} | {w['min']:.6f}–{w['max']:.6f} | {w['cv_pct']:.2f}% | {v['cpu_s']['median']:.6f} | {v['peak_bytes_median']/2**20:.2f} |")
    lines+=['','## Failures and incomplete combinations','']
    for key,current in stats.items():
        for variant,v in current.items():
            if v['failed']:lines.append(f"- {key}/{variant}: {v['failed']} failed observations: "+'; '.join(v['failures']))
    lines+=['',f'{completed}/48 cells attempted with all original repetitions; {verified}/48 cells pass every variant and repetition.',
        f'{corrections} original set-mode harness notices revalidated against the pinned SDK and Native; no test or input changed.',
        'This is Linux/amd64 loopback evidence. No live Datadog intake is measured. Chi has long built-in sleeps; race binaries include the Go race shutdown delay.','']
    (OUTPUT/'README.md').write_text('\n'.join(lines))
    report.benchmark.write_json(OUTPUT/'statistics.json',stats)
    manifest=dict(status='verified' if verified==48 else 'partial',completed_cells=completed,verified_cells=verified,
                  raw_observations=len(raw),validated_observations=len(validated),
                  set_mode_classification_corrections=corrections,
                  raw_ledger_sha256=report.benchmark.digest(SOURCE/'observations.jsonl'),
                  validator_sha256=report.benchmark.digest(Path(__file__)))
    report.benchmark.write_json(OUTPUT/'manifest.json',manifest)
    print(json.dumps(manifest,indent=2))


if __name__=='__main__':validate()
