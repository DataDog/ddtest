#!/usr/bin/env python3
"""Compare ddto's preparation between executables on one project.

usage: preparation_benchmark.py --subject DIR --output DIR [--runs N]
           [--cpus N] [--report DIR] NAME=DDTO...

Each ddto first compiles the subject's test binaries once, into its own output
directory, with its own Go build cache and user cache under --output. Then
--runs rounds run every ddto once, in rotating order, with DD_TRACE_DEBUG=true
and everything cached: `ddto test --runtime=mini -c -o OUT/ -ldflags=-w ./...`
under taskset. The report gives each executable's medians: the command's wall
time, and from its debug log the preparation, `go test` and whole-command
durations and how many packages are the CLI's own runtime sources. Build each
ddto from the revision to measure, such as a release executable from
scripts/release, which carries its runtime. --report copies results.json and
README.md into DIR, such as a directory of docs/results; never edit those by
hand.
"""
import argparse
import json
import os
from pathlib import Path
import platform
import re
import shutil
import statistics
import subprocess
import sys
import time

DEBUG = re.compile(r'TestOptimization\.build\S* v\S+ DEBUG: \+\S+ (.*)$')
DURATIONS = {'prepare': 'prepare finished', 'go_test': 'go test finished', 'ddto': 'ddto finished'}
UNITS = {'µs': 1e-3, 'ms': 1.0, 's': 1000.0}


def parse(stderr):
    """Returns the durations in milliseconds and the CLI's own runtime packages
    that a debug log reports."""
    result = {}
    for line in stderr.splitlines():
        match = DEBUG.search(line)
        if not match:
            continue
        text = match.group(1)
        for key, prefix in DURATIONS.items():
            if text.startswith(prefix):
                duration = re.search(r'duration=([0-9.]+)(µs|ms|s) ', text + ' ')
                if duration:
                    result[key] = float(duration.group(1)) * UNITS[duration.group(2)]
        own = re.match(r'runtime own_source_packages=(\d+)', text)
        if own:
            result['own_packages'] = int(own.group(1))
    return result


def command(ddto, cpus, output):
    return ['taskset', '-c', f'0-{cpus - 1}', str(ddto), 'test', '--runtime=mini', f'-p={cpus}', '-mod=readonly',
            '-c', '-o', str(output) + os.sep, '-ldflags=-w', './...']


def environment(out, name):
    env = dict(os.environ, GOFLAGS='', GOTOOLCHAIN='local', GOPROXY='off',
               GOCACHE=str(out / name / 'gocache'), XDG_CACHE_HOME=str(out / name / 'cache'))
    env.pop('DD_TRACE_DEBUG', None)
    return env


def executable(path):
    """Returns path absolute when it names a file, or as found in PATH."""
    if os.sep in path or (os.altsep and os.altsep in path) or Path(path).exists():
        return str(Path(path).resolve())
    found = shutil.which(path)
    return str(Path(found).resolve()) if found else path


def normalize(args):
    """Makes every path absolute: commands run in the subject's directory, and
    Go requires an absolute GOCACHE."""
    args.subject, args.output = args.subject.resolve(), args.output.resolve()
    if args.report:
        args.report = args.report.resolve()
    args.binaries = {name: executable(path) for name, path in (entry.split('=', 1) for entry in args.ddto)}
    return args


def run(args):
    out = args.output
    if out.exists():
        raise SystemExit(f'{out} exists')
    binaries = args.binaries
    for name in binaries:
        (out / name / 'out').mkdir(parents=True)
        primed = subprocess.run(command(binaries[name], args.cpus, out / name / 'out'), cwd=args.subject,
                                env=environment(out, name), capture_output=True, text=True)
        if primed.returncode:
            raise SystemExit(f'{name}: priming failed\n{primed.stderr[-4000:]}')
    samples = {name: [] for name in binaries}
    names = list(binaries)
    for round_ in range(args.runs):
        for name in names[round_ % len(names):] + names[:round_ % len(names)]:
            env = environment(out, name)
            env['DD_TRACE_DEBUG'] = 'true'
            start = time.perf_counter()
            done = subprocess.run(command(binaries[name], args.cpus, out / name / 'out'), cwd=args.subject,
                                  env=env, capture_output=True, text=True)
            wall = (time.perf_counter() - start) * 1000
            if done.returncode:
                raise SystemExit(f'{name}: exit {done.returncode}\n{done.stderr[-4000:]}')
            sample = parse(done.stderr)
            if set(DURATIONS) - set(sample):
                raise SystemExit(f'{name}: the debug log lacks durations\n{done.stderr[-4000:]}')
            sample['wall'] = wall
            samples[name].append(sample)
        print(f'round {round_ + 1} of {args.runs}', flush=True)
    subject = subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=args.subject, capture_output=True, text=True).stdout.strip()
    go = subprocess.run(['go', 'version'], capture_output=True, text=True).stdout.strip()
    return {'subject': str(args.subject), 'subject_commit': subject, 'go': go, 'cpus': args.cpus, 'runs': args.runs,
            'host': platform.platform(), 'binaries': binaries, 'samples': samples}


def median(samples, key):
    return statistics.median(sample[key] for sample in samples)


def report(results):
    lines = ['# ddto preparation', '', 'Generated by `scripts/preparation_benchmark.py`; do not edit by hand.', '',
             f"Subject `{Path(results['subject']).name}` at `{results['subject_commit'][:12]}`; {results['go']}; "
             f"{results['cpus']} CPUs; medians of {results['runs']} cached `ddto test -c` runs per executable, "
             'in rotating order. Durations come from each run\'s `DD_TRACE_DEBUG` log.', '',
             '| Executable | Wall time | Preparation | `go test` | Whole command | Own runtime packages |',
             '| --- | ---: | ---: | ---: | ---: | ---: |']
    for name, samples in results['samples'].items():
        own = sorted({sample.get('own_packages', 0) for sample in samples})
        lines.append(f"| {name} | {median(samples, 'wall'):.1f} ms | {median(samples, 'prepare'):.2f} ms | "
                     f"{median(samples, 'go_test'):.2f} ms | {median(samples, 'ddto'):.2f} ms | "
                     f"{', '.join(map(str, own))} |")
    return '\n'.join(lines) + '\n'


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--subject', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--runs', type=int, default=10)
    parser.add_argument('--cpus', type=int, default=4)
    parser.add_argument('--report', type=Path)
    parser.add_argument('ddto', nargs='+', metavar='NAME=DDTO')
    args = parser.parse_args()
    if any('=' not in entry for entry in args.ddto):
        parser.error('each executable is NAME=DDTO')
    results = run(normalize(args))
    args.output.joinpath('results.json').write_text(json.dumps(results, indent=1) + '\n')
    args.output.joinpath('README.md').write_text(report(results))
    if args.report:
        args.report.mkdir(parents=True, exist_ok=True)
        for name in ('results.json', 'README.md'):
            shutil.copy(args.output / name, args.report / name)
    print(report(results), end='')


if __name__ == '__main__':
    sys.exit(main())
