import os
import signal
import struct
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


class WireTests(unittest.TestCase):
    def payload(self, binary, ids, meta=None, metadata=None, version=1, test=None):
        """A payload with a suite and a test of that suite; ids offsets the IDs."""
        test_content = {"span_id": 10 + ids, "trace_id": 10 + ids, "start": 5 + ids, "duration": ids, "test_suite_id": 20 + ids,
                        "name": "test", "resource": "suite.T", "meta": dict(meta or {"k": "v"}, **{"_dd.p.tid": "%016x" % ids}),
                        "metrics": {"m": 1}}
        test_content.update(test or {})
        return {"version": version,
                "metadata": metadata or {"*": {"runtime-id": "%08x-0000-4000-8000-%012x" % (ids, ids), "language": "go"},
                                         "test": {"git.branch": "main", "cmd": binary + "/fixture.test"}},
                "events": [{"type": "test_suite_end", "version": 1, "content": {"test_suite_id": 20 + ids, "resource": "suite", "start": 3 + ids, "duration": 1}},
                           {"type": "test", "version": 2, "content": test_content}]}

    def compare(self, base, candidate):
        return benchmark.compare_wire(benchmark.wire_content(base, "/out/base"), benchmark.wire_content(candidate, "/out/candidate"))

    def test_runs_differ_only_in_what_varies(self):
        self.assertTrue(self.compare([self.payload("/out/base", 1)], [self.payload("/out/candidate", 2)])["identical"])

    def test_a_changed_tag_differs(self):
        result = self.compare([self.payload("/out/base", 1)], [self.payload("/out/candidate", 1, meta={"k": "other"})])
        self.assertFalse(result["identical"])
        self.assertIn('["k", ["str", "v"]]', result["only_base"][0])
        self.assertIn('["k", ["str", "other"]]', result["only_candidate"][0])

    def test_a_missing_event_differs(self):
        one = self.payload("/out/base", 1)
        two = dict(one, events=one["events"] + one["events"][1:])
        self.assertFalse(self.compare([one], [two])["identical"])

    def test_metadata_counts_for_each_payload(self):
        # The second payload's events lose an inherited tag, while the first
        # payload still holds it.
        base = [self.payload("/out/base", 1), self.payload("/out/base", 2)]
        bare = {"*": {"runtime-id": "x", "language": "go"}, "test": {"cmd": "/out/candidate/fixture.test"}}
        candidate = [self.payload("/out/candidate", 1), self.payload("/out/candidate", 2, metadata=bare)]
        self.assertFalse(self.compare(base, candidate)["identical"])

    def test_structure_counts(self):
        for name, candidate in {
            "missing identity": self.payload("/out/candidate", 1, test={"span_id": None}),
            "trace other than the span": self.payload("/out/candidate", 1, test={"trace_id": 99}),
            "broken suite reference": self.payload("/out/candidate", 1, test={"test_suite_id": 999}),
            "duration of another type": self.payload("/out/candidate", 1, test={"duration": "1"}),
            "envelope version": self.payload("/out/candidate", 1, version=2),
            "integer _dd.p.tid": self.payload("/out/candidate", 1, test={"meta": {"k": "v", "_dd.p.tid": 1234567890123456}}),
            "string that reads like a marker": self.payload("/out/candidate", 1, test={"duration": {"marker": "duration"}}),
        }.items():
            with self.subTest(name):
                self.assertFalse(self.compare([self.payload("/out/base", 1)], [candidate])["identical"])
        missing = self.payload("/out/candidate", 1)
        del missing["events"][1]["content"]["start"]
        self.assertFalse(self.compare([self.payload("/out/base", 1)], [missing])["identical"])


