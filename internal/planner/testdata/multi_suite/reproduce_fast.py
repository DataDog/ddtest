"""Run with: python3 reproduce_fast.py /absolute/path/to/ddtest.
Requires Ruby, Bundler, RSpec and datadog-ci installed locally (see README).
Uses real Ruby/CLI processes and a local fake backend; retains logs in /tmp.
"""
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import threading
from http.server import ThreadingHTTPServer

binary = str(Path(sys.argv[1]).resolve())
root = Path(tempfile.mkdtemp(prefix="ddtest-fast-multi-suite-"))
shutil.copytree(Path(__file__).parent, root, dirs_exist_ok=True)
print(f"Fixture and logs: {root}", flush=True)
env = {k: v for k, v in os.environ.items() if not k.startswith(("DD_", "BUNDLE_", "RUBYOPT"))}
env.update(DD_API_KEY="qa", DD_CIVISIBILITY_AGENTLESS_ENABLED="true",
           DD_INSTRUMENTATION_TELEMETRY_ENABLED="false",
           DD_CIVISIBILITY_GIT_UPLOAD_ENABLED="false",
           DD_CIVISIBILITY_GIT_METADATA_UPLOAD_ENABLED="false",
           DD_GIT_REPOSITORY_URL="https://example.com/multi-suite.git",
           DD_GIT_COMMIT_SHA="1" * 40, DDTEST_QA_FAIL_B="true",
           DDTEST_QA_FAIL_DISCOVERY="true")

def run(name, args):
    result = subprocess.run(args, cwd=root, env=env, capture_output=True, text=True, timeout=60)
    (root / f"{name}.log").write_text(result.stdout + result.stderr)
    return result

assert run("git", ["git", "init", "-q"]).returncode == 0
assert run("bundle", ["bundle", "lock", "--local"]).returncode == 0
native = run("native", ["bundle", "exec", "rspec"])
assert native.returncode != 0 and "RUNNABLE_B_FAILED" in native.stdout
spec = importlib.util.spec_from_file_location("backend", root / "backend.py")
backend = importlib.util.module_from_spec(spec)
spec.loader.exec_module(backend)
server = ThreadingHTTPServer(("127.0.0.1", 0), backend.Handler)
thread = threading.Thread(target=server.serve_forever, daemon=True)
thread.start()
env["DD_CIVISIBILITY_AGENTLESS_URL"] = f"http://127.0.0.1:{server.server_port}"
failures = []
try:
    for name, skips, duration, expected in [
        ("runnable_positive_timing", ["A"], "30000000000", 30000),
        ("runnable_zero_timing", ["A"], "0", 1000),
        ("sum_positive_timings", [], "30000000000", 40000),
    ]:
        (root / "scenario.json").write_text(json.dumps({"skippable": skips, "duration_b": duration}))
        shutil.rmtree(root / ".testoptimization", ignore_errors=True)
        plan = run(name + "-plan", [binary, "plan", "--framework", "rspec",
                   "--test-skipping-mode", "suite", "--strict-discovery",
                   "--min-parallelism", "1", "--max-parallelism", "1"])
        assert plan.returncode == 0, plan.stdout + plan.stderr
        cache = json.loads((root / ".testoptimization/runner/cache/test_suite_durations.json").read_text())
        weight = cache["testFileWeights"].get("spec/multiple_spec.rb")
        execution = run(name + "-run", [binary, "run", "--framework", "rspec",
                        "--test-skipping-mode", "suite", "--ci-node", "0"])
        failure_visible = execution.returncode != 0 and "RUNNABLE_B_FAILED" in execution.stdout
        print(f"{name}: weight={weight} (expected {expected}), native exit={native.returncode}, "
              f"ddtest exit={execution.returncode}, B failure visible={failure_visible}", flush=True)
        if weight != expected or not failure_visible:
            failures.append(name)
finally:
    server.shutdown()
    server.server_close()
    thread.join()
assert not failures, f"Fast discovery regression: {failures}; see {root}"
