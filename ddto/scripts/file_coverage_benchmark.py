"""Compare per-test coverage modes: Go's counters, which Test Impact Analysis
uses today, against ddto's experimental file coverage.

usage: file_coverage_benchmark.py --ddto DDTO --output DIR [--reps N]
           [--gomaxprocs N] [--bench NAME=REGEX]... [--report DIR]
           NAME=PROJECT[:PATTERN]...

For each project, every mode runs go test once to warm the build cache, then
--reps times in rotating order. The loopback receiver enables code coverage,
and the coverage modes' per-test files are compared with Go's counters, the
reference. --bench also times the benchmarks of project NAME that REGEX
matches. --report copies results.json and README.md into DIR, such as a
directory of docs/results; never edit those by hand. Each test's files stay in
the output's files.json.
"""
import argparse
import email.parser
import email.policy
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import statistics
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parent))
from benchmark_receiver import Receiver, decode  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent
COVERAGE = ['-cover', '-covermode=atomic', '-coverpkg=./...']
MODES = {
    # name: (label, ddto flags or None for plain go test, receiver coverage)
    'native': ('go test', None, False),
    'none': ('ddto, no per-test coverage', [], False),
    'counters': ("ddto, Go's counters", COVERAGE, True),
    'marks': ('ddto, file marks', ['--experimental-file-coverage'], True),
    'context': ('ddto, file marks =context', ['--experimental-file-coverage=context'], True),
}
COVERED = [mode for mode, (_, _, coverage) in MODES.items() if coverage]


def run_tests(args, project, pattern, mode, output):
    """Runs one go test and returns its wall time and, with coverage, each
    test's covered files."""
    _, flags, coverage = MODES[mode]
    receiver = Receiver(output, coverage)
    env = {k: v for k, v in os.environ.items() if not k.startswith('DD_')}
    env.update(GOWORK='off', GOFLAGS='-buildvcs=false', GOMAXPROCS=str(args.gomaxprocs),
               DD_SERVICE='file-coverage', DD_ENV='benchmark', DD_CIVISIBILITY_ENABLED='true',
               DD_CIVISIBILITY_AGENTLESS_ENABLED='false', DD_TRACE_AGENT_URL=receiver.url,
               DD_INSTRUMENTATION_TELEMETRY_ENABLED='false', DD_CIVISIBILITY_GIT_UPLOAD_ENABLED='false')
    command = (['go', 'test'] if flags is None else [str(args.ddto), 'test', *flags]) + ['-count=1', pattern]
    start = time.perf_counter()
    result = subprocess.run(command, cwd=project, env=env, capture_output=True, text=True, timeout=3600)
    wall = time.perf_counter() - start
    receiver.close()
    if result.returncode:
        raise SystemExit(f'{" ".join(command)} in {project}: exit {result.returncode}\n{result.stdout[-4000:]}\n{result.stderr[-4000:]}')
    check_mode(command, result.stderr)
    return wall, (per_test_files(args.decoder, receiver, project) if coverage else None)


def check_mode(command, stderr):
    """Rejects a run in which ddto fell back from the requested mode: it
    warns and builds another coverage mode, or runs go test without CI
    Visibility."""
    for line in stderr.splitlines():
        if 'WARN: --experimental-file-coverage' in line or 'running go test without CI Visibility' in line:
            raise SystemExit(f'{" ".join(command)} did not install the requested mode: {line}')


def per_test_files(decoder, receiver, project):
    """Returns {suite/name: [files]} from the receiver's test events and
    coverage payloads, with paths relative to the project."""
    names, files = {}, {}
    for request in receiver.requests:
        raw = (receiver.output / request['file']).read_bytes()
        body = gzip.decompress(raw) if request['encoding'] == 'gzip' else raw
        if request['path'].endswith('/citestcycle'):
            for event in decode(decoder, body).get('events', []):
                if event.get('type') == 'test':
                    content = event['content']
                    meta = content.get('meta', {})
                    names[content['span_id']] = meta.get('test.suite', '') + '/' + meta.get('test.name', '')
        elif request['path'].endswith('/citestcov'):
            message = email.parser.BytesParser(policy=email.policy.default).parsebytes(
                ('Content-Type: ' + request['content_type'] + '\r\n\r\n').encode() + body)
            for part in message.iter_parts():
                if part.get_param('name', header='content-disposition') == 'coveragex':
                    for row in decode(decoder, part.get_payload(decode=True)).get('coverages', []):
                        files.setdefault(row['span_id'], set()).update(f['filename'] for f in row.get('files', []))
    out = {}
    for span, covered in files.items():
        if span in names:
            out[names[span]] = sorted(relative(project, f) for f in covered)
    if not out:
        raise SystemExit(f'{project}: no per-test coverage arrived')
    return out


