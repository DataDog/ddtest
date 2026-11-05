#!/usr/bin/python3
"""Docker-free run_eval.py stand-in for run_catalog.py lifecycle tests.

FAKE_HARNESS_LOG receives one line per start, signal and cleanup.
FAKE_HARNESS_MODE: "complete" finishes at once; "teardown" waits for a signal,
then takes FAKE_HARNESS_TEARDOWN seconds to finalize run.json, like run_eval.py;
"stuck" ignores signals and never finishes; "barrier" waits until
FAKE_HARNESS_BARRIER_COUNT harnesses (default 2) have started, proving overlap.
A finished run writes the evidence catalog_check.py reads: run.json,
ddtest-results.jsonl and final-response.md.
"""
import json
import os
from pathlib import Path
import signal
import sys
import time

log = Path(os.environ["FAKE_HARNESS_LOG"])


def note(text):
    with log.open("a") as stream:
        stream.write(text + "\n")


if "--cleanup" in sys.argv:
    note("cleanup " + sys.argv[sys.argv.index("--cleanup") + 1])
    sys.exit(0)
output = Path(sys.argv[sys.argv.index("--output") + 1])
output.mkdir()
(output / "ownership.json").write_text("{}")
mode = os.environ.get("FAKE_HARNESS_MODE", "complete")
note(f"start {output.name} session={os.getsid(0) == os.getpid()}")
received = []


def handle(number, _frame):
    received.append(number)
    note(f"signal {number}")


for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
    signal.signal(number, handle)


def finish(status):
    run = {"status": status, "checkout": os.environ.get("FAKE_HARNESS_CHECKOUT", str(output / "missing")),
           "signals": received, "agent": sys.argv[sys.argv.index("--agent") + 1], "ddtest_version": "v0.0.0-fake",
           "isolation": {"cleanup": "removed"}, "agent_reporting": {"status": "pass", "reason": "fake"},
           "commands": [{"argv": ["codex", "exec"], "exit_code": 0 if status == "completed" else None,
                         "timeout_seconds": 5400, "output_excerpt": ""}]}
    if status != "completed":
        run["error"] = f"Interrupted by signal {received[0] if received else '?'}"
    (output / "run.json").write_text(json.dumps(run))
    final = "Onboarding validation: COMPLETE LOCALLY. Local validation: PASSED."
    result = {"status": "complete", "code": "validation_complete", "reason": "fake", "final_response": final}
    (output / "ddtest-results.jsonl").write_text(json.dumps({"turn": 1, "result": result}) + "\n")
    (output / "final-response.md").write_text(final + "\n")
    note(f"finished {output.name} at={time.time()}")


if mode == "complete":
    finish("completed")
    sys.exit(0)
if mode == "barrier":
    barrier = Path(os.environ["FAKE_HARNESS_BARRIER"])
    (barrier / output.name).touch()
    deadline = time.monotonic() + 20
    while len(list(barrier.iterdir())) < int(os.environ.get("FAKE_HARNESS_BARRIER_COUNT", "2")):
        if time.monotonic() > deadline:
            note("barrier timeout " + output.name)
            sys.exit(1)
        time.sleep(0.02)
    note(f"barrier passed {output.name} at={time.time()}")
    finish("completed")
    sys.exit(0)
note("ready")
while mode == "stuck" or not received:
    time.sleep(0.05)
time.sleep(float(os.environ.get("FAKE_HARNESS_TEARDOWN", "1")))
finish("interrupted")
sys.exit(1)
