"""Check a catalog trial's evidence against its case's `expected` outcome.

evaluate() reads one evidence folder (run.json, ddtest-results.jsonl,
final-response.md, retained/.testoptimization/testdrive.json,
retained/RETENTION.json) and classifies it:

  pass                 verdict and blockers match `expected`.
  verdict_mismatch     the verdict differs; `direction` is "worse" (a regression)
                       or "better" (an improvement: update `expected`).
  unexpected_blockers  the expected verdict, but a blocker `expected.blockers`
                       does not allow, or none of the expected ones.
  harness_failure      container, clone, setup, readiness, authentication,
                       timeout, interruption, retention, cleanup, a runner
                       error, or missing or malformed evidence.
  agent_failure        no ddtest result or report, no final answer, or the
                       answer fails run.json's agent_reporting check.

The verdict comes only from the retained report's summary.status. A report that
exists but is not valid JSON, or lacks summary.status or summary.blocking_checks,
is a harness_failure (`report`). Without a report the verdict is STOPPED, unless
ddtest's last result claims a validation verdict: then the report was lost, a
harness_failure (`report`), never a verdict taken from final_response.
A RETENTION.json whose status is not "ok" is a harness_failure (`retention`);
evidence from before RETENTION.json existed has none, which is not a failure.

Blockers are compared as stable identifiers (blocker_id) derived from
summary.blocking_checks, so a configuration's command line or a long remedy
text does not make a case fail.
"""

import fnmatch
import json
import os
from pathlib import Path
import re
import shlex
import traceback

import user_answers

EXPECTED_VERDICTS = ("COMPLETE LOCALLY", "PASSED", "INCOMPLETE", "FAILED")
# STOPPED: the agent ended without a validation report (only actual, never expected).
VERDICT_RANK = {"STOPPED": 0, "FAILED": 1, "INCOMPLETE": 2, "PASSED": 3, "COMPLETE LOCALLY": 4}
BLOCKER_VERDICTS = ("INCOMPLETE", "FAILED")
FAILING = ("verdict_mismatch:worse", "unexpected_blockers", "harness_failure", "agent_failure")
EXIT_PASS, EXIT_FAILED, EXIT_IMPROVED = 0, 1, 3
AGENTS = ("codex", "claude")
OVERRIDE_FIELDS = ("verdict", "blockers", "reason", "run", "ddtest")
FEATURES = ("auto-retries", "early-flake-detection", "skipping", "quarantine", "disabled", "attempt-to-fix")
# Lines that only restate other blockers; they never fail a case on their own
# when another blocker is present.
DERIVED = ("configuration: *", "not every project has every required feature result")
EXCERPT = 600
REPORT = Path("retained") / ".testoptimization" / "testdrive.json"
RETENTION = Path("retained") / "RETENTION.json"


def one_line(text, limit=None):
    text = " ".join(str(text).split())
    return text if limit is None or len(text) <= limit else text[:limit - 1] + "…"


# Context ddtest puts before an unresolved step's cause; the location already says where the step runs.
SCOPE_CONTEXT = re.compile(r"Runs (?:before|after) .*?, which (?:ddtest can't replay|it may prepare): ")
SCOPE_PLACE = re.compile(r"(?:^|; )([^;]+? / [^;]+? / step \d+) \(")
CLASS_LIMIT = 120


def without_context(cause):
    match = SCOPE_CONTEXT.match(cause)
    return cause[match.end():] if match else cause


def scope_class(cause):
    """The leading clause of an unresolved-scope cause, without its command or expression."""
    cause = without_context(cause)
    return one_line(re.split(r": |; |\. ", cause, maxsplit=1)[0].rstrip("."), CLASS_LIMIT)


def scope_ids(text):
    """unresolved CI scope: <file> / <job> / step <n>: <class>, one per step ddtest lists."""
    grouped = re.match(r"unresolved CI scope \(\d+ steps: (.*?)\): (.*)$", text)
    if grouped:
        places, cause = SCOPE_PLACE.findall(grouped[1]), grouped[2]
    else:
        rest = text.removeprefix("unresolved CI scope").removeprefix(":").strip()
        located = re.match(r"([^;]+? / [^;]+? / step \d+) \(", rest)
        if located and "): " in rest:
            places, cause = [located[1]], rest.split("): ", 1)[1]
        else:
            places, cause = [], rest
    label = scope_class(cause)
    return [f"unresolved CI scope: {place}: {label}" for place in places] or [f"unresolved CI scope: {label}"]


