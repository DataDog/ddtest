#!/usr/bin/env python3
"""Bounded wall-time comparison; does not claim total-process-tree CPU."""
import argparse
import json
import os
import pathlib
import shutil
import subprocess
import time

ROOT = pathlib.Path(__file__).resolve().parents[1]
ORCHESTRION_VERSION = "v1.13.2-0.20260917114356-5c24783fcd76"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--orchestrion", required=True)
    parser.add_argument("--ddtest", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--cold-repeats", type=int, default=2)
    parser.add_argument("--warm-repeats", type=int, default=5)
    args = parser.parse_args()
    if not 0 <= args.cold_repeats <= 2 or not 1 <= args.warm_repeats <= 5:
        parser.error("maximum: 2 cold rounds and 5 warm rounds")
    work = pathlib.Path(args.output).resolve()
    if work.exists():
        parser.error("output must be a new directory")
    work.mkdir(parents=True)
    (work / "tmp").mkdir()
    fixture = work / "fixture"
    shutil.copytree(ROOT / "testdata/fixture", fixture)
    env = {key: value for key, value in os.environ.items()
           if not key.startswith(("DD_", "ORCHESTRION_"))}
    env.update(TMPDIR=str(work / "tmp"), GOTMPDIR=str(work / "tmp"), GOFLAGS="",
               DD_CIVISIBILITY_ENABLED="false", DD_INSTRUMENTATION_TELEMETRY_ENABLED="false",
               GOMAXPROCS="8")

    def run(command, environment=None):
        return subprocess.run(command, cwd=fixture, env=environment or env,
                              text=True, capture_output=True, timeout=180, check=True)

    run(["go", "get", "github.com/DataDog/orchestrion@" + ORCHESTRION_VERSION])
    module = json.loads(run(["go", "list", "-m", "-json", "github.com/DataDog/dd-trace-go/v2"]).stdout)
    if module["Version"] != "v2.11.0-rc.1":
        raise RuntimeError("reference changed the pinned SDK")
    config = pathlib.Path(module["Dir"]) / "internal/civisibility/integrations/gotesting/orchestrion.yml"
    shutil.copyfile(config, fixture / "orchestrion.yml")
    (fixture / "orchestrion.tool.go").write_text(
        '//go:build tools\n\npackage fixture\nimport _ "github.com/DataDog/orchestrion"\n')
    targets = {variant: work / variant / "fixture.test"
               for variant in ("native", "overlay", "orchestrion")}
    for target in targets.values():
        target.parent.mkdir()
    commands = {
        "native": ["go", "test", "-c", "-o", str(targets["native"]), "."],
        "overlay": [str(pathlib.Path(args.ddtest).resolve()), "test", "-c", "-o", str(targets["overlay"]), "."],
        "orchestrion": ["go", "test", "-toolexec=" + str(pathlib.Path(args.orchestrion).resolve())
                        + " toolexec", "-c", "-o", str(targets["orchestrion"]), "."],
    }
    rows, caches = [], {}
    started = time.monotonic()
    toolchain = run(["go", "version"]).stdout.strip()

    def measure(variant, scenario, index, cache):
        if time.monotonic() - started > 900:
            raise RuntimeError("15-minute benchmark limit")
        before = time.perf_counter()
        run(commands[variant], dict(env, GOCACHE=str(cache)))
        row = {"variant": variant, "scenario": scenario, "iteration": index,
               "wall_seconds": time.perf_counter() - before, "binary_bytes": targets[variant].stat().st_size}
        rows.append(row)
        print(json.dumps(row), flush=True)
        result = {"sdk": module["Version"], "sdk_sum": module.get("Sum"),
                  "orchestrion": ORCHESTRION_VERSION, "toolchain": toolchain,
                  "gomaxprocs": 8, "rows": rows}
        (work / "results.json").write_text(json.dumps(result, indent=2))
        total = sum(path.stat().st_size for path in work.rglob("*") if path.is_file())
        if total > 8 * (1 << 30):
            raise RuntimeError("8 GiB benchmark artifact limit")

    def order(index):
        variants = list(commands)
        return variants if index % 2 == 0 else list(reversed(variants))

    for index in range(args.cold_repeats):
        for variant in order(index):
            cache = work / ("cache-" + variant + "-" + str(index))
            cache.mkdir()
            caches[variant] = cache
            measure(variant, "cold", index, cache)
    for variant in commands:
        if variant not in caches:
            cache = work / ("cache-" + variant + "-warm")
            cache.mkdir()
            caches[variant] = cache
            run(commands[variant], dict(env, GOCACHE=str(cache)))
    for index in range(args.warm_repeats):
        for variant in order(index):
            measure(variant, "warm", index, caches[variant])

    source = fixture / "sample.go"
    original = source.read_bytes()
    try:
        for index in range(args.warm_repeats):
            # Every variant receives the same body edit and its own warm cache.
            edited = original.replace(b"return a + b", ("return a + b + " + str(index + 1)).encode())
            if edited == original:
                raise RuntimeError("fixture no longer matches the edit benchmark")
            source.write_bytes(edited)
            for variant in order(index):
                measure(variant, "edited", index, caches[variant])
    finally:
        source.write_bytes(original)


if __name__ == "__main__":
    main()
