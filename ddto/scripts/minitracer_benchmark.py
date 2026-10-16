"""Compare Mini's event recording between two source trees of this repository:
the minitracer microbenchmarks, and a test binary of many empty tests.

usage: minitracer_benchmark.py --base DIR --candidate DIR --output DIR
           [--reps N] [--tests N] [--count N] [--alloc-count N]
           [--gomaxprocs N] [--benchtime D] [--own-benchmarks]
           [--same-wire] [--report DIR]

--base and --candidate are worktrees of this repository, such as one detached
at main and one at the change: ddto compiles Mini from the tree it was built
from, so each needs its own. Their runs alternate, and every figure is the
median of --reps runs.

Microbenchmarks: the candidate's internal/minitracer/performance_test.go runs
in both trees, through go test -overlay in the base, with GOMAXPROCS=1 so that
ns/op includes delivery. With --own-benchmarks, each tree runs its own file,
for a candidate whose benchmarks use internals that the base lacks.

Test binary: a generated module with --tests empty tests, in a Git checkout so
that the CI tags are shared, is built with go test -c, and with each tree's
ddto test -c. Each binary runs -test.count=--count against the loopback
receiver with --gomaxprocs. The report gives the test process's wall time, CPU
time (user and system) and peak RSS, and what ddto adds per test over plain go
test. The first run of each tree decodes every payload and checks that each
test event arrived. It also compares the trees' payloads, decoded with their
MessagePack types (integers, floats, strings and binary data stay apart): every event, with
its envelope's version and the metadata that it inherits. IDs, timestamps,
_dd.p.tid and the runtime ID, which differ between runs, count by presence and
type, hierarchy IDs by what they reference, and the binary's path is masked.
--same-wire stops the benchmark when the payloads differ. A last run of each binary, -test.count=--alloc-count with
every allocation profiled from the process's start (GODEBUG=memprofilerate=1),
counts the objects and bytes that it allocates per test until testing writes
the profile, before Mini's final delivery. --report copies results.json and README.md into DIR, such
as a directory of docs/results; never edit those by hand.
"""
import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import signal
import statistics
import struct
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
from benchmark_receiver import Receiver  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent
BENCHMARKS = '^(BenchmarkTestEventLifecycle|BenchmarkEventLifecycle|BenchmarkSpanMetadata)$'
PERFORMANCE_TEST = Path('internal/minitracer/performance_test.go')
TREES = ('base', 'candidate')
VARIANTS = ('native',) + TREES
# Runs a test binary from a fresh interpreter and prints its wall time and
# rusage as JSON on stderr; the binary's stderr goes to stdout. A child's peak
# RSS includes its parent's memory until exec, and the benchmark's own process
# holds the receiver's payloads.
MEASURE = """
import json, os, sys, time
start = time.perf_counter()
pid = os.posix_spawn(sys.argv[1], sys.argv[1:], os.environ, file_actions=[(os.POSIX_SPAWN_DUP2, 1, 2)])
_, status, usage = os.wait4(pid, 0)
print(json.dumps({'wall': time.perf_counter() - start, 'cpu': usage.ru_utime + usage.ru_stime,
                  'maxrss_kb': usage.ru_maxrss, 'exit': os.waitstatus_to_exitcode(status)}), file=sys.stderr)
"""
BENCH_LINE = re.compile(r'^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+([\d.]+) ns/op\s+([\d.]+) B/op\s+([\d.]+) allocs/op')


def go_env(**values):
    """Returns the environment without the caller's Datadog settings."""
    env = {k: v for k, v in os.environ.items() if not k.startswith('DD_')}
    env.update(GOWORK='off', GOFLAGS='', **values)
    return env


def run(command, cwd, env=None):
    result = subprocess.run(command, cwd=cwd, env=env or go_env(), capture_output=True, text=True, timeout=3600)
    if result.returncode:
        raise SystemExit(f'{" ".join(map(str, command))} in {cwd}: exit {result.returncode}\n{result.stdout[-4000:]}\n{result.stderr[-4000:]}')
    return result.stdout