def blocker_ids(check, features=()):
    """Stable identifiers of one summary.blocking_checks entry.

    feature <name>: <status>      "<project>/<name>: <status>"
    configuration: <status>       "configuration <command>: <status>"
    runtime fidelity: substitute <runtime>
    unresolved CI scope: <file> / <job> / step <n>: <class>
                                  one per listed step; <class> is the cause's
                                  leading clause, without its command, and
                                  without ddtest's "Runs before the job's ..."
                                  context
    workflow <name>: <status>, CI preservation: <status>   remedy dropped
    compatibility / CI runtime / CI tracer agreement: <status>
    anything else                 its headline, up to the first ": " or ". "
    """
    text = one_line(check)
    head, dash, _ = text.partition(" — ")
    if dash and head.startswith(("workflow ", "CI preservation: ")):
        return [head]
    feature = re.search(r"/([a-z][a-z0-9-]*): ([a-z][a-z_ -]*)$", text)
    if feature and feature[1] in (*FEATURES, *features):
        return [f"feature {feature[1]}: {feature[2]}"]
    if text.startswith("configuration ") and ": " in text:
        return ["configuration: " + text.rsplit(": ", 1)[1]]
    substitute = re.match(r"runtime fidelity: validated on substitute (\S+)", text)
    if substitute:
        return ["runtime fidelity: substitute " + substitute[1]]
    if text.startswith("unresolved CI scope"):
        return scope_ids(text)
    if text.startswith(("compatibility: ", "CI runtime: ", "CI tracer agreement: ")):
        return [one_line(re.split(r"\. ", text, maxsplit=1)[0].rstrip("."), CLASS_LIMIT)]
    return [one_line(re.split(r": |\. ", text, maxsplit=1)[0].rstrip("."), CLASS_LIMIT)]


def blocker_id(check, features=()):
    """The first identifier of a check (a grouped unresolved-scope line has one per step)."""
    return blocker_ids(check, features)[0]


def needle(check):
    """Text that marks where a blocker first appears in ddtest's results."""
    text = one_line(check)
    if text.startswith("unresolved CI scope"):
        cause = text.split("): ", 1)[1] if "): " in text else text.partition(": ")[2]
        return without_context(cause)[:CLASS_LIMIT]
    return text[:CLASS_LIMIT]


def strings(value):
    if isinstance(value, str):
        yield value
    elif isinstance(value, dict):
        for item in value.values():
            yield from strings(item)
    elif isinstance(value, list):
        for item in value:
            yield from strings(item)


def first_rows(checks, identifiers, rows, features=()):
    """{identifier: ddtest-results.jsonl row} where each blocker's text first appears."""
    texts = [(number, one_line(" ".join(strings(row["result"])))) for number, row in rows
             if isinstance(row.get("result"), dict)]
    found = {}
    for check in checks:
        for identifier in blocker_ids(check, features):
            if identifier in identifiers and identifier not in found:
                marker = needle(check)
                found[identifier] = next((number for number, text in texts if marker and marker in text), None)
    return found


def expected_for(case, agent=None):
    """The case's expected outcome, with expected.agents.<agent> fields applied over it."""
    expected = dict(case.get("expected") or {})
    override = (expected.pop("agents", None) or {}).get(agent) or {}
    expected.update({key: value for key, value in override.items() if key in OVERRIDE_FIELDS})
    if "verdict" in override and override["verdict"] not in BLOCKER_VERDICTS and "blockers" not in override:
        expected.pop("blockers", None)
    return expected


def matches(identifier, patterns):
    return any(fnmatch.fnmatchcase(identifier, pattern) for pattern in patterns)


def compare_blockers(actual, allowed):
    """(unexpected, missing) identifiers; derived lines count only when alone."""
    primary = [identifier for identifier in actual if not matches(identifier, DERIVED)]
    considered = primary or actual
    unexpected = [identifier for identifier in considered if not matches(identifier, allowed)]
    missing = [pattern for pattern in allowed if not any(fnmatch.fnmatchcase(i, pattern) for i in actual)]
    return unexpected, missing


