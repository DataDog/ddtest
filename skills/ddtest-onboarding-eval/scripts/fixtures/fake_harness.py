#!/usr/bin/python3
"""Docker-free run_eval.py stand-in for run_catalog.py lifecycle tests.

FAKE_HARNESS_LOG receives one line per start, signal and cleanup.
FAKE_HARNESS_MODE: "complete" finishes at once; "teardown" waits for a signal,
then takes FAKE_HARNESS_TEARDOWN seconds to finalize run.json, like run_eval.py;
"stuck" ignores signals and never finishes.
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
           "signals": received}
    (output / "run.json").write_text(json.dumps(run))
    note("finished " + output.name)


if mode == "complete":
    finish("completed")
    sys.exit(0)
note("ready")
while mode == "stuck" or not received:
    time.sleep(0.05)
time.sleep(float(os.environ.get("FAKE_HARNESS_TEARDOWN", "1")))
finish("interrupted")
sys.exit(1)
