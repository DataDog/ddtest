"""Scripted user answers, bounded excerpts and reference-preserving redaction."""

import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import run_eval
import test_run_eval as fixtures
import user_answers


def runtime_question(jobs=("Jenkinsfile / Run Tests",), runtime="Ruby", flag="ruby"):
    return {"schema_version": 2, "status": "needs_input", "phase": "discovery", "code": "ci_runtime_required",
            "reason": f"ddtest cannot read which {runtime} version these CI jobs run: ...",
            "location": "; ".join(jobs), "required_input": ["ci_runtime:" + job for job in jobs],
            "next_action": "ask_user",
            "agent_instruction": f"Stop and ask the user which {runtime} version each listed CI job runs. After their answer, "
                                 f"rerun the same command adding --ci-runtime {flag}=<version> when every listed job runs that version, "
                                 "or one --ci-runtime 'job:" + flag + "=<version>' per job. " + "Padding. " * 1000}


CONFIRMATION_QUESTION = {
    "schema_version": 2, "status": "needs_input", "code": "ci_setup_confirmation_required", "next_action": "ask_user",
    "required_input": ["ci_confirm:jenkins-plugin", "ci_confirm:jenkins-plugin-mode=agentless:<site>|agent",
                       "ci_confirm:jenkins-agent-runtime-matches"]}