def relative(project, name):
    path = Path(name)
    if path.is_absolute():
        try:
            return path.relative_to(project).as_posix()
        except ValueError:
            return name
    return name


def production(files):
    return {f for f in files if not f.endswith('_test.go')}


def compare(rows, reference):
    """Returns the per-test metrics of each mode's rows over reference's
    tests, and the tests that lack a production file of reference's. A test
    without a row has no files."""
    tests = sorted(reference)
    every = sorted({f for r in rows.values() for test in tests for f in production(r.get(test, []))})
    out = {}
    for mode, r in rows.items():
        files = {test: production(r.get(test, [])) for test in tests}
        missing = {test: sorted(production(reference[test]) - files[test])
                   for test in tests if not production(reference[test]) <= files[test]}
        out[mode] = {
            'tests': len(tests),
            'tests_without_rows': sum(1 for test in tests if test not in r),
            'files_per_test': round(statistics.mean(len(files[t]) for t in tests), 2) if tests else 0,
            'tests_per_changed_file': round(statistics.mean(sum(1 for t in tests if f in files[t]) for f in every), 1) if every else 0,
            'missing_tests': len(missing),
            'missing': dict(list(missing.items())[:10]),
        }
    return out


def run_benchmarks(args, project, regex, output):
    """Times the benchmarks that regex matches under native go test, Go's
    counters, file marks and context file marks, in ns/op."""
    results = {}
    for mode in ('native', 'counters', 'marks', 'context'):
        _, flags, _ = MODES[mode]
        receiver = Receiver(output / f'bench-{mode}', False)
        env = {k: v for k, v in os.environ.items() if not k.startswith('DD_')}
        env.update(GOWORK='off', GOFLAGS='-buildvcs=false', GOMAXPROCS=str(args.gomaxprocs),
                   DD_CIVISIBILITY_ENABLED='true', DD_TRACE_AGENT_URL=receiver.url,
                   DD_CIVISIBILITY_AGENTLESS_ENABLED='false', DD_INSTRUMENTATION_TELEMETRY_ENABLED='false',
                   DD_CIVISIBILITY_GIT_UPLOAD_ENABLED='false')
        command = (['go', 'test'] if flags is None else [str(args.ddto), 'test', *flags]) + [
            '-run=^$', '-bench=' + regex, '-benchtime=1s', '-count=5', '.']
        result = subprocess.run(command, cwd=project, env=env, capture_output=True, text=True, timeout=3600)
        receiver.close()
        if result.returncode:
            raise SystemExit(f'{" ".join(command)}: exit {result.returncode}\n{result.stdout[-4000:]}\n{result.stderr[-4000:]}')
        check_mode(command, result.stderr)
        samples = {}
        for line in result.stdout.splitlines():
            match = re.match(r'^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+([\d.]+) ns/op', line)
            if match:
                samples.setdefault(match.group(1), []).append(float(match.group(2)))
        results[mode] = {name: round(statistics.median(values), 1) for name, values in samples.items()}
    return results


def describe(project):
    head = subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=project, capture_output=True, text=True).stdout.strip()
    url = subprocess.run(['git', 'remote', 'get-url', 'origin'], cwd=project, capture_output=True, text=True).stdout.strip()
    diff = subprocess.run(['git', 'diff', 'HEAD'], cwd=project, capture_output=True).stdout
    return {'origin': url, 'commit': head, 'local_diff_sha256': hashlib.sha256(diff).hexdigest() if diff else '',
            'local_diff_files': subprocess.run(['git', 'diff', '--name-only', 'HEAD'], cwd=project, capture_output=True, text=True).stdout.split()}