def parse_benchmarks(output):
    """Returns {name: (ns/op, B/op, allocs/op)} from go test -bench output."""
    out = {}
    for line in output.splitlines():
        match = BENCH_LINE.match(line)
        if match:
            out[match.group(1)] = tuple(float(match.group(i)) for i in (2, 3, 4))
    return out


def fixture_source(tests):
    return 'package fixture\n\nimport "testing"\n\n' + ''.join(
        f'func TestEmpty{i:05d}(t *testing.T) {{}}\n' for i in range(tests))


def make_fixture(directory, tests):
    """Writes a module of empty tests into a new Git checkout with an origin,
    as CI has, so that Mini shares its Git tags."""
    directory.mkdir()
    (directory / 'go.mod').write_text('module example.com/fixture\n\ngo 1.25\n')
    (directory / 'many_test.go').write_text(fixture_source(tests))
    git = ['git', '-c', 'user.name=fixture', '-c', 'user.email=fixture@example.com']
    for command in (['init', '-q'], ['remote', 'add', 'origin', 'https://github.com/example/fixture.git'],
                    ['add', '-A'], ['commit', '-qm', 'Add the fixture']):
        run(git + command, directory)


def describe(tree):
    head = run(['git', 'rev-parse', 'HEAD'], tree).strip()
    diff = subprocess.run(['git', 'diff', 'HEAD'], cwd=tree, capture_output=True).stdout
    return {'commit': head, 'local_diff_sha256': hashlib.sha256(diff).hexdigest() if diff else ''}


def build(args, out):
    """Builds each tree's ddto and minitracer test binary, and the fixture's
    test binaries, into out/<variant>."""
    overlay = out / 'overlay.json'
    overlay.write_text(json.dumps({'Replace': {str(args.base / PERFORMANCE_TEST): str(args.candidate / PERFORMANCE_TEST)}}))
    for tree in TREES:
        (out / tree).mkdir()
        source = getattr(args, tree)
        run(['go', 'build', '-o', str(out / tree / 'ddto'), './cmd/ddto'], source)
        shared = tree == 'base' and not args.own_benchmarks
        run(['go', 'test', '-c', '-o', str(out / tree / 'minitracer.test'),
             *(['-overlay', str(overlay)] if shared else []), './internal/minitracer'], source)
    (out / 'native').mkdir()
    fixture = out / 'fixture'
    make_fixture(fixture, args.tests)
    env = go_env(DD_CIVISIBILITY_GIT_UPLOAD_ENABLED='false')
    run(['go', 'test', '-c', '-o', str(out / 'native' / 'fixture.test'), '.'], fixture, env)
    for tree in TREES:
        run([str(out / tree / 'ddto'), 'test', '-c', '-o', str(out / tree / 'fixture.test'), '.'], fixture, env)
    return fixture


def microbenchmarks(args, out):
    """Runs the microbenchmarks once per repetition and tree, alternating
    which tree goes first, and returns {tree: {name: [(ns, B, allocs)]}}."""
    samples = {tree: {} for tree in TREES}
    env = go_env(GOMAXPROCS='1')
    for rep in range(args.reps):
        for tree in TREES if rep % 2 == 0 else TREES[::-1]:
            output = run([str(out / tree / 'minitracer.test'), '-test.run=^$', '-test.bench=' + BENCHMARKS,
                          '-test.benchmem', '-test.count=1', '-test.benchtime=' + args.benchtime],
                         getattr(args, tree) / 'internal/minitracer', env)
            for name, values in parse_benchmarks(output).items():
                samples[tree].setdefault(name, []).append(values)
            print(f'microbenchmarks {tree} run {rep + 1}', flush=True)
    return samples