class HierarchyTests(unittest.TestCase):
    def payload(self, moved):
        """Two modules, each with a suite named shared_test.go; moved points a
        test of module A at module B's suite."""
        def end(kind, fields, resource):
            return {"type": kind, "version": 1, "content": dict(fields, resource=resource, start=1, duration=1)}
        events = [end("test_session_end", {"test_session_id": 1}, "session"),
                  end("test_module_end", {"test_module_id": 10, "test_session_id": 1}, "a"),
                  end("test_module_end", {"test_module_id": 20, "test_session_id": 1}, "b"),
                  end("test_suite_end", {"test_suite_id": 100, "test_module_id": 10, "test_session_id": 1}, "shared_test.go"),
                  end("test_suite_end", {"test_suite_id": 200, "test_module_id": 20, "test_session_id": 1}, "shared_test.go"),
                  {"type": "test", "version": 2, "content": {"span_id": 5, "trace_id": 5, "start": 1, "duration": 1, "resource": "T",
                                                             "test_suite_id": 200 if moved else 100, "test_module_id": 10, "test_session_id": 1}}]
        return [{"version": 1, "metadata": {}, "events": events}]

    def test_resources_cannot_forge_ancestry(self):
        # A suite "x@module:b" of module "a" and a suite "x" of module "b@module:a".
        def payload(suite):
            events = [{"type": "test_session_end", "version": 1, "content": {"test_session_id": 1, "resource": "s", "start": 1, "duration": 1}},
                      {"type": "test_module_end", "version": 1, "content": {"test_module_id": 10, "test_session_id": 1, "resource": "a", "start": 1, "duration": 1}},
                      {"type": "test_module_end", "version": 1, "content": {"test_module_id": 20, "test_session_id": 1, "resource": "b@module:a", "start": 1, "duration": 1}},
                      {"type": "test_suite_end", "version": 1, "content": {"test_suite_id": 100, "test_module_id": 10, "resource": "x@module:b", "start": 1, "duration": 1}},
                      {"type": "test_suite_end", "version": 1, "content": {"test_suite_id": 200, "test_module_id": 20, "resource": "x", "start": 1, "duration": 1}},
                      {"type": "test", "version": 2, "content": {"span_id": 5, "trace_id": 5, "start": 1, "duration": 1, "test_suite_id": suite}}]
            return [{"version": 1, "metadata": {}, "events": events}]
        result = benchmark.compare_wire(benchmark.wire_content(payload(100), "/b"), benchmark.wire_content(payload(200), "/c"))
        self.assertFalse(result["identical"])

    def test_suites_of_the_same_name_in_other_modules_differ(self):
        result = benchmark.compare_wire(benchmark.wire_content(self.payload(False), "/b"), benchmark.wire_content(self.payload(True), "/c"))
        self.assertFalse(result["identical"])
        self.assertTrue(benchmark.compare_wire(benchmark.wire_content(self.payload(False), "/b"), benchmark.wire_content(self.payload(False), "/c"))["identical"])


def pack(value):
    """Encodes value as MessagePack: ints, floats (64-bit), str, bytes, bool,
    None, lists and dicts with string keys."""
    if value is None or isinstance(value, bool):
        return bytes([{None: 0xc0, False: 0xc2, True: 0xc3}[value]])
    if isinstance(value, int):
        return bytes([value]) if 0 <= value <= 0x7f else b"\xd3" + struct.pack(">q", value)
    if isinstance(value, float):
        return b"\xcb" + struct.pack(">d", value)
    if isinstance(value, str):
        data = value.encode()
        return b"\xda" + struct.pack(">H", len(data)) + data
    if isinstance(value, bytes):
        return b"\xc5" + struct.pack(">H", len(value)) + value
    if isinstance(value, list):
        return b"\xdc" + struct.pack(">H", len(value)) + b"".join(pack(v) for v in value)
    return b"\xde" + struct.pack(">H", len(value)) + b"".join(pack(k) + pack(v) for k, v in value.items())