def read_json(path):
    try:
        return json.loads(path.read_text())
    except (OSError, ValueError):
        return None


def read_report(path):
    """(report, problem): (None, None) when there is no report file, (None, why)
    when one exists but is not a ddtest report, else (report, None)."""
    if not os.path.lexists(path):
        return None, None
    if path.is_symlink():
        return None, "is a symlink"
    try:
        text = path.read_text(errors="replace")
    except OSError as error:
        return None, f"unreadable ({error})"
    try:
        report = json.loads(text)
    except ValueError as error:
        return None, f"not valid JSON ({one_line(error, 200)})"
    problem = report_shape(report)
    return (None, problem) if problem else (report, None)


def retention_problem(evidence):
    """Why retained/RETENTION.json records a failed or unfinished retention, or None.
    Evidence retained before RETENTION.json existed has none: not recorded, not a failure."""
    path = evidence / RETENTION
    if not os.path.lexists(path):
        return None
    recorded = read_json(path)
    if not isinstance(recorded, dict):
        return f"retention outcome unreadable ({RETENTION.as_posix()})"
    if recorded.get("status") == "ok":
        return None
    if recorded.get("error"):
        return "retention failed: " + one_line(recorded["error"], 400)
    return f"retention {recorded.get('status')}: it did not finish ({RETENTION.as_posix()})"


def read_rows(path):
    rows = []
    try:
        lines = path.read_text(errors="replace").splitlines()
    except OSError:
        return rows
    for number, line in enumerate(lines, 1):
        try:
            row = json.loads(line)
        except ValueError:
            continue
        if isinstance(row, dict):
            rows.append((number, row))
    return rows


def response_verdict(text):
    match = re.search(r"Onboarding validation: (COMPLETE LOCALLY|PASSED|INCOMPLETE|FAILED)", text or "")
    return match[1] if match else None


def evidence_paths(evidence, log=None):
    paths = {"folder": evidence, "run_json": evidence / "run.json", "ddtest_results": evidence / "ddtest-results.jsonl",
             "final_response": evidence / "final-response.md", "changes": evidence / "changes.patch",
             "status": evidence / "status.txt", "testdrive": evidence / REPORT}
    candidates = [log] if log else [evidence.parent / (evidence.name + suffix) for suffix in (".log", ".runner.log")]
    paths["log"] = next((path for path in candidates if path and path.is_file()), candidates[0])
    return {key: str(path) for key, path in paths.items() if key in ("folder", "log") or path.exists()}


def failed_command(run):
    """(argv, finished, excerpt) of the last command that failed or never finished."""
    for command in reversed(run.get("commands") or []):
        if command.get("exit_code") != 0:
            return ([str(part) for part in command.get("argv") or []], command.get("exit_code") is not None,
                    str(command.get("output_excerpt") or "")[-EXCERPT:].strip())
    return [], True, ""


def harness_problem(run, log):
    """(category, step, excerpt) when the trial failed outside the agent's own work."""
    status, kind, stage = run.get("status"), run.get("failure_kind"), run.get("failure_stage")
    cleanup = (run.get("isolation") or {}).get("cleanup")
    if status == "completed" and cleanup in (None, "removed"):
        return None
    if status == "agent_failed" and kind != "infrastructure" or status == "failed" and kind == "agent":
        return None
    argv, finished, excerpt = failed_command(run)
    if status == "interrupted":
        category = "interrupted"
    elif status in ("preparing", "agent_running") or cleanup == "failed" or stage == "container_cleanup":
        category = "cleanup"
    elif kind == "authentication" or stage == "model_authentication":
        category = "authentication"
    elif stage in ("runtime_readiness", "storage_readiness"):
        category = "readiness"
    elif argv[:1] == ["git"] and argv[1:2] in (["clone"], ["fetch"], ["checkout"], ["rev-parse"]):
        category = "clone"
    elif argv and not finished:
        category = "timeout"
    elif kind == "evaluator_environment":
        category = "setup"
    elif kind == "infrastructure":
        category = "infrastructure"
    else:
        category = "trial"
    step = f"{category} (run.json status {status}" + "".join(
        f", {key} {value}" for key, value in (("failure_kind", kind), ("failure_stage", stage)) if value) + ")"
    if argv:
        step += ": " + one_line(shlex.join(argv), 200) + (" did not finish" if not finished else " failed")
    error = one_line(run.get("error") or "; ".join(map(str, run.get("collection_errors") or [])), 400)
    return category, step, "\n".join(part for part in (error, excerpt) if part) or log_tail(log)