def report(results):
    lines = ['# Per-test coverage modes', '',
             'Generated by `scripts/file_coverage_benchmark.py`; do not edit by hand.', '',
             f"ddto {results['ddto']['commit']} ({results['ddto']['version']}), {results['go']}, "
             f"GOMAXPROCS={results['gomaxprocs']}, {results['reps']} runs per mode after a warm-up run.", '',
             "Go's counters (`-cover -covermode=atomic -coverpkg=./...`) are what Test Impact Analysis uses "
             "today, and the reference for missing files: each run compares with the same repetition's Go's "
             "counters, over every test that they report; a test without a row has no files. Production files "
             'exclude `_test.go` files. "Tests per changed file" is the mean number of tests that a change to '
             'one production file selects. The other columns come from the first repetition, and so do the '
             'listed missing files.', '']
    for name, project in results['projects'].items():
        lines += [f"## {name}", '', f"`{project['origin']}` at `{project['commit'][:12]}`, package `{project['pattern']}`."]
        if project['local_diff_files']:
            lines += ['', f"Local changes: {', '.join(project['local_diff_files'])} (sha256 {project['local_diff_sha256'][:12]})."]
        lines += ['', '| Mode | Wall time (median) | Production files per test | Tests per changed file | Tests missing a file of Go\'s counters, per run |',
                  '| --- | --- | --- | --- | --- |']
        for mode, (label, _, coverage) in MODES.items():
            walls = project['walls'][mode]
            wall = f'{statistics.median(walls):.2f} s'
            if coverage:
                m = project['comparison'][mode]
                runs = ', '.join(str(n) for n in project['missing_tests_per_run'][mode])
                lines.append(f"| {label} | {wall} | {m['files_per_test']} | {m['tests_per_changed_file']} | {runs} of {m['tests']} |")
            else:
                lines.append(f'| {label} | {wall} | — | — | — |')
        for mode in COVERED:
            missing = project['comparison'][mode]['missing']
            if mode != 'counters' and missing:
                lines += ['', f"{MODES[mode][0]}: tests missing a file of Go's counters (first {len(missing)}):", '']
                lines += [f"- `{test}`: {', '.join(f'`{f}`' for f in files)}" for test, files in missing.items()]
        if project.get('benchmarks'):
            bench = project['benchmarks']
            names = sorted(bench['native'])
            lines += ['', '| Benchmark (ns/op) | go test | Go\'s counters | File marks | File marks =context |', '| --- | --- | --- | --- | --- |']
            for name in names:
                lines.append('| `' + name + '` | ' + ' | '.join(str(bench[m].get(name, '—')) for m in ('native', 'counters', 'marks', 'context')) + ' |')
        lines.append('')
    return '\n'.join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--ddto', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--reps', type=int, default=3)
    parser.add_argument('--gomaxprocs', type=int, default=4)
    parser.add_argument('--bench', action='append', default=[])
    parser.add_argument('--report', type=Path)
    parser.add_argument('projects', nargs='+')
    args = parser.parse_args()
    args.output = args.output.resolve()
    args.output.mkdir(parents=True)
    args.decoder = args.output / 'decode'
    subprocess.run(['go', 'build', '-o', str(args.decoder), './scripts/testdata/runtime-benchmark/decode.go'],
                   cwd=ROOT, check=True, env=dict(os.environ, GOWORK='off', GOFLAGS=''))
    version = subprocess.run([str(args.ddto), '--version'], capture_output=True, text=True).stdout.strip()
    benches = dict(spec.partition('=')[::2] for spec in args.bench)
    every = {}
    results = {'ddto': {'commit': subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=ROOT, capture_output=True, text=True).stdout.strip(),
                        'version': version},
               'go': subprocess.run(['go', 'version'], capture_output=True, text=True).stdout.strip(),
               'gomaxprocs': args.gomaxprocs, 'reps': args.reps, 'projects': {}}
    for spec in args.projects:
        name, _, location = spec.partition('=')
        directory, _, pattern = location.partition(':')
        project = Path(directory).resolve()
        pattern = pattern or '.'
        entry = dict(describe(project), pattern=pattern, walls={mode: [] for mode in MODES})
        runs = []
        order = list(MODES)
        for rep in range(args.reps + 1):
            for mode in order[rep % len(order):] + order[:rep % len(order)]:
                wall, files = run_tests(args, project, pattern, mode, args.output / f'{name}-{mode}-{rep}')
                print(f'{name} {mode} run {rep}: {wall:.2f} s', flush=True)
                if rep == 0:
                    continue
                entry['walls'][mode].append(round(wall, 3))
                if files is not None:
                    if len(runs) < rep:
                        runs.append({})
                    runs[rep - 1][mode] = files
        # Each run compares with that run's Go's counters; the first one's
        # details are reported.
        entry['comparison'] = compare(runs[0], runs[0]['counters'])
        entry['missing_tests_per_run'] = {mode: [compare(run, run['counters'])[mode]['missing_tests'] for run in runs] for mode in COVERED}
        every[name] = runs
        if name in benches:
            entry['benchmarks'] = run_benchmarks(args, project, benches[name], args.output / f'{name}-bench')
        results['projects'][name] = entry
    (args.output / 'files.json').write_text(json.dumps(every, sort_keys=True) + '\n')
    (args.output / 'results.json').write_text(json.dumps(results, indent=1, sort_keys=True) + '\n')
    (args.output / 'README.md').write_text(report(results))
    if args.report:
        args.report.mkdir(parents=True, exist_ok=True)
        for name in ('results.json', 'README.md'):
            shutil.copy(args.output / name, args.report / name)
    print(report(results))


if __name__ == '__main__':
    main()