class UnpackTests(unittest.TestCase):
    def test_round_trip(self):
        value = {"version": 1, "metadata": {"*": {"language": "go"}}, "events": [{"type": "test", "content": {
            "span_id": 1 << 62, "start": -5, "error": 0, "ok": True, "none": None, "f": 0.5, "meta": {"k": "v"}}}]}
        self.assertEqual(value, benchmark.unpack(pack(value)))

    def test_types_that_json_merges_stay_apart(self):
        self.assertEqual(1, benchmark.unpack(pack(1)))
        self.assertIsInstance(benchmark.unpack(pack(1.0)), float)
        self.assertEqual(b"ab", benchmark.unpack(pack(b"ab")))
        self.assertIsInstance(benchmark.unpack(b"\xca" + struct.pack(">f", 1.5)), benchmark.Float32)
        self.assertEqual(200, benchmark.unpack(b"\xcc\xc8"))
        self.assertEqual(-1, benchmark.unpack(b"\xff"))

    def test_malformed_input_fails(self):
        for data in (b"\xda\x00\x05ab", pack(1) + b"\x00", b"\x81\x01\x02", b"\xc1"):
            with self.subTest(data=data), self.assertRaises(ValueError):
                benchmark.unpack(data)

    def test_maps_cannot_stand_for_scalars_or_markers(self):
        def payload(metric, meta_value, span):
            return {"version": 1, "metadata": {}, "events": [{"type": "test", "version": 2, "content": {
                "span_id": span, "metrics": {"m": metric}, "meta": {"k": meta_value}}}]}
        def content(data):
            return benchmark.wire_content([benchmark.unpack(data)], "/b")
        float32 = b"\xca" + struct.pack(">f", 1.5)
        base = pack(payload(0, "v", 7)).replace(pack({"m": 0}), b"\xde\x00\x01" + pack("m") + float32)
        for name, candidate in {
            "a map for a 32-bit float": pack(payload({"float32": 1.5}, "v", 7)),
            "a map for binary data": pack(payload(0, {"bin": "6162"}, 7)),
            "a map for a marker": pack(payload(0, "v", {"marker": "id"})),
        }.items():
            with self.subTest(name):
                reference = base if name == "a map for a 32-bit float" else pack(payload(0, b"ab" if "binary" in name else "v", 7))
                self.assertFalse(benchmark.compare_wire(content(reference), content(candidate))["identical"])

    def test_wire_types_count(self):
        def payload(duration, tid):
            return pack({"version": 1, "metadata": {}, "events": [{"type": "test", "version": 2, "content": {
                "span_id": 7, "trace_id": 7, "start": 1, "duration": duration, "meta": {"_dd.p.tid": tid}}}]})
        base = benchmark.wire_content([benchmark.unpack(payload(1, "0123456789abcdef"))], "/b")
        for name, candidate in {"float duration": payload(1.0, "0123456789abcdef"), "binary tid": payload(1, b"0123456789abcdef")}.items():
            with self.subTest(name):
                self.assertFalse(benchmark.compare_wire(base, benchmark.wire_content([benchmark.unpack(candidate)], "/c"))["identical"])


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
            "tests": 2, "count": 2, "gomaxprocs": 4, "benchtime": "1s", "own_benchmarks": True, "alloc_count": 3,
            "allocations": {"native": {"objects_per_test": 10.0, "bytes_per_test": 1000},
                            "base": {"objects_per_test": 60.5, "bytes_per_test": 7000},
                            "candidate": {"objects_per_test": 48.8, "bytes_per_test": 5870}},
            "microbenchmarks": {"base": {"BenchmarkTestEventLifecycle": [(200, 30, 3), (200, 30, 3)]},
                                "candidate": {"BenchmarkTestEventLifecycle": [(150, 20, 2), (150, 20, 2)]}},
            "wire": {"identical": True, "distinct_events": 7, "only_base": [], "only_candidate": []},
            "test_binaries": {"native": [run(1.0, 1.0)], "base": [run(1.000004, 1.000008)], "candidate": [run(1.000002, 1.000004)]},
        }
        text = benchmark.report(results)
        self.assertIn("| `BenchmarkTestEventLifecycle` | 200 | 150 | -25.0% | 30 | 20 | 3 | 2 |", text)
        self.assertIn("| ddto, base | 1.000 s | 1.000 s | 2 MiB | 1.00 µs | 2.00 µs | 3, 1.0 MiB |", text)
        self.assertIn("| ddto, candidate | 1.000 s | 1.000 s | 2 MiB | 0.50 µs | 1.00 µs | 3, 1.0 MiB |", text)
        self.assertIn("candidate with local changes (sha256 dddddddddddd)", text)
        self.assertIn("The first base run delivered 4 `test`, 1 `test_suite_end` events.", text)
        self.assertIn("Each tree runs its own `performance_test.go`.", text)
        self.assertIn("| ddto, candidate | 48.8 | 5870 |", text)
        self.assertIn("payloads hold the same events: 7 distinct ones.", text)
        results["wire"] = {"identical": False, "distinct_events": 7, "only_base": ["a"], "only_candidate": ["b"]}
        text = benchmark.report(results)
        self.assertIn("Only the base has, for example:\n\n- `a`", text)
        self.assertIn("Only the candidate has:\n\n- `b`", text)

    def test_pprof_total(self):
        self.assertEqual("123", benchmark.PPROF_TOTAL.search("Showing nodes accounting for 5, 4% of 123 total").group(1))
        self.assertEqual("184505376", benchmark.PPROF_TOTAL.search("accounting for 38881568B, 21.07% of 184505376B total").group(1))


if __name__ == "__main__":
    unittest.main()