def log_tail(log):
    try:
        return Path(log).read_text(errors="replace")[-EXCERPT:].strip()
    except (OSError, TypeError):
        return ""


def agent_problem(run, rows, report, evidence):
    results = [(number, row["result"]) for number, row in rows if isinstance(row.get("result"), dict)]
    if run.get("status") in ("agent_failed", "failed"):
        reporting = run.get("agent_reporting") or {}
        reason = "the agent CLI reported an error" if run.get("failure_kind") == "agent" else \
            "run.json agent_reporting " + str(reporting.get("status")) + ": " + one_line(reporting.get("reason", ""), 200)
        return reason + ("; " + one_line(run["error"], 300) if run.get("error") else "")
    if not results and report is None:
        return "the agent finished without any ddtest result or report"
    if not (evidence / "final-response.md").is_file():
        return "no final-response.md: the agent ended without a final answer"
    reporting = run.get("agent_reporting") or {}
    if reporting.get("status") != "pass":
        details = [*reporting.get("differences", []), *reporting.get("missing", []), *reporting.get("unsupported", [])]
        return ("run.json agent_reporting " + str(reporting.get("status", "missing")) + ": "
                + one_line(reporting.get("reason", "no agent_reporting check"), 200)
                + ("; " + one_line("; ".join(map(str, details)), 300) if details else ""))
    return None


def last_result(rows):
    """(row number, first row of the last turn, ddtest result) of the last ddtest result."""
    results = [(number, row) for number, row in rows if isinstance(row.get("result"), dict)]
    if not results:
        return None, None, {}
    number, row = results[-1]
    first = min((n for n, r in rows if r.get("turn") == row.get("turn")), default=number)
    return number, first, row["result"]


def base_record(case, evidence, log=None, agent=None, ddtest_label=None):
    """A record with no outcome yet: the case's expected outcome and the evidence paths."""
    evidence = Path(evidence)
    agent = agent or (evidence.name.rsplit("-", 1)[-1] if evidence.name.rsplit("-", 1)[-1] in AGENTS else None)
    expected = expected_for(case, agent)
    allowed = list(expected.get("blockers") or [])
    record = {"id": case["id"], "agent": agent, "provider": case.get("ci_provider"), "framework": case.get("framework"),
              "outcome": None, "direction": None,
              "expected": {key: expected.get(key) for key in ("verdict", "blockers", "reason", "run", "ddtest")},
              "actual": {"verdict": None, "blockers": [], "blocking_checks": [], "final_reason": None,
                         "run_status": None, "ddtest_version": None, "ddtest_label": ddtest_label},
              "problems": [], "notes": [], "evidence": evidence_paths(evidence, log), "next_step": None}
    record["expected"]["source"] = f"expected.agents.{agent}" if agent in ((case.get("expected") or {}).get("agents") or {}) \
        else "expected"
    record["expected"]["blockers"] = allowed
    return record, expected, allowed


def default_rerun(case):
    return f"run_catalog.py {case['id'].split('-')[0]} <same options> --output <new folder>"


def failure(case, evidence, category, problem, excerpt="", log=None, rerun=None, agent=None, ddtest_label=None):
    """A harness_failure record for a case whose evidence was not (or could not be) evaluated."""
    record, _, _ = base_record(case, evidence, log, agent, ddtest_label)
    record["harness_category"] = category
    return finish(record, "harness_failure", problem, excerpt, rerun or default_rerun(case))


def error_text(error):
    """A one-line summary and a traceback excerpt of an exception."""
    trace = "".join(traceback.format_exception(type(error), error, error.__traceback__))
    return one_line(f"{type(error).__name__}: {error}", 300), trace[-EXCERPT:].strip()