def measure(command, cwd, env, timeout=3600):
    """Returns the output of command and its wall time, CPU seconds, peak RSS
    and exit code, measured by MEASURE. The command runs in MEASURE's process
    group, which is killed after timeout seconds or when waiting fails, such as
    on Ctrl+C."""
    process = subprocess.Popen([sys.executable, '-I', '-c', MEASURE, *command], cwd=cwd, env=env,
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    try:
        stdout, stderr = process.communicate(timeout=timeout)
    except BaseException as error:
        os.killpg(process.pid, signal.SIGKILL)
        process.communicate()
        if isinstance(error, subprocess.TimeoutExpired):
            raise SystemExit(f'{" ".join(command)}: still running after {timeout} s') from None
        raise
    if process.returncode:
        raise SystemExit(f'measuring {" ".join(command)}: exit {process.returncode}\n{stderr[-4000:].decode(errors="replace")}')
    return stdout, json.loads(stderr.decode().splitlines()[-1])


def binary_env(args, variant, url):
    env = go_env(GOMAXPROCS=str(args.gomaxprocs))
    if variant != 'native':
        env.update(DD_CIVISIBILITY_ENABLED='parent', DD_TRACE_AGENT_URL=url, DD_SERVICE='fixture',
                   DD_ENV='benchmark', DD_CIVISIBILITY_AGENTLESS_ENABLED='false',
                   DD_CIVISIBILITY_GIT_UPLOAD_ENABLED='false', DD_CIVISIBILITY_IMPACTED_TESTS_DETECTION_ENABLED='false')
    return env


# Values that differ between two runs of one binary. A valid one becomes a
# Marker of its kind; any other stays as it is. Hierarchy IDs become a Marker
# that names the session, module or suite that they reference, with its
# ancestors. canonical tags every value with its type, so no wire value can
# stand for a marker or for a value of another type.
HIERARCHY = {'test_session_end': 'test_session_id', 'test_module_end': 'test_module_id', 'test_suite_end': 'test_suite_id'}
TID = re.compile(r'[0-9a-f]{16}')
UUID = re.compile(r'[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}')


def normalize(value, binary_dir):
    """Replaces the binary's directory, which differs between trees."""
    if isinstance(value, str):
        return value.replace(binary_dir, '<binary>')
    if isinstance(value, dict):
        return {k: normalize(v, binary_dir) for k, v in value.items()}
    if isinstance(value, list):
        return [normalize(v, binary_dir) for v in value]
    return value


class Float32(float):
    """A 32-bit MessagePack float."""


class Marker(tuple):
    """What replaces a value that differs between runs."""


def integer(value, minimum):
    return isinstance(value, int) and not isinstance(value, bool) and value >= minimum


def mark(value, kind, valid):
    return Marker((kind,)) if valid else value


def canonical(value):
    """Returns value with each of its parts tagged by its type."""
    if isinstance(value, Marker):
        return ['marker', [canonical(v) for v in value]]
    if isinstance(value, tuple):
        return ['tuple', [canonical(v) for v in value]]
    if isinstance(value, dict):
        return ['map', sorted([k, canonical(v)] for k, v in value.items())]
    if isinstance(value, list):
        return ['array', [canonical(v) for v in value]]
    if isinstance(value, bool):
        return ['bool', value]
    if isinstance(value, int):
        return ['int', value]
    if isinstance(value, Float32):
        return ['float32', value]
    if isinstance(value, float):
        return ['float64', value]
    if isinstance(value, bytes):
        return ['bin', value.hex()]
    if isinstance(value, str):
        return ['str', value]
    if value is None:
        return ['nil']
    raise TypeError(f'unexpected decoded value {value!r}')


# MessagePack types whose size follows them: (length bytes, kind).
SIZED = {0xc4: (1, 'bin'), 0xc5: (2, 'bin'), 0xc6: (4, 'bin'), 0xd9: (1, 'str'), 0xda: (2, 'str'), 0xdb: (4, 'str'),
         0xdc: (2, 'array'), 0xdd: (4, 'array'), 0xde: (2, 'map'), 0xdf: (4, 'map')}
NUMBERS = {0xca: '>f', 0xcb: '>d', 0xcc: '>B', 0xcd: '>H', 0xce: '>I', 0xcf: '>Q', 0xd0: '>b', 0xd1: '>h', 0xd2: '>i', 0xd3: '>q'}


def unpack(data):
    """Decodes one MessagePack value and keeps the types that JSON merges:
    integers are int, 64-bit floats float, 32-bit floats Float32 and binary
    data bytes. Map keys must be strings."""
    def take(i, n):
        if i + n > len(data):
            raise ValueError('truncated MessagePack value')
        return data[i:i + n]

    def value(i):
        b = take(i, 1)[0]
        i += 1
        if b <= 0x7f:
            return b, i
        if b >= 0xe0:
            return b - 0x100, i
        if b <= 0x8f:
            return mapping(i, b & 0x0f)
        if b <= 0x9f:
            return array(i, b & 0x0f)
        if b <= 0xbf:
            return take(i, b & 0x1f).decode(), i + (b & 0x1f)
        if b in (0xc0, 0xc2, 0xc3):
            return {0xc0: None, 0xc2: False, 0xc3: True}[b], i
        if b in SIZED:
            width, kind = SIZED[b]
            n = int.from_bytes(take(i, width), 'big')
            i += width
            if kind == 'bin':
                return bytes(take(i, n)), i + n
            if kind == 'str':
                return take(i, n).decode(), i + n
            return (array if kind == 'array' else mapping)(i, n)
        if b in NUMBERS:
            width = struct.calcsize(NUMBERS[b])
            number = struct.unpack(NUMBERS[b], take(i, width))[0]
            return (Float32(number) if b == 0xca else number), i + width
        raise ValueError(f'unsupported MessagePack type 0x{b:02x}')

    def array(i, n):
        items = []
        for _ in range(n):
            item, i = value(i)
            items.append(item)
        return items, i

    def mapping(i, n):
        out = {}
        for _ in range(n):
            key, i = value(i)
            if not isinstance(key, str):
                raise ValueError(f'map key {key!r} is not a string')
            out[key], i = value(i)
        return out, i

    decoded, end = value(0)
    if end != len(data):
        raise ValueError(f'{len(data) - end} bytes after the MessagePack value')
    return decoded


def hierarchy_labels(payloads, binary_dir):
    """Returns, for each hierarchy ID field, the label of each ID: the kind and
    resource of the end event that has it, and its parent's label."""
    ends = {kind: {} for kind in HIERARCHY}
    for payload in payloads:
        for event in payload.get('events', []):
            field = HIERARCHY.get(event.get('type'))
            content = event.get('content', {})
            if field and field in content:
                ends[event['type']][content[field]] = content
    def resource(content):
        return normalize(content.get('resource'), binary_dir)
    sessions = {i: ('session', resource(c)) for i, c in ends['test_session_end'].items()}
    modules = {i: ('module', resource(c), sessions.get(c.get('test_session_id'), ('unresolved',)))
               for i, c in ends['test_module_end'].items()}
    suites = {i: ('suite', resource(c), modules.get(c.get('test_module_id'), ('unresolved',)))
              for i, c in ends['test_suite_end'].items()}
    return {'test_session_id': sessions, 'test_module_id': modules, 'test_suite_id': suites}


def wire_content(payloads, binary_dir):
    """Returns a count of the payloads' normalized events. Each carries its
    envelope's version and the event-kind metadata that it inherits."""
    labels = hierarchy_labels(payloads, binary_dir)
    events = Counter()
    for payload in payloads:
        metadata = {}
        for kind, values in payload.get('metadata', {}).items():
            values = dict(values)
            if 'runtime-id' in values:
                runtime = values['runtime-id']
                values['runtime-id'] = mark(runtime, 'runtime-id', isinstance(runtime, str) and UUID.fullmatch(runtime))
            metadata[kind] = values
        for event in payload.get('events', []):
            content = dict(event.get('content', {}))
            for field, known in labels.items():
                if field in content:
                    value = content[field]
                    if integer(value, 1) and value in known:
                        content[field] = Marker(('reference', known[value]))
            span = content.get('span_id')
            for field in ('span_id', 'parent_id'):
                if field in content:
                    content[field] = mark(content[field], 'id', integer(content[field], 1))
            if 'trace_id' in content:
                trace = content['trace_id']
                content['trace_id'] = mark(trace, 'span_id' if trace == span else 'id', integer(trace, 1))
            for field, minimum in (('start', 1), ('duration', 0)):
                if field in content:
                    content[field] = mark(content[field], field, integer(content[field], minimum))
            if isinstance(content.get('meta'), dict) and '_dd.p.tid' in content['meta']:
                tid = content['meta']['_dd.p.tid']
                content['meta'] = dict(content['meta'], **{'_dd.p.tid': mark(tid, 'tid', isinstance(tid, str) and TID.fullmatch(tid))})
            inherited = {kind: values for kind, values in metadata.items() if kind in ('*', event.get('type'))}
            key = {'payload_version': payload.get('version'), 'metadata': inherited,
                   'type': event.get('type'), 'version': event.get('version'), 'content': content}
            events[json.dumps(canonical(normalize(key, binary_dir)))] += 1
    return events


def compare_wire(base, candidate):
    """Returns whether two wire_content counts are equal, with examples of
    events that only one of them holds."""
    return {'identical': base == candidate, 'distinct_events': len(candidate),
            'only_base': list((base - candidate).elements())[:3], 'only_candidate': list((candidate - base).elements())[:3]}


def run_binary(args, out, fixture, variant, directory, verify):
    """Runs one test binary and returns its wall time, CPU seconds, peak RSS
    and the receiver's test-cycle traffic. verify decodes every payload."""
    receiver = Receiver(directory, False)
    command = [str(out / variant / 'fixture.test'), f'-test.count={args.count}']
    output, usage = measure(command, fixture, binary_env(args, variant, receiver.url))
    if usage['exit'] or not output.rstrip().endswith(b'PASS'):
        receiver.close()
        raise SystemExit(f'{" ".join(command)}: exit {usage["exit"]}\n{output[-4000:].decode(errors="replace")}')
    cycle = [r for r in receiver.requests if r['path'].endswith('/citestcycle')]
    events = None
    if verify:
        events = {}
        payloads = [unpack(receiver.bodies[request['file']]) for request in cycle]
        for payload in payloads:
            for event in payload.get('events', []):
                events[event['type']] = events.get(event['type'], 0) + 1
        if events.get('test') != args.tests * args.count:
            raise SystemExit(f'{variant}: {events.get("test")} test events arrived, want {args.tests * args.count}')
        args.wire[variant] = wire_content(payloads, str(out / variant))
    receiver.bodies.clear()  # Only requests.json is kept.
    receiver.close()
    return {'wall': round(usage['wall'], 4), 'cpu': round(usage['cpu'], 4), 'maxrss_kb': usage['maxrss_kb'],
            'cycle_requests': len(cycle), 'cycle_bytes': sum(r['wire_bytes'] for r in cycle), 'events': events}


PPROF_TOTAL = re.compile(r'of (\d+)B? total')


def pprof_total(binary, profile, index):
    """Returns a heap profile's total for the sample index, in objects or bytes."""
    output = run(['go', 'tool', 'pprof', '-sample_index=' + index, '-unit=B', '-top', '-nodecount=1', str(binary), str(profile)], ROOT)
    match = PPROF_TOTAL.search(output)
    if not match:
        raise SystemExit(f'no {index} total in pprof output:\n{output[-2000:]}')
    return int(match.group(1))


def allocations(args, out, fixture):
    """Runs each test binary -test.count=--alloc-count with every allocation
    profiled from its start, and returns the objects and bytes allocated per
    test. testing would otherwise set the rate after Mini's setup."""
    result = {}
    for variant in VARIANTS:
        receiver = Receiver(out / 'allocations' / variant, False)
        profile = out / variant / 'allocations.pprof'
        command = [str(out / variant / 'fixture.test'), f'-test.count={args.alloc_count}',
                   f'-test.memprofile={profile}', '-test.memprofilerate=1']
        env = binary_env(args, variant, receiver.url)
        env['GODEBUG'] = ','.join(filter(None, (env.get('GODEBUG'), 'memprofilerate=1')))
        output, usage = measure(command, fixture, env)
        receiver.bodies.clear()
        receiver.close()
        if usage['exit'] or not output.rstrip().endswith(b'PASS'):
            raise SystemExit(f'{" ".join(command)}: exit {usage["exit"]}\n{output[-4000:].decode(errors="replace")}')
        tests = args.tests * args.alloc_count
        binary = out / variant / 'fixture.test'
        result[variant] = {'objects_per_test': round(pprof_total(binary, profile, 'alloc_objects') / tests, 1),
                           'bytes_per_test': round(pprof_total(binary, profile, 'alloc_space') / tests)}
        print(f'allocations {variant}: {result[variant]}', flush=True)
    return result


def test_binaries(args, out, fixture):
    """Runs each test binary once to warm up, then --reps times in rotating
    order, and returns {variant: [run]}."""
    runs = {variant: [] for variant in VARIANTS}
    for rep in range(args.reps + 1):
        order = VARIANTS[rep % len(VARIANTS):] + VARIANTS[:rep % len(VARIANTS)]
        for variant in order:
            verify = rep == 1 and variant != 'native'
            result = run_binary(args, out, fixture, variant, out / 'runs' / f'{variant}-{rep}', verify)
            print(f'test binary {variant} run {rep}: {result["wall"]:.2f} s wall, {result["cpu"]:.2f} s CPU', flush=True)
            if rep:
                runs[variant].append(result)
    return runs


def order(name):
    """Sorts the realistic benchmark first, then numbers in names by value."""
    return (not name.startswith('BenchmarkTestEvent'), [int(p) if p.isdigit() else p for p in re.split(r'(\d+)', name)])


def change(base, candidate):
    return f'{(candidate - base) / base * 100:+.1f}%' if base else '—'


def report(results):
    tests = results['tests'] * results['count']
    lines = ['# Mini event recording: base against candidate', '',
             'Generated by `scripts/minitracer_benchmark.py`; do not edit by hand.', '',
             f"Base `{results['base']['commit']}`, candidate `{results['candidate']['commit']}`"
             + ''.join(f", {tree} with local changes (sha256 {results[tree]['local_diff_sha256'][:12]})"
                       for tree in TREES if results[tree]['local_diff_sha256']) + '.', '',
             f"{results['go']}, {results['cpu']}, {results['os']}. Medians of {results['reps']} runs per tree; "
             'the trees alternate.', '',
             '## Microbenchmarks', '',
             "`go test ./internal/minitracer -bench` with GOMAXPROCS=1 and `-benchtime="
             f"{results['benchtime']}`. "
             + ("Each tree runs its own `performance_test.go`." if results['own_benchmarks']
                else "Both trees run the candidate's `performance_test.go`."), '',
             '| Benchmark | ns/op base | ns/op candidate | Change | B/op base | B/op candidate | allocs/op base | allocs/op candidate |',
             '| --- | --- | --- | --- | --- | --- | --- | --- |']
    bench = results['microbenchmarks']
    for name in sorted(bench['candidate'], key=order):
        base = [statistics.median(v[i] for v in bench['base'].get(name, [(0, 0, 0)])) for i in range(3)]
        cand = [statistics.median(v[i] for v in bench['candidate'][name]) for i in range(3)]
        lines.append(f'| `{name}` | {base[0]:.0f} | {cand[0]:.0f} | {change(base[0], cand[0])} | {base[1]:.0f} | '
                     f'{cand[1]:.0f} | {base[2]:.0f} | {cand[2]:.0f} |')
    lines += ['', '## Test binary', '',
              f"{results['tests']} empty tests run `-test.count={results['count']}`, {tests} test events, "
              f"with GOMAXPROCS={results['gomaxprocs']} against the loopback receiver. CPU is the test process's "
              'user and system time. Added per test is the difference with go test, divided by the test events.', '',
              '| Binary | Wall time | CPU time | Peak RSS | Wall added per test | CPU added per test | Test-cycle payloads |',
              '| --- | --- | --- | --- | --- | --- | --- |']
    binaries = results['test_binaries']
    native = {key: statistics.median(r[key] for r in binaries['native']) for key in ('wall', 'cpu')}
    labels = {'native': 'go test', 'base': 'ddto, base', 'candidate': 'ddto, candidate'}
    for variant in VARIANTS:
        runs = binaries[variant]
        wall, cpu = (statistics.median(r[key] for r in runs) for key in ('wall', 'cpu'))
        rss = statistics.median(r['maxrss_kb'] for r in runs) / 1024
        if variant == 'native':
            lines.append(f'| {labels[variant]} | {wall:.3f} s | {cpu:.3f} s | {rss:.0f} MiB | — | — | — |')
            continue
        payloads = statistics.median(r['cycle_bytes'] for r in runs) / (1 << 20)
        requests = statistics.median(r['cycle_requests'] for r in runs)
        lines.append(f"| {labels[variant]} | {wall:.3f} s | {cpu:.3f} s | {rss:.0f} MiB | "
                     f"{(wall - native['wall']) / tests * 1e6:.2f} µs | {(cpu - native['cpu']) / tests * 1e6:.2f} µs | "
                     f'{requests:.0f}, {payloads:.1f} MiB |')
    for tree in TREES:
        events = binaries[tree][0]['events']
        lines += ['', f"The first {tree} run delivered " + ', '.join(f'{n} `{kind}`' for kind, n in sorted(events.items())) + ' events.']
    allocs = results['allocations']
    lines += ['', '## Allocations per test', '',
              f"Each binary runs `-test.count={results['alloc_count']}` once more, with every allocation profiled from "
              "its start (`GODEBUG=memprofilerate=1`). The objects and bytes are those that the test process "
              "allocates until testing writes the profile, before Mini's final delivery, divided by the test events.", '',
              '| Binary | Objects per test | Bytes per test |', '| --- | --- | --- |']
    for variant in VARIANTS:
        lines.append(f"| {labels[variant]} | {allocs[variant]['objects_per_test']} | {allocs[variant]['bytes_per_test']} |")
    wire = results['wire']
    lines += ['', '## Payload content', '',
              "Each event is compared with its envelope's version and the metadata that it inherits. IDs, "
              "timestamps, `_dd.p.tid` and the runtime ID count by presence and type, hierarchy IDs by the "
              "session, module or suite that they reference, and the binary's path is masked.", '',
              f"Both trees' test-cycle payloads hold the same events: {wire['distinct_events']} distinct ones."
              if wire['identical'] else "The trees' test-cycle payloads differ. Only the base has, for example:", '']
    if not wire['identical']:
        lines += [f'- `{item}`' for item in wire['only_base']] + ['', 'Only the candidate has:', '']
        lines += [f'- `{item}`' for item in wire['only_candidate']] + ['']
    lines.append('')
    return '\n'.join(lines)


def cpu_model():
    try:
        for line in Path('/proc/cpuinfo').read_text().splitlines():
            if line.startswith('model name'):
                return line.split(':', 1)[1].strip()
    except OSError:
        pass
    return platform.processor()


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--base', type=Path, required=True)
    parser.add_argument('--candidate', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--reps', type=int, default=10)
    parser.add_argument('--tests', type=int, default=10000)
    parser.add_argument('--count', type=int, default=30)
    parser.add_argument('--gomaxprocs', type=int, default=4)
    parser.add_argument('--alloc-count', type=int, default=3)
    parser.add_argument('--benchtime', default='1s')
    parser.add_argument('--own-benchmarks', action='store_true')
    parser.add_argument('--same-wire', action='store_true')
    parser.add_argument('--report', type=Path)
    args = parser.parse_args()
    args.base, args.candidate = args.base.resolve(), args.candidate.resolve()
    out = args.output.resolve()
    out.mkdir(parents=True)
    fixture = build(args, out)
    results = {'base': describe(args.base), 'candidate': describe(args.candidate),
               'go': run(['go', 'version'], ROOT).strip(), 'cpu': cpu_model(), 'os': f'{platform.system()} {platform.release()}',
               'reps': args.reps, 'tests': args.tests, 'count': args.count, 'gomaxprocs': args.gomaxprocs,
               'benchtime': args.benchtime, 'own_benchmarks': args.own_benchmarks, 'alloc_count': args.alloc_count}
    results['microbenchmarks'] = microbenchmarks(args, out)
    args.wire = {}
    results['test_binaries'] = test_binaries(args, out, fixture)
    results['wire'] = compare_wire(args.wire['base'], args.wire['candidate'])
    if args.same_wire and not results['wire']['identical']:
        raise SystemExit('the payloads differ:\n' + json.dumps(results['wire'], indent=1))
    results['allocations'] = allocations(args, out, fixture)
    (out / 'results.json').write_text(json.dumps(results, indent=1, sort_keys=True) + '\n')
    (out / 'README.md').write_text(report(results))
    if args.report:
        args.report.mkdir(parents=True, exist_ok=True)
        for name in ('results.json', 'README.md'):
            shutil.copy(out / name, args.report / name)
    print(report(results))


if __name__ == '__main__':
    main()
