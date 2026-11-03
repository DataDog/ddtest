"""Scripted user answers, bounded excerpts and reference-preserving redaction."""

import json
import os
import re
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
        self.assertEqual(user_answers.parse_answer("ci_runtime=ruby=v3.3.6")["version"], "v3.3.6")
        # ddtest takes only numeric versions, and a Ruby version with its minor.
        for invalid in ("node=22", "ci_runtime=22", "ci_runtime=node=", "ci_confirm=", "ci_confirm=fact=",
                        "ci_provider=Jenkins CI", "unknown=value", "ci_runtime=ruby=banana", "ci_runtime=ruby=3",
                        "ci_runtime=node=lts", "ci_runtime=node=22.1.2.3", "ci_runtime=python=latest"):
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

    def test_runtime_aliases_match_ddtest_ci_runtime_names(self):
        # ddtest's --ci-runtime takes js for node, in any case.
        for text in ("ci_runtime=js=22", "ci_runtime=JS=22", "ci_runtime= Node =22", "ci_runtime=A / test:js=22"):
            with self.subTest(text=text):
                self.assertEqual(user_answers.parse_answer(text)["runtime"], "node")
        with self.assertRaises(ValueError):
            user_answers.parse_answer("ci_runtime=js=lts")
        for flag in ("node", "js"):
            with self.subTest(flag=flag):
                asked = user_answers.question(runtime_question(("A / test", "B / test"), runtime="Node", flag=flag))
                composed = user_answers.reply(asked, user_answers.load_answers(["ci_runtime=js=22", "ci_runtime=B / test:JS=20"]))
                self.assertEqual(composed["reply"], "A / test runs Node 22.\nB / test runs Node 20.")

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
        # A value must have one of the listed forms.
        # A placeholder takes only what ddtest's parser accepts there.
        for value, covered in (("agent", True), ("agentless:datadoghq.eu", True), ("bogus", False),
                               ("agentless:", False), ("agents", False), ("agentless", False),
                               ("agentless:us5.datadoghq.com", True), ("agentless:localhost", False),
                               ("agentless:DATADOGHQ.COM", False), ("agentless:datadoghq.com1", False),
                               ("agentless:-datadoghq.com", False)):
            with self.subTest(value=value):
                composed = user_answers.reply(asked, [complete[0], complete[2]] + user_answers.load_answers(
                    ["ci_confirm=jenkins-plugin-mode=" + value]))
                self.assertEqual(composed["reply"] is not None, covered, composed)

    def test_placeholders_follow_ddtest_parser(self):
        # Every placeholder ddtest's ci_confirm forms name is known here, with
        # the rule ddtest's parser applies.
        root = Path(__file__).resolve().parents[3]
        setup = (root / "internal/onboard/ci_provider_jenkins_setup.go").read_text()
        site = re.search(r"jenkinsSiteValue = regexp\.MustCompile\(`\^(.*)\$`\)", setup)
        self.assertIsNotNone(site)
        self.assertEqual(user_answers.PLACEHOLDER_VALUES["site"], site[1])
        sources = "".join(path.read_text() for path in (root / "internal/onboard").glob("*.go") if not path.name.endswith("_test.go"))
        placeholders = {name for values in re.findall(r'Values:\s*"([^"]*)"', sources) for name in re.findall(r"<([^<>]*)>", values)}
        self.assertTrue(placeholders)
        self.assertLessEqual(placeholders, set(user_answers.PLACEHOLDER_VALUES))
        # A form with a placeholder the evaluator does not know covers nothing.
        self.assertFalse(user_answers.offered("mode:<unknown>|agent", "mode:x"))
        self.assertTrue(user_answers.offered("mode:<unknown>|agent", "agent"))

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

    def test_only_whole_references_in_an_expanding_context_survive_redaction(self):
        # A leading $ does not make a literal a reference: the whole value must
        # be one, and single quotes or a data file's quoted key keep $NAME literal.
        kept = ("token=$TOKEN", "token=$A$B", 'password="$PASSWORD"', "token=$(vault read secret)",
                "api_key = os.environ['DD_API_KEY']", 'api_key = os.environ.get("DD_API_KEY")',
                '"api_key": "${{ secrets.DD_API_KEY }}"', '"api_key": "${DD_API_KEY}"', "${DD_API_KEY:-\"$OTHER\"}")
        for text in kept:
            with self.subTest(text=text):
                self.assertEqual(run_eval.redact(text), text)
        for text, expected in (('password: "$actual-secret"', 'password: "[REDACTED]"'),
                               ("password='$ecret123'", "password='[REDACTED]'"),
                               ('"password": "$ecret123"', '"password": "[REDACTED]"'),
                               ("password: $2b$12$abcdefghijklmnopqrstuv", "password: [REDACTED]"),
                               ("token='$(vault read secret)'", "token='[REDACTED] read secret)'"),
                               ("token=$$abc123", "token=[REDACTED]"),
                               ("apiKey: process.env.DD_API_KEY||abc123", "apiKey: [REDACTED]"),
                               # Defaults follow the same rules.
                               ("${DD_API_KEY:-$2b$10$abcdef}", "${DD_API_KEY:-[REDACTED]}"),
                               ("${DD_API_KEY:-'$ecret123'}", "${DD_API_KEY:-[REDACTED]}"),
                               ("DD_API_KEY=${KEY:-'$ecret123'}", "DD_API_KEY=${KEY:-[REDACTED]}")):
            with self.subTest(text=text):
                self.assertEqual(run_eval.redact(text), expected)

    def test_parameter_expansions_survive_and_only_literal_defaults_are_redacted(self):
        # Only a default supplies the variable's value; the other operands
        # replace, test, trim or slice it (B06's changes.patch had ${DD_API_KEY:+set}).
        kept = ('if [ "${DD_API_KEY:+set}" = set ]; then', "${DD_API_KEY+set}", "$${DD_API_KEY:+set}",
                "${DD_API_KEY:?DD_API_KEY must be set}", "${DD_API_KEY?unset}", "${DD_API_KEY#dd}",
                "${DD_API_KEY##*/}", "${DD_API_KEY%x}", "${DD_API_KEY%%x}", "${#DD_API_KEY}", "${!GITHUB_TOKEN}",
                "${DD_API_KEY:0:4}", "${DD_API_KEY: -4}", "${DD_API_KEY/abc/x}", "${DD_API_KEY//a/b}",
                "${DD_API_KEY^^}", "${DD_API_KEY,,}", "${DD_API_KEY@Q}", "${GITHUB_TOKEN:+-H x}",
                "${DD_API_KEY:-$OTHER}", "${DD_API_KEY:=${OTHER}}", '${DD_API_KEY:-""}', "${DD_API_KEY-}",
                "api_key: ${DD_API_KEY:+set}", "DD_API_KEY=${DD_API_KEY:+set}", "${DD_API_KEY:+set",
                "DD_API_KEY=${KEY}", "DD_API_KEY=${KEY:-$OTHER}", "DD_API_KEY=${KEY:+set}", "DD_API_KEY=${KEY:-}",
                # A nested reference without a literal default of its own.
                "DD_API_KEY=${PRIMARY:-${FALLBACK}}", "DD_API_KEY=${PRIMARY:-${FALLBACK:-}}",
                "DD_API_KEY=${PRIMARY:-${FALLBACK:-$OTHER}}", "${DD_API_KEY:-${FALLBACK:+set}}")
        for text in kept:
            self.assertEqual(run_eval.redact(text), text)
        for text, expected in (("${DD_API_KEY-abc123}", "${DD_API_KEY-[REDACTED]}"),
                               ("${DD_API_KEY=abc123}", "${DD_API_KEY=[REDACTED]}"),
                               ("${DD_API_KEY:=abc123}", "${DD_API_KEY:=[REDACTED]}"),
                               ('${DD_API_KEY:-"abc123"}', "${DD_API_KEY:-[REDACTED]}"),
                               ("DD_API_KEY=${DD_API_KEY:-abc123}", "DD_API_KEY=${DD_API_KEY:-[REDACTED]}"),
                               # A secret-named assignment's literal default, whatever variable it expands.
                               ("DD_API_KEY=${VAULT_VALUE:-actual-secret}", "DD_API_KEY=${VAULT_VALUE:-[REDACTED]}"),
                               ('export DD_API_KEY="${KEY:=abc def}"', 'export DD_API_KEY="${KEY:=[REDACTED]}"'),
                               ("api_key: ${KEY-abc123}", "api_key: ${KEY-[REDACTED]}"),
                               ("DD_API_KEY=${KEY:-abc123\nnext}", "DD_API_KEY=${KEY:-[REDACTED]\nnext}"),
                               # Without its closing brace (a truncated line), a default is still a value.
                               ("${DD_API_KEY:-abc123", "${DD_API_KEY:-[REDACTED]"),
                               ("${DD_API_KEY=abc123", "${DD_API_KEY=[REDACTED]"),
                               # A nested default supplies the value when every enclosing variable is unset.
                               ("DD_API_KEY=${PRIMARY:-${FALLBACK:-opaque-secret-value}}", "DD_API_KEY=${PRIMARY:-[REDACTED]}}"),
                               ("DD_API_KEY=${VAULT_VALUE:-${FALLBACK:-opaqueLiteralCredential12}}", "DD_API_KEY=${VAULT_VALUE:-[REDACTED]}}"),
                               ("${DD_API_KEY:-${FALLBACK:-opaqueLiteral12}}", "${DD_API_KEY:-[REDACTED]}}"),
                               ("DD_API_KEY=${A:-${B:=${C:-deep-literal-1}}}", "DD_API_KEY=${A:-[REDACTED]}}}"),
                               ("DD_API_KEY=${A:-${B:-opaque-literal", "DD_API_KEY=${A:-[REDACTED]"),
                               # Recognized token shapes are redacted in any operand.
                               ("${DD_API_KEY:+sk-1234567890abcdefghijk}", "${DD_API_KEY:+[REDACTED]}")):
            self.assertEqual(run_eval.redact(text), expected)

    def test_lockfile_keys_and_non_secret_values_survive_redaction(self):
        # B02's changes.patch lost '"node_modules/jsonwebtoken": {' to [REDACTED].
        lockfile = ('     "node_modules/jsonwebtoken": {\n'
                    '       "version": "9.0.2",\n'
                    '+    "node_modules/@scope/auth-token": {"version": "1.0.0"},\n'
                    '+    "node_modules/a/node_modules/csrf-token": [\n'
                    '+        "jsonwebtoken": "^9.0.2",\n'
                    '+        "github-token": "workspace:*",\n'
                    '+  jsonwebtoken: ">=8 <10"\n')
        self.assertEqual(run_eval.redact(lockfile), lockfile)
        kept = ("token: {", "api_key: [", "secrets: []", "token: null", "password: false", "use_token: true",
                "token: ~", "api_key: 1.2.3", "token: v2.0.0-rc.1", "token: ^9.0.2", "token: ~1.2.x", "token: >=6",
                '"auth-token": "npm:jsonwebtoken@9"', "token: node_modules/jsonwebtoken/index.js")
        for text in kept:
            self.assertEqual(run_eval.redact(text), text)
        for text, expected in (("token: real-token-value", "token: [REDACTED]"),
                               ("token={abc123", "token=[REDACTED]"),
                               ("api_key: [abc123", "api_key: [REDACTED]"),
                               ("password: 123456", "password: [REDACTED]"),
                               ("password=truest", "password=[REDACTED]"),
                               ('"node_modules/x": {"api_key": "realvalue"}', '"node_modules/x": {"api_key": "[REDACTED]"}'),
                               ("node_modules/x token=realvalue", "node_modules/x token=[REDACTED]")):
            self.assertEqual(run_eval.redact(text), expected)
        structured = run_eval.redact({"node_modules/jsonwebtoken": {"version": "9.0.2"}, "has_token": True, "token": None,
                                      "api_key": "abc123", "tokens": {"refresh_token": "abc123"}})
        self.assertEqual(structured, {"node_modules/jsonwebtoken": {"version": "9.0.2"}, "has_token": True, "token": None,
                                      "api_key": "[REDACTED]", "tokens": "[REDACTED]"})

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