def type_name(value):
    return "null" if value is None else type(value).__name__


def report_shape(report):
    """Why a parsed testdrive.json can't be read as a report, or None. A report
    needs summary.status (the verdict) and summary.blocking_checks (its blockers)."""
    if not isinstance(report, dict):
        return f"top level is {type_name(report)}, not an object"
    for key, kind in (("summary", dict), ("features", list)):
        if report.get(key) is not None and not isinstance(report[key], kind):
            return f"{key} is {type_name(report[key])}, not {'an object' if kind is dict else 'a list'}"
    summary = report.get("summary")
    if summary is None:
        return "summary is missing"
    if summary.get("status") is None:
        return "summary.status is missing"
    if not isinstance(summary["status"], str):
        return f"summary.status is {type_name(summary['status'])}, not a string"
    if not summary["status"].strip():
        return "summary.status is empty"
    checks = summary.get("blocking_checks")
    if checks is None:
        return "summary.blocking_checks is missing"
    if not isinstance(checks, list):
        return f"summary.blocking_checks is {type_name(checks)}, not a list"
    for index, check in enumerate(checks):
        if not isinstance(check, str):
            return f"summary.blocking_checks[{index}] is {type_name(check)}, not a string"
    return None


def evaluate(case, evidence, log=None, rerun=None, agent=None, ddtest_label=None):
    """Outcome record for one case's evidence folder (see the module docstring).

    Malformed evidence never raises: a report of the wrong shape is a
    harness_failure (`report`), and any other error while reading the evidence
    is a harness_failure (`evaluation`) with the exception and a traceback excerpt.
    """
    rerun = rerun or default_rerun(case)
    try:
        return classify(case, Path(evidence), log, rerun, agent, ddtest_label)
    except Exception as error:  # noqa: BLE001 - malformed evidence must not stop the batch
        summary, trace = error_text(error)
        return failure(case, evidence, "evaluation", f"evaluating the evidence failed: {summary}", trace,
                       log, rerun, agent, ddtest_label)


