#!/usr/bin/env python3
"""Measure a prebuilt test-binary group in an exclusive cgroup v2 scope."""
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import threading
import time


def measure(configuration):
    config = json.loads(Path(configuration).read_text())
    output = Path(config['output'])
    relative = next(line.split(':', 2)[2] for line in Path('/proc/self/cgroup').read_text().splitlines()
                    if line.startswith('0::'))
    group = Path('/sys/fs/cgroup') / relative.lstrip('/')
    if not any(part.startswith('ddci-bench-runtime-') for part in group.parts):
        raise ValueError('runtime measurement needs its exclusive cgroup')

    def cpu():
        return dict(line.split() for line in (group / 'cpu.stat').read_text().splitlines())

    def survivors():
        processes = set()
        for path in group.rglob('cgroup.procs'):
            try:
                processes.update(map(int, path.read_text().split()))
            except FileNotFoundError:
                pass
        return processes - {os.getpid()}

    expired = threading.Event()

    def terminate():
        expired.set()
        for pid in survivors():
            try:
                os.kill(pid, signal.SIGKILL)
            except ProcessLookupError:
                pass

    def run_binary(binary):
        path = Path(binary['path'])
        stdout = output.parent / (path.name + '.stdout.log')
        stderr = output.parent / (path.name + '.stderr.log')
        args = [str(path), '-test.v', '-test.count=1', '-test.timeout=5m',
                '-test.parallel=' + str(config['cpus'])]
        started = time.perf_counter()
        with stdout.open('wb') as out, stderr.open('wb') as err:
            child = subprocess.Popen(args, cwd=binary['cwd'], env=config['environment'],
                                     stdout=out, stderr=err)
            code = child.wait()
        return {'binary': path.name, 'command': args, 'cwd': binary['cwd'], 'returncode': code,
                'wall_s': time.perf_counter() - started,
                'stdout': str(stdout), 'stderr': str(stderr), 'pid': child.pid}

    with ThreadPoolExecutor(max_workers=min(config['cpus'], len(config['binaries']))) as pool:
        before = cpu()
        timer = threading.Timer(config['timeout'], terminate)
        started = time.perf_counter()
        timer.start()
        try:
            futures = [pool.submit(run_binary, binary) for binary in config['binaries']]
            binaries = [future.result() for future in futures]
        finally:
            timer.cancel()
        wall = time.perf_counter() - started
    drain_started = time.perf_counter()
    while survivors() and time.perf_counter() - drain_started < 20:
        time.sleep(.02)
    after, remaining = cpu(), survivors()
    code = max(abs(b['returncode']) for b in binaries)
    row = {'wall_s': wall, 'returncode': code, 'binaries': binaries,
           'cpu_s': (int(after['usage_usec']) - int(before['usage_usec'])) / 1e6,
           'user_s': (int(after['user_usec']) - int(before['user_usec'])) / 1e6,
           'system_s': (int(after['system_usec']) - int(before['system_usec'])) / 1e6,
           'peak_bytes': int((group / 'memory.peak').read_text()),
           'process_tree_drained': not remaining, 'surviving_pids': sorted(remaining),
           'post_exit_wait_s': time.perf_counter() - drain_started,
           'timeout': expired.is_set(), 'affinity': sorted(os.sched_getaffinity(0))}
    output.write_text(json.dumps(row, indent=2) + '\n')
    if remaining:
        terminate()
    return code or int(bool(remaining or expired.is_set()))


if __name__ == '__main__':
    sys.exit(measure(sys.argv[1]))
