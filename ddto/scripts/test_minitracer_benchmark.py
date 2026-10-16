import os
import signal
import subprocess
import sys
import tempfile
import time
import unittest

import minitracer_benchmark as benchmark


class ParseTests(unittest.TestCase):
    def test_benchmark_lines(self):
        output = """goos: linux
BenchmarkTestEventLifecycle 	  248616	      4595 ns/op	    2336 B/op	      16 allocs/op
BenchmarkSpanMetadata/ci/tags=0-4  	 1409478	       848.4 ns/op	    1344 B/op	      10 allocs/op
PASS
"""
        self.assertEqual({"BenchmarkTestEventLifecycle": (4595.0, 2336.0, 16.0),
                          "BenchmarkSpanMetadata/ci/tags=0": (848.4, 1344.0, 10.0)},
                         benchmark.parse_benchmarks(output))

    def test_fixture_source(self):
        source = benchmark.fixture_source(2)
        self.assertIn("func TestEmpty00000(t *testing.T) {}\n", source)
        self.assertIn("func TestEmpty00001(t *testing.T) {}\n", source)
        self.assertEqual(2, source.count("func Test"))


class MeasureTests(unittest.TestCase):
    def test_output_and_usage(self):
        command = [sys.executable, "-c", "import sys; print('out', flush=True); print('err', file=sys.stderr); sys.exit(3)"]
        output, usage = benchmark.measure(command, ".", None)
        self.assertEqual(b"out\nerr\n", output)
        self.assertEqual(3, usage["exit"])
        self.assertGreater(usage["maxrss_kb"], 0)
        self.assertGreaterEqual(usage["wall"], usage["cpu"] / 64)

    def test_timeout_kills_the_command(self):
        with tempfile.TemporaryDirectory() as directory:
            pidfile = os.path.join(directory, "pid")
            with self.assertRaises(SystemExit):
                benchmark.measure(sleeper(pidfile), ".", None, timeout=2)
            self.assertTrue(ended(read_pid(pidfile)), "the measured command outlived the timeout")

    def test_interrupt_kills_the_command(self):
        with tempfile.TemporaryDirectory() as directory:
            pidfile = os.path.join(directory, "pid")
            harness = subprocess.Popen(
                [sys.executable, "-c", "import sys, minitracer_benchmark as b; b.measure(sys.argv[1:], '.', None)", *sleeper(pidfile)],
                cwd=os.path.dirname(os.path.abspath(__file__)), env=dict(os.environ, PYTHONDONTWRITEBYTECODE="1"),
                stderr=subprocess.DEVNULL)
            pid = read_pid(pidfile)
            harness.send_signal(signal.SIGINT)
            harness.wait(timeout=10)
            self.assertTrue(ended(pid), "the measured command outlived Ctrl+C")


def sleeper(pidfile):
    """Returns a command that writes its pid to pidfile, then sleeps."""
    return [sys.executable, "-c", "import os, sys, time; open(sys.argv[1] + '.tmp', 'w').write(str(os.getpid())); "
            "os.rename(sys.argv[1] + '.tmp', sys.argv[1]); time.sleep(60)", pidfile]


def read_pid(pidfile):
    deadline = time.monotonic() + 10
    while not os.path.exists(pidfile):
        if time.monotonic() > deadline:
            raise AssertionError("the command did not start")
        time.sleep(0.05)
    with open(pidfile) as f:
        return f.read()


def ended(pid):
    """Waits up to 10 s for pid to exit."""
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline and running(pid):
        time.sleep(0.05)
    return not running(pid)


def running(pid):
    """Reports whether pid exists and is not a zombie waiting for its reaper."""
    try:
        with open(f"/proc/{pid}/stat") as f:
            return f.read().rsplit(")", 1)[1].split()[0] != "Z"
    except (FileNotFoundError, ProcessLookupError):
        return False


class ReportTests(unittest.TestCase):
    def test_tables(self):
        run = lambda wall, cpu: {"wall": wall, "cpu": cpu, "maxrss_kb": 2048, "cycle_requests": 3, "cycle_bytes": 1 << 20,
                                 "events": {"test": 4, "test_suite_end": 1}}
        results = {
            "base": {"commit": "b" * 40, "local_diff_sha256": ""},
            "candidate": {"commit": "c" * 40, "local_diff_sha256": "d" * 64},
            "go": "go version go1.27.0 linux/amd64", "cpu": "CPU", "os": "Linux 7", "reps": 2,
            "tests": 2, "count": 2, "gomaxprocs": 4, "benchtime": "1s",
            "microbenchmarks": {"base": {"BenchmarkTestEventLifecycle": [(200, 30, 3), (200, 30, 3)]},
                                "candidate": {"BenchmarkTestEventLifecycle": [(150, 20, 2), (150, 20, 2)]}},
            "test_binaries": {"native": [run(1.0, 1.0)], "base": [run(1.000004, 1.000008)], "candidate": [run(1.000002, 1.000004)]},
        }
        text = benchmark.report(results)
        self.assertIn("| `BenchmarkTestEventLifecycle` | 200 | 150 | -25.0% | 30 | 20 | 3 | 2 |", text)
        self.assertIn("| ddto, base | 1.000 s | 1.000 s | 2 MiB | 1.00 µs | 2.00 µs | 3, 1.0 MiB |", text)
        self.assertIn("| ddto, candidate | 1.000 s | 1.000 s | 2 MiB | 0.50 µs | 1.00 µs | 3, 1.0 MiB |", text)
        self.assertIn("candidate with local changes (sha256 dddddddddddd)", text)
        self.assertIn("The first base run delivered 4 `test`, 1 `test_suite_end` events.", text)


if __name__ == "__main__":
    unittest.main()
