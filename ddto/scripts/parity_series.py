#!/usr/bin/env python3
"""Summarize balanced whole-matrix observations; retain each input report."""

import argparse
import json
from pathlib import Path
import statistics

import parity_report


def summarize(paths):
    if len(paths) < 2 or len(paths) % 2 or len({p.resolve() for p in paths}) != len(paths):
        raise ValueError("use an even number of distinct reports, at least two")
    samples, identity, contracts, orders = [], None, None, []
    for path in paths:
        # Require all matrix and supplemental feature evidence before timing it.
        parity_report.render(path)
        report = json.loads(path.read_text())
        if report.get("schema_version") != 3:
            raise ValueError("whole-matrix timings require schema 3")
        current_identity = {key: report[key] for key in
                            ("sdk_commit", "sdk_version", "sdk_instrumentation", "go", "os", "architecture")}
        current_contracts = [(row["scenario"], row["features"], row["sdk"], row["mini"], row["sdk_exit"], row["mini_exit"])
                             for row in report["scenarios"]]
        if identity is None:
            identity, contracts = current_identity, current_contracts
        elif current_identity != identity or current_contracts != contracts:
            raise ValueError("reference, runner or scenario contracts changed between rounds")
        block = report["execution_block"]
        if samples and block["scope"] != samples[0]["scope"]:
            raise ValueError("measured scope changed between rounds")
        order = report["execution_order"]
        orders.append(order)
        samples.append({"report": str(path), "order": order, "scope": block["scope"],
                        "sdk_wall_ns": block["sdk_wall_ns"], "mini_wall_ns": block["mini_wall_ns"]})
    if orders.count(["sdk", "mini"]) != orders.count(["mini", "sdk"]):
        raise ValueError("SDK-first and Mini-first rounds must be balanced")
    stats = {}
    for backend in ("sdk", "mini"):
        values = [sample[backend + "_wall_ns"] / 1e9 for sample in samples]
        mean = statistics.mean(values)
        stats[backend] = {"median_seconds": statistics.median(values), "mean_seconds": mean,
                          "min_seconds": min(values), "max_seconds": max(values),
                          "cv_percent": 100 * statistics.pstdev(values) / mean}
    delta = 100 * (stats["mini"]["median_seconds"] / stats["sdk"]["median_seconds"] - 1)
    paired = statistics.median([100 * (s["mini_wall_ns"] / s["sdk_wall_ns"] - 1) for s in samples])
    return {"identity": identity, "scenario_count": len(contracts), "samples": samples,
            "statistics": stats, "median_delta_percent": delta, "median_paired_delta_percent": paired}


def markdown(summary):
    identity, stats = summary["identity"], summary["statistics"]
    lines = ["# Repeated CI parity execution", "",
             f"Runner: `{identity['os']}/{identity['architecture']}`, `{identity['go']}`.",
             f"SDK: `{identity['sdk_commit']}`; instrumentation: `{identity['sdk_instrumentation']}`.", "",
             f"{len(summary['samples'])} measured rounds, with equal numbers of SDK-first and Mini-first rounds.",
             f"Every round compares all {summary['scenario_count']} matrix scenarios and all additional fixtures.",
             "The primary measurement is one continuous 65-scenario block per variant,",
             "including receiver setup, child startup, settings, execution/retries and shutdown/flush.",
             "Compilation and the differential comparisons are outside that block.",
             "The seven additional fixtures have separate raw timing records and are not included in the block.", "",
             "| Scope | SDK median (s) | Mini median (s) | Mini vs SDK |",
             "| --- | ---: | ---: | ---: |",
             f"| Entire matrix execution | {stats['sdk']['median_seconds']:.6f} | {stats['mini']['median_seconds']:.6f} | {summary['median_delta_percent']:+.1f}% |", "",
             "| Variant | Mean (s) | Min (s) | Max (s) | CV |",
             "| --- | ---: | ---: | ---: | ---: |"]
    for backend in ("sdk", "mini"):
        value = stats[backend]
        lines.append(f"| {backend} | {value['mean_seconds']:.6f} | {value['min_seconds']:.6f} | {value['max_seconds']:.6f} | {value['cv_percent']:.2f}% |")
    lines += ["", "CV is the standard deviation divided by the mean; lower means less spread.",
              "Negative Mini-versus-SDK percentages indicate shorter duration.",
              f"Median of the paired per-round differences: {summary['median_paired_delta_percent']:+.1f}%.", "",
              "| Round | Order | SDK block (s) | Mini block (s) | Mini vs SDK |",
              "| --- | --- | ---: | ---: | ---: |"]
    for i, sample in enumerate(summary["samples"], 1):
        sdk, mini = sample["sdk_wall_ns"] / 1e9, sample["mini_wall_ns"] / 1e9
        lines.append(f"| {i} | {' then '.join(sample['order'])} | {sdk:.6f} | {mini:.6f} | {(mini / sdk - 1) * 100:+.1f}% |")
    lines += ["", "Individual reports retain event counts, CI-attribute comparisons, case durations",
              "and the separate Testify comparison. Compilation time and per-fixture clocks are not interchangeable",
              "with the continuous execution clock. These are local loopback results, not real-intake measurements.", ""]
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("reports", nargs="+", type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--json-output", required=True, type=Path)
    args = parser.parse_args()
    summary = summarize(args.reports)
    args.output.write_text(markdown(summary))
    args.json_output.write_text(json.dumps(summary, indent=2) + "\n")


if __name__ == "__main__":
    main()