class ParseTests(unittest.TestCase):
    def test_answer_forms(self):
        self.assertEqual(user_answers.parse_answer("ci_runtime=node=22")["version"], "22")
        job = user_answers.parse_answer("ci_runtime=.gitlab-ci.yml / test:node=20.0.0")
        self.assertEqual((job["job"], job["runtime"], job["version"]), (".gitlab-ci.yml / test", "node", "20.0.0"))
        mode = user_answers.parse_answer("ci_confirm=jenkins-plugin-mode=agentless:datadoghq.com")
        self.assertEqual((mode["fact"], mode["value"]), ("jenkins-plugin-mode", "agentless:datadoghq.com"))
        self.assertIsNone(user_answers.parse_answer("ci_confirm=jenkins-plugin")["value"])
        self.assertEqual(user_answers.parse_answer("ci_provider=jenkins")["provider"], "jenkins")
        for invalid in ("node=22", "ci_runtime=22", "ci_runtime=node=", "ci_confirm=", "ci_confirm=fact=",
                        "ci_provider=Jenkins CI", "unknown=value"):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                user_answers.parse_answer(invalid)

    def test_answers_file_ignores_comments_and_blank_lines(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "answers.txt"
            path.write_text("# Jenkins facts\n\nci_confirm=jenkins-plugin\n  ci_runtime=ruby=3.4  \n")
            answers = user_answers.load_answers(["ci_provider=jenkins"], path)
        self.assertEqual([answer["text"] for answer in answers],
                         ["ci_provider=jenkins", "ci_confirm=jenkins-plugin", "ci_runtime=ruby=3.4"])


class QuestionTests(unittest.TestCase):
    def test_agent_result_is_read_from_whole_tool_output(self):
        question = runtime_question()
        for text in ("Exit code 1\n" + json.dumps(question), "log line\n" + json.dumps(question, indent=2) + "\ntrailer"):
            self.assertEqual(user_answers.agent_result(text), question)
        # Reports and other JSON are not agent results.
        self.assertIsNone(user_answers.agent_result(json.dumps({"schema_version": 2, "summary": {}})))
        self.assertIsNone(user_answers.agent_result("no json {here"))
        self.assertEqual(user_answers.tool_output_text([{"type": "text", "text": "a"}, {"type": "text", "text": "b"}]), "a\nb")

    def test_only_ask_user_results_are_questions(self):
        self.assertIsNone(user_answers.question({**runtime_question(), "next_action": "run_command"}))
        asked = user_answers.question(runtime_question())
        self.assertEqual((asked["runtime"], asked["runtime_name"]), ("ruby", "Ruby"))

    def test_runtime_reply_states_only_the_asked_jobs_in_plain_words(self):
        asked = user_answers.question(runtime_question(("A / test", "B / test")))
        answers = user_answers.load_answers(["ci_runtime=ruby=3.4", "ci_runtime=B / test:ruby=3.3", "ci_confirm=jenkins-plugin"])
        composed = user_answers.reply(asked, answers)
        self.assertEqual(composed["reply"], "A / test runs Ruby 3.4.\nB / test runs Ruby 3.3.")
        self.assertEqual(composed["missing"], [])
        self.assertEqual([answer["answer"] for answer in composed["answers"]], ["ci_runtime=ruby=3.4", "ci_runtime=B / test:ruby=3.3"])
        self.assertNotIn("--", composed["reply"])

    def test_answer_for_another_runtime_does_not_cover_the_question(self):
        composed = user_answers.reply(user_answers.question(runtime_question()), user_answers.load_answers(["ci_runtime=node=22"]))
        self.assertIsNone(composed["reply"])
        self.assertEqual(composed["missing"], ["ci_runtime:Jenkinsfile / Run Tests"])

    def test_confirmations_need_every_fact_and_a_value_where_asked(self):
        asked = user_answers.question(CONFIRMATION_QUESTION)
        complete = user_answers.load_answers(["ci_confirm=jenkins-plugin", "ci_confirm=jenkins-plugin-mode=agentless:datadoghq.com",
                                              "ci_confirm=jenkins-agent-runtime-matches=no"])
        self.assertEqual(user_answers.reply(asked, complete)["reply"],
                         "jenkins-plugin: yes.\njenkins-plugin-mode: agentless:datadoghq.com.\njenkins-agent-runtime-matches: no.")
        partial = user_answers.reply(asked, complete[:2])
        self.assertIsNone(partial["reply"])
        self.assertEqual(partial["missing"], ["ci_confirm:jenkins-agent-runtime-matches"])
        valueless = user_answers.reply(asked, user_answers.load_answers(
            ["ci_confirm=jenkins-plugin", "ci_confirm=jenkins-plugin-mode", "ci_confirm=jenkins-agent-runtime-matches"]))
        self.assertEqual(valueless["missing"], ["ci_confirm:jenkins-plugin-mode=agentless:<site>|agent"])

    def test_provider_and_unknown_questions(self):
        provider = {"code": "ci_provider_required", "required_input": ["github", "jenkins"]}
        self.assertEqual(user_answers.reply(provider, user_answers.load_answers(["ci_provider=jenkins"]))["reply"], "CI provider: jenkins.")
        self.assertEqual(user_answers.reply(provider, user_answers.load_answers(["ci_provider=gitlab"]))["missing"], ["ci_provider:github|jenkins"])
        unknown = user_answers.reply({"code": "something_new", "required_input": []}, user_answers.load_answers(["ci_provider=jenkins"]))
        self.assertEqual((unknown["reply"], unknown["missing"]), (None, ["something_new"]))


class EvidenceTests(unittest.TestCase):
    def test_variable_references_survive_and_literal_values_are_redacted(self):
        kept = ('if [ -n "${DD_API_KEY:-}" ]; then', 'if [ -n "$${DD_API_KEY:-}" ]; then', "export DD_API_KEY=${DD_API_KEY:-}",
                "DD_API_KEY: $DD_API_KEY", "QLTY_COVERAGE_TOKEN = credentials('qlty-coverage-token')",
                'api_key: ENV["DD_API_KEY"]', "apiKey: process.env.DD_API_KEY", "api_key: ${{ secrets.DD_API_KEY }}")
        for text in kept:
            self.assertEqual(run_eval.redact(text), text)
        for text, expected in (("${DD_API_KEY:-abcdef123456}", "${DD_API_KEY:-[REDACTED]}"),
                               ("DD_API_KEY=abc123secret", "DD_API_KEY=[REDACTED]"),
                               ("password=hunter2(", "password=[REDACTED]"),
                               ("Authorization: Bearer abcdefg", "Authorization: [REDACTED]")):
            self.assertEqual(run_eval.redact(text), expected)
        with patch.dict(os.environ, {"DD_API_KEY": "real-known-key-value"}):
            self.assertNotIn("real-known-key-value", run_eval.redact("${DD_API_KEY:-real-known-key-value}"))

    def test_excerpt_keeps_start_and_end_within_the_limit(self):
        text = "START" + "x" * 100_000 + "END"
        kept = run_eval.excerpt(text, 1000)
        self.assertTrue(kept.startswith("START"))
        self.assertTrue(kept.endswith("END"))
        self.assertLess(len(kept), 1100)
        self.assertEqual(run_eval.excerpt("short", 1000), "short")

    def test_resume_continues_the_same_conversation(self):
        codex = run_eval.agent_command("codex", "model-x")
        self.assertEqual(run_eval.resume_command("codex", codex, "thread-1"),
                         ["codex", "exec", "resume", *codex[2:-1], "thread-1", "-"])
        claude = run_eval.agent_command("claude", None)
        self.assertEqual(run_eval.resume_command("claude", claude, "session-1"), [*claude, "--resume", "session-1"])


class TrialTests(unittest.TestCase):
    def setUp(self):
        self.case = fixtures.RunnerTests()
        self.case.setUp()
        self.addCleanup(self.case.doCleanups)
        self.args = self.case.args
        self.args.agent = "claude"

    def trial(self, answers=(), extra=None):
        self.args.user_answer = list(answers)
        env = {"FAKE_ASK_USER": json.dumps(runtime_question()), **(extra or {})}
        code, manifest = self.case.trial(env)
        checkout = Path(manifest["checkout"])
        turns = [json.loads(line) for line in (checkout / "turns.jsonl").read_text().splitlines()]
        activity = [json.loads(line) for line in (self.args.output / "activity.jsonl").read_text().splitlines()]
        return code, manifest, turns, activity

    def test_scripted_answer_continues_the_conversation_as_the_user(self):
        for agent, session in (("claude", "session-1"), ("codex", "thread-1")):
            with self.subTest(agent=agent):
                self.args.agent = agent
                self.args.output = self.case.root / f"{agent}-answered"
                code, manifest, turns, activity = self.trial(["ci_runtime=ruby=3.4", "ci_confirm=jenkins-plugin"])
                self.assertEqual(code, 0)
                self.assertEqual(manifest["status"], "completed")
                self.assertEqual(len(turns), 2)
                self.assertEqual(turns[0]["stdin"], manifest["prompt"])
                # Only the asked fact, in plain words; no flags or coaching.
                self.assertEqual(turns[1]["stdin"], "Jenkinsfile / Run Tests runs Ruby 3.4.")
                self.assertIn(session, turns[1]["argv"])
                self.assertIn("--resume" if agent == "claude" else "resume", turns[1]["argv"])
                record = manifest["user_answers"]
                self.assertEqual(record["status"], "answered")
                self.assertEqual(record["scripted"], ["ci_runtime=ruby=3.4", "ci_confirm=jenkins-plugin"])
                turn, = record["turns"]
                self.assertEqual(turn["question"]["code"], "ci_runtime_required")
                self.assertEqual(turn["question"]["required_input"], ["ci_runtime:Jenkinsfile / Run Tests"])
                self.assertEqual(turn["answers"], [{"required_input": "ci_runtime:Jenkinsfile / Run Tests", "answer": "ci_runtime=ruby=3.4"}])
                self.assertIn("Which Ruby version", turn["agent_message"])
                self.assertLessEqual(turn["asked_at"], turn["answered_at"])
                kinds = [event["type"] for event in activity]
                self.assertLess(kinds.index("user_question"), kinds.index("user_reply"))
                self.assertEqual(sum(1 for event in activity if event["type"] == "harness_command"
                                     and event["argv"][0] == agent and "--version" not in event["argv"]), 2)
                self.assertEqual((self.args.output / "final-response.md").read_text(), "Done\n")

    def test_without_answers_the_trial_stops_and_records_the_question(self):
        code, manifest, turns, activity = self.trial()
        self.assertEqual(code, 0)
        self.assertEqual(len(turns), 1)
        record = manifest["user_answers"]
        self.assertEqual(record["status"], "unanswered")
        turn, = record["turns"]
        self.assertEqual(turn["missing"], ["ci_runtime:Jenkinsfile / Run Tests"])
        self.assertEqual(turn["unanswered"], "the scripted answers do not cover it")
        self.assertNotIn("reply", turn)
        unanswered, = [event for event in activity if event["type"] == "user_question_unanswered"]
        self.assertEqual(unanswered["missing"], ["ci_runtime:Jenkinsfile / Run Tests"])
        self.assertIn("Which Ruby version", (self.args.output / "final-response.md").read_text())

    def test_uncovered_question_is_not_answered_partially(self):
        code, manifest, turns, _ = self.trial(["ci_runtime=node=22"])
        self.assertEqual(len(turns), 1)
        self.assertEqual(manifest["user_answers"]["status"], "unanswered")

    def test_repeated_questions_stop_at_the_reply_limit(self):
        self.args.max_user_turns = 2
        code, manifest, turns, _ = self.trial(["ci_runtime=ruby=3.4"], {"FAKE_ASK_AGAIN": "1"})
        self.assertEqual(len(turns), 3)
        record = manifest["user_answers"]
        self.assertEqual(record["status"], "unanswered")
        self.assertEqual([turn.get("reply") for turn in record["turns"]], ["Jenkinsfile / Run Tests runs Ruby 3.4."] * 2 + [None])
        self.assertIn("limit of 2", record["turns"][-1]["unanswered"])

    def test_invalid_answers_stop_before_any_trial(self):
        self.args.user_answer = ["ruby=3.4"]
        with self.assertRaises(ValueError):
            run_eval.run(self.args)
        self.assertFalse(self.args.output.exists())

    def test_bundle_binstubs_are_private_and_on_path(self):
        bundle = self.case.root / "bundle"
        bundle.write_text("#!/bin/sh\n[ \"$1\" = binstubs ] || exit 0\nshift\nfor gem in \"$@\"; do echo stub > \"$BUNDLE_BIN/${gem%-core}\"; done\n")
        bundle.chmod(0o755)
        self.args.bundle_binstubs = ["rspec-core"]
        self.args.output = self.case.root / "binstubs"
        code, manifest = self.case.trial()
        self.assertEqual(code, 0)
        directory = Path(manifest["trial_directory"]) / "bundle-bin"
        self.assertEqual(manifest["bundle_binstubs"]["executables"], ["rspec"])
        self.assertEqual(manifest["bundle_binstubs"]["gems"], ["rspec-core"])
        observed = json.loads((Path(manifest["checkout"]) / "observed.json").read_text())
        self.assertIn(str(directory), observed["environment"]["PATH"].split(os.pathsep))
        # The folder the prompt names still holds only ddtest.
        self.assertEqual([path.name for path in (directory.parent / "bin").iterdir()], ["ddtest"])

    def test_activity_excerpt_limit_is_configurable_and_recorded(self):
        self.args.excerpt_limit = 1000
        self.args.output = self.case.root / "excerpt"
        code, manifest, _, activity = self.trial()
        self.assertEqual(manifest["activity_excerpts"]["limit_characters"], 1000)
        result, = [event for event in activity if event["type"] == "tool_result"]
        self.assertIn("characters omitted]", result["output"])
        self.assertIn("ci_runtime_required", result["output"])
        # The question was read from the whole output, not the excerpt.
        self.assertEqual(manifest["user_answers"]["turns"][0]["question"]["code"], "ci_runtime_required")


if __name__ == "__main__":
    unittest.main()