class RecorderTests(unittest.TestCase):
    def test_wrapper_is_transparent_and_records_standard_output(self):
        import shlex
        import subprocess
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            records = root / "records"
            records.mkdir()
            # The runner keeps the real binary in the harness folder.
            (root / "harness").mkdir()
            real = root / "harness" / run_eval.REAL_DDTEST
            real.write_text('#!/bin/sh\nread line\nprintf "out:%s:%s\\n" "$1" "$line"\necho err >&2\nexit 3\n')
            real.chmod(0o755)
            wrapper = root / "ddtest"
            wrapper.write_text(run_eval.DDTEST_WRAPPER.format(records=shlex.quote(str(records)), real=shlex.quote(str(real))))
            wrapper.chmod(0o755)
            saved = root / "saved.txt"
            result = subprocess.run(f"{shlex.quote(str(wrapper))} 'a b' > {shlex.quote(str(saved))}", shell=True,
                                    input="typed\n", capture_output=True, text=True, timeout=30)
            self.assertEqual((result.returncode, result.stdout, result.stderr), (3, "", "err\n"))
            self.assertEqual(saved.read_text(), "out:a b:typed\n")
            self.assertEqual(run_eval.harvest_ddtest_records(records), ["out:a b:typed\n"])
            self.assertEqual(run_eval.harvest_ddtest_records(records), [])
            self.assertEqual(list(records.iterdir()), [])

    def test_special_records_are_removed_unread_and_large_records_are_bounded(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            records = root / "records"
            records.mkdir()
            outside = root / "outside.txt"
            outside.write_text("host file")
            # A FIFO would block a plain read; a symlink would read outside the trial.
            os.mkfifo(records / "a.done")
            (records / "b.done").symlink_to(outside)
            (records / "c.done").write_bytes(b"x" * (run_eval.RECORD_LIMIT + 10))
            (records / "d").write_text("still being written")
            harvested = run_eval.harvest_ddtest_records(records)
            self.assertEqual(sorted(len(record) for record in harvested), [0, 0, run_eval.RECORD_LIMIT])
            self.assertEqual([path.name for path in records.iterdir()], ["d"])
            self.assertEqual(outside.read_text(), "host file")
            # A records folder replaced by a symlink is not followed.
            target = root / "target"
            target.mkdir()
            (target / "e.done").write_text("elsewhere")
            linked = root / "linked"
            linked.symlink_to(target)
            self.assertEqual(run_eval.harvest_ddtest_records(linked), [])
            self.assertTrue((target / "e.done").exists())
            # Nor is a harness folder replaced by a symlink to a host folder
            # holding records/.
            host = root / "host"
            (host / "records").mkdir(parents=True)
            (host / "records" / "host-file.done").write_text("host record")
            (root / "harness").symlink_to(host)
            self.assertEqual(run_eval.harvest_ddtest_records(root / "harness" / "records"), [])
            self.assertEqual((host / "records" / "host-file.done").read_text(), "host record")


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

    def test_question_is_detected_when_the_agent_redirects_ddtest_output(self):
        # A11: the agent wrote ddtest's JSON to a file and printed a reformatted
        # summary, so no tool output held the ask_user result.
        question = json.dumps(CONFIRMATION_QUESTION)
        self.case.binary.write_text('#!/bin/sh\n[ "$1" = --version ] && { echo "ddtest 1.0"; exit 0; }\n'
                                    'printf "%s\\n" "$FAKE_DDTEST_RESULT"\nexit 1\n')
        for agent in ("claude", "codex"):
            with self.subTest(agent=agent):
                self.args.agent = agent
                self.args.output = self.case.root / f"{agent}-redirected"
                code, manifest, turns, activity = self.trial(
                    ["ci_confirm=jenkins-plugin", "ci_confirm=jenkins-plugin-mode=agentless:datadoghq.com",
                     "ci_confirm=jenkins-agent-runtime-matches"],
                    {"FAKE_DDTEST_RESULT": question, "FAKE_REDIRECT_DDTEST": "1"})
                self.assertEqual(code, 0)
                outputs = [event["output"] for event in activity if event["type"] in ("tool_result", "item.completed") and "output" in event]
                self.assertTrue(outputs)
                self.assertTrue(any("ci_setup_confirmation_required" in output for output in outputs))
                self.assertTrue(all(user_answers.agent_result(output) is None for output in outputs))
                self.assertEqual(len(turns), 2)
                self.assertEqual(turns[1]["stdin"], "jenkins-plugin: yes.\njenkins-plugin-mode: agentless:datadoghq.com.\n"
                                                    "jenkins-agent-runtime-matches: yes.")
                record = manifest["user_answers"]
                self.assertEqual(record["status"], "answered")
                self.assertEqual(record["turns"][0]["source"], "ddtest log")
                # The harness log is evidence on the host, not in the trial.
                logged = [json.loads(line) for line in (self.args.output / "ddtest-results.jsonl").read_text().splitlines()]
                self.assertEqual([entry["result"]["code"] if entry["result"] else None for entry in logged],
                                 ["ci_setup_confirmation_required"])
                self.assertEqual(logged[0]["turn"], 1)
                records = Path(manifest["trial_directory"]) / "harness" / "records"
                self.assertEqual(list(records.iterdir()), [])
                self.assertNotIn("records", (self.args.output / "status.txt").read_text())

    def test_tool_output_remains_the_fallback_without_a_ddtest_record(self):
        code, manifest, turns, _ = self.trial(["ci_runtime=ruby=3.4"])
        self.assertEqual(len(turns), 2)
        self.assertEqual(manifest["user_answers"]["turns"][0]["source"], "tool output")

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

    def test_zero_reply_limit_is_rejected_before_any_trial(self):
        self.args.max_user_turns = 0
        with self.assertRaisesRegex(ValueError, "max-user-turns"):
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
        self.assertEqual(sorted(path.name for path in (directory.parent / "bin").iterdir()), ["ddtest"])

    def test_binstub_folder_replaced_by_a_symlink_is_not_listed(self):
        outside = self.case.root / "host-folder"
        outside.mkdir()
        (outside / "host-only-file").write_text("")
        bundle = self.case.root / "bundle"
        bundle.write_text(f"#!/bin/sh\n[ \"$1\" = binstubs ] || exit 0\nrmdir \"$BUNDLE_BIN\" && ln -s {outside} \"$BUNDLE_BIN\"\n")
        bundle.chmod(0o755)
        self.args.bundle_binstubs = ["rspec-core"]
        self.args.output = self.case.root / "binstubs-link"
        code, manifest = self.case.trial()
        self.assertEqual(code, 1)
        self.assertEqual(manifest["failure_stage"], "bundle_binstubs")
        self.assertNotIn("bundle_binstubs", manifest)
        self.assertNotIn("host-only-file", (self.args.output / "run.json").read_text())

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