def classify(case, evidence, log, rerun, agent, ddtest_label):
    record, expected, allowed = base_record(case, evidence, log, agent, ddtest_label)
    agent = record["agent"]
    actual = record["actual"]
    log = record["evidence"]["log"]
    if not evidence.is_dir():
        record["harness_category"] = "evidence"
        return finish(record, "harness_failure", f"no evidence folder {evidence}", log_tail(log), rerun)
    run = read_json(evidence / "run.json")
    if not isinstance(run, dict):
        record["harness_category"] = "evidence"
        return finish(record, "harness_failure", "run.json missing or unreadable", log_tail(log), rerun)
    actual["run_status"] = run.get("status")
    actual["ddtest_version"] = run.get("ddtest_version")
    actual["ddtest_label"] = run.get("ddtest_label") or ddtest_label
    rows = read_rows(evidence / "ddtest-results.jsonl")
    row, first_row, result = last_result(rows)
    report, malformed = read_report(evidence / REPORT)
    summary = (report or {}).get("summary") or {}
    features = [str(feature.get("name")) for feature in (report or {}).get("features") or [] if isinstance(feature, dict)]
    checks = [str(check) for check in summary.get("blocking_checks") or []]
    actual["blocking_checks"] = [one_line(check, 240) for check in checks]
    actual["blockers"] = list(dict.fromkeys(identifier for check in checks for identifier in blocker_ids(check, features)))
    if result:
        actual["final_reason"] = one_line(" ".join(str(result.get(key)) for key in ("status", "code") if result.get(key))
                                          + ": " + str(result.get("reason") or ""), 240)
        record["ddtest_rows"] = {"last": row, "last_turn_from": first_row}
        code = user_answers.unscriptable(result)
        if code:
            # The trial ended on a decision only the user can make; no answer form covers it.
            record["question"] = {"code": code, "scriptable": False, "row": row, "next_action": result.get("next_action")}
            record["notes"].append(f"question not scriptable: {code} (ddtest-results.jsonl row {row})")
    # The verdict is the report's; final_response never stands in for a missing report.
    claimed = response_verdict(result.get("final_response")) if report is None and not malformed else None
    actual["verdict"] = summary.get("status") if report else "STOPPED" if result and not (claimed or malformed) else None
    retention = retention_problem(evidence)
    problem = harness_problem(run, log)
    if problem:
        record["harness_category"] = problem[0]
        record["problems"] += [text for text in (retention, malformed and f"malformed {REPORT.as_posix()}: {malformed}")
                               if text]
        return finish(record, "harness_failure", *problem[1:], rerun)
    if retention:
        record["harness_category"] = "retention"
        if malformed:
            record["problems"].append(f"malformed {REPORT.as_posix()}: {malformed}")
        return finish(record, "harness_failure", retention, "", rerun)
    if malformed:
        record["harness_category"] = "report"
        return finish(record, "harness_failure", f"malformed {REPORT.as_posix()}: {malformed}", "", rerun)
    if claimed:
        record["harness_category"] = "report"
        return finish(record, "harness_failure", f"no {REPORT.as_posix()}, but ddtest's last result (ddtest-results.jsonl "
                      f"row {row}) claims Onboarding validation: {claimed}; the verdict is never taken from "
                      "final_response", "", rerun)
    problem = agent_problem(run, rows, report, evidence)
    if problem:
        return finish(record, "agent_failure", problem, "", rerun)
    want, got = expected.get("verdict"), actual["verdict"]
    new = [identifier for identifier in actual["blockers"]
           if not matches(identifier, DERIVED) and not matches(identifier, allowed)]
    if new and (VERDICT_RANK.get(got, -1) < VERDICT_RANK.get(want, -1) or got == want and allowed):
        by_number = dict(rows)
        record["introduced"] = {
            identifier: {"row": number, "result": one_line(" ".join(
                str(by_number[number]["result"].get(key)) for key in ("status", "code")
                if by_number[number]["result"].get(key))) if number else None}
            for identifier, number in first_rows(checks, new, rows, features).items()}
    if got != want:
        record["direction"] = "better" if VERDICT_RANK.get(got, -1) > VERDICT_RANK.get(want, -1) else "worse"
        return finish(record, "verdict_mismatch", f"verdict {got}, expected {want}", "", rerun)
    if want in BLOCKER_VERDICTS and allowed:
        unexpected, missing = compare_blockers(actual["blockers"], allowed)
        if unexpected:
            return finish(record, "unexpected_blockers", "blockers not in expected.blockers: " + "; ".join(unexpected),
                          "", rerun)
        if len(missing) == len(allowed):
            return finish(record, "unexpected_blockers", "none of expected.blockers reported: " + "; ".join(missing),
                          "", rerun)
        if missing:
            record["notes"].append("expected blockers no longer reported (trim expected.blockers): " + "; ".join(missing))
    return finish(record, "pass", None, "", rerun)


def finish(record, outcome, problem, excerpt, rerun):
    record["outcome"] = outcome
    if problem:
        record["problems"].append(problem)
    if excerpt:
        record["excerpt"] = excerpt[-EXCERPT:]
    record["next_step"] = next_step(record, rerun)
    return record


HARNESS_HINTS = {
    "evidence": "the trial left no usable run.json; read {log}",
    "interrupted": "the trial was interrupted; nothing to fix in ddtest",
    "retention": "copying the reports and untracked files to {evidence}/retained failed (see problems); "
                 "check disk space and permissions there",
    "cleanup": "run `run_eval.py --cleanup {evidence}` and confirm its container is gone (docker ps) before retrying",
    "authentication": "the agent CLI login failed; log in again on the host",
    "readiness": "{name}'s readiness_commands failed on the image; check the image and the commands",
    "clone": "check network access and that {name}'s pinned commit still exists",
    "timeout": "a command hit --timeout or was killed; check the trial for a hang, or raise --timeout",
    "setup": "{name}'s setup failed on the image (setup_commands, trims or harness flags); check the image and the command",
    "infrastructure": "the agent CLI lost its model connection; check network or service status",
    "trial": "check Docker, network and {log}",
    "exists": "{evidence} is from an earlier run; check it with --evaluate-only or pick a new --output",
    "report": "the retained testdrive.json is missing or not a ddtest report (see problems); inspect "
              "{evidence}/retained and {log}",
    "evaluation": "catalog_check.py could not read {evidence} (see problems and output); fix the evidence or the "
                  "checker and check it with --evaluate-only",
    "runner": "run_catalog.py failed on this case (see problems and output); its cleanup ran (see {log}); "
              "fix the cause",
    "not_run": "the case never ran because its lane stopped (see problems); fix the cause",
}


