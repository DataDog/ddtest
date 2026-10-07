#!/usr/bin/env python3
"""Render differential evidence without turning a known gap into a parity claim."""

import argparse
import json
from pathlib import Path


COUNT_FIELDS = ("sessions", "modules", "suites", "tests", "spans")


def counts(value):
    return "/".join(str(value[key]) for key in COUNT_FIELDS)


def cell(value):
    return str(value).replace("|", "\\|").replace("\n", " ")


def timing_cells(item, required):
    timing = item.get("timing")
    if timing is None:
        if required:
            raise ValueError("missing SDK/Mini timing observation")
        return ("Not recorded", "Not recorded", "Not recorded")
    if not isinstance(timing.get("scope"), str) or not timing["scope"]:
        raise ValueError("missing timing scope")
    sdk, mini = timing["sdk_wall_ns"], timing["mini_wall_ns"]
    if any(type(value) is not int or value <= 0 for value in (sdk, mini)):
        raise ValueError("walltimes must be positive integer nanoseconds")
    return (f"{sdk / 1e9:.6f}", f"{mini / 1e9:.6f}", f"{(mini / sdk - 1) * 100:+.1f}%")


FUZZ_EXAMPLE_SCENARIOS = {
    "pass", "fuzz-failure", "seed-lifecycle", "root-cleanup-goexit",
    "fuzz-missing-call", "example-mismatch", "example-panic", "example-panic-nil",
    "test-management", "active-fuzz", "filtered", "fatal-shutdown",
    "skip-lifecycle", "parallel-duration", "corpus-lifecycle", "repeat-run",
}
FUZZ_EXAMPLE_COMMIT = "7b32e1812cb5c1fb807a63cc5042750f3d3cd672"


def validate_fuzz_examples(report, expected=None):
    if report.get("sdk_commit") != FUZZ_EXAMPLE_COMMIT:
        raise ValueError("unexpected fuzz/example SDK reference")
    if expected is None:
        expected = {(scenario, mode, deferred) for scenario in FUZZ_EXAMPLE_SCENARIOS
                    for mode in ("manual", "orchestrion") for deferred in (False, True)}
    seen = set()
    for row in report["scenarios"]:
        identity = row["scenario"], row["mode"], row["deferred"]
        if type(row["deferred"]) is not bool or identity in seen or identity not in expected:
            raise ValueError("unexpected or duplicated fuzz/example scenario")
        seen.add(identity)
        timing_cells(row, True)
        if row["status"] != "passed" or row["sdk"] != row["mini"]:
            raise ValueError("failed fuzz/example scenario")
    if seen != expected:
        raise ValueError("missing fuzz/example combinations")


