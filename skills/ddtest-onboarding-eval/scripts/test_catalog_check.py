"""Offline checks of catalog_check.py and run_catalog.py --evaluate-only on synthetic evidence."""

import contextlib
import copy
import io
import json
import os
from pathlib import Path
import tempfile
import unittest

import catalog_check
import run_catalog

FIXTURES = Path(__file__).resolve().parent / "fixtures" / "catalog"

try:
    import yaml
except ImportError:
    yaml = None


def real_report(name):
    """Trimmed testdrive.json of a real trial: status, blocking checks, features."""
    return json.loads((FIXTURES / f"{name}-testdrive.json").read_text())


def report(status, checks=(), final=None):
    final = final or f"Onboarding validation: {status}. Local validation: PASSED."
    return {"summary": {"status": status, "blocking_checks": list(checks), "final_response": final}, "features": []}


def write_evidence(folder, run=None, testdrive=None, rows=None, final=True, log=None):
    """A trial evidence folder as run_eval.py and run_catalog.py leave it."""
    folder.mkdir(parents=True)
    run = {"status": "completed", "isolation": {"cleanup": "removed"}, "ddtest_version": "v1.0.0-test",
           "agent_reporting": {"status": "pass", "reason": "ok"},
           "commands": [{"argv": ["git", "clone", "--", "https://example.invalid/r.git", "/trial/repo"], "exit_code": 0,
                         "output_excerpt": ""}], **(run or {})}
    (folder / "run.json").write_text(json.dumps(run))
    if testdrive is not None:
        path = folder / "retained" / ".testoptimization" / "testdrive.json"
        path.parent.mkdir(parents=True)
        path.write_text(json.dumps(testdrive))
    if rows is None:
        summary = (testdrive or {}).get("summary", {})
        rows = [{"turn": 1, "result": None},
                {"turn": 1, "result": {"status": "needs_input", "code": "ci_provider_required", "reason": "which CI?"}},
                {"turn": 2, "result": {"status": "validation_failed" if summary.get("status") != "COMPLETE LOCALLY" else "complete",
                                       "code": "validation_incomplete", "reason": "; ".join(summary.get("blocking_checks", [])),
                                       "final_response": summary.get("final_response", "")}}]
    (folder / "ddtest-results.jsonl").write_text("".join(json.dumps(row) + "\n" for row in rows))
    if final:
        (folder / "final-response.md").write_text(((testdrive or {}).get("summary", {}).get("final_response") or "done") + "\n")
    (folder / "changes.patch").write_text("")
    (folder / "status.txt").write_text("")
    if log is not None:
        (folder.parent / f"{folder.name}.log").write_text(log)
    return folder


def case(identifier="E08-kitchensink", verdict="INCOMPLETE", blockers=None, agents=None):
    expected = {"verdict": verdict, "reason": "the known reason", "run": "rerun4", "ddtest": "abc"}
    if blockers is not None:
        expected["blockers"] = blockers
    if agents is not None:
        expected["agents"] = agents
    return {"id": identifier, "ci_provider": "gitlab", "framework": "cypress", "expected": expected}