def ddtest_provenance(actual):
    """The ddtest build to record in expected.ddtest: the label, else a real version."""
    if actual.get("ddtest_label"):
        return actual["ddtest_label"]
    version = actual.get("ddtest_version")
    if version and version != "dev":
        return version
    return f"<ddtest commit> (run.json says {version or 'no version'}; pass --ddtest-label)"


def introduced_text(record, span):
    introduced = record.get("introduced") or {}
    if not introduced:
        return f"investigate ddtest from {span}"
    parts = []
    for identifier, where in introduced.items():
        parts.append(f"`{identifier}` first appears in ddtest-results.jsonl row {where['row']} ({where['result']})"
                     if where["row"] else f"`{identifier}` appears only in the final report")
    return "investigate ddtest: " + "; ".join(parts)


def next_step(record, rerun):
    name, outcome = record["id"], record["outcome"]
    actual, expected = record["actual"], record["expected"]
    rows = record.get("ddtest_rows") or {}
    span = ("ddtest-results.jsonl " + (f"row {rows['last']}" if rows["last_turn_from"] == rows["last"] else
                                       f"rows {rows['last_turn_from']}-{rows['last']}")) if rows else "ddtest-results.jsonl"
    provenance = f"run <this output folder>, ddtest {ddtest_provenance(actual)}"
    target = f"cases.yaml {name} {expected.get('source', 'expected')}"
    if outcome == "pass":
        return None
    if outcome == "harness_failure":
        hint = HARNESS_HINTS[record.get("harness_category", "trial")].format(
            name=name, evidence=record["evidence"]["folder"], log=record["evidence"]["log"])
        return f"harness: {hint}; then rerun `{rerun}`"
    if outcome == "agent_failure":
        return (f"agent ({record['agent'] or 'unknown'}): inspect final-response.md vs ddtest final_response (testdrive.json summary.final_response) and "
                f"run.json agent_reporting; activity.jsonl shows what the agent did; rerun `{rerun}` if it was a one-off")
    suggested = "; ".join(i for i in actual["blockers"] if not matches(i, DERIVED)) or "; ".join(actual["blockers"])
    blockers = suggested if actual["verdict"] in BLOCKER_VERDICTS and suggested else "none"
    if outcome == "verdict_mismatch" and record["direction"] == "better":
        return (f"update expected: set {target}.verdict to {actual['verdict']}, blockers to {blockers}, "
                f"reason to why, and {provenance}")
    agent = record["agent"] or "<agent>"
    if outcome == "verdict_mismatch":
        head = (f"verdict regression (expected {expected['verdict']}: {one_line(expected.get('reason') or '', 160)}); "
                f"compare {span} and testdrive.json summary.blocking_checks, then pick one")
        change = f"verdict {actual['verdict']}, blockers {blockers}"
    elif outcome == "unexpected_blockers":
        head = f"blockers: read the listed checks in testdrive.json summary.blocking_checks and {span}, then pick one"
        change = f"blockers {suggested or 'the reported ones'}"
    else:
        return None
    return (f"{head}: (1) ddtest bug: {introduced_text(record, span)}; keep expected so the regression stays visible; "
            f"(2) intentional ddtest change: update cases.yaml {name} expected ({change}, reason, {provenance}); "
            f"(3) agent-specific: add cases.yaml {name} expected.agents.{agent} ({change}, its own reason, {provenance})")


def label(record):
    outcome = record["outcome"]
    if outcome == "verdict_mismatch":
        return "verdict_mismatch (better: update expected)" if record["direction"] == "better" else \
            "verdict_mismatch (worse)"
    return outcome


def is_failing(record):
    key = record["outcome"] + (":" + record["direction"] if record["direction"] else "")
    return key in FAILING


