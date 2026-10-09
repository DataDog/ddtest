#!/usr/bin/env python3
"""Render all collected observations while keeping compatibility failures explicit."""
import json
from pathlib import Path
import generate_report as report
import validate_runtime_results as validator
ROOT=report.ROOT
SOURCE=ROOT/'runtime-completion'

def run():
    manifest=report.benchmark.read_json(SOURCE/'manifest.json')
    if len(manifest['completed_cells'])!=48:
        raise RuntimeError('runtime collection is incomplete')
    raw=[json.loads(line) for line in (SOURCE/'observations.jsonl').read_text().splitlines()]
    if len(raw)!=1440 or sum(not row['warmup'] for row in raw)!=1200:
        raise RuntimeError('missing original runtime repetitions')
    old=(ROOT/'runtime-continuation/observations.jsonl').read_bytes()
    if not (SOURCE/'observations.jsonl').read_bytes().startswith(old):
        raise RuntimeError('preserved original observations changed')
    build=report.benchmark.read_json(ROOT/'build-stable/manifest.json')
    for filename,expected in {**build['source_and_tool_sha256'],**manifest['helper_sha256']}.items():
        if report.benchmark.digest(Path(filename))!=expected:
            raise RuntimeError('frozen input drift: '+filename)
    validator.SOURCE=SOURCE
    validator.OUTPUT=SOURCE/'validated'
    validator.validate()
    valid=report.benchmark.read_json(validator.OUTPUT/'manifest.json')
    if valid['completed_cells']!=48 or valid['raw_observations']!=1440:
        raise RuntimeError('validation lost collected observations')
    # Preserve already verified build, parity and Agent measurements; only the
    # report is refreshed. No compilation or measured execution is repeated.
    parity=report.benchmark.read_json(ROOT/'parity/manifest.json')
    agent=report.benchmark.read_json(ROOT/'runtime-agent/manifest.json')
    if parity['status']!='verified' or parity['complete_rounds']!=12:
        raise RuntimeError('repeated parity proof missing')
    if agent['status']!='verified' or agent['commands']!=32:
        raise RuntimeError('Agent telemetry proof missing')
    lines=['# Local build, runtime and CI parity comparison','',
        f"POC head `{build['source_head']}`; {build['toolchain']}. SDK `{build['sdk_commit']}`.",
        f"Orchestrion `{build['orchestrion_version']}`.",'',
        'Collection complete: all build and runtime combinations use their original repetitions.',
        f"Runtime compatibility: {valid['verified_cells']}/48 cells pass every mode and repetition. Failed combinations remain explicit.",
        'No source, configuration, index, commit or remote state was changed. This report is local evidence.','',
        '[All compilation tables: five scenarios, 24 combinations, 4/32 CPUs](build-stable/README.md).',
        '[All runtime tables, explicit failures, CPU, memory and ranges](runtime-completion/validated/README.md).',
        '[Gin Agent delivery with telemetry](runtime-agent/README.md).',
        '[All 115 parity cases, six rounds at each CPU count](parity/README.md).','',
        '6,224 measured comparative builds; 1,200 measured original runtime groups plus 240 warmups.',
        'The Agent control adds 24 measured groups and eight warmups; 20 setup-validation groups are retained.',
        'All 1,380 parity scenario comparisons pass, plus manual, additional spans, multiple packages, fuzz, telemetry, Unix Agent and goleak fixtures.',
        'Each runtime group executes original pre-edit binaries. Test names, status and multiplicity match Native; CI inventories and coverage match the pinned SDK where the SDK run succeeds.',
        'Real gzip/MessagePack is delivered to a successful loopback receiver; startup, execution, settings and final delivery are timed together.',
        'Compilation, CLI preparation, receiver startup and offline decoding are excluded from runtime clocks. No live Datadog intake is measured.','',
        '## Compatibility failures','',
        'Four Gin race cells contain real races and are not valid complete timing comparisons.',
        '`TestMappingTime` changes `time.Local` while an SDK background writer calls `time.Now`.',
        'With atomic coverage the SDK-derived asynchronous coverage processor also exposes this race in Mini and Mini deferred.',
        'No tests were excluded, no race detector was disabled, and no failing sample was replaced.',
        'Passing Mini plain-race samples use the verified non-race SDK inventory only if the Native test inventory is identical and there is no coverage.','',
        '## Coverage and retained evidence','',
        'Go default `-covermode=set` emits aggregate Go coverage. The pinned SDK and Mini support per-test upload only in count/atomic modes.',
        'The initial temporary harness incorrectly demanded per-test uploads in set mode. Its notices are preserved and reclassified against SDK code, Native coverage percentages and CI event counts.',
        'Atomic coverage still requires real nonempty per-test uploads.',
        'The first 80-run Native stability control failed; the second 160-run control passed the original convergence thresholds. All observations and failed controls remain.',
        'Every duration, input hash, executable, stdout/stderr log, HTTP body and cgroup CPU/memory measurement is retained in its phase directory.',
        'All measured phases run serially. Four logical CPUs bind four distinct physical cores; 32 uses all threads of 16 cores.',
        'The stopped interval while awaiting budget approval contains no measurements. Collection resumed from the preserved 1,061-row prefix.',
        'Temporary local runtime helpers are under `tools/`; the build runner remains in the repository.',
        'Read ranges, CV and bootstrap median intervals when assessing small differences. Linux/amd64 evidence does not establish behavior on other platforms.','']
    (ROOT/'README.md').write_text('\n'.join(lines))
    print(json.dumps({'build_observations':6224,'runtime_groups':len(raw),'runtime_complete_cells':48,'runtime_verified_cells':valid['verified_cells'],'parity_cases':1380,'set_notice_corrections':valid['set_mode_classification_corrections'],'report':str(ROOT/'README.md')}))

if __name__=='__main__':run()