class BlockerIdTests(unittest.TestCase):
    def test_real_reports_reduce_to_stable_identifiers(self):
        e08 = real_report("E08")
        self.assertEqual([catalog_check.blocker_id(check) for check in e08["summary"]["blocking_checks"]],
                         ["Cypress real-suite coverage was not observed", "configuration: incomplete"])
        e15 = real_report("E15")
        identifiers = list(dict.fromkeys(catalog_check.blocker_id(check) for check in e15["summary"]["blocking_checks"]))
        self.assertEqual(identifiers, ["compatibility: suspected regression",
                                       *(f"feature {name}: inconclusive" for name in catalog_check.FEATURES),
                                       "not every project has every required feature result", "configuration: incomplete"])
        self.assertEqual(real_report("E02")["summary"]["blocking_checks"], [])

    def test_other_blocker_forms(self):
        for check, identifier in (
                ("runtime fidelity: validated on substitute Java 17.0.20.1; .github/workflows/ci.yml / build runs on Java 21. "
                 "Accept this evidence with --accept-substitute-runtime, or set JAVA_HOME and rerun", "runtime fidelity: substitute Java"),
                ("unresolved CI scope: release.yml / r / step 4 (./mvnw release:prepare): Repository script may run "
                 "scalatest tests; ddtest cannot tell which ones: ./mvnw release:prepare",
                 "unresolved CI scope: release.yml / r / step 4: Repository script may run scalatest tests"),
                ("unresolved CI scope: .github/ci.yml / test: matrix include is not a list",
                 "unresolved CI scope: .github/ci.yml / test"),
                ("workflow git_report_exclusions: failed — add /.testoptimization/ to .gitignore", "workflow git_report_exclusions: failed"),
                ("CI preservation: failed — CI matrix entries were removed", "CI preservation: failed"),
                ("CI runtime: inconclusive", "CI runtime: inconclusive"),
                ("Java application coverage was not observed. dd-java-agent reports per-test coverage", "Java application coverage was not observed"),
                ("configuration: ./gradlew test (A=b: c)/quarantine: failed", "feature quarantine: failed"),
                ("owned cleanup did not pass", "owned cleanup did not pass")):
            self.assertEqual(catalog_check.blocker_id(check), identifier)

    def test_grouped_scope_line_has_one_identifier_per_step(self):
        check = ("unresolved CI scope (2 steps: a.yml / j / step 1 (echo x; y); a.yml / j / step 2 (gpg --import)): Runs "
                 "before the job's scalatest tests and may change what they run, which ddtest can't replay: Unsupported "
                 "CI shell: pwsh")
        self.assertEqual(catalog_check.blocker_ids(check),
                         ["unresolved CI scope: a.yml / j / step 1: Unsupported CI shell",
                          "unresolved CI scope: a.yml / j / step 2: Unsupported CI shell"])

    def test_real_unresolved_scope_reduces_to_class_and_step(self):
        e17 = real_report("E17")["summary"]["blocking_checks"]
        identifiers = list(dict.fromkeys(i for check in e17 for i in catalog_check.blocker_ids(check)))
        self.assertEqual(identifiers, [
            "runtime fidelity: substitute Java", "CI runtime: inconclusive",
            *(f"unresolved CI scope: .github/workflows/release.yml / release / step {step}: "
              "CI command expression was not resolved" for step in (1, 5, 9))])
        self.assertTrue(all(len(identifier) < 120 for identifier in identifiers))
        e04 = real_report("E04")["summary"]["blocking_checks"]
        self.assertIn("unresolved CI scope: .gitlab-ci.yml / acceptance test / step 4: Cannot statically resolve command",
                      [catalog_check.blocker_id(check) for check in e04])

    def test_derived_lines_only_count_alone(self):
        allowed = ["Cypress real-suite coverage was not observed"]
        self.assertEqual(catalog_check.compare_blockers(
            ["Cypress real-suite coverage was not observed", "configuration: incomplete"], allowed), ([], []))
        self.assertEqual(catalog_check.compare_blockers(["configuration: incomplete"], allowed),
                         (["configuration: incomplete"], allowed))
        self.assertEqual(catalog_check.compare_blockers(["feature skipping: failed"], ["feature *: inconclusive"]),
                         (["feature skipping: failed"], ["feature *: inconclusive"]))


class EvaluateTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)

    def evaluate(self, expected_case, name="E08-kitchensink-codex", **evidence):
        folder = write_evidence(self.root / name, **evidence)
        return catalog_check.evaluate(expected_case, folder)

    def test_real_e08_matches_its_expected_blocker(self):
        record = self.evaluate(case(blockers=["Cypress real-suite coverage was not observed"]), testdrive=real_report("E08"))
        self.assertEqual((record["outcome"], record["agent"], record["actual"]["verdict"]), ("pass", "codex", "INCOMPLETE"))
        self.assertIsNone(record["next_step"])

    def test_real_e15_matches_patterns(self):
        record = self.evaluate(case("E15-spring-boot-cucumber", "FAILED",
                                    ["compatibility: suspected regression", "feature *: inconclusive"]),
                               name="E15-spring-boot-cucumber-codex", testdrive=real_report("E15"))
        self.assertEqual(record["outcome"], "pass")

    def test_new_blocker_with_the_expected_verdict(self):
        testdrive = real_report("E08")
        testdrive["summary"]["blocking_checks"].append("CI runtime: inconclusive")
        record = self.evaluate(case(blockers=["Cypress real-suite coverage was not observed"]), testdrive=testdrive)
        self.assertEqual(record["outcome"], "unexpected_blockers")
        self.assertIn("CI runtime: inconclusive", record["problems"][0])
        self.assertIn("(2) intentional ddtest change: update cases.yaml E08-kitchensink expected (blockers Cypress "
                      "real-suite coverage was not observed; CI runtime: inconclusive, reason", record["next_step"])
        self.assertIn("(1) ddtest bug: investigate ddtest: `CI runtime: inconclusive` first appears in "
                      "ddtest-results.jsonl row 3 (validation_failed validation_incomplete)", record["next_step"])
        self.assertIn("(3) agent-specific: add cases.yaml E08-kitchensink expected.agents.codex", record["next_step"])
        self.assertTrue(catalog_check.is_failing(record))

    def test_none_of_the_expected_blockers(self):
        record = self.evaluate(case(blockers=["Cypress real-suite coverage was not observed"]),
                               testdrive=report("INCOMPLETE", ["configuration npm test: incomplete"]))
        self.assertEqual(record["outcome"], "unexpected_blockers")
        self.assertIn("configuration: incomplete", record["problems"][0])

    def test_a_blocker_no_longer_reported_is_a_note(self):
        record = self.evaluate(case(blockers=["Cypress real-suite coverage was not observed", "CI runtime: inconclusive"]),
                               testdrive=real_report("E08"))
        self.assertEqual(record["outcome"], "pass")
        self.assertIn("no longer reported", record["notes"][0])

    def test_worse_verdict_is_a_regression(self):
        record = self.evaluate(case(verdict="COMPLETE LOCALLY"), testdrive=real_report("E08"))
        self.assertEqual((record["outcome"], record["direction"]), ("verdict_mismatch", "worse"))
        self.assertIn("verdict regression (expected COMPLETE LOCALLY: the known reason); compare ddtest-results.jsonl "
                      "row 3 ", record["next_step"])
        self.assertIn("(2) intentional ddtest change: update cases.yaml E08-kitchensink expected (verdict INCOMPLETE, "
                      "blockers Cypress real-suite coverage was not observed, reason, run <this output folder>, "
                      "ddtest v1.0.0-test)", record["next_step"])
        self.assertEqual(catalog_check.exit_code([record]), catalog_check.EXIT_FAILED)
        block = catalog_check.error_block(record)
        for text in ("ERROR E08-kitchensink (codex): verdict_mismatch (worse)", "expected: COMPLETE LOCALLY",
                     "actual: INCOMPLETE, run completed", "ddtest final (row 3): validation_failed validation_incomplete:",
                     "blocking: Cypress real-suite coverage was not observed", "report: " + str(self.root),
                     "next: verdict regression"):
            self.assertIn(text, block)

    def test_real_e04_regression_names_the_row_that_introduced_the_blocker(self):
        rows = [json.loads(line) for line in (FIXTURES / "E04-ddtest-results.jsonl").read_text().splitlines()]
        record = self.evaluate(case("E04-battleship", "COMPLETE LOCALLY"), name="E04-battleship-claude",
                               testdrive=real_report("E04"), rows=rows, run={"ddtest_version": "dev"})
        self.assertEqual((record["outcome"], record["direction"]), ("verdict_mismatch", "worse"))
        scope = "unresolved CI scope: .gitlab-ci.yml / acceptance test / step 4: Cannot statically resolve command"
        # ddtest's own ci_edit_required advice (row 6) first names the cat step the agent then added.
        self.assertEqual(record["introduced"][scope], {"row": 6, "result": "needs_input ci_edit_required"})
        step = record["next_step"]
        for text in ("(1) ddtest bug: investigate ddtest: ",
                     f"`{scope}` first appears in ddtest-results.jsonl row 6 (needs_input ci_edit_required)", "keep expected so the regression stays visible",
                     "(2) intentional ddtest change: update cases.yaml E04-battleship expected (verdict INCOMPLETE",
                     "(3) agent-specific: add cases.yaml E04-battleship expected.agents.claude (verdict INCOMPLETE",
                     "ddtest <ddtest commit> (run.json says dev; pass --ddtest-label)"):
            self.assertIn(text, step)
        labelled = catalog_check.evaluate(case("E04-battleship", "COMPLETE LOCALLY"), self.root / "E04-battleship-claude",
                                          ddtest_label="b7a9d2ba")
        self.assertIn("ddtest b7a9d2ba)", labelled["next_step"])
        self.assertNotIn("dev", labelled["next_step"])
        self.assertIn("ddtest b7a9d2ba, blockers", catalog_check.error_block(labelled))

    def test_ddtest_label_from_run_json_wins(self):
        record = self.evaluate(case(verdict="FAILED"), testdrive=real_report("E08"),
                               run={"ddtest_version": "dev", "ddtest_label": "b7a9d2ba (main)"})
        self.assertEqual(record["actual"]["ddtest_label"], "b7a9d2ba (main)")
        self.assertIn("ddtest b7a9d2ba (main)", record["next_step"])

    def test_trial_ending_on_an_unscriptable_question_is_recorded(self):
        rows = [json.loads(line) for line in (FIXTURES / "E14-ddtest-results.jsonl").read_text().splitlines()]
        rows[-1]["result"]["code"] = "scope_decision_required"
        record = self.evaluate(case("E14-tradukisto", blockers=["runtime fidelity: substitute Java"]),
                               name="E14-tradukisto-claude", testdrive=real_report("E14"), rows=rows)
        self.assertEqual(record["outcome"], "pass")
        self.assertEqual(record["question"]["code"], "scope_decision_required")
        self.assertIn("question not scriptable: scope_decision_required (ddtest-results.jsonl row 4)", record["notes"])
        stream = io.StringIO()
        catalog_check.report([record], stream)
        self.assertIn("NOTE E14-tradukisto (claude): question not scriptable: scope_decision_required", stream.getvalue())
        # A scriptable question, or a result that is no question, is not flagged.
        for result in ({"status": "needs_input", "code": "ci_runtime_required", "next_action": "ask_user"},
                       {"status": "needs_input", "code": "setup_dependencies_missing", "next_action": "run_command"},
                       {"status": "validation_failed", "code": "validation_incomplete", "next_action": "report_result"}):
            self.assertIsNone(catalog_check.user_answers.unscriptable(result))
        self.assertEqual(catalog_check.user_answers.unscriptable(
            {"status": "needs_input", "code": "scope_decision_required", "next_action": "supply_input",
             "required_input": ["User decision for the CI steps in location: approve ..."]}), "scope_decision_required")

    def test_better_verdict_is_an_improvement(self):
        record = self.evaluate(case(verdict="FAILED"), testdrive=real_report("E08"))
        self.assertEqual((record["outcome"], record["direction"]), ("verdict_mismatch", "better"))
        self.assertIn("update expected: set cases.yaml E08-kitchensink expected.verdict to INCOMPLETE, blockers to "
                      "Cypress real-suite coverage was not observed", record["next_step"])
        self.assertIn("ddtest v1.0.0-test", record["next_step"])
        self.assertFalse(catalog_check.is_failing(record))
        self.assertEqual(catalog_check.exit_code([record]), catalog_check.EXIT_IMPROVED)
        self.assertTrue(catalog_check.error_block(record).startswith("IMPROVEMENT E08-kitchensink (codex)"))

    def test_setup_failure_is_a_harness_failure_with_its_step(self):
        run = {"status": "failed", "failure_kind": "evaluator_environment", "error": "Command failed: npm ci",
               "commands": [{"argv": ["git", "clone", "--", "u", "r"], "exit_code": 0},
                            {"argv": ["npm", "ci", "--no-audit"], "exit_code": 1, "output_excerpt": "npm ERR! ERESOLVE"}]}
        record = self.evaluate(case(), run=run, rows=[], final=False)
        self.assertEqual((record["outcome"], record["harness_category"]), ("harness_failure", "setup"))
        self.assertIn("npm ci --no-audit failed", record["problems"][0])
        self.assertIn("npm ERR! ERESOLVE", record["excerpt"])
        self.assertIn("E08-kitchensink's setup failed on the image", record["next_step"])
        self.assertIn("rerun `run_catalog.py E08", record["next_step"])

    def test_harness_categories(self):
        for name, run, category in (
                ("clone", {"status": "failed", "failure_kind": "evaluator_environment",
                           "commands": [{"argv": ["git", "clone", "--", "u", "r"], "exit_code": 128,
                                         "output_excerpt": "Could not resolve host"}]}, "clone"),
                ("readiness", {"status": "failed", "failure_kind": "evaluator_environment", "failure_stage": "runtime_readiness",
                               "commands": [{"argv": ["bundle", "check"], "exit_code": 1}]}, "readiness"),
                ("timeout", {"status": "failed", "failure_kind": "infrastructure",
                             "commands": [{"argv": ["codex", "exec"], "timeout_seconds": 60}]}, "timeout"),
                ("interrupted", {"status": "interrupted", "error": "Interrupted by signal 2"}, "interrupted"),
                ("cleanup", {"status": "failed", "isolation": {"cleanup": "failed"}}, "cleanup"),
                ("auth", {"status": "failed", "failure_kind": "authentication", "failure_stage": "model_authentication"},
                 "authentication"),
                ("transport", {"status": "agent_failed", "failure_kind": "infrastructure",
                               "commands": [{"argv": ["codex", "exec"], "exit_code": 1}]}, "infrastructure")):
            with self.subTest(name):
                record = self.evaluate(case(), name=name + "-codex", run=run, rows=[], final=False)
                self.assertEqual((record["outcome"], record.get("harness_category")), ("harness_failure", category))
        missing = catalog_check.evaluate(case(), self.root / "absent-codex")
        self.assertEqual((missing["outcome"], missing["harness_category"]), ("harness_failure", "evidence"))

    def test_malformed_reports_are_harness_failures_not_crashes(self):
        for name, testdrive, text in (
                ("features", {"features": 42}, "features is int, not a list"),
                ("top", [1, 2], "top level is list, not an object"),
                ("summary", {"summary": "COMPLETE LOCALLY"}, "summary is str, not an object"),
                ("checks", {"summary": {"status": "INCOMPLETE", "blocking_checks": 5}},
                 "summary.blocking_checks is int, not a list"),
                ("status", {"summary": {"status": 3}}, "summary.status is int, not a string")):
            with self.subTest(name):
                record = self.evaluate(case(), name=name + "-codex", testdrive=testdrive, final=False,
                                       rows=[{"turn": 1, "result": {"status": "complete", "code": "done"}}])
                self.assertEqual((record["outcome"], record["harness_category"]), ("harness_failure", "report"))
                self.assertEqual(record["problems"], ["malformed retained/.testoptimization/testdrive.json: " + text])
                self.assertIn("is missing or not a ddtest report", record["next_step"])
                self.assertTrue(catalog_check.is_failing(record))
                catalog_check.error_block(record)

    def claiming_evidence(self, name, text=None):
        """Evidence whose last ddtest result and final answer claim COMPLETE LOCALLY, with text as the
        retained testdrive.json (None: no report at all)."""
        final = "Onboarding validation: COMPLETE LOCALLY. Local validation: PASSED."
        rows = [{"turn": 1, "result": {"status": "complete", "code": "validation_complete", "reason": "ok",
                                       "final_response": final}}]
        folder = write_evidence(self.root / name, rows=rows)
        (folder / "final-response.md").write_text(final + "\n")
        if text is not None:
            (folder / catalog_check.REPORT).parent.mkdir(parents=True)
            (folder / catalog_check.REPORT).write_text(text)
        return folder

    def test_an_existing_report_that_is_not_a_report_never_passes_from_final_response(self):
        valid = report("COMPLETE LOCALLY")
        self.assertEqual(catalog_check.evaluate(case(verdict="COMPLETE LOCALLY"),
                                                self.claiming_evidence("valid-codex", json.dumps(valid)))["outcome"], "pass")
        for name, text, problem in (
                ("truncated", json.dumps(valid)[:40], "not valid JSON"),
                ("empty-file", "", "not valid JSON"),
                ("null", "null", "top level is null, not an object"),
                ("empty", "{}", "summary is missing"),
                ("no-status", json.dumps({"summary": {"blocking_checks": []}}), "summary.status is missing"),
                ("blank-status", json.dumps({"summary": {"status": " ", "blocking_checks": []}}), "summary.status is empty"),
                ("no-checks", json.dumps({"summary": {"status": "COMPLETE LOCALLY"}}), "summary.blocking_checks is missing"),
                ("null-checks", json.dumps({"summary": {"status": "COMPLETE LOCALLY", "blocking_checks": None}}),
                 "summary.blocking_checks is missing"),
                ("check-type", json.dumps({"summary": {"status": "INCOMPLETE", "blocking_checks": [{"a": 1}]}}),
                 "summary.blocking_checks[0] is dict, not a string"),
                ("features-type", json.dumps({"summary": None, "features": "x"}), "features is str, not a list")):
            with self.subTest(name):
                record = catalog_check.evaluate(case(verdict="COMPLETE LOCALLY"), self.claiming_evidence(name + "-codex", text))
                self.assertEqual((record["outcome"], record["harness_category"]), ("harness_failure", "report"))
                self.assertIn("malformed retained/.testoptimization/testdrive.json: " + problem, record["problems"][0])
                self.assertIsNone(record["actual"]["verdict"])

    def test_a_symlinked_report_is_not_read(self):
        outside = self.root / "outside.json"
        outside.write_text(json.dumps(report("COMPLETE LOCALLY")))
        folder = self.claiming_evidence("linked-codex", None)
        (folder / catalog_check.REPORT).parent.mkdir(parents=True)
        (folder / catalog_check.REPORT).symlink_to(outside)
        record = catalog_check.evaluate(case(verdict="COMPLETE LOCALLY"), folder)
        self.assertEqual((record["outcome"], record["harness_category"]), ("harness_failure", "report"))
        self.assertIn("is a symlink", record["problems"][0])

    def test_an_absent_report_never_passes_from_final_response(self):
        record = catalog_check.evaluate(case(verdict="COMPLETE LOCALLY"), self.claiming_evidence("absent-codex"))
        self.assertEqual((record["outcome"], record["harness_category"]), ("harness_failure", "report"))
        self.assertIn("no retained/.testoptimization/testdrive.json, but ddtest's last result (ddtest-results.jsonl row 1) "
                      "claims Onboarding validation: COMPLETE LOCALLY", record["problems"][0])
        self.assertIsNone(record["actual"]["verdict"])
        self.assertTrue(catalog_check.is_failing(record))

    def test_recorded_retention_outcome(self):
        for name, recorded, outcome in (
                ("ok", {"status": "ok", "reports": 1}, ("pass", None)),
                ("failed", {"status": "failed", "error": "[Errno 28] No space left on device"}, ("harness_failure", "retention")),
                ("started", {"status": "started"}, ("harness_failure", "retention")),
                ("garbage", None, ("harness_failure", "retention"))):
            with self.subTest(name):
                folder = write_evidence(self.root / f"{name}-codex", testdrive=real_report("E08"))
                (folder / catalog_check.RETENTION).write_text(json.dumps(recorded) if recorded else "{trunc")
                record = catalog_check.evaluate(case(blockers=["Cypress real-suite coverage was not observed"]), folder)
                self.assertEqual((record["outcome"], record.get("harness_category")), outcome)
                if name == "failed":
                    self.assertEqual(record["problems"], ["retention failed: [Errno 28] No space left on device"])
                if name == "started":
                    self.assertIn("retention started: it did not finish", record["problems"][0])
        # Evidence retained before RETENTION.json existed has none: not recorded, not a failure.
        folder = write_evidence(self.root / "older-codex", testdrive=real_report("E08"))
        self.assertFalse((folder / catalog_check.RETENTION).exists())
        self.assertEqual(catalog_check.evaluate(case(blockers=["Cypress real-suite coverage was not observed"]),
                                                folder)["outcome"], "pass")

    def test_unreadable_evidence_is_an_evaluation_failure_with_its_traceback(self):
        record = self.evaluate(case(), testdrive=real_report("E08"), run={"agent_reporting": "pass"})
        self.assertEqual((record["outcome"], record["harness_category"]), ("harness_failure", "evaluation"))
        self.assertIn("evaluating the evidence failed: AttributeError: ", record["problems"][0])
        self.assertIn("in agent_problem", record["excerpt"])
        self.assertIn("log", record["evidence"])
        self.assertIn("could not read", record["next_step"])

    def test_agent_failures(self):
        for name, evidence, text in (
                ("silent", {"rows": []}, "without any ddtest result or report"),
                ("no-answer", {"testdrive": real_report("E08"), "final": False}, "no final-response.md"),
                ("reporting", {"testdrive": real_report("E08"),
                               "run": {"agent_reporting": {"status": "review", "reason": "Missing canonical handoff needs "
                                                           "independent review.", "missing": ["summary.final_response"]}}},
                 "agent_reporting review"),
                ("contradiction", {"testdrive": real_report("E08"),
                                   "run": {"status": "agent_failed", "failure_kind": "agent_reporting",
                                           "agent_reporting": {"status": "fail", "reason": "Direct claims contradict JSON."}}},
                 "agent_reporting fail")):
            with self.subTest(name):
                record = self.evaluate(case(blockers=["Cypress real-suite coverage was not observed"]),
                                       name=name + "-claude", **evidence)
                self.assertEqual(record["outcome"], "agent_failure")
                self.assertIn(text, record["problems"][0])
                self.assertTrue(record["next_step"].startswith("agent (claude): inspect final-response.md vs ddtest final_response"))
                self.assertIn("ERROR E08-kitchensink (claude): agent_failure", catalog_check.error_block(record))

    def test_stopped_without_a_report(self):
        record = self.evaluate(case(), rows=[{"turn": 1, "result": {"status": "needs_input", "code": "scope_unresolved",
                                                                    "reason": "decide"}}])
        self.assertEqual((record["outcome"], record["direction"], record["actual"]["verdict"]),
                         ("verdict_mismatch", "worse", "STOPPED"))
        self.assertEqual(record["actual"]["final_reason"], "needs_input scope_unresolved: decide")

    def test_agent_override_applies_only_to_its_agent(self):
        override = case(verdict="COMPLETE LOCALLY", agents={"claude": {"verdict": "INCOMPLETE", "reason": "asks first",
                                                                     "blockers": ["Cypress real-suite coverage was not observed"]}})
        claude = self.evaluate(override, name="E08-kitchensink-claude", testdrive=real_report("E08"))
        codex = self.evaluate(override, name="E08-kitchensink-codex", testdrive=real_report("E08"))
        self.assertEqual((claude["outcome"], claude["expected"]["source"]), ("pass", "expected.agents.claude"))
        self.assertEqual((codex["outcome"], codex["direction"]), ("verdict_mismatch", "worse"))
        self.assertEqual(catalog_check.expected_for(override, "codex").get("blockers"), None)

    def test_agents_disagree_and_table(self):
        codex = self.evaluate(case(blockers=["Cypress real-suite coverage was not observed"]), testdrive=real_report("E08"))
        claude = self.evaluate(case(blockers=["Cypress real-suite coverage was not observed"]),
                               name="E08-kitchensink-claude", testdrive=report("COMPLETE LOCALLY"))
        stream = io.StringIO()
        code = catalog_check.report([codex, claude], stream)
        text = stream.getvalue()
        self.assertEqual(code, catalog_check.EXIT_IMPROVED)
        self.assertRegex(text, r"case\s+agent\s+provider\s+framework\s+expected\s+actual\s+outcome")
        self.assertRegex(text, r"E08-kitchensink\s+codex\s+gitlab\s+cypress\s+INCOMPLETE\s+INCOMPLETE\s+pass")
        self.assertRegex(text, r"E08-kitchensink\s+claude\s+gitlab\s+cypress\s+INCOMPLETE\s+COMPLETE LOCALLY\s+"
                               r"verdict_mismatch \(better: update expected\)")
        self.assertIn("NOTE E08-kitchensink: agents disagree: codex INCOMPLETE, claude COMPLETE LOCALLY", text)
        self.assertIn("Only improvements", text)