def exit_code(records):
    if any(is_failing(record) for record in records):
        return EXIT_FAILED
    if any(record["outcome"] != "pass" for record in records):
        return EXIT_IMPROVED
    return EXIT_PASS


def disagreements(records):
    """Note cases whose agents reached different verdicts: agent behavior, not ddtest."""
    verdicts = {}
    for record in records:
        verdicts.setdefault(record["id"], {})[record["agent"]] = record["actual"]["verdict"]
    notes = []
    for identifier, by_agent in verdicts.items():
        if len(by_agent) > 1 and len(set(by_agent.values())) > 1:
            text = "agents disagree: " + ", ".join(f"{agent} {verdict or 'no verdict'}" for agent, verdict in by_agent.items())
            notes.append(f"{identifier}: {text}")
            note = text + " (agent behavior, not ddtest: compare their activity.jsonl)"
            for record in records:
                if record["id"] == identifier and note not in record["notes"]:
                    record["notes"].append(note)
    return notes


def table(records):
    header = ("case", "agent", "provider", "framework", "expected", "actual", "outcome")
    rows = [header] + [(r["id"], r["agent"] or "-", r["provider"] or "", r["framework"] or "", r["expected"]["verdict"] or "-",
                        r["actual"]["verdict"] or "-", label(r)) for r in records]
    widths = [max(len(str(row[index])) for row in rows) for index in range(len(header))]
    return "\n".join("  ".join(str(cell).ljust(width) for cell, width in zip(row, widths)).rstrip() for row in rows)


def error_block(record):
    actual, expected = record["actual"], record["expected"]
    title = "IMPROVEMENT" if record["outcome"] == "verdict_mismatch" and record["direction"] == "better" else "ERROR"
    lines = [f"{title} {record['id']} ({record['agent'] or 'unknown agent'}): {label(record)}",
             "  what: " + "; ".join(record["problems"]),
             f"  expected: {expected['verdict']}" + (f", blockers {'; '.join(expected['blockers'])}" if expected["blockers"] else "")
             + f" ({one_line(expected.get('reason') or '', 160)})",
             f"  actual: {actual['verdict'] or 'no verdict'}, run {actual['run_status'] or 'unknown'}, "
             f"ddtest {actual.get('ddtest_label') or actual.get('ddtest_version') or 'unknown'}"
             + (f", blockers {'; '.join(actual['blockers'])}" if actual["blockers"] else "")]
    if actual["final_reason"]:
        rows = record.get("ddtest_rows") or {}
        lines.append(f"  ddtest final (row {rows.get('last')}): {actual['final_reason']}")
    for check in actual["blocking_checks"][:5]:
        lines.append("  blocking: " + check)
    if len(actual["blocking_checks"]) > 5:
        lines.append(f"  blocking: … {len(actual['blocking_checks']) - 5} more in testdrive.json")
    if record.get("excerpt"):
        lines += ["  output:"] + ["    " + line for line in record["excerpt"].splitlines()[-8:]]
    paths = record["evidence"]
    names = [Path(paths[key]).name for key in ("run_json", "ddtest_results", "final_response", "changes", "status")
             if key in paths]
    lines.append(f"  evidence: {paths['folder']}" + (f" ({', '.join(names)})" if names else " (missing)"))
    if "testdrive" in paths:
        lines.append(f"  report: {paths['testdrive']}")
    lines.append(f"  log: {paths.get('log')}")
    for note in record["notes"]:
        lines.append("  note: " + note)
    lines.append("  next: " + record["next_step"])
    return "\n".join(lines)


def report(records, stream):
    """Print the summary table, then one block per case that did not pass."""
    disagree = disagreements(records)
    print("\n" + table(records), file=stream)
    for note in disagree:
        print("NOTE " + note, file=stream)
    for record in records:
        if record["outcome"] != "pass":
            print("\n" + error_block(record), file=stream)
        elif record["notes"]:
            print(f"\nNOTE {record['id']} ({record['agent']}): " + "; ".join(record["notes"]), file=stream)
    code = exit_code(records)
    if code == EXIT_IMPROVED:
        print(f"\nOnly improvements: update cases.yaml expected as each block says (exit {EXIT_IMPROVED}).", file=stream)
    stream.flush()
    return code