def render(path):
    report = json.loads(path.read_text())
    rows = report["scenarios"]
    schema = report.get("schema_version", 1)
    if type(schema) is not int or schema not in (1, 2, 3, 4):
        raise ValueError("unsupported parity report schema")
    require_timing = schema >= 2
    if report["sdk_instrumentation"] != "orchestrion":
        raise ValueError("full SDK/Orchestrion reference was not executed")
    expected_rows = 64 if schema == 4 else 65
    if len(rows) < expected_rows or len({row["scenario"] for row in rows}) != len(rows):
        raise ValueError("missing or duplicated matrix scenarios")
    for row in rows:
        timing_cells(row, require_timing)
        if row["status"] != "passed" or row["sdk"] != row["mini"] or row["sdk_exit"] != row["mini_exit"]:
            raise ValueError("failed scenario: " + row["scenario"])

    evidence = {}
    names = ["manual", "spans", "packages", "fuzz", "telemetry", "testify"]
    if report["os"] in ("linux", "darwin"):
        names.append("uds")
    for name in names:
        evidence[name] = json.loads(path.with_name(path.stem + "-" + name + ".json").read_text())
    for name, item in evidence.items():
        timing_cells(item, require_timing)
        if name == "telemetry":
            if not item["semantic_counts_equal"] or not item["request_counts_match_http"]:
                raise ValueError("CI telemetry comparison was not verified")
        elif name != "testify" and (item["status"] != "passed" or item["sdk"] != item["mini"]):
            raise ValueError("failed additional fixture: " + name)
    fuzz_examples = None
    if schema == 4:
        fuzz_examples = json.loads(path.with_name(path.stem + "-fuzz-examples.json").read_text())
        validate_fuzz_examples(fuzz_examples)
        covered = json.loads(path.with_name(path.stem + "-fuzz-examples-coverage.json").read_text())
        covered_expected = {(scenario, "orchestrion", deferred)
                            for scenario in ("pass", "seed-lifecycle", "test-management", "active-fuzz", "skip-lifecycle", "parallel-duration", "filtered")
                            for deferred in (False, True)}
        validate_fuzz_examples(covered, covered_expected)
        if evidence["fuzz"].get("sdk_commit") != fuzz_examples["sdk_commit"]:
            raise ValueError("fuzz campaign reference differs from the full fuzz/example matrix")
    testify = evidence["testify"]
    if testify["status"] not in ("gap", "passed"):
        raise ValueError("Testify comparison was not verified")
    if testify["status"] == "passed":
        if not (testify["sdk_with_orchestrion"] == testify["sdk_with_poc"] == testify["mini"]):
            raise ValueError("Testify event counts differ")
        testify_rows = testify.get("scenarios", [])
        if len(testify_rows) < 25 or len({row["scenario"] for row in testify_rows}) != len(testify_rows):
            raise ValueError("missing or duplicated Testify scenarios")
        if testify.get("external_callers") and not any(row["scenario"] == "external-module-helper" for row in testify_rows):
            raise ValueError("missing external-module Testify evidence")
        for row in testify_rows:
            timing_cells(row, require_timing)
            if row["status"] != "passed" or row["sdk"] != row["mini"] or row["sdk_exit"] != row["mini_exit"]:
                raise ValueError("failed Testify scenario: " + row["scenario"])

    if schema == 3 or schema == 4 and report.get("execution_block") is not None:
        timing_cells({"timing": report["execution_block"]}, True)
        if report["execution_order"] not in (["sdk", "mini"], ["mini", "sdk"]):
            raise ValueError("invalid grouped execution order")

    totals = {key: sum(row["sdk"][key] for row in rows) for key in COUNT_FIELDS}
    lines = [
        "# CI Visibility parity evidence", "",
        ("Complete feature parity: **no**. The Testify instrumentation gap remains."
         if testify["status"] == "gap" else
         ("Testify parity passes for local and external-module callers. Complete product parity remains unverified."
          if testify.get("external_callers") else "Testify parity passes for local callers and helpers. Complete product parity remains unverified.")), "",
        f"SDK base: `{report['sdk_commit']}` (`{report['sdk_version']}`).",
        f"Runner: `{report['os']}/{report['architecture']}`, `{report['go']}`.", "",
        "Counts below are sessions/modules/suites/tests/spans. Every matrix row",
        "also compares CI attributes, exit status and hierarchy references;",
        "coverage and side payloads are checked when that feature is selected.", "",
        f"Matrix: {len(rows)} passing scenarios; aggregate SDK = Mini `{counts(totals)}`.", "",
        "Child walltimes are seconds for one observation per variant per case.",
        "They include startup, settings, execution/retries and shutdown/flush of",
        "the prebuilt binary. Compilation and harness comparison are excluded.",
        "Mini versus SDK is `(Mini / SDK - 1) * 100`; a negative value is shorter.",
        "These functional fixtures have no timing pass/fail threshold.", "",
        "| Scenario | Features | SDK events | Mini events | SDK wall (s) | Mini wall (s) | Mini vs SDK | Result |",
        "| --- | --- | --- | --- | ---: | ---: | ---: | --- |",
    ]
    if schema == 3 or schema == 4 and report.get("execution_block") is not None:
        sdk_wall, mini_wall, delta = timing_cells({"timing": report["execution_block"]}, True)
        # Insert the block measurement before the individual scenario table.
        lines[-2:-2] = [
            "Execution order: " + " then ".join(report["execution_order"]) + ".", "",
            f"| Entire {len(rows)}-scenario block | SDK wall (s) | Mini wall (s) | Mini vs SDK |",
            "| --- | ---: | ---: | ---: |",
            f"| Continuous walltime | {sdk_wall} | {mini_wall} | {delta} |", "",
            "Measured scope: " + report["execution_block"]["scope"] + ".", "",
        ]
    for row in rows:
        sdk_wall, mini_wall, delta = timing_cells(row, require_timing)
        lines.append(f"| {cell(row['scenario'])} | {cell(', '.join(row['features']))} | {counts(row['sdk'])} | {counts(row['mini'])} | {sdk_wall} | {mini_wall} | {delta} | Passed |")
    lines += ["", "## Additional fixtures", "",
              "| Fixture | SDK events | Mini events | Scope |", "| --- | --- | --- | --- |"]
    for name in names:
        if name in ("telemetry", "testify"):
            continue
        item = evidence[name]
        lines.append(f"| {name} | {counts(item['sdk'])} | {counts(item['mini'])} | {cell(item['scope'])} |")
    lines += ["", "### Additional fixture walltimes", "",
              "Scopes differ: the packages fixture also prepares and compiles via the CLI.",
              "The manual fixture injects a settings delay; historical Testify records may retain a grouping gap.",
              "Use the scope recorded in each JSON observation when comparing runs.", "",
              "| Fixture | SDK wall (s) | Mini wall (s) | Mini vs SDK | Measured scope |",
              "| --- | ---: | ---: | ---: | --- |"]
    for name in names:
        item = evidence[name]
        sdk_wall, mini_wall, delta = timing_cells(item, require_timing)
        scope = item.get("timing", {}).get("scope", "Not recorded")
        if name == "testify":
            scope += "; SDK column uses full Orchestrion reference"
        lines.append(f"| {name} | {sdk_wall} | {mini_wall} | {delta} | {cell(scope)} |")
    lines += ["", "CI telemetry: semantic count/rate metrics match; request counts are",
              "checked against each sender's actual HTTP requests. Batch sizes and",
              "timings may differ. Distributions are outside this counter fixture.", "",
              f"Testify: full SDK `{counts(testify['sdk_with_orchestrion'])}`, POC SDK `{counts(testify['sdk_with_poc'])}`, Mini `{counts(testify['mini'])}`.",
              (f"Known gap: {cell(testify['reason'])}." if testify["status"] == "gap" else
               f"Testify: {len(testify.get('scenarios', []))} passing scenarios; " + ("external-module callers covered." if testify.get("external_callers") else "external dependency call sites remain outside the overlay boundary.")), "",
              "This is loopback protocol evidence. Real intake/UI acceptance, an actual",
              "Bazel toolchain run and an external APM shim are unverified. Read",
              "`docs/ci-parity.md` for the exclusions and remaining feature coverage.", ""]
    if testify["status"] == "passed":
        lines += ["## Testify combinations", "",
                  "| Scenario | SDK events | Mini events | SDK wall (s) | Mini wall (s) | Mini vs SDK |",
                  "| --- | --- | --- | ---: | ---: | ---: |"]
        for row in testify_rows:
            sdk_wall, mini_wall, delta = timing_cells(row, require_timing)
            lines.append(f"| {cell(row['scenario'])} | {counts(row['sdk'])} | {counts(row['mini'])} | {sdk_wall} | {mini_wall} | {delta} |")
        lines.append("")
    if fuzz_examples is not None:
        lines += ["", "## Go Fuzz and executable Examples", "",
                  f"SDK PR #5442 reference: `{fuzz_examples['sdk_commit']}` (`{fuzz_examples['sdk_version']}`).",
                  "Manual and automatic instrumentation each run all 16 SDK fixture scenarios.",
                  "Mini is checked with normal and deferred delivery. Event counts and CI attributes",
                  "match; the fixtures also assert native exit codes, cleanup ordering, panic precedence",
                  "and durations. Active mutations and fuzz workers do not emit CI events.", "",
                  "| Scenario | Instrumentation | Deferred | SDK events | Mini events | SDK wall (s) | Mini wall (s) | Mini vs SDK |",
                  "| --- | --- | --- | --- | --- | ---: | ---: | ---: |"]
        for item in fuzz_examples["scenarios"]:
            sdk_wall, mini_wall, delta = timing_cells(item, True)
            lines.append(f"| {cell(item['scenario'])} | {cell(item['mode'])} | {item['deferred']} | {counts(item['sdk'])} | {counts(item['mini'])} | {sdk_wall} | {mini_wall} | {delta} |")

        lines += ["", "### Coverage combinations", "",
                  "Atomic coverage uses a production helper with real counters; session coverage",
                  "and the uploaded file bitmaps are compared. Ordinary tests must upload coverage.",
                  "Fuzz roots/seeds and Examples retain the SDK PR's upload behavior.", "",
                  "| Scenario | Deferred | SDK events | Mini events | SDK wall (s) | Mini wall (s) | Mini vs SDK |",
                  "| --- | --- | --- | --- | ---: | ---: | ---: |"]
        for item in covered["scenarios"]:
            sdk_wall, mini_wall, delta = timing_cells(item, True)
            lines.append(f"| {cell(item['scenario'])} | {item['deferred']} | {counts(item['sdk'])} | {counts(item['mini'])} | {sdk_wall} | {mini_wall} | {delta} |")

    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("report", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    text = render(args.report)
    args.output.write_text(text)


if __name__ == "__main__":
    main()