@unittest.skipIf(yaml is None, "PyYAML is not installed")
class EvaluateOnlyTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.catalog = run_catalog.load_catalog()

    def main(self, *argv, catalog=None):
        if catalog is not None:
            path = self.root / "cases.yaml"
            path.write_text(yaml.safe_dump(catalog, sort_keys=False))
            argv = (*argv, "--catalog", str(path))
        output = io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(io.StringIO()):
            code = run_catalog.main(["--evaluate-only", "--output", str(self.root / "out"), *argv])
        return code, output.getvalue(), json.loads((self.root / "out" / "results.json").read_text())

    def test_passing_evidence_exits_zero_and_writes_results(self):
        write_evidence(self.root / "out" / "E02-pipeline-schema-codex", testdrive=real_report("E02"))
        write_evidence(self.root / "out" / "E08-kitchensink-codex", testdrive=real_report("E08"))
        code, output, results = self.main("--all")
        self.assertEqual(code, 0, output)
        self.assertIn("[-] E01-nion: not evaluated, no evidence (found: none)", output)
        self.assertEqual([(case["id"], case["outcome"]) for case in results["cases"]],
                         [("E02-pipeline-schema", "pass"), ("E08-kitchensink", "pass")])
        self.assertEqual((results["exit_code"], results["outcomes"]), (0, {"pass": 2}))
        entry = results["cases"][1]
        self.assertEqual(entry["expected"]["blockers"], ["Cypress real-suite coverage was not observed"])
        self.assertEqual(entry["actual"]["blockers"], ["Cypress real-suite coverage was not observed", "configuration: incomplete"])
        self.assertEqual(entry["evidence"]["testdrive"],
                         str(self.root / "out" / "E08-kitchensink-codex" / "retained" / ".testoptimization" / "testdrive.json"))
        for key in ("provider", "framework", "agent", "next_step", "problems"):
            self.assertIn(key, entry)

    def test_quoted_non_utf8_filename_does_not_abort_evaluate_only_batch(self):
        affected = write_evidence(self.root / 'out' / 'E02-pipeline-schema-codex', testdrive=real_report('E02'))
        write_evidence(self.root / 'out' / 'E08-kitchensink-codex', testdrive=real_report('E08'))
        patch = ('diff --git "a/bad\\377.py" "b/bad\\377.py"\n'
                 '--- "a/bad\\377.py"\n+++ "b/bad\\377.py"\n@@ -1 +1 @@\n-old\n+new\n')
        (affected / 'changes.patch').write_text(patch)
        (affected / 'status.txt').write_text(' M "bad\\377.py"\n')
        code, output, results = self.main('E02', 'E08')
        self.assertEqual(code, 0, output)
        self.assertEqual([(record['id'], record['outcome']) for record in results['cases']],
                         [('E02-pipeline-schema', 'pass'), ('E08-kitchensink', 'pass')])
        trajectory = results['cases'][0]['trajectory']
        self.assertEqual(trajectory['status'], 'review')
        self.assertIn('Trajectory evaluation failed:', trajectory['reason'])
        self.assertEqual(json.loads((affected / 'trajectory.json').read_text()), trajectory)
        self.assertEqual(results['cases'][1]['trajectory']['status'], 'pass')
        self.assertEqual((affected / 'changes.patch').read_text(), patch)

    def test_forced_mismatch_prints_an_error_block_and_exits_one(self):
        write_evidence(self.root / "out" / "E08-kitchensink-codex", testdrive=real_report("E08"))
        catalog = copy.deepcopy(self.catalog)
        expected = next(case for case in catalog["cases"] if case["id"] == "E08-kitchensink")["expected"]
        expected["verdict"] = "COMPLETE LOCALLY"
        del expected["blockers"]
        code, output, results = self.main("E08", catalog=catalog)
        self.assertEqual(code, 1)
        self.assertIn("ERROR E08-kitchensink (codex): verdict_mismatch (worse)", output)
        self.assertEqual((results["exit_code"], results["cases"][0]["direction"]), (1, "worse"))

    def test_only_improvements_exit_three(self):
        write_evidence(self.root / "out" / "E08-kitchensink-codex", testdrive=report("COMPLETE LOCALLY"))
        code, output, results = self.main("E08")
        self.assertEqual(code, catalog_check.EXIT_IMPROVED)
        self.assertIn("IMPROVEMENT E08-kitchensink (codex)", output)
        self.assertEqual(results["outcomes"], {"verdict_mismatch (better: update expected)": 1})

    def test_requested_agent_without_evidence_is_reported_not_evaluated(self):
        write_evidence(self.root / "out" / "E02-pipeline-schema-codex", testdrive=real_report("E02"))
        code, output, results = self.main("E02", "--agent", "claude")
        self.assertEqual((code, results["cases"], results["exit_code"]), (1, [], 1))
        self.assertIn("[claude] E02-pipeline-schema: not evaluated, no claude evidence (found: codex)", output)
        self.assertNotIn("harness_failure", output)
        self.assertEqual(results["not_evaluated"], [{"id": "E02-pipeline-schema", "agent": "claude",
                                                     "reason": "no claude evidence (found: codex)"}])
        code, output, _ = self.main("E08")
        self.assertEqual(code, 1)
        self.assertIn("[-] E08-kitchensink: not evaluated, no evidence (found: none)", output)

    def test_agents_are_inferred_from_evidence_folders(self):
        write_evidence(self.root / "out" / "E08-kitchensink-claude", testdrive=real_report("E08"))
        code, output, results = self.main("E08")
        self.assertEqual(code, 0, output)
        self.assertEqual([(case["id"], case["agent"], case["outcome"]) for case in results["cases"]],
                         [("E08-kitchensink", "claude", "pass")])
        write_evidence(self.root / "out" / "E08-kitchensink-codex", testdrive=real_report("E08"))
        code, output, results = self.main("E08")
        self.assertEqual([case["agent"] for case in results["cases"]], ["codex", "claude"])

    def test_named_cases_merge_into_the_batch_results(self):
        write_evidence(self.root / "out" / "E02-pipeline-schema-claude", testdrive=real_report("E02"))
        write_evidence(self.root / "out" / "E08-kitchensink-claude", testdrive=real_report("E08"))
        code, _, batch = self.main("--all", "--ddtest-label", "b7a9d2ba")
        self.assertEqual((code, len(batch["cases"]), batch["ddtest_label"]), (0, 2, "b7a9d2ba"))
        catalog = copy.deepcopy(self.catalog)
        expected = next(case for case in catalog["cases"] if case["id"] == "E08-kitchensink")["expected"]
        expected["verdict"] = "COMPLETE LOCALLY"
        del expected["blockers"]
        code, output, results = self.main("E08", catalog=catalog)
        self.assertEqual(code, 1)
        self.assertIn("(merged: 1 earlier entries kept)", output)
        self.assertEqual([(case["id"], case["agent"], case["outcome"]) for case in results["cases"]],
                         [("E02-pipeline-schema", "claude", "pass"), ("E08-kitchensink", "claude", "verdict_mismatch")])
        self.assertEqual((results["outcomes"], results["exit_code"], results["merged_entries_kept"]),
                         ({"pass": 1, "verdict_mismatch (worse)": 1}, 1, 1))
        # Fixing the expectation and re-evaluating the case recomputes the counts and exit code.
        code, _, results = self.main("E08")
        self.assertEqual((code, results["outcomes"], results["exit_code"]), (0, {"pass": 2}, 0))
        # A full batch again replaces every entry of the agents it covers.
        (self.root / "out" / "E02-pipeline-schema-claude" / "run.json").unlink()
        code, _, results = self.main("--all", "--agent", "claude")
        self.assertEqual(len(results["cases"]), 2)
        self.assertEqual(results["cases"][0]["outcome"], "harness_failure")

    def test_results_of_another_output_are_not_merged(self):
        target = self.root / "out" / "results.json"
        target.parent.mkdir()
        target.write_text(json.dumps({"output": "/elsewhere", "cases": []}))
        with self.assertRaises(SystemExit), contextlib.redirect_stderr(io.StringIO()):
            run_catalog.main(["--evaluate-only", "E02", "--output", str(self.root / "out")])
        self.assertEqual(json.loads(target.read_text())["output"], "/elsewhere")

    def test_unscriptable_questions_are_listed_in_results(self):
        rows = [json.loads(line) for line in (FIXTURES / "E14-ddtest-results.jsonl").read_text().splitlines()]
        rows[-1]["result"]["code"] = "scope_decision_required"
        write_evidence(self.root / "out" / "E14-tradukisto-claude", testdrive=real_report("E14"), rows=rows)
        # This historical fixture stops before accepting the substitute runtime.
        catalog = copy.deepcopy(self.catalog)
        expected = next(case for case in catalog["cases"] if case["id"] == "E14-tradukisto")["expected"]
        expected.update(verdict="INCOMPLETE", blockers=["runtime fidelity: substitute Java"],
                        reason="Historical fixture stops for a runtime decision", run="catalog fixture", ddtest="fixture")
        code, output, results = self.main("E14", catalog=catalog)
        self.assertEqual(code, 0, output)
        self.assertEqual(results["questions_not_scriptable"],
                         ["E14-tradukisto (claude): question not scriptable: scope_decision_required"])
        self.assertEqual(results["cases"][0]["question"]["code"], "scope_decision_required")

    def test_results_path_override(self):
        write_evidence(self.root / "out" / "E02-pipeline-schema-codex", testdrive=real_report("E02"))
        target = self.root / "elsewhere" / "r.json"
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertEqual(run_catalog.main(["--evaluate-only", "E02", "--output", str(self.root / "out"),
                                               "--results", str(target)]), 0)
        self.assertTrue(target.is_file())
        self.assertFalse((self.root / "out" / "results.json").exists())


@unittest.skipIf(yaml is None, "PyYAML is not installed")
class ExpectedSchemaTests(unittest.TestCase):
    def setUp(self):
        self.catalog = run_catalog.load_catalog()

    def test_every_case_has_a_checkable_expectation(self):
        for entry in self.catalog["cases"]:
            expected = entry["expected"]
            self.assertIn(expected["verdict"], catalog_check.EXPECTED_VERDICTS, entry["id"])
            if expected["verdict"] != "COMPLETE LOCALLY":
                self.assertTrue(expected.get("reason"), entry["id"])
            if "blockers" in expected:
                self.assertIn(expected["verdict"], catalog_check.BLOCKER_VERDICTS, entry["id"])
            for agent, override in (expected.get("agents") or {}).items():
                self.assertIn(agent, catalog_check.AGENTS)
                resolved = catalog_check.expected_for(entry, agent)
                if "blockers" in resolved:
                    self.assertIn(resolved["verdict"], catalog_check.BLOCKER_VERDICTS)
        self.assertEqual({entry["id"] for entry in self.catalog["cases"] if entry["expected"].get("blockers")},
                         {"E08-kitchensink", "E12-kalamar-export",
                          "E15-spring-boot-cucumber", "E16-spring-karate-demo", "E17-feel-scala"})

    def problems(self, **changes):
        broken = copy.deepcopy(self.catalog)
        expected = broken["cases"][0]["expected"]
        for key, value in changes.items():
            if value is None:
                expected.pop(key, None)
            else:
                expected[key] = value
        return "\n".join(run_catalog.validate(broken))

    def test_schema_rejects_bad_expectations(self):
        self.assertIn("blockers only with an INCOMPLETE or FAILED verdict", self.problems(blockers=["x"]))
        self.assertIn("verdict must be one of", self.problems(verdict="STOPPED"))
        self.assertIn("expected.reason is missing", self.problems(reason=None))
        self.assertIn("non-empty list of distinct identifiers", self.problems(verdict="INCOMPLETE", blockers=["a", "a"]))
        self.assertIn("unknown fields notes", self.problems(notes="x"))
        self.assertIn("unknown agent", self.problems(agents={"gemini": {"reason": "x"}}))
        self.assertIn("a verdict override needs its own reason", self.problems(agents={"claude": {"verdict": "INCOMPLETE"}}))
        self.assertIn("expected.agents.claude: blockers only with an INCOMPLETE or FAILED verdict",
                      self.problems(agents={"claude": {"blockers": ["x"]}}))
        self.assertEqual(self.problems(agents={"claude": {"verdict": "INCOMPLETE", "reason": "asks", "blockers": ["x"]}}), "")


if __name__ == "__main__":
    unittest.main()
